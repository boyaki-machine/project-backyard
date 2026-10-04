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

// 委任（Delegation.md 4章・5章・12.2）を**実際のDBに対して**通す。pb-246。
//
// 単体テストはフェイクを差し替えるので queries/delegation.sql は一度も
// 実行されない。**ここでしか確かめられないものが3つある。**
//
//   - **delegation_root_ticket_id_key が実在し、名前が一致すること**
//     （isUniqueViolation がこの名前で判定している。綴りがずれると 409 ではなく
//     500 になる）
//   - **GetActiveDelegationByMainActor / ListActiveDelegationsForProject の
//     JOIN が実際の行を拾うこと**（フェイクは Go 側のマップを返すだけ）
//   - **12.2 の強制がエージェントの Bearer トークンを通しても効くこと**
//     （フェイクは Principal を直接作るので、認証の経路自体は確かめられない）
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationIntegration(t *testing.T) {
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
	adminEmail := "deleg-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"委任結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	// main・評価の2体を登録し、main にはトークンを発行する（12.2 を Bearer で通す）。
	var createdAgents []string
	t.Cleanup(func() {
		bg := context.Background()
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

	var mainToken issuedAgentTokenJSON
	decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+main.ID+"/tokens", session,
		`{"expires_in_days":30}`), &mainToken)

	rootSeq, _ := createIntegrationTicket(t, r, session, base, "大学受験に合格する")
	outsideSeq, _ := createIntegrationTicket(t, r, session, base, "普段の仕事")
	delegationPath := fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq)

	var delegationID string

	t.Run("POST は委任を始め、execution_mode を delegated にする", func(t *testing.T) {
		rec := postWithCookie(r, delegationPath, session,
			fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"合格する"}}`,
				main.ID, evaluator.ID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		delegationID = v["id"].(string)
		if v["status"] != "planning" {
			t.Errorf("status = %v, want planning", v["status"])
		}

		got := viewOf(t, getWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, rootSeq), session))
		if got["execution_mode"] != "delegated" {
			t.Errorf("execution_mode = %v, want delegated", got["execution_mode"])
		}
	})

	// **同じ根に2件目は 409。** delegation_root_ticket_id_key が実在し、
	// isUniqueViolation がその名前で判定していることをここで確かめる。
	t.Run("同じ根への2件目は409", func(t *testing.T) {
		rec := postWithCookie(r, delegationPath, session,
			fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{}}`,
				main.ID, evaluator.ID))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// ── 12.2：main は計画中のあいだ木の中にも起票できない ──────────
	t.Run("main は計画中のあいだ起票できない", func(t *testing.T) {
		rec := bearerPost(r, base+"/tickets", mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"演習1","parent_seq":%d}`, rootSeq))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// 承認済への遷移はテストから直接 UPDATE する。このテストの対象は5章の強制で、
	// 承認の手順（評価の合格 → PM の承認）は delegation_approval_integration_test.go が通す。
	if _, err := pool.Exec(ctx, `UPDATE delegation SET status = 'approved' WHERE id = $1`, delegationID); err != nil {
		t.Fatalf("委任を承認済にできない: %v", err)
	}

	t.Run("承認済なら main は木の中に起票できる", func(t *testing.T) {
		rec := bearerPost(r, base+"/tickets", mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"演習1","parent_seq":%d}`, rootSeq))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("承認済でも main は木の外を指せない", func(t *testing.T) {
		rec := bearerPost(r, base+"/tickets", mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"よそ見","parent_seq":%d}`, outsideSeq))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("承認済でも main は parent_seq を省けない", func(t *testing.T) {
		rec := bearerPost(r, base+"/tickets", mainToken.Token,
			`{"type":"task","title":"根無し"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
	})
}
