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

// 計画の版（Delegation.md 6章・12.2）を**実際のDBに対して**通す。pb-247。
//
// 単体テストはフェイクを差し替えるので queries/delegation_plan.sql は一度も
// 実行されない。**ここでしか確かめられないものが2つある。**
//
//   - **CountTicketsInDelegationTreeSince の再帰 CTE が実際に動くこと**
//     （子チケットを実際に作って数える）
//   - **uq_delegation_plan_version が実在すること**（同じ版番号の2本目を
//     防ぐのはアプリ側の NextDelegationPlanVersion だが、競合時の最後の砦は DB）
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationPlanIntegration(t *testing.T) {
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
	adminEmail := "deleg-plan-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-plan-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key)
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"計画の版の結合テスト","workflow_template":"simple"}`, key))
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
	var mainToken issuedAgentTokenJSON
	decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+main.ID+"/tokens", session,
		`{"expires_in_days":30}`), &mainToken)

	rootSeq, _ := createIntegrationTicket(t, r, session, base, "大学受験に合格する")

	drec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq), session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"合格する"}}`, main.ID, evaluator.ID))
	if drec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", drec.Code, drec.Body.String())
	}

	// 承認済みへの遷移はテストから直接 UPDATE する。このテストの対象は 6.3 の判定で、
	// 承認の手順（評価の合格 → PM の承認）は delegation_approval_integration_test.go が通す。
	if _, err := pool.Exec(ctx,
		`UPDATE delegation SET status = 'approved' WHERE root_ticket_id =
		   (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)`,
		key, rootSeq); err != nil {
		t.Fatalf("委任を承認済みにできない: %v", err)
	}

	planPath := fmt.Sprintf("%s/tickets/%d/plan", base, rootSeq)

	t.Run("初版はproposedのまま（評価待ち）", func(t *testing.T) {
		rec := bearerPost(r, planPath, mainToken.Token,
			`{"skeleton":{"branches":[{"name":"情報収集","budget_share":0.2},{"name":"演習","budget_share":0.8}]},"body_md":"初版","reason_md":"開始"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["status"] != "proposed" {
			t.Errorf("status = %v, want proposed", v["status"])
		}
		if v["measured_class"] != nil {
			t.Errorf("measured_class = %v, want null（初版は判定規則を適用しない）", v["measured_class"])
		}
	})

	// 初版の確定も同じ理由で直接 UPDATE する（経路は delegation_approval_integration_test.go）。
	if _, err := pool.Exec(ctx,
		`UPDATE delegation_plan SET status = 'accepted' WHERE delegation_id =
		   (SELECT id FROM delegation WHERE root_ticket_id =
		     (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2))
		   AND version = 1`,
		key, rootSeq); err != nil {
		t.Fatalf("初版を承認済みにできない: %v", err)
	}

	t.Run("骨格を変えない版は軽微で即座に反映される", func(t *testing.T) {
		rec := bearerPost(r, planPath, mainToken.Token,
			`{"skeleton":{"branches":[{"name":"情報収集","budget_share":0.2},{"name":"演習","budget_share":0.8}]},"body_md":"誤字修正","reason_md":"説明文を直した"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["measured_class"] != "minor" {
			t.Errorf("measured_class = %v, want minor", v["measured_class"])
		}
		if v["status"] != "accepted" {
			t.Errorf("status = %v, want accepted（軽微は即時反映）", v["status"])
		}
	})

	t.Run("新規5件超は実際の子チケット数をCTEで数えてstructuralになる", func(t *testing.T) {
		for i := 0; i < 6; i++ {
			rec := bearerPost(r, base+"/tickets", mainToken.Token,
				fmt.Sprintf(`{"type":"task","title":"演習%d","parent_seq":%d}`, i, rootSeq))
			if rec.Code != http.StatusCreated {
				t.Fatalf("子チケット作成 status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
			}
		}
		rec := bearerPost(r, planPath, mainToken.Token,
			`{"skeleton":{"branches":[{"name":"情報収集","budget_share":0.2},{"name":"演習","budget_share":0.8}]},"body_md":"骨格は同じ","reason_md":"大量に起票した後"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["measured_class"] != "structural" {
			t.Errorf("measured_class = %v, want structural（新規5件超・CTEで実測）", v["measured_class"])
		}
		if v["status"] != "proposed" {
			t.Errorf("status = %v, want proposed（評価待ち）", v["status"])
		}
	})

	t.Run("PMは前の版へ差し戻せる", func(t *testing.T) {
		rec := postWithCookie(r, planPath+"/2/revert", session, `{"reason_md":"やはり誤字修正の版へ戻す"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["status"] != "accepted" {
			t.Errorf("status = %v, want accepted（PM の差し戻しは即時確定）", v["status"])
		}
		if int(v["reverts_to"].(float64)) != 2 {
			t.Errorf("reverts_to = %v, want 2", v["reverts_to"])
		}
	})
}
