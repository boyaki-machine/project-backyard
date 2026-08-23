package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/lexorank"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// チケットAPI（ApiDesign.md 9.2 / 9.3 / 9.4）を**実際のDBに対して**通す。
//
// 単体テストはフェイクを差し替えるため queries/ticket.sql は一度も実行されない。
// ここでしか確かめられないものが7つある。
//
//   - 13種類のフィルタが実際に絞れること（AND と OR の組み合わせ、none の扱い）
//   - **sort_key の比較が COLLATE "C" で行われること**。DBの既定は ja-JP-x-icu で、
//     ICU は句読点の重みを言語規則で決めるため、':' を含むキーの大小がバイト順と
//     一致する保証がない（lexorank が仮定しているのはバイト順である）
//   - ORDER BY が設計どおりであること（priority と status は**意味の順**）
//   - project_counter の1文が採番として働き、欠番を出さないこと
//   - parent フィルタの再帰CTEが子孫まで届くこと
//   - 窓関数の total / MAX(updated_at) が絞り込み全体のものになること
//   - activity への記録が業務トランザクションと同時に確定すること
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`
// （Development.md 6.1。接続文字列を手で書かない）。
func TestTicketsIntegration(t *testing.T) {
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
	adminEmail := "tkt-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "tk-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"チケット結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	var projectID string
	if err := pool.QueryRow(ctx, `SELECT id FROM project WHERE key = $1`, key).Scan(&projectID); err != nil {
		t.Fatalf("project_id を読めない: %v", err)
	}
	base := "/api/v1/projects/" + key

	// タグとスプリントを1つずつ用意する（フィルタとグループ化の材料）。
	rec = postWithCookie(r, base+"/tags", session, `{"name":"設計"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("タグ作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	tagID := viewOf(t, rec)["id"].(string)
	rec = postWithCookie(r, base+"/sprints", session, `{"name":"Sprint X"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("スプリント作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	sprintID := viewOf(t, rec)["id"].(string)

	// ── ① 作成：採番・初期ステータス・sort_key・reporter ──────────
	//
	// **欠番を出さないこと**（DbDesign.md 6.4.1）。作った順に 1,2,3… になる。
	epic := createTicketIT(t, r, session, base,
		fmt.Sprintf(`{"type":"epic","title":"親の仕事","priority":"highest","tag_ids":[%q],"sprint_id":%q,"due_date":"2026-08-14","start_date":"2026-08-09"}`,
			tagID, sprintID))
	if epic["seq"].(float64) != 1 {
		t.Errorf("最初の seq = %v, want 1", epic["seq"])
	}
	status := epic["status"].(map[string]any)
	if status["key"] != "todo" || status["category"] != "todo" {
		t.Errorf("初期ステータス = %v, want todo（category='todo' かつ sort_order 最小）", status)
	}
	if epic["version"].(float64) != 1 {
		t.Errorf("version = %v, want 1", epic["version"])
	}
	if reporter, _ := epic["reporter"].(map[string]any); reporter == nil || reporter["id"] != adminID {
		t.Errorf("reporter = %v, want 呼び出し元のアクター", epic["reporter"])
	}
	if !lexorank.Valid(epic["sort_key"].(string)) {
		t.Errorf("sort_key = %v が LexoRank の形式でない", epic["sort_key"])
	}
	// **date 列は時刻を持たない**（前日へずれる経路を作らない）。
	if epic["due_date"] != "2026-08-14" || epic["start_date"] != "2026-08-09" {
		t.Errorf("日付 = %v / %v（YYYY-MM-DD のまま返すこと）", epic["start_date"], epic["due_date"])
	}
	if tags, _ := epic["tags"].([]any); len(tags) != 1 {
		t.Errorf("tags = %v, want 1件", epic["tags"])
	}

	child := createTicketIT(t, r, session, base,
		fmt.Sprintf(`{"type":"task","title":"子の仕事","priority":"low","parent_seq":1,"assignee_id":%q}`, adminID))
	if child["seq"].(float64) != 2 {
		t.Errorf("2件目の seq = %v, want 2", child["seq"])
	}
	if parent, _ := child["parent"].(map[string]any); parent == nil || parent["seq"].(float64) != 1 {
		t.Errorf("parent = %v, want seq=1（9.5.1）", child["parent"])
	}

	grandchild := createTicketIT(t, r, session, base,
		`{"type":"task","title":"孫の仕事","priority":"medium","parent_seq":2}`)
	loner := createTicketIT(t, r, session, base,
		`{"type":"bug","title":"独りの仕事","priority":"lowest"}`)
	// **優先度を持たないチケットを必ず1件混ぜる。** 一覧の SELECT に順位の列を
	// 出していた版では、この行があると NULL を読めずに 500 になった（実サーバの
	// 検証で気づいた）。単体テストはフェイクを返すので、この経路を通らない。
	noPriority := createTicketIT(t, r, session, base,
		`{"type":"task","title":"優先度を決めていない仕事"}`)
	if noPriority["priority"] != nil {
		t.Errorf("priority = %v, want null", noPriority["priority"])
	}

	// dod / links / comment_count は手順18 まで空・0（B-4）。
	for _, key := range []string{"dod", "links"} {
		if arr, _ := epic[key].([]any); len(arr) != 0 {
			t.Errorf("%s = %v, want 空配列", key, epic[key])
		}
	}
	if epic["comment_count"].(float64) != 0 {
		t.Errorf("comment_count = %v, want 0", epic["comment_count"])
	}

	// ── ② 業務履歴（9.1.1）──────────────────────────────
	//
	// **activity に書き、audit_log には書かない。**
	var activityCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM activity WHERE project_id = $1 AND entity_type = 'ticket' AND action = 'create'`,
		projectID).Scan(&activityCount); err != nil {
		t.Fatalf("activity を数えられない: %v", err)
	}
	if activityCount != 5 {
		t.Errorf("activity の件数 = %d, want 5（作成した5件）", activityCount)
	}
	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE target_type = 'ticket'`).Scan(&auditCount); err != nil {
		t.Fatalf("audit_log を数えられない: %v", err)
	}
	if auditCount != 0 {
		t.Errorf("audit_log にチケットの記録が %d 件ある（9.1.1 は activity だけ）", auditCount)
	}

	// ── ③ 一覧：既定の並びは sort_key 昇順 ────────────────────
	if got := ticketSeqs(t, r, session, base, ""); !equalInts(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("既定の並び = %v, want [1 2 3 4 5]（sort_key 昇順＝作った順）", got)
	}
	// **COLLATE "C" が効いていること。** ICU の照合で並べると、':' を含むキーの
	// 大小がバイト順と食い違い、この順序が崩れうる。
	if got := ticketSeqs(t, r, session, base, "?order=desc"); !equalInts(got, []int{5, 4, 3, 2, 1}) {
		t.Errorf("降順 = %v, want [5 4 3 2 1]", got)
	}

	// ── ④ 並び順は意味の順（A-2）────────────────────────────
	//
	// highest(1) > medium(3) > low(2) > lowest(4) の順に並ぶこと。
	// キーの辞書順なら high < low < lowest < medium < highest になり、別の並びになる。
	if got := ticketSeqs(t, r, session, base, "?sort=priority&order=desc"); !equalInts(got, []int{1, 3, 2, 4, 5}) {
		t.Errorf("優先度の降順 = %v, want [1 3 2 4 5]（highest→medium→low→lowest→未設定）", got)
	}
	// **未設定は昇順でも末尾**（NULLS LAST を両方向に付けてある）。
	if got := ticketSeqs(t, r, session, base, "?sort=priority&order=asc"); !equalInts(got, []int{4, 2, 3, 1, 5}) {
		t.Errorf("優先度の昇順 = %v, want [4 2 3 1 5]（lowest→low→medium→highest→未設定）", got)
	}

	// ── ⑤ フィルタ ────────────────────────────────────────
	cases := []struct {
		name  string
		query string
		want  []int
	}{
		{"種別", "?type=bug", []int{4}},
		{"種別の複数指定は OR", "?type=bug,epic", []int{1, 4}},
		{"種別と優先度は AND", "?type=task&priority=low", []int{2}},
		{"優先度が未設定のものは priority で絞ると出ない", "?priority=lowest,low,medium,high,highest", []int{1, 2, 3, 4}},
		{"担当（me）", "?assignee=me", []int{2}},
		{"担当（未割当）", "?assignee=none", []int{1, 3, 4, 5}},
		{"タグ", "?tag=" + tagID, []int{1}},
		{"タグ（未分類）", "?tag=none", []int{2, 3, 4, 5}},
		{"スプリント", "?sprint=" + sprintID, []int{1}},
		{"スプリント（未割当）", "?sprint=none", []int{2, 3, 4, 5}},
		{"未完了のみ", "?open=true", []int{1, 2, 3, 4, 5}},
		{"完了のみ", "?open=false", nil},
		{"分類", "?status_category=todo", []int{1, 2, 3, 4, 5}},
		{"分類（該当なし）", "?status_category=done", nil},
		// 再帰CTE：1 の部分木は 1・2・3（孫まで届く）
		{"部分木", "?parent=1", []int{1, 2, 3}},
		{"部分木（葉）", "?parent=3", []int{3}},
		// due_within は期限超過を含み、due_date が NULL のものは除く
		{"期限あり", "?due_within=3650d", []int{1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ticketSeqs(t, r, session, base, c.query); !equalInts(got, c.want) {
				t.Errorf("%s = %v, want %v", c.query, got, c.want)
			}
		})
	}

	// ── ⑥ 窓関数の total と ETag ────────────────────────────
	rec = getWithCookie(r, base+"/tickets?type=bug", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)
	if view["total"].(float64) != 1 {
		t.Errorf("total = %v, want 1（絞り込み全体の件数）", view["total"])
	}
	if view["per_page"].(float64) != 200 {
		t.Errorf("per_page = %v, want 200", view["per_page"])
	}
	etagBug := rec.Header().Get("ETag")
	if !strings.HasPrefix(etagBug, `W/"tkt-`) {
		t.Errorf("ETag = %q（弱い検証子の形になっていない）", etagBug)
	}
	rec = getWithCookie(r, base+"/tickets?type=epic", session)
	if got := rec.Header().Get("ETag"); got == etagBug {
		t.Errorf("件数も最終更新も同じ別条件で ETag が一致した: %q", got)
	}

	// ── ⑦ ページングは規約どおり返る（9.2.3）────────────────
	rec = getWithCookie(r, base+"/tickets?per_page=2&sort=seq&order=asc", session)
	view = viewOf(t, rec)
	if view["total"].(float64) != 5 || view["total_pages"].(float64) != 3 {
		t.Errorf("total/total_pages = %v/%v, want 5/3", view["total"], view["total_pages"])
	}
	if items, _ := view["items"].([]any); len(items) != 2 {
		t.Errorf("1ページ目の件数 = %d, want 2", len(items))
	}

	// ── ⑧ 並べ替え（9.4）───────────────────────────────────
	//
	// 4 を先頭へ。**現在値は毎回読み直す**（固定値を埋めない）。
	rec = postWithCookie(r, base+"/tickets/4/move", session, `{"position":"first"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("先頭へ移動の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	moved := viewOf(t, rec)
	if moved["version"].(float64) != 2 {
		t.Errorf("version = %v, want 2（+1 されること）", moved["version"])
	}
	if moved["rebalanced"].(bool) {
		t.Error("rebalanced = true（この規模では振り直しは起きない）")
	}
	if got := ticketSeqs(t, r, session, base, ""); !equalInts(got, []int{4, 1, 2, 3, 5}) {
		t.Errorf("先頭へ移動後の並び = %v, want [4 1 2 3 5]", got)
	}

	// 2 を 3 の直後へ
	rec = postWithCookie(r, base+"/tickets/2/move", session, `{"after_seq":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("after_seq の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if got := ticketSeqs(t, r, session, base, ""); !equalInts(got, []int{4, 1, 3, 2, 5}) {
		t.Errorf("after_seq 後の並び = %v, want [4 1 3 2 5]", got)
	}

	// 1 を 4 の直前へ（＝先頭）
	rec = postWithCookie(r, base+"/tickets/1/move", session, `{"before_seq":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("before_seq の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if got := ticketSeqs(t, r, session, base, ""); !equalInts(got, []int{1, 4, 3, 2, 5}) {
		t.Errorf("before_seq 後の並び = %v, want [1 4 3 2 5]", got)
	}

	// **sort_key が NULL の行があっても回復する。** 直接 INSERT した行や
	// 手順16a 以前のデータがこの状態になる。
	orphan := ulidgen.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ticket (id, project_id, seq, type, title, status_key)
		VALUES ($1, $2, 99, 'task', '並び順を持たない行', 'todo')`,
		orphan, projectID); err != nil {
		t.Fatalf("sort_key の無い行を作れない: %v", err)
	}
	rec = postWithCookie(r, base+"/tickets/2/move", session, `{"after_seq":99}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("sort_key の無い行を基準にした移動の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if !viewOf(t, rec)["rebalanced"].(bool) {
		t.Error("rebalanced = false（基準のキーが無いなら振り直すこと）")
	}
	var nullKeys int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ticket WHERE project_id = $1 AND sort_key IS NULL`,
		projectID).Scan(&nullKeys); err != nil {
		t.Fatalf("sort_key を数えられない: %v", err)
	}
	if nullKeys != 0 {
		t.Errorf("振り直した後も sort_key が NULL の行が %d 件ある", nullKeys)
	}

	// ── ⑨ 検証エラー（9.3 / 9.14）───────────────────────────
	//
	// **他プロジェクトの資源を指したら 422。** 別のプロジェクトを立てて確かめる。
	otherKey := key + "b"
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, otherKey); err != nil {
			t.Errorf("2つめのプロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec = postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"よそのプロジェクト","workflow_template":"simple"}`, otherKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("2つめのプロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	rec = postWithCookie(r, "/api/v1/projects/"+otherKey+"/tags", session, `{"name":"よそのタグ"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("よそのタグ作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	foreignTagID := viewOf(t, rec)["id"].(string)

	invalid := []struct {
		name string
		body string
		code string
	}{
		{"よそのタグ", fmt.Sprintf(`{"type":"task","title":"x","tag_ids":[%q]}`, foreignTagID), "not_found"},
		{"無い親", `{"type":"task","title":"x","parent_seq":9999}`, "not_found"},
		{"非メンバーの担当", `{"type":"task","title":"x","assignee_id":"01K2NOTAMEMBER00000000001"}`, "not_a_member"},
	}
	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			rec := postWithCookie(r, base+"/tickets", session, c.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), c.code) {
				t.Errorf("details[].code に %s が無い: %s", c.code, rec.Body.String())
			}
		})
	}

	// **採番は失敗しても進まない。** 上の3件はいずれも 422 で終わっており、
	// project_counter は動いていない……ではなく、参照先の検証を採番より前に
	// 置いてあるかを実測で見る。
	next := createTicketIT(t, r, session, base, `{"type":"task","title":"採番の確認"}`)
	if got := next["seq"].(float64); got != 6 {
		t.Errorf("次の seq = %v, want 6（422 で終わった要求は採番を消費しない）", got)
	}

	// ── ⑩ 他プロジェクトのチケットは見えない ────────────────
	if got := ticketSeqs(t, r, session, "/api/v1/projects/"+otherKey, ""); len(got) != 0 {
		t.Errorf("よそのプロジェクトに %v が見えている（project_id で閉じていない）", got)
	}
	rec = postWithCookie(r, "/api/v1/projects/"+otherKey+"/tickets/1/move", session, `{"position":"last"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("よその番号を指した move の status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}

	_ = grandchild
	_ = loner
	_ = noPriority
}

// createTicketIT はチケットを1件作り、応答を返す。
func createTicketIT(t *testing.T, r http.Handler, session, base, body string) map[string]any {
	t.Helper()
	rec := postWithCookie(r, base+"/tickets", session, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("チケット作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/tickets/") {
		t.Errorf("Location = %q", loc)
	}
	return viewOf(t, rec)
}

// ticketSeqs は一覧を引いて seq の並びを返す。
//
// **並び順の正本はサーバである**（LEARNINGS #25）。検証側で並べ直さず、
// 応答に現れた順をそのまま比べる。
func ticketSeqs(t *testing.T, r http.Handler, session, base, query string) []int {
	t.Helper()
	rec := getWithCookie(r, base+"/tickets"+query, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	items, _ := viewOf(t, rec)["items"].([]any)
	out := make([]int, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		out = append(out, int(m["seq"].(float64)))
	}
	return out
}

func equalInts(a, b []int) bool {
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
