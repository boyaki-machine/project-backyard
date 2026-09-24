package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// コメント・DoD・関連チケット（ApiDesign.md 9.8 / 9.9 / 9.10.1）を
// **実際のDBに対して**通す。手順18a。
//
// 単体テストはフェイクを差し替えるので queries/comment.sql・dod.sql・link.sql は
// 一度も実行されない。**ここでしか確かめられないものが7つある。**
//
//   - **ORDER BY / LIMIT / OFFSET が DB のものであること**（コメントの並びと
//     ページング。フェイクは Go 側で写しているだけ）
//   - **UNION ALL の双方向が実際に両側から拾えること**（9.10.1）。フェイクは
//     行を1つのスライスに置いているだけで、source / target の区別を通らない
//   - **uq_ticket_link が実在すること**（アプリ側の重複検査を外しても DB が止める）
//   - **ck_ticket_link_diff が実在すること**（自己リンク）
//   - **UpdateDoDItem の satisfied_set が3列を撃ち分けること**（CASE WHEN は SQL）
//   - **チケットを消すと3つとも CASCADE で消えること**（FK の挙動は DB のもの）
//   - **論理削除が updated_at を動かすこと**（trg_comment_updated）——ETag が
//     削除で変わる根拠がここにある
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`
// （Development.md 6.1。接続文字列を手で書かない）。
func TestCommentDoDLinkIntegration(t *testing.T) {
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
	adminEmail := "cdl-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "cdl-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"コメント結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	// 主役のチケットと、リンクの相手を2件作る。
	mainSeq, mainID := createIntegrationTicket(t, r, session, base, "認証APIの実装")
	otherSeq, otherID := createIntegrationTicket(t, r, session, base, "DB設計")
	thirdSeq, _ := createIntegrationTicket(t, r, session, base, "ログイン画面")

	comments := fmt.Sprintf("%s/tickets/%d/comments", base, mainSeq)
	dod := fmt.Sprintf("%s/tickets/%d/dod", base, mainSeq)
	links := fmt.Sprintf("%s/tickets/%d/links", base, mainSeq)

	// ── コメント（9.8）──────────────────────────────────────

	var firstCommentID, secondCommentID string

	t.Run("POST はコメントを作る", func(t *testing.T) {
		rec := postWithCookie(r, comments, session,
			`{"body_md":"レビューをお願いします","kind":"progress"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		firstCommentID = v["id"].(string)
		if v["origin"] != "human" {
			t.Errorf("origin = %v, want human", v["origin"])
		}
		author, _ := v["author"].(map[string]any)
		if author == nil || author["id"] != adminID {
			t.Errorf("author = %v, want 呼び出し元 %s", v["author"], adminID)
		}
		if v["deleted_at"] != nil {
			t.Errorf("deleted_at = %v, want null", v["deleted_at"])
		}
	})

	t.Run("POST は返信を作れる", func(t *testing.T) {
		rec := postWithCookie(r, comments, session,
			fmt.Sprintf(`{"body_md":"確認しました","in_reply_to":%q}`, firstCommentID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		secondCommentID = v["id"].(string)
		if v["in_reply_to"] != firstCommentID {
			t.Errorf("in_reply_to = %v, want %s", v["in_reply_to"], firstCommentID)
		}
		if v["kind"] != "discussion" {
			t.Errorf("kind = %v, want discussion（既定）", v["kind"])
		}
	})

	// **効くことを先に確かめてから、断られる側を測る**（LEARNINGS #35）。
	t.Run("POST は他チケットのコメントへの返信を拒む", func(t *testing.T) {
		other := fmt.Sprintf("%s/tickets/%d/comments", base, otherSeq)
		rec := postWithCookie(r, other, session,
			fmt.Sprintf(`{"body_md":"別チケットから返信","in_reply_to":%q}`, firstCommentID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "not_found") {
			t.Errorf("details[].code に not_found が無い: %s", rec.Body.String())
		}
	})

	t.Run("GET は古い順で、ETag と total を返す", func(t *testing.T) {
		rec := getWithCookie(r, comments, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		items, _ := v["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("items = %d件, want 2（body=%s）", len(items), rec.Body.String())
		}
		// **並びは DB の ORDER BY である。**
		if items[0].(map[string]any)["id"] != firstCommentID {
			t.Errorf("先頭 = %v, want %s（created_at 昇順）",
				items[0].(map[string]any)["id"], firstCommentID)
		}
		if v["total"].(float64) != 2 {
			t.Errorf("total = %v, want 2", v["total"])
		}
		if v["per_page"].(float64) != 50 {
			t.Errorf("per_page = %v, want 50（9.8 の既定）", v["per_page"])
		}
		if rec.Header().Get("ETag") == "" {
			t.Error("ETag が空")
		}
	})

	t.Run("GET は order=desc で逆順になる", func(t *testing.T) {
		rec := getWithCookie(r, comments+"?order=desc", session)
		items, _ := viewOf(t, rec)["items"].([]any)
		if len(items) != 2 || items[0].(map[string]any)["id"] != secondCommentID {
			t.Fatalf("先頭 = %v, want %s（desc）", items[0], secondCommentID)
		}
	})

	// **LIMIT / OFFSET が効くこと。** フェイクは Go 側で切っているだけである。
	t.Run("GET はページングする", func(t *testing.T) {
		rec := getWithCookie(r, comments+"?per_page=1&page=2", session)
		v := viewOf(t, rec)
		items, _ := v["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("items = %d件, want 1", len(items))
		}
		if items[0].(map[string]any)["id"] != secondCommentID {
			t.Errorf("2ページ目 = %v, want %s", items[0], secondCommentID)
		}
		if v["total"].(float64) != 2 || v["total_pages"].(float64) != 2 {
			t.Errorf("total/total_pages = %v/%v, want 2/2", v["total"], v["total_pages"])
		}
	})

	t.Run("PATCH は本文と kind を変える", func(t *testing.T) {
		rec := patchProjectWithCookie(r, comments+"/"+secondCommentID, session, "",
			`{"body_md":"確認しました（修正）","kind":"decision"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["body_md"] != "確認しました（修正）" || v["kind"] != "decision" {
			t.Errorf("応答 = %v", v)
		}
	})

	// **論理削除が updated_at を動かすこと**（trg_comment_updated）。
	// ETag が削除で変わる根拠がここにある。
	t.Run("DELETE は論理削除で、行は残り updated_at が動く", func(t *testing.T) {
		before := commentUpdatedAt(t, pool, secondCommentID)
		etagBefore := etagOf(t, r, comments, session)

		rec := deleteWithCookie(r, comments+"/"+secondCommentID, session)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}

		after := commentUpdatedAt(t, pool, secondCommentID)
		if !after.After(before) {
			t.Errorf("updated_at = %v, want %v より後（trg_comment_updated）", after, before)
		}
		if etagAfter := etagOf(t, r, comments, session); etagAfter == etagBefore {
			t.Errorf("削除で ETag が変わらない: %s", etagBefore)
		}

		// items に残り、body_md が null になる（9.8）。
		v := viewOf(t, getWithCookie(r, comments, session))
		items, _ := v["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("items = %d件, want 2（削除済みも残す）", len(items))
		}
		deleted, _ := items[1].(map[string]any)
		if deleted["body_md"] != nil {
			t.Errorf("body_md = %v, want null", deleted["body_md"])
		}
		if deleted["deleted_at"] == nil {
			t.Error("deleted_at が null")
		}
		if v["total"].(float64) != 2 {
			t.Errorf("total = %v, want 2（削除済みも数える）", v["total"])
		}
	})

	// **9.5.1 の comment_count は deleted_at IS NULL で数える**（9.8 とは答えが違う）。
	t.Run("comment_count は削除済みを数えない", func(t *testing.T) {
		v := viewOf(t, getWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, mainSeq), session))
		if v["comment_count"].(float64) != 1 {
			t.Errorf("comment_count = %v, want 1（削除済みを除く。一覧の total は 2）",
				v["comment_count"])
		}
	})

	t.Run("DELETE を2回目は 404", func(t *testing.T) {
		rec := deleteWithCookie(r, comments+"/"+secondCommentID, session)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// ── 完了条件（9.9）──────────────────────────────────────

	var dodID, dodID2 string

	t.Run("POST は完了条件を作り、末尾へ置く", func(t *testing.T) {
		rec := postWithCookie(r, dod, session, `{"body":"ユニットテストが通ること"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		dodID = v["id"].(string)
		if v["type"] != "manual" {
			t.Errorf("type = %v, want manual", v["type"])
		}
		if v["sort_order"].(float64) != 10 {
			t.Errorf("sort_order = %v, want 10（最初の1件）", v["sort_order"])
		}

		rec = postWithCookie(r, dod, session, `{"body":"設計文書を更新すること"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("2件目の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v = viewOf(t, rec)
		dodID2 = v["id"].(string)
		// **NextDoDSortOrder が「最大値 + 10」を返すこと**（SQL の COALESCE(max)）。
		if v["sort_order"].(float64) != 20 {
			t.Errorf("sort_order = %v, want 20（最大値 + 10）", v["sort_order"])
		}
	})

	t.Run("POST は manual 以外の type を拒む", func(t *testing.T) {
		rec := postWithCookie(r, dod, session, `{"type":"assertion","body":"pytest が通る"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "unsupported_type") {
			t.Errorf("details[].code に unsupported_type が無い: %s", rec.Body.String())
		}
	})

	// **satisfied_set が3列を撃ち分けること**（CASE WHEN は SQL 側にある）。
	t.Run("PATCH のチェックは satisfied_at と satisfied_by を同時に立てる", func(t *testing.T) {
		rec := patchProjectWithCookie(r, dod+"/"+dodID, session, "", `{"is_satisfied":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["is_satisfied"] != true || v["satisfied_at"] == nil {
			t.Fatalf("3列が揃っていない: %v", v)
		}
		by, _ := v["satisfied_by"].(map[string]any)
		if by == nil || by["id"] != adminID {
			t.Errorf("satisfied_by = %v, want %s", v["satisfied_by"], adminID)
		}
	})

	t.Run("PATCH のチェック解除は両方を NULL へ戻す", func(t *testing.T) {
		rec := patchProjectWithCookie(r, dod+"/"+dodID, session, "", `{"is_satisfied":false}`)
		v := viewOf(t, rec)
		if v["is_satisfied"] != false || v["satisfied_at"] != nil || v["satisfied_by"] != nil {
			t.Fatalf("戻っていない: %v", v)
		}
	})

	// **本文だけを変えても充足状態が壊れないこと**（satisfied_set=false の側）。
	t.Run("PATCH の本文だけの変更は充足状態を触らない", func(t *testing.T) {
		if rec := patchProjectWithCookie(r, dod+"/"+dodID2, session, "",
			`{"is_satisfied":true}`); rec.Code != http.StatusOK {
			t.Fatalf("前提のチェックに失敗: %d（%s）", rec.Code, rec.Body.String())
		}
		rec := patchProjectWithCookie(r, dod+"/"+dodID2, session, "",
			`{"body":"設計文書を更新すること（改訂）"}`)
		v := viewOf(t, rec)
		if v["is_satisfied"] != true || v["satisfied_at"] == nil {
			t.Errorf("充足状態が落ちた: %v", v)
		}
		if v["body"] != "設計文書を更新すること（改訂）" {
			t.Errorf("body = %v", v["body"])
		}
	})

	// **並びは DB の ORDER BY**（sort_order → created_at）。
	t.Run("GET は sort_order の昇順", func(t *testing.T) {
		items, _ := viewOf(t, getWithCookie(r, dod, session))["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("items = %d件, want 2", len(items))
		}
		if items[0].(map[string]any)["id"] != dodID {
			t.Errorf("先頭 = %v, want %s", items[0], dodID)
		}
	})

	// ── 関連チケット（9.10.1）────────────────────────────────

	var linkID string

	t.Run("POST はリンクを作る", func(t *testing.T) {
		rec := postWithCookie(r, links, session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":"blocks"}`, otherSeq))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		linkID = v["id"].(string)
		if v["direction"] != "outgoing" {
			t.Errorf("direction = %v, want outgoing", v["direction"])
		}
		ticket, _ := v["ticket"].(map[string]any)
		if ticket == nil || ticket["seq"].(float64) != float64(otherSeq) {
			t.Errorf("ticket = %v, want seq=%d", v["ticket"], otherSeq)
		}
		if v["origin"] != "human" {
			t.Errorf("origin = %v, want human", v["origin"])
		}
	})

	// **uq_ticket_link が実在すること。**
	t.Run("POST の重複は 409", func(t *testing.T) {
		rec := postWithCookie(r, links, session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":"blocks"}`, otherSeq))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "already_exists") {
			t.Errorf("error.code = %s, want already_exists", rec.Body.String())
		}
	})

	// **ck_ticket_link_diff が実在すること**（アプリ側の検査も同時に効いている）。
	t.Run("POST の自己リンクは 422", func(t *testing.T) {
		rec := postWithCookie(r, links, session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":"relates"}`, mainSeq))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "self_link") {
			t.Errorf("details[].code に self_link が無い: %s", rec.Body.String())
		}
	})

	// **UNION ALL の双方向。** 相手側から見ると incoming になる。
	t.Run("GET は相手側から incoming に見える", func(t *testing.T) {
		otherLinks := fmt.Sprintf("%s/tickets/%d/links", base, otherSeq)
		items, _ := viewOf(t, getWithCookie(r, otherLinks, session))["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("items = %d件, want 1", len(items))
		}
		v, _ := items[0].(map[string]any)
		if v["direction"] != "incoming" {
			t.Errorf("direction = %v, want incoming", v["direction"])
		}
		// **ticket に入るのは相手（＝主役のチケット）であって自分ではない。**
		ticket, _ := v["ticket"].(map[string]any)
		if ticket["seq"].(float64) != float64(mainSeq) {
			t.Errorf("ticket.seq = %v, want %d", ticket["seq"], mainSeq)
		}
	})

	t.Run("GET は outgoing を先に並べる", func(t *testing.T) {
		// 主役から3件目へ relates を張り、双方向が同じ一覧に並ぶ形を作る。
		third := fmt.Sprintf("%s/tickets/%d/links", base, thirdSeq)
		if rec := postWithCookie(r, third, session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":"relates"}`, mainSeq)); rec.Code != http.StatusCreated {
			t.Fatalf("前提のリンク作成に失敗: %d（%s）", rec.Code, rec.Body.String())
		}
		items, _ := viewOf(t, getWithCookie(r, links, session))["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("items = %d件, want 2（双方向）", len(items))
		}
		if items[0].(map[string]any)["direction"] != "outgoing" ||
			items[1].(map[string]any)["direction"] != "incoming" {
			t.Errorf("並び = %v, %v", items[0], items[1])
		}
	})

	// **incoming も同じエンドポイントから消せること**（9.10.1）。
	t.Run("DELETE は incoming も消せる", func(t *testing.T) {
		items, _ := viewOf(t, getWithCookie(r, links, session))["items"].([]any)
		var incomingID string
		for _, it := range items {
			v, _ := it.(map[string]any)
			if v["direction"] == "incoming" {
				incomingID = v["id"].(string)
			}
		}
		if incomingID == "" {
			t.Fatal("incoming の行が見つからない")
		}
		rec := deleteWithCookie(r, links+"/"+incomingID, session)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("DELETE は outgoing を消す", func(t *testing.T) {
		rec := deleteWithCookie(r, links+"/"+linkID, session)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}
		items, _ := viewOf(t, getWithCookie(r, links, session))["items"].([]any)
		if len(items) != 0 {
			t.Errorf("items = %d件, want 0", len(items))
		}
	})

	// ── activity（9.1.1）────────────────────────────────────

	// **業務トランザクションと同時に確定すること。** 3つの field が並ぶ。
	t.Run("activity に3つの field が並ぶ", func(t *testing.T) {
		rows := activityRows(t, pool, mainID, "update")
		got := map[string]int{}
		for _, a := range rows {
			got[a.field]++
		}
		for _, field := range []string{"comment", "dod", "link"} {
			if got[field] == 0 {
				t.Errorf("field=%s の行が無い（全体=%v）", field, got)
			}
		}
		// **action='delete' を使っていないこと**（チケットごと消えたように見える）。
		if del := activityRows(t, pool, mainID, "delete"); len(del) != 0 {
			t.Errorf("action='delete' が %d行ある（子資源の削除に使ってはならない）", len(del))
		}
	})

	// **相手のチケットの履歴には書かない**（9.10.1）。
	t.Run("リンクの相手側には activity を書かない", func(t *testing.T) {
		for _, a := range activityRows(t, pool, otherID, "update") {
			if a.field == "link" {
				t.Errorf("相手側に field=link の行がある: %+v", a)
			}
		}
	})

	// ── CASCADE（DbDesign.md 6.6 / 6.7 / 6.11）───────────────

	// **チケットを消すと3つとも消えること。** FK の挙動は DB のものであり、
	// フェイクでは一度も通らない。
	t.Run("チケットを消すと3つとも CASCADE で消える", func(t *testing.T) {
		delSeq, delID := createIntegrationTicket(t, r, session, base, "消されるチケット")
		delBase := fmt.Sprintf("%s/tickets/%d", base, delSeq)

		if rec := postWithCookie(r, delBase+"/comments", session,
			`{"body_md":"消える"}`); rec.Code != http.StatusCreated {
			t.Fatalf("コメント作成に失敗: %d（%s）", rec.Code, rec.Body.String())
		}
		if rec := postWithCookie(r, delBase+"/dod", session,
			`{"body":"消える条件"}`); rec.Code != http.StatusCreated {
			t.Fatalf("完了条件の作成に失敗: %d（%s）", rec.Code, rec.Body.String())
		}
		if rec := postWithCookie(r, delBase+"/links", session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":"relates"}`, mainSeq)); rec.Code != http.StatusCreated {
			t.Fatalf("リンク作成に失敗: %d（%s）", rec.Code, rec.Body.String())
		}

		if rec := deleteWithCookie(r, delBase, session); rec.Code != http.StatusNoContent {
			t.Fatalf("チケット削除の status = %d（%s）", rec.Code, rec.Body.String())
		}

		for _, c := range []struct {
			table string
			col   string
		}{
			{"comment", "ticket_id"},
			{"dod_item", "ticket_id"},
			{"ticket_link", "source_ticket_id"},
		} {
			var n int
			if err := pool.QueryRow(ctx,
				fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, c.table, c.col),
				delID).Scan(&n); err != nil {
				t.Fatalf("%s を数えられない: %v", c.table, err)
			}
			if n != 0 {
				t.Errorf("%s に %d行 残っている（CASCADE が効いていない）", c.table, n)
			}
		}
	})
}

// createIntegrationTicket はチケットを1件作り、seq と id を返す。
func createIntegrationTicket(
	t *testing.T, r http.Handler, session, base, title string,
) (int, string) {
	t.Helper()
	rec := postWithCookie(r, base+"/tickets", session,
		fmt.Sprintf(`{"type":"task","title":%q}`, title))
	if rec.Code != http.StatusCreated {
		t.Fatalf("チケット %q の作成 status = %d（body=%s）", title, rec.Code, rec.Body.String())
	}
	v := viewOf(t, rec)
	return int(v["seq"].(float64)), v["id"].(string)
}

// commentUpdatedAt は comment.updated_at を1つ読む。
//
// **応答からは読めない。** 9.8 の応答は秒精度（ApiDesign.md 2.2）で、同じ秒に
// 更新すると文字列が変わらない。トリガ（trg_comment_updated）が動いたかを
// 測るには、列をマイクロ秒のまま直接見る必要がある。
func commentUpdatedAt(t *testing.T, pool *pgxpool.Pool, id string) time.Time {
	t.Helper()
	var at time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT updated_at FROM comment WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("comment.updated_at を読めない: %v", err)
	}
	return at
}

// etagOf は一覧の ETag を1つ読む。
func etagOf(t *testing.T, r http.Handler, path, session string) string {
	t.Helper()
	rec := getWithCookie(r, path, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("ETag を読むための GET が %d（body=%s）", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("ETag")
}
