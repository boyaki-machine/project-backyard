package v1

import (
	"context"
	"encoding/json"
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

// 評価（Delegation.md 7章・12.2）を**実際のDBに対して**通す。pb-248。
//
// 単体テストはフェイクを差し替えるので queries/evaluation.sql は一度も実行
// されない。**ここでしか確かめられないものが3つある。**
//
//   - **ck_evaluation_target が実在すること**（plan/deliverable の排他が
//     アプリ側の分岐だけでなく DB 制約としても効く）
//   - **CountEvaluationsSinceLastPass が実際の行を数えること**（合格を
//     挟むと round が1へ戻る）
//   - **ticket.transition の検証8が、実DBの evaluation 行を読んで判定すること**
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestEvaluationIntegration(t *testing.T) {
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
	adminEmail := "deleg-eval-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-eval-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key)
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"評価の結合テスト","workflow_template":"with_review"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	var createdAgents []string
	t.Cleanup(func() {
		bg := context.Background()
		// **プロジェクトを先に消す**（pb-287）。t.Cleanup は後に登録したものから走るので、
		// 上のプロジェクトの後始末より先にここが走る。委任・評価がエージェントを
		// ON DELETE RESTRICT で参照しているため、先に actor を消すと黙って失敗し、
		// エージェントが DB に残り続けていた。
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
		for _, id := range createdAgents {
			pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = $1`, id)
			if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, id); err != nil {
				t.Errorf("エージェントの後始末に失敗した: %v", err)
			}
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
	// **delegation.evaluate は既定に無い**（0050。エージェントの発行時に選ぶ）。
	evaluatorScopes, err := json.Marshal(append(append([]string{}, agentDefaultScopes...), "delegation.evaluate"))
	if err != nil {
		t.Fatalf("scopes を組み立てられない: %v", err)
	}
	decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+evaluator.ID+"/tokens", session,
		`{"expires_in_days":30,"scopes":`+string(evaluatorScopes)+`}`), &evaluatorToken)

	// **main（エージェント）が遷移するには、担当が自分の所有者（admin）であることが要る**
	// （検証6。9.6）。起票時に assignee_id を指定する。
	trec := postWithCookie(r, base+"/tickets", session,
		fmt.Sprintf(`{"type":"task","title":"大学受験に合格する","assignee_id":%q}`, adminID))
	if trec.Code != http.StatusCreated {
		t.Fatalf("根チケットの作成 status = %d（body=%s）", trec.Code, trec.Body.String())
	}
	rootSeq := int(viewOf(t, trec)["seq"].(float64))

	drec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq), session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"合格する"}}`, main.ID, evaluator.ID))
	if drec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", drec.Code, drec.Body.String())
	}
	evalPath := fmt.Sprintf("%s/tickets/%d/evaluation", base, rootSeq)
	planPath := fmt.Sprintf("%s/tickets/%d/plan", base, rootSeq)
	reportsPath := fmt.Sprintf("%s/tickets/%d/reports", base, rootSeq)
	transitionPath := fmt.Sprintf("%s/tickets/%d/transition", base, rootSeq)

	passFindings := `[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},` +
		`{"aspect":"values","verdict":"pass","note_md":""}]`
	failFindings := `[{"aspect":"purpose_and_dod","verdict":"fail","note_md":"演習量が不足"},` +
		`{"aspect":"values","verdict":"pass","note_md":""}]`

	t.Run("計画中のmainと評価Agentも認可された計測を記録できる", func(t *testing.T) {
		path := fmt.Sprintf("%s/tickets/%d/work-sessions", base, rootSeq)
		for i, token := range []string{mainToken.Token, evaluatorToken.Token} {
			rec := bearerPost(r, path, token, fmt.Sprintf(`{"request_id":"role-%d-start"}`, i))
			if rec.Code != 200 {
				t.Fatal(rec.Body.String())
			}
			var row testWorkView
			decodeJSONBody(t, rec, &row)
			rec = bearerPost(r, path+"/"+row.ID+"/events", token, fmt.Sprintf(`{"request_id":"role-%d-finish","action":"finish","result":"completed","reported_active_ms":0,"note":"fixture measured zero runtime"}`, i))
			if rec.Code != 200 {
				t.Fatal(rec.Body.String())
			}
		}
	})
	t.Run("plan対象version1は評価してもacceptされない", func(t *testing.T) {
		rec := bearerPost(r, planPath, mainToken.Token,
			`{"skeleton":{"branches":[{"name":"演習","budget_share":1.0}]},"body_md":"初版","reason_md":"開始"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("計画の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}

		rec = bearerPost(r, evalPath, evaluatorToken.Token,
			fmt.Sprintf(`{"target_kind":"plan","plan_version":1,"verdict":"pass","findings":%s}`, passFindings))
		if rec.Code != http.StatusCreated {
			t.Fatalf("評価の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["round"] != float64(1) {
			t.Errorf("round = %v, want 1", v["round"])
		}

		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM delegation_plan WHERE delegation_id =
			   (SELECT id FROM delegation WHERE root_ticket_id =
			     (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2))
			   AND version = 1`, key, rootSeq).Scan(&status); err != nil {
			t.Fatalf("計画の状態を読めない: %v", err)
		}
		if status != "proposed" {
			t.Errorf("status = %q, want proposed（初版はPMの承認待ちになるだけ。6.1）", status)
		}
	})

	// **PM が委任範囲を承認する**（6.1。pb-260）。初版の評価が合格した後なので通る。
	if rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation/approve", base, rootSeq), session,
		`{"plan_version":1}`); rec.Code != http.StatusOK {
		t.Fatalf("委任範囲の承認 status = %d（body=%s）", rec.Code, rec.Body.String())
	}

	t.Run("plan対象version2は不合格でrejectedになり出し直せる", func(t *testing.T) {
		rec := bearerPost(r, planPath, mainToken.Token,
			`{"skeleton":{"branches":[{"name":"演習","budget_share":0.5},{"name":"模試","budget_share":0.5}]},"body_md":"枝を増やす","reason_md":"方針変更"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("計画の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["measured_class"] != "structural" {
			t.Fatalf("measured_class = %v, want structural（前提）", v["measured_class"])
		}

		rec = bearerPost(r, evalPath, evaluatorToken.Token,
			fmt.Sprintf(`{"target_kind":"plan","plan_version":2,"verdict":"fail","findings":%s}`, failFindings))
		if rec.Code != http.StatusCreated {
			t.Fatalf("評価の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}

		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM delegation_plan WHERE delegation_id =
			   (SELECT id FROM delegation WHERE root_ticket_id =
			     (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2))
			   AND version = 2`, key, rootSeq).Scan(&status); err != nil {
			t.Fatalf("計画の状態を読めない: %v", err)
		}
		if status != "rejected" {
			t.Errorf("status = %q, want rejected", status)
		}

		// 出し直し（version 3）。同じ骨格で合格させる。
		rec = bearerPost(r, planPath, mainToken.Token,
			`{"skeleton":{"branches":[{"name":"演習","budget_share":0.5},{"name":"模試","budget_share":0.5}]},"body_md":"同じ案で再提出","reason_md":"指摘を踏まえた再提出"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("計画の再提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		rec = bearerPost(r, evalPath, evaluatorToken.Token,
			fmt.Sprintf(`{"target_kind":"plan","plan_version":3,"verdict":"pass","findings":%s}`, passFindings))
		if rec.Code != http.StatusCreated {
			t.Fatalf("評価の再提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v = viewOf(t, rec)
		// 直前の合格（無し）からの件数+1 ではなく、version2のfail＋version3のpassで
		// 同一対象2回目＝round 2 になる（合格のたびに0から数え直す。7.3）。
		if v["round"] != float64(2) {
			t.Errorf("round = %v, want 2（plan対象への2回目）", v["round"])
		}

		if err := pool.QueryRow(ctx,
			`SELECT status FROM delegation_plan WHERE delegation_id =
			   (SELECT id FROM delegation WHERE root_ticket_id =
			     (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2))
			   AND version = 3`, key, rootSeq).Scan(&status); err != nil {
			t.Fatalf("計画の状態を読めない: %v", err)
		}
		if status != "accepted" {
			t.Errorf("status = %q, want accepted", status)
		}
	})

	t.Run("根の完了は評価の合格に掛かる", func(t *testing.T) {
		rec := bearerPost(r, transitionPath, mainToken.Token, `{"to":"in_progress"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("in_progress への遷移 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		rec = bearerPost(r, transitionPath, mainToken.Token, `{"to":"review"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("review への遷移 status = %d（body=%s）", rec.Code, rec.Body.String())
		}

		// まだ完了レポートが無いので、合格していない。
		rec = postWithCookie(r, transitionPath, session, `{"to":"done"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("done への遷移（成果の評価前） status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}

		rec = bearerPost(r, reportsPath, mainToken.Token, `{"effort_unavailable":{"target":"both","reason":"fixture work has no measured timing"},"status":"completed"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("完了レポートの提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		firstReport, _ := viewOf(t, rec)["id"].(string)

		// 完了レポートはあるが、まだ評価されていない（評価待ち）。
		rec = postWithCookie(r, transitionPath, session, `{"to":"done"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("done への遷移（評価待ち中） status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}

		// **main は評価待ちのあいだも出し直せる**（7.2。pb-305）。評価中の注意に応えた修正を
		// 正式な報告に載せるため。以前は 403 だった（pb-248）。
		rec = bearerPost(r, reportsPath, mainToken.Token, `{"effort_unavailable":{"target":"both","reason":"fixture work has no measured timing"},"status":"completed"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("完了レポートの出し直し（評価待ち中） status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		secondReport, _ := viewOf(t, rec)["id"].(string)
		if firstReport == "" || secondReport == "" || firstReport == secondReport {
			t.Fatalf("完了レポートの ID = %q / %q, want 別々の値", firstReport, secondReport)
		}

		// 評価役は delegation-status で最新の報告を知る。
		st := viewOf(t, bearerGet(r, fmt.Sprintf("%s/tickets/%d/delegation-status", base, rootSeq), evaluatorToken.Token))
		if lr, _ := st["latest_report"].(map[string]any); lr == nil || lr["id"] != secondReport || lr["evaluated"] != false {
			t.Errorf("latest_report = %v, want id=%s・evaluated=false", st["latest_report"], secondReport)
		}

		// 古い報告を名指しした評価は、取り違えを防ぐため 409 で拒む。
		rec = bearerPost(r, evalPath, evaluatorToken.Token,
			fmt.Sprintf(`{"target_kind":"deliverable","agent_report_id":%q,"verdict":"pass","findings":%s}`, firstReport, passFindings))
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stale_report") {
			t.Fatalf("古い報告への評価 status = %d, want 409 stale_report（body=%s）", rec.Code, rec.Body.String())
		}

		rec = bearerPost(r, evalPath, evaluatorToken.Token,
			fmt.Sprintf(`{"target_kind":"deliverable","agent_report_id":%q,"verdict":"pass","findings":%s}`, secondReport, passFindings))
		if rec.Code != http.StatusCreated {
			t.Fatalf("成果の評価 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if got, _ := viewOf(t, rec)["agent_report_id"].(string); got != secondReport {
			t.Errorf("評価の対象 = %q, want %q（最新の報告）", got, secondReport)
		}

		// **停止中は、合格していても受け入れられない**（検証9。Delegation.md 4.3「受け入れ」）。
		delegPath := fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq)
		if rec = postWithCookie(r, delegPath+"/pause", session, `{}`); rec.Code != http.StatusOK {
			t.Fatalf("停止 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		rec = postWithCookie(r, transitionPath, session, `{"to":"done"}`)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), rootNotAcceptableReason) ||
			!strings.Contains(rec.Body.String(), "delegation_stage") {
			t.Fatalf("done への遷移（停止中） status = %d, want 403 と検証9 の文言（body=%s）", rec.Code, rec.Body.String())
		}
		// 9.7 も同じ関数を通り、押せない「完了」に理由が付く。
		rec = getWithCookie(r, transitionPath+"s", session)
		if !strings.Contains(rec.Body.String(), rootNotAcceptableReason) {
			t.Errorf("遷移先の一覧に検証9 の理由が無い（body=%s）", rec.Body.String())
		}
		if rec = postWithCookie(r, delegPath+"/resume", session, `{}`); rec.Code != http.StatusOK {
			t.Fatalf("再開 status = %d（body=%s）", rec.Code, rec.Body.String())
		}

		// 関門に数えないヒアリングは、開いたまま受け入れられる。その通知は受け入れで取り下がる
		// （Delegation.md 8.2）。
		rec = bearerPost(r, fmt.Sprintf("%s/tickets/%d/confirmation", base, rootSeq), mainToken.Token,
			`{"reason":"hearing","question_md":"ほかに気になる点はありますか"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("ヒアリングの提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		hearingKey := "delegation_confirmation:" + viewOf(t, rec)["id"].(string)
		countNotices := func() int {
			var n int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification WHERE subject_key = $1`, hearingKey).Scan(&n); err != nil {
				t.Fatalf("通知を数えられない: %v", err)
			}
			return n
		}
		if n := countNotices(); n != 1 {
			t.Fatalf("ヒアリングの通知 = %d 件, want 1", n)
		}

		// 合格したので、PM は根を完了にできる。**これが受け入れである。**
		rec = postWithCookie(r, transitionPath, session, `{"to":"done"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("done への遷移（合格後） status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if n := countNotices(); n != 0 {
			t.Errorf("受け入れ後のヒアリングの通知 = %d 件, want 0（取り下げる）", n)
		}

		// 委任は ended、根の execution_mode は delegated のまま（取り下げとの違い）。
		var delegStatus, mode string
		if err := pool.QueryRow(ctx, `
			SELECT d.status, t.execution_mode FROM delegation d JOIN ticket t ON t.id = d.root_ticket_id
			JOIN project p ON p.id = t.project_id WHERE p.key = $1 AND t.seq = $2`, key, rootSeq,
		).Scan(&delegStatus, &mode); err != nil {
			t.Fatalf("委任を読めない: %v", err)
		}
		if delegStatus != "ended" || mode != "delegated" {
			t.Errorf("受け入れ後 delegation.status = %q, execution_mode = %q, want ended / delegated", delegStatus, mode)
		}

		// main は「終了」を読んで止まれる（pb_wait とパックが読む口）。
		v := viewOf(t, bearerGet(r, fmt.Sprintf("%s/tickets/%d/delegation-status", base, rootSeq), mainToken.Token))
		if v["in_delegation"] != false || v["status"] != "ended" {
			t.Errorf("受け入れ後の delegation-status = %v, want in_delegation=false / status=ended", v)
		}

		// 根に判断のコメントが残る（pb_wait が変化として返す）。
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM comment c JOIN ticket t ON t.id = c.ticket_id JOIN project p ON p.id = t.project_id
			WHERE p.key = $1 AND t.seq = $2 AND c.kind = 'decision' AND c.body_md LIKE '%自律実行を受け入れた%'`,
			key, rootSeq).Scan(&n); err != nil {
			t.Fatalf("コメントを数えられない: %v", err)
		}
		if n != 1 {
			t.Errorf("受け入れのコメント = %d 件, want 1", n)
		}
	})
}
