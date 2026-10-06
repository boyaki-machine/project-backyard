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

// 委任範囲の承認・却下（Delegation.md 4.3・6.1）を**実際のDBに対して**通す。pb-260。
//
// 単体テストはフェイクを差し替えるので、ここでしか確かめられないものがある。
//
//   - **GetLatestDelegationPlanWithVerdict が、最新の版とその版への直近の評価を
//     読むこと**（相関サブクエリの結び先を誤ると、別の版の合格で承認が通る）
//   - **承認で委任・計画・古い版が1つのトランザクションで進むこと**
//     （scope_version が 0→1→2 と進み、出し直しで残った版が superseded になる）
//   - **範囲の変更の版が、評価の合格だけでは accepted にならないこと**（pb-260 で
//     直した不具合。提出が委任を計画中へ戻し、PM の承認で承認済へ戻る）
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationApprovalIntegration(t *testing.T) {
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
	adminEmail := "deleg-appr-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-appr-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key)
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"委任の承認の結合テスト","workflow_template":"with_review"}`, key))
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
	evaluatorScopes, err := json.Marshal(append(append([]string{}, agentDefaultScopes...), "delegation.evaluate"))
	if err != nil {
		t.Fatalf("scopes を組み立てられない: %v", err)
	}
	decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+evaluator.ID+"/tokens", session,
		`{"expires_in_days":30,"scopes":`+string(evaluatorScopes)+`}`), &evaluatorToken)

	rootSeq, _ := createIntegrationTicket(t, r, session, base, "大学受験に合格する")
	drec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq), session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"合格する"}}`, main.ID, evaluator.ID))
	if drec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", drec.Code, drec.Body.String())
	}

	planPath := fmt.Sprintf("%s/tickets/%d/plan", base, rootSeq)
	evalPath := fmt.Sprintf("%s/tickets/%d/evaluation", base, rootSeq)
	approvePath := fmt.Sprintf("%s/tickets/%d/delegation/approve", base, rootSeq)
	rejectPath := fmt.Sprintf("%s/tickets/%d/delegation/reject", base, rootSeq)
	passFindings := `[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},` +
		`{"aspect":"values","verdict":"pass","note_md":""}]`

	type delegState struct {
		status       string
		scope        string
		scopeVersion int
	}
	readDeleg := func(t *testing.T) delegState {
		t.Helper()
		var s delegState
		if err := pool.QueryRow(ctx,
			`SELECT status, scope::text, scope_version FROM delegation WHERE root_ticket_id =
			   (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)`,
			key, rootSeq).Scan(&s.status, &s.scope, &s.scopeVersion); err != nil {
			t.Fatalf("委任を読めない: %v", err)
		}
		return s
	}
	planStatus := func(t *testing.T, version int) (status string, decidedBy *string) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT status, decided_by FROM delegation_plan WHERE version = $3 AND delegation_id =
			   (SELECT id FROM delegation WHERE root_ticket_id =
			     (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2))`,
			key, rootSeq, version).Scan(&status, &decidedBy); err != nil {
			t.Fatalf("計画 v%d を読めない: %v", version, err)
		}
		return status, decidedBy
	}
	submitPlan := func(t *testing.T, body string) {
		t.Helper()
		if rec := bearerPost(r, planPath, mainToken.Token, body); rec.Code != http.StatusCreated {
			t.Fatalf("計画の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
	}
	passPlan := func(t *testing.T, version int) {
		t.Helper()
		rec := bearerPost(r, evalPath, evaluatorToken.Token,
			fmt.Sprintf(`{"target_kind":"plan","plan_version":%d,"verdict":"pass","findings":%s}`, version, passFindings))
		if rec.Code != http.StatusCreated {
			t.Fatalf("評価の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
	}

	t.Run("計画が無ければ承認できない", func(t *testing.T) {
		rec := postWithCookie(r, approvePath, session, `{"plan_version":1}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("評価の前は承認できないが却下はできる", func(t *testing.T) {
		submitPlan(t, `{"skeleton":{"branches":[{"name":"演習","budget_share":1.0}]},"body_md":"初版","reason_md":"開始"}`)

		rec := postWithCookie(r, approvePath, session, `{"plan_version":1}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("承認 status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}

		rec = postWithCookie(r, rejectPath, session, `{"plan_version":1,"reason_md":"模試の枝が無い"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("却下 status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if s, by := planStatus(t, 1); s != "rejected" || by == nil || *by != adminID {
			t.Errorf("v1 = %s / decided_by=%v, want rejected / PM", s, by)
		}
		if d := readDeleg(t); d.status != "planning" || d.scopeVersion != 0 {
			t.Errorf("委任 = %+v, want planning / scope_version 0", d)
		}
	})

	t.Run("出し直した版を、評価の合格の後に承認する", func(t *testing.T) {
		// v2 を出したまま v3 を出し直す。v2 は判定されずに残る。
		submitPlan(t, `{"skeleton":{"branches":[{"name":"演習","budget_share":0.5},{"name":"模試","budget_share":0.5}]},"body_md":"模試を足す","reason_md":"却下の指摘"}`)
		submitPlan(t, `{"skeleton":{"branches":[{"name":"演習","budget_share":0.6},{"name":"模試","budget_share":0.4}]},"body_md":"配分を直す","reason_md":"出し直し"}`)

		// **別の版の合格では通らない**（v2 に合格を付け、最新の v3 は未評価）。
		passPlan(t, 2)
		if s, _ := planStatus(t, 2); s != "proposed" {
			t.Fatalf("v2 = %s, want proposed（計画中の合格は PM を待つ）", s)
		}
		rec := postWithCookie(r, approvePath, session, `{"plan_version":3}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("v3 未評価で承認 status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}

		// **見ていない版は承認できない**（最新は v3）。
		rec = postWithCookie(r, approvePath, session, `{"plan_version":2}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("古い版で承認 status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}

		passPlan(t, 3)
		rec = postWithCookie(r, approvePath, session, `{"plan_version":3,"reason_md":"この配分で進めてよい"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("承認 status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if v := viewOf(t, rec); v["status"] != "approved" || v["scope_version"] != float64(1) {
			t.Errorf("応答 = %v, want approved / scope_version 1", v)
		}
		if s, by := planStatus(t, 3); s != "accepted" || by == nil || *by != adminID {
			t.Errorf("v3 = %s / decided_by=%v, want accepted / PM", s, by)
		}
		if s, _ := planStatus(t, 2); s != "superseded" {
			t.Errorf("v2 = %s, want superseded（承認の時点で畳む）", s)
		}
		if s, _ := planStatus(t, 1); s != "rejected" {
			t.Errorf("v1 = %s, want rejected のまま", s)
		}

		// 承認済になったので、main は木の中に起票できる（5章）。
		rec = bearerPost(r, base+"/tickets", mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"演習1","parent_seq":%d}`, rootSeq))
		if rec.Code != http.StatusCreated {
			t.Fatalf("承認後の起票 status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("承認済では承認・却下できない", func(t *testing.T) {
		rec := postWithCookie(r, approvePath, session, `{"plan_version":3}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("範囲の変更は評価の合格だけでは反映されずPMの承認で戻る", func(t *testing.T) {
		submitPlan(t, `{"scope":{"goal":"第一志望に合格する"},"skeleton":{"branches":[{"name":"演習","budget_share":0.6},{"name":"模試","budget_share":0.4}]},"body_md":"目的を絞る","reason_md":"ヒアリングの結果"}`)
		if d := readDeleg(t); d.status != "planning" {
			t.Fatalf("委任 = %s, want planning（範囲の変更で戻る。前提）", d.status)
		}

		passPlan(t, 4)
		if s, _ := planStatus(t, 4); s != "proposed" {
			t.Fatalf("v4 = %s, want proposed（評価の合格だけで範囲を変えない）", s)
		}

		rec := postWithCookie(r, approvePath, session, `{"plan_version":4}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("承認 status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		d := readDeleg(t)
		if d.status != "approved" || d.scopeVersion != 2 || !strings.Contains(d.scope, "第一志望") {
			t.Errorf("委任 = %+v, want approved / scope_version 2 / 新しい範囲", d)
		}
	})

	t.Run("判断は根に決定のコメントとして残る", func(t *testing.T) {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM comment WHERE kind = 'decision' AND ticket_id =
			   (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)`,
			key, rootSeq).Scan(&n); err != nil {
			t.Fatalf("コメントを数えられない: %v", err)
		}
		if n != 3 {
			t.Errorf("決定のコメント = %d 件, want 3（却下1・承認2）", n)
		}
	})
}
