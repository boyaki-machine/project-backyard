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

// 引き継ぎノート（Delegation.md 9.3・11章・12.2）を**実際のDBに対して**通す。
// pb-250。
//
// 単体テストはフェイクを差し替えるので queries/delegation_handoff.sql は
// 一度も実行されない。**ここでしか確かめられないのは、uq_delegation_handoff_version
// が実在すること（追記のたびに版が実際に1つずつ増えること）と、
// 次の一手が実DBの計画・確認待ちの状態から正しく計算されることである。**
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationHandoffIntegration(t *testing.T) {
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
	adminEmail := "deleg-handoff-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-hnd-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key)
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"引き継ぎノートの結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

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

	drec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq), session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"合格する"}}`, main.ID, evaluator.ID))
	if drec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", drec.Code, drec.Body.String())
	}

	handoffPath := fmt.Sprintf("%s/tickets/%d/handoff", base, rootSeq)

	t.Run("計画中・初版なしの次の一手はヒアリング", func(t *testing.T) {
		rec := bearerPost(r, handoffPath, mainToken.Token,
			`{"body":{"purpose":"大学受験に合格する"},"trigger":"session_end"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["version"] != float64(1) {
			t.Errorf("version = %v, want 1", v["version"])
		}
		if v["next_move"] != "目的を掘り下げる問いを立てる（ヒアリング）" {
			t.Errorf("next_move = %v", v["next_move"])
		}
	})

	t.Run("追記のたびに版が増える", func(t *testing.T) {
		rec := bearerPost(r, handoffPath, mainToken.Token,
			`{"body":{"purpose":"大学受験に合格する","decisions":"まず情報収集から"},"trigger":"session_end"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["version"] != float64(2) {
			t.Errorf("version = %v, want 2", v["version"])
		}
	})
}
