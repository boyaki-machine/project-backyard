package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 委任の状態の集約読み取り（Delegation.md 9.1）を**実際のDBに対して**通す。
// pb-251。
//
// 単体テストはフェイクを差し替えるので、5本のクエリ（計画・評価・確認待ち・
// 引き継ぎノート・委任そのもの）を1つの応答にまとめる実際のSQLは一度も
// 実行されない。**ここでしか確かめられないのは、それぞれのLEFT JOIN相当の
// 個別クエリが実データに対して正しく「無い」を返し、集約が壊れないことである。**
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationStatusIntegration(t *testing.T) {
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
	adminEmail := "deleg-status-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-st-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key)
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"委任状態の結合テスト","workflow_template":"simple"}`, key))
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

	t.Run("委任の木の外ではin_delegationだけ返る", func(t *testing.T) {
		seq, _ := createIntegrationTicket(t, r, session, base, "委任に関係ない仕事")
		rec := bearerGet(r, fmt.Sprintf("%s/tickets/%d/delegation-status", base, seq), mainToken.Token)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["in_delegation"] != false {
			t.Errorf("in_delegation = %v, want false", v["in_delegation"])
		}
	})

	rootSeq, _ := createIntegrationTicket(t, r, session, base, "大学受験に合格する")
	drec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq), session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"合格する"}}`, main.ID, evaluator.ID))
	if drec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", drec.Code, drec.Body.String())
	}

	t.Run("根では委任・計画・評価・確認待ち・引き継ぎノートが無い状態を読める", func(t *testing.T) {
		rec := bearerGet(r, fmt.Sprintf("%s/tickets/%d/delegation-status", base, rootSeq), mainToken.Token)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["in_delegation"] != true {
			t.Fatalf("in_delegation = %v, want true", v["in_delegation"])
		}
		if v["role"] != "main" {
			t.Errorf("role = %v, want main", v["role"])
		}
		if v["status"] != "planning" {
			t.Errorf("status = %v, want planning", v["status"])
		}
		if v["latest_plan"] != nil {
			t.Errorf("latest_plan = %v, want null（まだ計画が無い）", v["latest_plan"])
		}
		if v["next_move"] != "目的を掘り下げる問いを立てる（ヒアリング）" {
			t.Errorf("next_move = %v", v["next_move"])
		}
	})

	t.Run("計画の提出後は最新の計画が載る", func(t *testing.T) {
		rec := bearerPost(r, fmt.Sprintf("%s/tickets/%d/plan", base, rootSeq), mainToken.Token,
			`{"skeleton":{"branches":[{"name":"確認","budget_share":1}],"checkpoints":["共有"]},"body_md":"初版","reason_md":"開始"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("計画の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		submitted := viewOf(t, rec)

		rec = bearerGet(r, fmt.Sprintf("%s/tickets/%d/delegation-status", base, rootSeq), mainToken.Token)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		plan := v["latest_plan"].(map[string]any)
		if plan["version"] != float64(1) || plan["status"] != "proposed" {
			t.Errorf("latest_plan = %v", plan)
		}
		for _, field := range []string{"skeleton", "body_md", "reason_md"} {
			if !reflect.DeepEqual(plan[field], submitted[field]) {
				t.Errorf("latest_plan[%s] = %v, want submitted %v", field, plan[field], submitted[field])
			}
		}
	})
}
