package v1

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// チケット1件のAPI（ApiDesign.md 9.5 / 9.6 / 9.7）を**実際のDBに対して**通す。手順17a。
//
// 単体テストはフェイクを差し替えるので queries/ticket.sql と workflow.sql は
// 一度も実行されない。**ここでしか確かめられないものが7つある。**
//
//   - UpdateTicket の COALESCE と CASE WHEN ..._set が、「据え置き」と「NULL にする」を
//     実際に撃ち分けること（フェイクは Go 側で写しているだけで SQL を通らない）
//   - **楽観ロックが WHERE version = ... で成り立つこと**（0行返しが 409 になる）
//   - IsTicketDescendant の再帰CTEが孫まで届くこと
//   - **タグの付け外しで updated_at が動くこと**（トリガは DB にしかない）
//   - closed_at が category='done' の遷移でだけ立ち、戻すと NULL へ帰ること
//   - **子が ON DELETE SET NULL で残ること**（FK の挙動は DB のもの）
//   - activity と comment が業務トランザクションと同時に確定すること
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`
// （Development.md 6.1。接続文字列を手で書かない）。
func TestTicketDetailIntegration(t *testing.T) {
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
	adminEmail := "tkd-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	// **with_review を使う。** simple では in_progress → done が定義されているため、
	// 9.7 の「定義が無い先も返す」と 9.6 の検証2 を測れない（DbDesign.md 7.4）。
	key := "td-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"チケット詳細結合テスト","workflow_template":"with_review"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	rec = postWithCookie(r, base+"/tags", session, `{"name":"設計"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("タグ作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	tagID := viewOf(t, rec)["id"].(string)

	// ── 9.5.1 GET ───────────────────────────────────────────
	t.Run("GET は詳細の形を返す", func(t *testing.T) {
		parent := createTicketIT(t, r, session, base, `{"type":"epic","title":"認証"}`)
		parentSeq := int(parent["seq"].(float64))
		child := createTicketIT(t, r, session, base,
			fmt.Sprintf(`{"type":"task","title":"ログインAPI","parent_seq":%d,"body_md":"# 概要"}`, parentSeq))
		childSeq := int(child["seq"].(float64))

		got := getTicketIT(t, r, session, base, childSeq)
		if got["body_md"] != "# 概要" {
			t.Errorf("body_md = %v", got["body_md"])
		}
		p, _ := got["parent"].(map[string]any)
		if p == nil || int(p["seq"].(float64)) != parentSeq {
			t.Errorf("parent = %v, want seq=%d", got["parent"], parentSeq)
		}
		if got["comment_count"] != float64(0) {
			t.Errorf("comment_count = %v, want 0", got["comment_count"])
		}

		// 親から見ると children に子が1件出る（孫は含めない）。
		gotParent := getTicketIT(t, r, session, base, parentSeq)
		children, _ := gotParent["children"].([]any)
		if len(children) != 1 {
			t.Fatalf("children = %v, want 1件", gotParent["children"])
		}
		if int(children[0].(map[string]any)["seq"].(float64)) != childSeq {
			t.Errorf("children[0].seq = %v, want %d", children[0], childSeq)
		}

		missing := getWithCookie(r, base+"/tickets/99999", session)
		if missing.Code != http.StatusNotFound {
			t.Errorf("存在しない番号の status = %d, want 404", missing.Code)
		}
	})

	// ── 9.5.2 PATCH：据え置きと NULL の撃ち分け ─────────────────
	t.Run("PATCHは送った項目だけを更新する", func(t *testing.T) {
		created := createTicketIT(t, r, session, base,
			`{"type":"task","title":"部分更新","priority":"high","estimate_point":5,"due_date":"2026-09-30"}`)
		seq := int(created["seq"].(float64))

		// ① title だけ送る → priority と due_date は据え置き。
		got := patchTicketIT(t, r, session, base, seq, `"1"`, `{"title":"部分更新（改）"}`)
		if got["title"] != "部分更新（改）" {
			t.Errorf("title = %v", got["title"])
		}
		if got["priority"] != "high" {
			t.Errorf("priority = %v, want high（送っていないので据え置き）", got["priority"])
		}
		if got["due_date"] != "2026-09-30" {
			t.Errorf("due_date = %v, want 2026-09-30（据え置き）", got["due_date"])
		}
		if got["version"] != float64(2) {
			t.Errorf("version = %v, want 2", got["version"])
		}

		// ② null を送る → 空になる。**据え置きと撃ち分けられること。**
		got = patchTicketIT(t, r, session, base, seq, `"2"`,
			`{"priority":null,"due_date":null,"estimate_point":null}`)
		for _, field := range []string{"priority", "due_date", "estimate_point"} {
			if got[field] != nil {
				t.Errorf("%s = %v, want null（null は空にする）", field, got[field])
			}
		}
	})

	t.Run("PATCHの楽観ロック", func(t *testing.T) {
		created := createTicketIT(t, r, session, base, `{"type":"task","title":"楽観ロック"}`)
		seq := int(created["seq"].(float64))
		path := fmt.Sprintf("%s/tickets/%d", base, seq)

		// **先に「合っていれば通る」ことを見る。**
		ok := patchProjectWithCookie(r, path, session, `"1"`, `{"title":"1回目"}`)
		if ok.Code != http.StatusOK {
			t.Fatalf("version 一致の status = %d, want 200（body=%s）", ok.Code, ok.Body.String())
		}

		stale := patchProjectWithCookie(r, path, session, `"1"`, `{"title":"2回目"}`)
		if stale.Code != http.StatusConflict {
			t.Errorf("古い If-Match の status = %d, want 409（body=%s）", stale.Code, stale.Body.String())
		}

		none := patchProjectWithCookie(r, path, session, "", `{"title":"3回目"}`)
		if none.Code != http.StatusUnprocessableEntity {
			t.Errorf("If-Match 無しの status = %d, want 422（body=%s）", none.Code, none.Body.String())
		}
		if !hasDetail(errorOf(t, none), "If-Match", "required") {
			t.Errorf("details = %v, want If-Match/required", errorOf(t, none).Details)
		}
	})

	// ── 9.5.2 PATCH：タグと updated_at（引き継ぎの要件）──────────
	//
	// **突き合わせるのは一覧の ETag である**（LEARNINGS #25）。要件は
	// 「タグだけ変えたときに一覧の ETag が変わらず 304 が返り続ける」を防ぐこと
	// なので、ETag そのものを見る。**応答の updated_at では測れない**——
	// 2.2 に従って秒精度で出しているため、同じ秒のうちに更新すると文字列が
	// 変わらない（ETag は UnixNano を使うので変わる）。
	t.Run("タグだけ変えても一覧のETagが変わる", func(t *testing.T) {
		created := createTicketIT(t, r, session, base, `{"type":"task","title":"タグと更新時刻"}`)
		seq := int(created["seq"].(float64))
		ticketID := created["id"].(string)

		beforeETag := ticketsETagOf(t, r, session, base)
		beforeStamp := scalarString(t, pool,
			`SELECT updated_at::text FROM ticket WHERE id = $1`, ticketID)

		got := patchTicketIT(t, r, session, base, seq, `"1"`,
			fmt.Sprintf(`{"tag_ids":[%q]}`, tagID))
		tags, _ := got["tags"].([]any)
		if len(tags) != 1 {
			t.Fatalf("tags = %v, want 1件", got["tags"])
		}

		if after := ticketsETagOf(t, r, session, base); after == beforeETag {
			t.Errorf("一覧の ETag が変わっていない（%s のまま。304 が返り続ける）", after)
		}
		// DB 側でも updated_at が動いていること（トリガが効いている）。
		if after := scalarString(t, pool,
			`SELECT updated_at::text FROM ticket WHERE id = $1`, ticketID); after == beforeStamp {
			t.Errorf("updated_at = %q のまま（トリガが動いていない）", after)
		}

		// 空配列で全て外れる。
		got = patchTicketIT(t, r, session, base, seq, `"2"`, `{"tag_ids":[]}`)
		if tags, _ := got["tags"].([]any); len(tags) != 0 {
			t.Errorf("tags = %v, want 0件", got["tags"])
		}
	})

	// ── 9.5.2 PATCH：parent_cycle（再帰CTE）──────────────────
	t.Run("親に子孫を指定すると422", func(t *testing.T) {
		gp := createTicketIT(t, r, session, base, `{"type":"epic","title":"祖父"}`)
		gpSeq := int(gp["seq"].(float64))
		parent := createTicketIT(t, r, session, base,
			fmt.Sprintf(`{"type":"story","title":"親","parent_seq":%d}`, gpSeq))
		parentSeq := int(parent["seq"].(float64))
		child := createTicketIT(t, r, session, base,
			fmt.Sprintf(`{"type":"task","title":"子","parent_seq":%d}`, parentSeq))
		childSeq := int(child["seq"].(float64))

		// **まず「無関係な相手なら親にできる」ことを見る**（LEARNINGS #35）。
		other := createTicketIT(t, r, session, base, `{"type":"epic","title":"別のエピック"}`)
		otherSeq := int(other["seq"].(float64))
		okRec := patchProjectWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, parentSeq),
			session, `"1"`, fmt.Sprintf(`{"parent_seq":%d}`, otherSeq))
		if okRec.Code != http.StatusOK {
			t.Fatalf("無関係な親の status = %d, want 200（body=%s）", okRec.Code, okRec.Body.String())
		}
		// 元へ戻す（以降の子孫関係を保つため）。
		back := patchProjectWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, parentSeq),
			session, `"2"`, fmt.Sprintf(`{"parent_seq":%d}`, gpSeq))
		if back.Code != http.StatusOK {
			t.Fatalf("親を戻せない status = %d（body=%s）", back.Code, back.Body.String())
		}

		// 自分自身。
		self := patchProjectWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, gpSeq),
			session, `"1"`, fmt.Sprintf(`{"parent_seq":%d}`, gpSeq))
		if self.Code != http.StatusUnprocessableEntity {
			t.Errorf("自分自身を親の status = %d, want 422（body=%s）", self.Code, self.Body.String())
		}
		if !hasDetail(errorOf(t, self), "parent_seq", "parent_cycle") {
			t.Errorf("details = %v, want parent_seq/parent_cycle", errorOf(t, self).Details)
		}

		// **孫を親に指定する**——再帰CTEが2段下まで届いていないと通ってしまう。
		grandchild := patchProjectWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, gpSeq),
			session, `"1"`, fmt.Sprintf(`{"parent_seq":%d}`, childSeq))
		if grandchild.Code != http.StatusUnprocessableEntity {
			t.Errorf("孫を親の status = %d, want 422（body=%s）",
				grandchild.Code, grandchild.Body.String())
		}
		if !hasDetail(errorOf(t, grandchild), "parent_seq", "parent_cycle") {
			t.Errorf("details = %v, want parent_seq/parent_cycle", errorOf(t, grandchild).Details)
		}
	})

	// ── 9.5.2 PATCH：受け付けない項目 ─────────────────────────
	t.Run("サーバが決める項目は422", func(t *testing.T) {
		created := createTicketIT(t, r, session, base, `{"type":"task","title":"不変フィールド"}`)
		seq := int(created["seq"].(float64))
		path := fmt.Sprintf("%s/tickets/%d", base, seq)

		for _, c := range []struct{ body, field, code string }{
			{`{"seq":99}`, "seq", "immutable_field"},
			{`{"sort_key":"0|zz:"}`, "sort_key", "use_move_endpoint"},
			{`{"status_key":"done"}`, "status_key", "use_transition_endpoint"},
			{`{"closed_at":"2026-08-01T00:00:00Z"}`, "closed_at", "use_transition_endpoint"},
		} {
			out := patchProjectWithCookie(r, path, session, `"1"`, c.body)
			if out.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s の status = %d, want 422（body=%s）", c.field, out.Code, out.Body.String())
				continue
			}
			if !hasDetail(errorOf(t, out), c.field, c.code) {
				t.Errorf("%s の details = %v, want %s", c.field, errorOf(t, out).Details, c.code)
			}
		}
	})

	// ── 9.5.2 PATCH：オンステージの規則（D-2）──────────────────
	t.Run("オンステージの行は段に置けない形へ変えられない", func(t *testing.T) {
		created := createTicketIT(t, r, session, base, `{"type":"task","title":"段の規則"}`)
		seq := int(created["seq"].(float64))
		path := fmt.Sprintf("%s/tickets/%d", base, seq)

		// **まずバックログにいる間は epic にできることを確かめる**——
		// これを測らないと、下の 422 は「常に弾く」実装でも緑になる。
		up := patchProjectWithCookie(r, path, session, `"1"`, `{"type":"epic"}`)
		if up.Code != http.StatusOK {
			t.Fatalf("バックログでの種別変更の status = %d, want 200（body=%s）", up.Code, up.Body.String())
		}
		back := patchProjectWithCookie(r, path, session, `"2"`, `{"type":"task"}`)
		if back.Code != http.StatusOK {
			t.Fatalf("種別を戻せない status = %d（body=%s）", back.Code, back.Body.String())
		}

		// オンステージへ上げる（9.4.1）。
		moved := postWithCookie(r, path+"/move", session, `{"staged":true,"position":"first"}`)
		if moved.Code != http.StatusOK {
			t.Fatalf("オンステージへの move の status = %d（body=%s）", moved.Code, moved.Body.String())
		}
		version := fmt.Sprintf(`"%d"`, int(viewOf(t, moved)["version"].(float64)))

		// ① epic にできない。
		toEpic := patchProjectWithCookie(r, path, session, version, `{"type":"epic"}`)
		if toEpic.Code != http.StatusUnprocessableEntity {
			t.Errorf("オンステージで epic の status = %d, want 422（body=%s）",
				toEpic.Code, toEpic.Body.String())
		}
		if !hasDetail(errorOf(t, toEpic), "type", "not_stageable") {
			t.Errorf("details = %v, want type/not_stageable", errorOf(t, toEpic).Details)
		}

		// ② エピック以外の子にできない。
		story := createTicketIT(t, r, session, base, `{"type":"story","title":"エピックでない親"}`)
		storySeq := int(story["seq"].(float64))
		toChild := patchProjectWithCookie(r, path, session, version,
			fmt.Sprintf(`{"parent_seq":%d}`, storySeq))
		if toChild.Code != http.StatusUnprocessableEntity {
			t.Errorf("オンステージで非エピックの子 status = %d, want 422（body=%s）",
				toChild.Code, toChild.Body.String())
		}
		if !hasDetail(errorOf(t, toChild), "parent_seq", "not_stageable") {
			t.Errorf("details = %v, want parent_seq/not_stageable", errorOf(t, toChild).Details)
		}

		// ③ **エピックの子にはできる**（9.4.1 と同じ条件であることの確認）。
		epic := createTicketIT(t, r, session, base, `{"type":"epic","title":"エピックの親"}`)
		epicSeq := int(epic["seq"].(float64))
		toEpicChild := patchProjectWithCookie(r, path, session, version,
			fmt.Sprintf(`{"parent_seq":%d}`, epicSeq))
		if toEpicChild.Code != http.StatusOK {
			t.Errorf("オンステージでエピックの子 status = %d, want 200（body=%s）",
				toEpicChild.Code, toEpicChild.Body.String())
		}
	})

	// ── 9.5.2 PATCH：activity の粒度（D-4）────────────────────
	t.Run("activityは変更した項目ごとに1行", func(t *testing.T) {
		created := createTicketIT(t, r, session, base,
			`{"type":"task","title":"履歴の粒度","priority":"low"}`)
		seq := int(created["seq"].(float64))
		ticketID := created["id"].(string)

		got := patchTicketIT(t, r, session, base, seq, `"1"`,
			`{"title":"履歴の粒度（改）","priority":"high","body_md":"本文を書いた"}`)
		if got["title"] != "履歴の粒度（改）" {
			t.Fatalf("title = %v", got["title"])
		}

		rows := activityRows(t, pool, ticketID, "update")
		if len(rows) != 3 {
			t.Fatalf("update の activity = %d件, want 3（title/priority/body_md）: %v", len(rows), rows)
		}
		byField := map[string][2]any{}
		for _, row := range rows {
			byField[row.field] = [2]any{row.oldValue, row.newValue}
		}
		if v, ok := byField["title"]; !ok || v[0] != "履歴の粒度" || v[1] != "履歴の粒度（改）" {
			t.Errorf("title の old/new = %v", v)
		}
		if v, ok := byField["priority"]; !ok || v[0] != "low" || v[1] != "high" {
			t.Errorf("priority の old/new = %v", v)
		}
		// **body_md は値を載せない**（9.5.2）。
		if v, ok := byField["body_md"]; !ok {
			t.Errorf("body_md の行が無い")
		} else if v[0] != nil || v[1] != nil {
			t.Errorf("body_md の old/new = %v, want どちらも NULL", v)
		}

		// 同じ値を送っても増えない。
		if _, err := pool.Exec(ctx, `DELETE FROM activity WHERE entity_id = $1`, ticketID); err != nil {
			t.Fatalf("activity を消せない: %v", err)
		}
		patchTicketIT(t, r, session, base, seq, `"2"`, `{"title":"履歴の粒度（改）"}`)
		if rows := activityRows(t, pool, ticketID, "update"); len(rows) != 0 {
			t.Errorf("同値の更新で activity = %v, want 0件", rows)
		}
	})

	// ── 9.6 transition ──────────────────────────────────────
	t.Run("遷移とclosed_at", func(t *testing.T) {
		created := createTicketIT(t, r, session, base, `{"type":"task","title":"遷移"}`)
		seq := int(created["seq"].(float64))
		ticketID := created["id"].(string)
		path := fmt.Sprintf("%s/tickets/%d/transition", base, seq)

		if created["closed_at"] != nil {
			t.Fatalf("作りたてで closed_at = %v, want null", created["closed_at"])
		}

		// todo → in_progress（定義あり・権限あり）。
		out := postWithCookie(r, path, session, `{"to":"in_progress"}`)
		if out.Code != http.StatusOK {
			t.Fatalf("遷移の status = %d, want 200（body=%s）", out.Code, out.Body.String())
		}
		got := viewOf(t, out)
		if got["status"].(map[string]any)["key"] != "in_progress" {
			t.Errorf("status = %v", got["status"])
		}
		if got["closed_at"] != nil {
			t.Errorf("closed_at = %v, want null（category が done ではない）", got["closed_at"])
		}

		// **in_progress → done は with_review に定義が無い**（409）。
		bad := postWithCookie(r, path, session, `{"to":"done"}`)
		if bad.Code != http.StatusConflict {
			t.Fatalf("定義の無い遷移の status = %d, want 409（body=%s）", bad.Code, bad.Body.String())
		}
		if errorOf(t, bad).Code != "invalid_transition" {
			t.Errorf("code = %q, want invalid_transition", errorOf(t, bad).Code)
		}

		// ワークフローに無いステータスは 422。
		unknown := postWithCookie(r, path, session, `{"to":"archived"}`)
		if unknown.Code != http.StatusUnprocessableEntity {
			t.Errorf("未知のステータスの status = %d, want 422（body=%s）", unknown.Code, unknown.Body.String())
		}
		if !hasDetail(errorOf(t, unknown), "to", "unknown_status") {
			t.Errorf("details = %v, want to/unknown_status", errorOf(t, unknown).Details)
		}

		// **人が遷移しても working_agent は立たない**（9.6。手順26b）。
		// あれは実行者の自己申告で、立てるのはエージェントだけである。
		if wa := got["working_agent"]; wa != nil {
			t.Errorf("人の遷移で working_agent = %v, want null", wa)
		}

		// **検証6 は人には掛からない**（9.6）。このチケットは担当が未割当だが、
		// ticket.transition を持つ人は進められている（直前の 200 がその実測）。
		if created["assignee"] != nil {
			t.Fatalf("前提が崩れている：assignee = %v, want null", created["assignee"])
		}

		// in_progress → review（コメント付き）。
		out = postWithCookie(r, path, session, `{"to":"review","comment":"確認をお願いします"}`)
		if out.Code != http.StatusOK {
			t.Fatalf("review への遷移の status = %d（body=%s）", out.Code, out.Body.String())
		}
		if got := viewOf(t, out)["comment_count"]; got != float64(1) {
			t.Errorf("comment_count = %v, want 1", got)
		}
		kind := scalarString(t, pool,
			`SELECT kind FROM comment WHERE ticket_id = $1`, ticketID)
		if kind != "progress" {
			t.Errorf("コメントの kind = %q, want progress", kind)
		}

		// review → done（category=done なので closed_at が立つ）。
		out = postWithCookie(r, path, session, `{"to":"done"}`)
		if out.Code != http.StatusOK {
			t.Fatalf("done への遷移の status = %d（body=%s）", out.Code, out.Body.String())
		}
		if viewOf(t, out)["closed_at"] == nil {
			t.Errorf("closed_at = null, want 値あり（category=done）")
		}

		// **ここで確かめるのは「立った」ことまで。** 戻して NULL へ帰るところは、
		// 次の副試験が done → in_progress（0026 で足した再オープン）で測る。
		closedAt := scalarString(t, pool,
			`SELECT coalesce(closed_at::text, '') FROM ticket WHERE id = $1`, ticketID)
		if closedAt == "" {
			t.Errorf("DB の closed_at が NULL のまま")
		}

		// activity は transition。
		rows := activityRows(t, pool, ticketID, "transition")
		if len(rows) != 3 {
			t.Fatalf("transition の activity = %d件, want 3: %v", len(rows), rows)
		}
		for _, row := range rows {
			if row.field != "status_key" {
				t.Errorf("field = %q, want status_key", row.field)
			}
		}
	})

	// **0026 が3テンプレートすべてに再オープンを入れたこと**を、複製元の行で測る
	// （DbDesign.md 7.4。pb-69）。**プロジェクトのワークフローはテンプレートの複製**
	// なので、ここが欠けると新しく作るプロジェクトすべてで完了から戻せなくなる。
	t.Run("再オープンはテンプレート3件すべてに入っている", func(t *testing.T) {
		got := scalarInt(t, pool, `
			SELECT count(*) FROM workflow_transition t
			  JOIN workflow w ON w.id = t.workflow_id
			 WHERE w.is_template
			   AND t.from_status_key = 'done' AND t.to_status_key = 'in_progress'
			   AND t.required_permission = 'ticket.close'
			   AND t.allowed_actor_kinds = '["user"]'::jsonb`)
		if got != 3 {
			t.Errorf("テンプレートの再オープン = %d件, want 3（simple / with_review / with_approval）", got)
		}
	})

	// **完了から進行中へ戻せる**（DbDesign.md 7.4「再オープン」。0026。pb-69）。
	//
	// **9.6 の表の下半分——closed_at が NULL へ帰る——を、API から実際に作った完了
	// 状態に対して測る。** 0026 の前はテンプレートが done から出る遷移を1つも持たず、
	// closed_at を UPDATE で直接立ててから todo → in_progress を撃つ「1段下げた」
	// 測り方しかできなかった（LEARNINGS #37）。**いまは本物の経路がある。**
	t.Run("完了から進行中へ戻すとclosed_atがNULLへ帰る", func(t *testing.T) {
		created := createTicketIT(t, r, session, base, `{"type":"task","title":"再オープン"}`)
		seq := int(created["seq"].(float64))
		ticketID := created["id"].(string)
		path := fmt.Sprintf("%s/tickets/%d/transition", base, seq)

		// with_review の順路で完了まで進める。
		for _, to := range []string{"in_progress", "review", "done"} {
			out := postWithCookie(r, path, session, fmt.Sprintf(`{"to":%q}`, to))
			if out.Code != http.StatusOK {
				t.Fatalf("%s への遷移の status = %d（body=%s）", to, out.Code, out.Body.String())
			}
		}
		if scalarString(t, pool,
			`SELECT coalesce(closed_at::text,'') FROM ticket WHERE id = $1`, ticketID) == "" {
			t.Fatalf("前提が作れていない（closed_at が NULL のまま）")
		}

		// **done → in_progress。** 0026 が入れた行が無ければ検証2 で 409 になる。
		out := postWithCookie(r, path, session, `{"to":"in_progress"}`)
		if out.Code != http.StatusOK {
			t.Fatalf("再オープンの status = %d, want 200（body=%s）", out.Code, out.Body.String())
		}
		got := viewOf(t, out)
		if got["status"].(map[string]any)["key"] != "in_progress" {
			t.Errorf("status = %v, want in_progress", got["status"])
		}
		if got["closed_at"] != nil {
			t.Errorf("応答の closed_at = %v, want null", got["closed_at"])
		}
		if left := scalarString(t, pool,
			`SELECT coalesce(closed_at::text,'') FROM ticket WHERE id = $1`, ticketID); left != "" {
			t.Errorf("DB の closed_at = %q, want NULL", left)
		}
	})

	// **再オープンに要るのは ticket.transition ではなく ticket.close である**
	// （DbDesign.md 7.4。pb-69）。
	//
	// **プロジェクトロールでは測れない。** 実効権限はシステムロールとプロジェクト
	// ロールの和であり（auth.EffectivePermissions）、システムロールは operator と
	// administrator の2つしかなく、**どちらも 7.3 で ticket.close を持つ**。つまり
	// いまの PB に「ticket.close を持たない人」は存在しない（Design.md 付録A の論点②）。
	//
	// **scope を絞ったトークンが唯一の観測点である**（ApiDesign.md 4.4.2。scope は
	// 実効権限を絞り込む）。ticket.transition だけを載せたトークンは
	// **todo → in_progress は通るのに、完了 → 進行中では 403 になる**——これが
	// 「閉じられる人だけが開け直せる」の実測であり、差し戻し遷移と扱いを分けた
	// ことの証跡でもある。
	t.Run("ticket.closeを持たないトークンは再オープンできない", func(t *testing.T) {
		rec := bodyWithCookie(r, http.MethodPost, "/api/v1/me/tokens", session,
			`{"name":"pb-69 再オープンの実測","expires_in_days":30,`+
				`"scopes":["project.view","ticket.view","ticket.transition"]}`, "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("トークン発行の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var issued tokenJSON
		decodeJSONBody(t, rec, &issued)
		t.Cleanup(func() {
			del := bodyWithCookie(r, http.MethodDelete,
				"/api/v1/me/tokens/"+issued.ID, session, "", "")
			if del.Code != http.StatusNoContent {
				t.Errorf("トークンの後始末に失敗した: status = %d", del.Code)
			}
		})

		created := createTicketIT(t, r, session, base, `{"type":"task","title":"再オープンの権限"}`)
		seq := int(created["seq"].(float64))
		path := fmt.Sprintf("%s/tickets/%d/transition", base, seq)

		// **同じトークンで todo → in_progress は通る。** 落ちているのが
		// ticket.close であって、トークンそのものではないことの対照である。
		if out := bearerPost(r, path, issued.Token, `{"to":"in_progress"}`); out.Code != http.StatusOK {
			t.Fatalf("todo → in_progress の status = %d, want 200（body=%s）",
				out.Code, out.Body.String())
		}

		// 完了まではセッション（ticket.close を持つ）で進める。
		for _, to := range []string{"review", "done"} {
			if out := postWithCookie(r, path, session, fmt.Sprintf(`{"to":%q}`, to)); out.Code != http.StatusOK {
				t.Fatalf("%s への遷移の status = %d（body=%s）", to, out.Code, out.Body.String())
			}
		}

		// **403 であって 409 ではない。** 順路はあるが、このトークンでは通れない（9.6）。
		out := bearerPost(r, path, issued.Token, `{"to":"in_progress"}`)
		if out.Code != http.StatusForbidden {
			t.Fatalf("再オープンの status = %d, want 403（body=%s）", out.Code, out.Body.String())
		}

		// **9.7 は同じ判定を通す**ので、選択肢では allowed=false と理由が並ぶ。
		// 画面が出した選択肢が押した瞬間に断られることがない、という保証である。
		list := bearerGet(r, fmt.Sprintf("%s/tickets/%d/transitions", base, seq), issued.Token)
		if list.Code != http.StatusOK {
			t.Fatalf("選択肢の status = %d（body=%s）", list.Code, list.Body.String())
		}
		items, _ := viewOf(t, list)["items"].([]any)
		found := false
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m["key"] != "in_progress" {
				continue
			}
			found = true
			if m["allowed"] != false {
				t.Errorf("in_progress = %v, want allowed=false", m)
			}
			if reason, _ := m["reason"].(string); !strings.Contains(reason, "ticket.close") {
				t.Errorf("reason = %q, want ticket.close を含む", reason)
			}
		}
		if !found {
			t.Errorf("選択肢に in_progress が無い: %v", items)
		}
	})

	// ── 9.7 transitions ─────────────────────────────────────
	t.Run("遷移先の一覧は全ステータスを返す", func(t *testing.T) {
		created := createTicketIT(t, r, session, base, `{"type":"task","title":"遷移先の一覧"}`)
		seq := int(created["seq"].(float64))
		path := fmt.Sprintf("%s/tickets/%d/transitions", base, seq)

		out := getWithCookie(r, path, session)
		if out.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", out.Code, out.Body.String())
		}
		view := viewOf(t, out)
		current, _ := view["current"].(map[string]any)
		if current == nil || current["key"] != "todo" {
			t.Fatalf("current = %v, want todo", view["current"])
		}
		items, _ := view["items"].([]any)
		// with_review は4ステータス。現在（todo）を除いた3件。
		if len(items) != 3 {
			t.Fatalf("items = %d件, want 3: %v", len(items), items)
		}

		byKey := map[string]map[string]any{}
		for _, it := range items {
			m, _ := it.(map[string]any)
			byKey[m["key"].(string)] = m
		}
		// todo → in_progress は定義あり・ticket.transition を持つので通る。
		if ip := byKey["in_progress"]; ip == nil || ip["allowed"] != true {
			t.Errorf("in_progress = %v, want allowed=true", byKey["in_progress"])
		}
		// **todo → review / done は定義が無い**が、items には出る（9.7）。
		for _, k := range []string{"review", "done"} {
			it := byKey[k]
			if it == nil {
				t.Fatalf("%s が items に無い（定義が無い先も返すこと）", k)
			}
			if it["allowed"] != false {
				t.Errorf("%s の allowed = %v, want false", k, it["allowed"])
			}
			reason, _ := it["reason"].(string)
			if !strings.Contains(reason, "直接進められません") {
				t.Errorf("%s の reason = %q", k, reason)
			}
		}
	})

	// ── 9.5.3 DELETE ────────────────────────────────────────
	t.Run("削除しても子とactivityは残る", func(t *testing.T) {
		parent := createTicketIT(t, r, session, base, `{"type":"epic","title":"消される親"}`)
		parentSeq := int(parent["seq"].(float64))
		parentID := parent["id"].(string)
		child := createTicketIT(t, r, session, base,
			fmt.Sprintf(`{"type":"task","title":"残る子","parent_seq":%d}`, parentSeq))
		childSeq := int(child["seq"].(float64))

		out := deleteWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, parentSeq), session)
		if out.Code != http.StatusNoContent {
			t.Fatalf("削除の status = %d, want 204（body=%s）", out.Code, out.Body.String())
		}

		// 親は消えた。
		if got := getWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, parentSeq), session); got.Code != http.StatusNotFound {
			t.Errorf("削除後の GET status = %d, want 404", got.Code)
		}
		// **子は残り、親を失ってトップレベルへ上がる**（ON DELETE SET NULL）。
		gotChild := getTicketIT(t, r, session, base, childSeq)
		if gotChild["parent_seq"] != nil {
			t.Errorf("子の parent_seq = %v, want null", gotChild["parent_seq"])
		}
		// **activity は消えず、delete の行が足される**（9.5.3）。
		if rows := activityRows(t, pool, parentID, "create"); len(rows) != 1 {
			t.Errorf("create の activity = %d件, want 1（消してはならない）", len(rows))
		}
		if rows := activityRows(t, pool, parentID, "delete"); len(rows) != 1 {
			t.Errorf("delete の activity = %d件, want 1", len(rows))
		}

		// 二重削除は 404。
		if again := deleteWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, parentSeq), session); again.Code != http.StatusNotFound {
			t.Errorf("二重削除の status = %d, want 404", again.Code)
		}
	})
}

// ── 補助 ────────────────────────────────────────────────────

func getTicketIT(t *testing.T, r http.Handler, session, base string, seq int) map[string]any {
	t.Helper()
	rec := getWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, seq), session)
	if rec.Code != http.StatusOK {
		t.Fatalf("詳細の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	return viewOf(t, rec)
}

func patchTicketIT(
	t *testing.T, r http.Handler, session, base string, seq int, ifMatch, body string,
) map[string]any {
	t.Helper()
	rec := patchProjectWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, seq), session, ifMatch, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	return viewOf(t, rec)
}

func deleteWithCookie(r http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// activityRow は activity の1行（検証で読む列だけ）。
//
// **old_value / new_value は any である**——NULL と空文字を区別する必要がある
// （9.5.2 の body_md は「値を載せない」ので NULL になる）。
type activityRow struct {
	field              string
	oldValue, newValue any
}

func activityRows(t *testing.T, pool *pgxpool.Pool, entityID, action string) []activityRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT coalesce(field,''), old_value, new_value
		   FROM activity WHERE entity_id = $1 AND action = $2 ORDER BY occurred_at, id`,
		entityID, action)
	if err != nil {
		t.Fatalf("activity を読めない: %v", err)
	}
	defer rows.Close()

	var out []activityRow
	for rows.Next() {
		var a activityRow
		var oldV, newV *string
		if err := rows.Scan(&a.field, &oldV, &newV); err != nil {
			t.Fatalf("activity の行を読めない: %v", err)
		}
		if oldV != nil {
			a.oldValue = *oldV
		}
		if newV != nil {
			a.newValue = *newV
		}
		out = append(out, a)
	}
	return out
}

// ticketsETagOf は一覧（9.2.5）の ETag を1つ読む。
func ticketsETagOf(t *testing.T, r http.Handler, session, base string) string {
	t.Helper()
	rec := getWithCookie(r, base+"/tickets", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("一覧に ETag が無い（9.2.5）")
	}
	return etag
}
