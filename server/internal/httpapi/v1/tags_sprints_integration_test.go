package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// タグAPI・スプリントAPI（ApiDesign.md 9.11 / 9.12）を**実際のDBに対して**通す。
//
// 単体テストはフェイクを差し替えるため、queries/tag.sql と queries/sprint.sql の
// SQL は一度も実行されない。ここでしか確かめられないものが5つある。
//
//   - 相関副問い合わせの ticket_count / closed_count が実際に数えられること
//     （ticket 行は直接 INSERT して用意する。チケットAPIは手順16b）
//   - 一意制約 uq_tag_project_name が本当に捕まり 409 に写ること
//     （pgconn.PgError の TableName / ConstraintName が期待どおりに入るか）
//   - ORDER BY が設計どおりであること（タグは sort_order,name／スプリントは
//     start_date DESC NULLS LAST, created_at DESC）
//   - タグ削除で ticket_tag が CASCADE で消え、**チケットは残る**こと
//   - スプリント削除で ticket.sprint_id が SET NULL になり、**チケットは残る**こと
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`
// （Development.md 6.1。接続文字列を手で書かない）。
func TestTagsSprintsIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})

	adminID := ulidgen.New()
	adminEmail := "tag-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "tg-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"タグ結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	var projectID string
	if err := pool.QueryRow(ctx, `SELECT id FROM project WHERE key = $1`, key).Scan(&projectID); err != nil {
		t.Fatalf("project_id を読めない: %v", err)
	}

	base := "/api/v1/projects/" + key

	// ── ① タグの作成と並び ────────────────────────────────
	//
	// 作る順と sort_order の順をわざと食い違わせる。ORDER BY が
	// created_at ではなく sort_order で効いていることを見るためである。
	tagIDs := map[string]string{}
	for _, c := range []struct {
		name      string
		sortOrder string
	}{
		{"設計", `,"sort_order":30`},
		{"GUI", `,"sort_order":10`},
		{"MCP", `,"sort_order":20`},
	} {
		rec := postWithCookie(r, base+"/tags", session,
			fmt.Sprintf(`{"name":%q%s}`, c.name, c.sortOrder))
		if rec.Code != http.StatusCreated {
			t.Fatalf("タグ %s の作成 status = %d（body=%s）", c.name, rec.Code, rec.Body.String())
		}
		tagIDs[c.name] = viewOf(t, rec)["id"].(string)
	}

	if got := tagNames(t, r, session, base); !equalStrings(got, []string{"GUI", "MCP", "設計"}) {
		t.Errorf("タグの並び = %v, want [GUI MCP 設計]（sort_order 昇順）", got)
	}

	// ── ② 一意制約が 409 に写ること ───────────────────────
	rec = postWithCookie(r, base+"/tags", session, `{"name":"設計"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("重複したタグ名の status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "already_exists" {
		t.Errorf("code = %q, want already_exists", got)
	}

	// **大文字と小文字は区別する**（B-5。uq_tag_project_name は text であって citext ではない）。
	rec = postWithCookie(r, base+"/tags", session, `{"name":"gui"}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("大小違いの status = %d, want 201（別のタグとして作れる）（body=%s）", rec.Code, rec.Body.String())
	}
	guiLowerID := viewOf(t, rec)["id"].(string)

	// ── ③ sort_order 省略時は末尾（最大値 + 10）──────────────
	//
	// 現在の最大は 30（設計）なので 40 になる。**固定値を書かず、
	// いま入っている最大値から期待値を計算する**……ではなく、ここは
	// 上で作った3件が確定しているので 40 を直接期待してよい。
	// ただし gui（末尾）が既に 40 を取っているため、次は 50 になる。
	rec = postWithCookie(r, base+"/tags", session, `{"name":"運用"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("末尾追加の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if got := viewOf(t, rec)["sort_order"].(float64); got != 50 {
		t.Errorf("sort_order = %v, want 50（gui が 40 を取った次）", got)
	}

	// ── ④ ticket_count が実際に数えられること ────────────────
	//
	// チケットAPIは手順16b なので、行は直接 INSERT する。
	ticketID := ulidgen.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ticket (id, project_id, seq, type, title, status_key)
		VALUES ($1, $2, 1, 'task', '結合テスト用チケット', 'todo')`,
		ticketID, projectID); err != nil {
		t.Fatalf("チケットを作れない: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO ticket_tag (ticket_id, tag_id) VALUES ($1, $2), ($1, $3)`,
		ticketID, tagIDs["設計"], tagIDs["GUI"]); err != nil {
		t.Fatalf("ticket_tag を作れない: %v", err)
	}

	counts := tagCounts(t, r, session, base)
	if counts["設計"] != 1 || counts["GUI"] != 1 {
		t.Errorf("ticket_count = %v, want 設計:1 GUI:1", counts)
	}
	if counts["MCP"] != 0 {
		t.Errorf("MCP の ticket_count = %d, want 0", counts["MCP"])
	}

	// ── ⑤ 改名と並べ替え（9.11.1）──────────────────────────
	rec = bodyWithCookie(r, http.MethodPatch, base+"/tags/"+tagIDs["MCP"], session,
		`{"name":"MCP連携","sort_order":5}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("改名の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if got := tagNames(t, r, session, base); got[0] != "MCP連携" {
		t.Errorf("並べ替え後の先頭 = %q, want MCP連携（sort_order=5）", got[0])
	}

	// 他のタグと同じ名前へは改名できない（UPDATE 側でも一意制約が効く）。
	rec = bodyWithCookie(r, http.MethodPatch, base+"/tags/"+tagIDs["MCP"], session,
		`{"name":"設計"}`, "")
	if rec.Code != http.StatusConflict {
		t.Errorf("重複する名前への改名 status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}

	// ── ⑥ タグ削除で ticket_tag が消え、チケットは残る ────────
	rec = bodyWithCookie(r, http.MethodDelete, base+"/tags/"+tagIDs["設計"], session, "", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("タグ削除の status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	var links int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ticket_tag WHERE ticket_id = $1`, ticketID).Scan(&links); err != nil {
		t.Fatalf("ticket_tag を数えられない: %v", err)
	}
	if links != 1 {
		t.Errorf("ticket_tag = %d件, want 1（設計だけ CASCADE で消える）", links)
	}
	var ticketAlive int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ticket WHERE id = $1`, ticketID).Scan(&ticketAlive); err != nil {
		t.Fatalf("ticket を数えられない: %v", err)
	}
	if ticketAlive != 1 {
		t.Errorf("チケットが消えた（タグ削除でチケットは消えない。9.11）")
	}

	// 使い終わった小文字タグを片付ける（後続の期待値に混ざらないように）。
	if rec := bodyWithCookie(r, http.MethodDelete, base+"/tags/"+guiLowerID, session, "", ""); rec.Code != http.StatusNoContent {
		t.Errorf("gui の削除 status = %d", rec.Code)
	}

	// ── ⑦ スプリントの作成と並び（9.12）──────────────────────
	//
	// 作る順と start_date の順を食い違わせ、ORDER BY を見る。
	sprintIDs := map[string]string{}
	for _, c := range []struct{ name, body string }{
		{"Sprint 2", `{"name":"Sprint 2","start_date":"2026-07-22","end_date":"2026-08-04","status":"completed"}`},
		{"Sprint 3", `{"name":"Sprint 3","goal":"認証を通す","start_date":"2026-08-05","end_date":"2026-08-18","status":"active"}`},
		{"日程未定", `{"name":"日程未定"}`},
	} {
		rec := postWithCookie(r, base+"/sprints", session, c.body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("スプリント %s の作成 status = %d（body=%s）", c.name, rec.Code, rec.Body.String())
		}
		sprintIDs[c.name] = viewOf(t, rec)["id"].(string)
	}

	// start_date 降順・NULL は末尾。
	if got := sprintNames(t, r, session, base); !equalStrings(got, []string{"Sprint 3", "Sprint 2", "日程未定"}) {
		t.Errorf("スプリントの並び = %v, want [Sprint 3, Sprint 2, 日程未定]", got)
	}

	// ── ⑧ ticket_count / closed_count が数えられること ──────
	//
	// closed_count は closed_at IS NOT NULL で数える（status_category ではない）。
	closedID := ulidgen.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ticket (id, project_id, seq, type, title, status_key, sprint_id, closed_at)
		VALUES ($1, $2, 2, 'task', '完了済み', 'done', $3, now())`,
		closedID, projectID, sprintIDs["Sprint 3"]); err != nil {
		t.Fatalf("完了チケットを作れない: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE ticket SET sprint_id = $1 WHERE id = $2`, sprintIDs["Sprint 3"], ticketID); err != nil {
		t.Fatalf("チケットにスプリントを付けられない: %v", err)
	}

	s3 := sprintByName(t, r, session, base, "Sprint 3")
	if got := s3["ticket_count"].(float64); got != 2 {
		t.Errorf("ticket_count = %v, want 2", got)
	}
	if got := s3["closed_count"].(float64); got != 1 {
		t.Errorf("closed_count = %v, want 1（closed_at IS NOT NULL の1件）", got)
	}
	if got := s3["goal"].(string); got != "認証を通す" {
		t.Errorf("goal = %q", got)
	}

	// ── ⑨ PATCH の日付検証が現在値と突き合わせること ─────────
	//
	// 開始 8/05・終了 8/18 の行に、終了だけ 8/01 を送る。
	rec = bodyWithCookie(r, http.MethodPatch, base+"/sprints/"+sprintIDs["Sprint 3"], session,
		`{"end_date":"2026-08-01"}`, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("矛盾する end_date の status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}

	// null で日付を消せる。
	rec = bodyWithCookie(r, http.MethodPatch, base+"/sprints/"+sprintIDs["Sprint 3"], session,
		`{"start_date":null}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start_date を消す PATCH の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if got := viewOf(t, rec)["start_date"]; got != nil {
		t.Errorf("start_date = %v, want null", got)
	}
	// 消したので NULL 扱いになり、末尾へ回る。
	if got := sprintNames(t, r, session, base); got[0] != "Sprint 2" {
		t.Errorf("start_date を消した後の先頭 = %q, want Sprint 2", got[0])
	}

	// ── ⑩ スプリント削除でチケットが残ること ────────────────
	rec = bodyWithCookie(r, http.MethodDelete, base+"/sprints/"+sprintIDs["Sprint 3"], session, "", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("スプリント削除の status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	var sprintOfTicket *string
	if err := pool.QueryRow(ctx,
		`SELECT sprint_id FROM ticket WHERE id = $1`, ticketID).Scan(&sprintOfTicket); err != nil {
		t.Fatalf("ticket.sprint_id を読めない: %v", err)
	}
	if sprintOfTicket != nil {
		t.Errorf("sprint_id = %v, want NULL（ON DELETE SET NULL）", *sprintOfTicket)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ticket WHERE id = $1`, ticketID).Scan(&ticketAlive); err != nil {
		t.Fatalf("ticket を数えられない: %v", err)
	}
	if ticketAlive != 1 {
		t.Errorf("チケットが消えた（スプリント削除でチケットは消えない。9.12）")
	}

	// ── ⑪ 他プロジェクトの ID は 404 ───────────────────────
	//
	// 別プロジェクトを作り、そのタグ ID を最初のプロジェクトの下で叩く。
	otherKey := "tg2" + strings.ToLower(adminID[len(adminID)-7:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, otherKey); err != nil {
			t.Errorf("2つ目のプロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec = postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"別プロジェクト","workflow_template":"simple"}`, otherKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("2つ目のプロジェクト作成 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	rec = postWithCookie(r, "/api/v1/projects/"+otherKey+"/tags", session, `{"name":"別のタグ"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("別プロジェクトのタグ作成 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	otherTagID := viewOf(t, rec)["id"].(string)

	rec = bodyWithCookie(r, http.MethodPatch, base+"/tags/"+otherTagID, session, `{"name":"x"}`, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("他プロジェクトのタグへの PATCH = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
	rec = bodyWithCookie(r, http.MethodDelete, base+"/tags/"+otherTagID, session, "", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("他プロジェクトのタグへの DELETE = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
	// 消えていないこと（404 を返しつつ実際に消していたら最悪である）。
	var otherAlive int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tag WHERE id = $1`, otherTagID).Scan(&otherAlive); err != nil {
		t.Fatalf("tag を数えられない: %v", err)
	}
	if otherAlive != 1 {
		t.Errorf("404 を返したのに他プロジェクトのタグを消した")
	}
}

// ── 応答を読む補助 ──────────────────────────────────────────

func tagItems(t *testing.T, r http.Handler, session, base string) []any {
	t.Helper()
	rec := getWithCookie(r, base+"/tags", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("タグ一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	items, _ := viewOf(t, rec)["items"].([]any)
	return items
}

func tagNames(t *testing.T, r http.Handler, session, base string) []string {
	t.Helper()
	out := []string{}
	for _, it := range tagItems(t, r, session, base) {
		out = append(out, it.(map[string]any)["name"].(string))
	}
	return out
}

func tagCounts(t *testing.T, r http.Handler, session, base string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, it := range tagItems(t, r, session, base) {
		m := it.(map[string]any)
		out[m["name"].(string)] = int(m["ticket_count"].(float64))
	}
	return out
}

func sprintItems(t *testing.T, r http.Handler, session, base string) []any {
	t.Helper()
	rec := getWithCookie(r, base+"/sprints", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("スプリント一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	items, _ := viewOf(t, rec)["items"].([]any)
	return items
}

func sprintNames(t *testing.T, r http.Handler, session, base string) []string {
	t.Helper()
	out := []string{}
	for _, it := range sprintItems(t, r, session, base) {
		out = append(out, it.(map[string]any)["name"].(string))
	}
	return out
}

func sprintByName(t *testing.T, r http.Handler, session, base, name string) map[string]any {
	t.Helper()
	for _, it := range sprintItems(t, r, session, base) {
		m := it.(map[string]any)
		if m["name"] == name {
			return m
		}
	}
	t.Fatalf("スプリント %q が一覧に無い", name)
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
