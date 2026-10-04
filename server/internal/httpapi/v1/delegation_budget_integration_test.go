package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 委任の予算の計測と自動停止、停止と再開（Delegation.md 4.2・8.2・12.1）を
// **実際のDBに対して**通す。pb-253。
//
// 単体テストは GetDelegationBudgetUsage をフェイクで差し替えるので、ここでしか
// 確かめられないものがある。
//
//   - **再帰 CTE が子孫の数と深さを正しく数えること**（根を数えない、根は深さ0）
//   - **トークンが木の中の実行記録と評価の和になり、申告の無い件数が数えられること**
//   - **今月の分がプロジェクトのタイムゾーンで区切られること**（ここでは全件が今月）
//   - **拒んだ起票と同じトランザクションで停止と確認待ちが確定すること**（ロールバック
//     されると、403 は返ったのに委任は止まっていない状態になる）
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationBudgetIntegration(t *testing.T) {
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
	adminEmail := "deleg-bud-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-bud-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key)
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"委任の予算の結合テスト","workflow_template":"with_review"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	var createdAgents []string
	t.Cleanup(func() {
		bg := context.Background()
		// **プロジェクトを先に消す。** 委任・評価・確認待ちがエージェントを
		// ON DELETE RESTRICT で参照しているので、先に actor を消そうとすると
		// 黙って失敗し、エージェントが DB に残り続ける。
		pool.Exec(bg, `DELETE FROM project WHERE key = $1`, key)
		for _, id := range createdAgents {
			pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = $1`, id)
			pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, id)
		}
	})
	register := func(name string) agentJSON {
		t.Helper()
		rec := postWithCookie(r, "/api/v1/me/agents", session,
			fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":"claude_code"}`, name, key))
		if rec.Code != http.StatusCreated {
			t.Fatalf("エージェント登録の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got agentJSON
		decodeJSONBody(t, rec, &got)
		createdAgents = append(createdAgents, got.ID)
		return got
	}
	main := register("main")
	evaluator := register("evaluator")
	var mainToken, evaluatorToken issuedAgentTokenJSON
	decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+main.ID+"/tokens", session,
		`{"expires_in_days":30}`), &mainToken)
	evaluatorScopes, err := json.Marshal(append(append([]string{}, agentDefaultScopes...), "delegation.evaluate"))
	if err != nil {
		t.Fatalf("scopes を組み立てられない: %v", err)
	}
	decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+evaluator.ID+"/tokens", session,
		`{"expires_in_days":30,"scopes":`+string(evaluatorScopes)+`}`), &evaluatorToken)

	rootSeq, _ := createIntegrationTicket(t, r, session, base, "大学受験に合格する")
	ticketPath := fmt.Sprintf("%s/tickets/%d", base, rootSeq)

	t.Run("委任の作成で予算の形を検証する", func(t *testing.T) {
		other, _ := createIntegrationTicket(t, r, session, base, "別の根")
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, other), session,
			fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"budget":{"ticket":3}}}`, main.ID, evaluator.ID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// 委任を作り、計画 → 評価の合格 → PM の承認で承認済にする（pb-260 の経路）。
	// 予算：起票2件・深さ2段・トークン1000。
	drec := postWithCookie(r, ticketPath+"/delegation", session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"合格する"}}`, main.ID, evaluator.ID))
	if drec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", drec.Code, drec.Body.String())
	}
	if rec := bearerPost(r, ticketPath+"/plan", mainToken.Token,
		`{"scope":{"goal":"合格する","budget":{"tickets":2,"depth":2,"tokens":1000}},"skeleton":{"branches":[{"name":"演習","budget_share":1.0}]},"body_md":"初版","reason_md":"開始"}`); rec.Code != http.StatusCreated {
		t.Fatalf("計画の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	passFindings := `[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},` +
		`{"aspect":"values","verdict":"pass","note_md":""}]`
	// 評価はトークンを申告しない（tokens_missing に数えられる）。
	if rec := bearerPost(r, ticketPath+"/evaluation", evaluatorToken.Token,
		`{"target_kind":"plan","plan_version":1,"verdict":"pass","findings":`+passFindings+`}`); rec.Code != http.StatusCreated {
		t.Fatalf("評価 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := postWithCookie(r, ticketPath+"/delegation/approve", session, `{"plan_version":1}`); rec.Code != http.StatusOK {
		t.Fatalf("承認 status = %d（body=%s）", rec.Code, rec.Body.String())
	}

	createChild := func(t *testing.T, parentSeq int, title string) (int, *httptest.ResponseRecorder) {
		t.Helper()
		rec := bearerPost(r, base+"/tickets", mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":%q,"parent_seq":%d}`, title, parentSeq))
		if rec.Code != http.StatusCreated {
			return 0, rec
		}
		return int(viewOf(t, rec)["seq"].(float64)), rec
	}
	delegStatus := func(t *testing.T) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM delegation WHERE root_ticket_id =
			   (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)`,
			key, rootSeq).Scan(&s); err != nil {
			t.Fatalf("委任を読めない: %v", err)
		}
		return s
	}
	openBudgetConfirmation := func(t *testing.T) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx,
			`SELECT c.id FROM confirmation c JOIN delegation d ON d.id = c.delegation_id
			 WHERE d.root_ticket_id = (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)
			   AND c.reason = 'budget_exceeded' AND c.status = 'open'`, key, rootSeq).Scan(&id)
		if err != nil {
			t.Fatalf("開いている budget_exceeded の確認待ちが無い: %v", err)
		}
		return id
	}

	var child1 int
	t.Run("深さの上限：根の子・孫は通り、ひ孫は拒まれて停止する", func(t *testing.T) {
		var rec *httptest.ResponseRecorder
		child1, rec = createChild(t, rootSeq, "演習")
		if child1 == 0 {
			t.Fatalf("子 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		grand, rec := createChild(t, child1, "演習の1回目")
		if grand == 0 {
			t.Fatalf("孫 status = %d（body=%s）", rec.Code, rec.Body.String())
		}

		// 起票数は2件で上限ちょうど。ひ孫は深さ3（上限2）でも、起票数3件目でも拒まれる。
		_, rec = createChild(t, grand, "演習の1回目の準備")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("ひ孫 status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
		msg := errorOf(t, rec).Message
		if !strings.Contains(msg, "深さ") || !strings.Contains(msg, "起票数") {
			t.Errorf("message = %q, want 深さ と 起票数 の両方", msg)
		}
		if s := delegStatus(t); s != "paused" {
			t.Fatalf("委任 = %s, want paused（拒否と同じトランザクションで確定する）", s)
		}
		openBudgetConfirmation(t)
	})

	t.Run("delegation-statusが消費を返す", func(t *testing.T) {
		rec := getWithCookie(r, ticketPath+"/delegation-status", session)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var v struct {
			Budget budgetStatusView `json:"budget"`
		}
		decodeJSONBody(t, rec, &v)
		b := v.Budget
		if b.Tickets != 2 || b.MaxDepth != 2 || b.TokensTotal != 0 || b.TokensMissing != 1 {
			t.Errorf("budget = %+v, want 起票2・深さ2・トークン0・申告なし1（評価）", b)
		}
		if b.Limits.Tickets == nil || *b.Limits.Tickets != 2 || len(b.Today) != 10 {
			t.Errorf("limits = %+v / today = %q", b.Limits, b.Today)
		}
	})

	t.Run("確認待ちに答えるまで再開できず、答えたら予算を直して再開できる", func(t *testing.T) {
		rec := postWithCookie(r, ticketPath+"/delegation/resume", session, `{}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("回答前の再開 status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}

		cid := openBudgetConfirmation(t)
		rec = postWithCookie(r, fmt.Sprintf("%s/confirmation/%s/answer", ticketPath, cid), session,
			`{"answer_md":"起票数を広げる"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("回答 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if s := delegStatus(t); s != "paused" {
			t.Fatalf("委任 = %s, want paused のまま（答えるだけでは再開しない。8.2）", s)
		}

		rec = postWithCookie(r, ticketPath+"/delegation/resume", session,
			`{"budget":{"tickets":10,"depth":3,"tokens":1000},"reason_md":"演習を細かく分けてよい"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("再開 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["status"] != "approved" || v["scope_version"] != float64(2) {
			t.Errorf("応答 = %v, want approved / scope_version 2", v)
		}

		var status, scope string
		if err := pool.QueryRow(ctx,
			`SELECT p.status, p.scope::text FROM delegation_plan p JOIN delegation d ON d.id = p.delegation_id
			 WHERE d.root_ticket_id = (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)
			 ORDER BY p.version DESC LIMIT 1`, key, rootSeq).Scan(&status, &scope); err != nil {
			t.Fatalf("計画を読めない: %v", err)
		}
		if status != "accepted" || !strings.Contains(scope, `"tickets": 10`) {
			t.Errorf("最新の版 = %s / %s, want accepted で新しい予算", status, scope)
		}

		// 再開後は、深さ3まで起票できる。
		if seq, rec := createChild(t, child1, "模試"); seq == 0 {
			t.Fatalf("再開後の起票 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("再開後の軽微な計画の変更は範囲の変更にならない", func(t *testing.T) {
		rec := bearerPost(r, ticketPath+"/plan", mainToken.Token,
			`{"skeleton":{"branches":[{"name":"演習","budget_share":1.0}]},"body_md":"説明を直した","reason_md":"誤字"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("計画の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if v := viewOf(t, rec); v["status"] != "accepted" {
			t.Errorf("status = %v / measured = %v, want 軽微で accepted", v["status"], v["measured_class"])
		}
		if s := delegStatus(t); s != "approved" {
			t.Errorf("委任 = %s, want approved のまま", s)
		}
	})

	t.Run("トークンの超過は報告を受け付けてから停止する", func(t *testing.T) {
		rec := bearerPost(r, fmt.Sprintf("%s/tickets/%d/reports", base, child1), mainToken.Token,
			`{"status":"completed","cost":{"tokens":1200}}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		if s := delegStatus(t); s != "paused" {
			t.Fatalf("委任 = %s, want paused", s)
		}
		cid := openBudgetConfirmation(t)
		var question string
		if err := pool.QueryRow(ctx, `SELECT question_md FROM confirmation WHERE id = $1`, cid).Scan(&question); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(question, "総予算（1000）") || !strings.Contains(question, "消費 1200") {
			t.Errorf("question = %q", question)
		}
	})

	t.Run("PMが停止と再開をする", func(t *testing.T) {
		cid := openBudgetConfirmation(t)
		postWithCookie(r, fmt.Sprintf("%s/confirmation/%s/answer", ticketPath, cid), session, `{"answer_md":"総予算を増やす"}`)
		rec := postWithCookie(r, ticketPath+"/delegation/resume", session, `{"budget":{"tickets":10,"depth":3,"tokens":5000}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("再開 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		rec = postWithCookie(r, ticketPath+"/delegation/pause", session, `{"reason_md":"週末は止める"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("停止 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if s := delegStatus(t); s != "paused" {
			t.Fatalf("委任 = %s, want paused", s)
		}
		// PM が止めたときは確認待ちを立てないので、そのまま再開できる。
		rec = postWithCookie(r, ticketPath+"/delegation/resume", session, `{}`)
		if rec.Code != http.StatusOK || viewOf(t, rec)["status"] != "approved" {
			t.Fatalf("再開 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
	})
}
