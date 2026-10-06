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

// 指標の値・周期の要約・checkpoint の確認待ち（Delegation.md 4.2・8.1 の 7・8.3）を
// **実際のDBに対して**通す。pb-277。
//
// ここでしか確かめられないもの：numeric の値の往復、GetCheckpointBase の起点、
// uq_confirmation_open_checkpoint による二重の防止、checkpoint が main を止めること。
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationSummaryIntegration(t *testing.T) {
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
	adminEmail := "deleg-sm-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-sm-" + strings.ToLower(adminID[len(adminID)-8:])
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"周期の要約の結合テスト","workflow_template":"with_review"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	var createdAgents []string
	t.Cleanup(func() {
		bg := context.Background()
		// **プロジェクトを先に消す**（委任がエージェントを RESTRICT で参照する）。
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
	ticketPath := fmt.Sprintf("%s/tickets/%d", base, rootSeq)
	scope := `{"metrics":[{"key":"mock","name":"模試の偏差値","target":65,"direction":"higher"}],"report_every_days":7}`

	t.Run("指標の定義が不正なら422", func(t *testing.T) {
		other, _ := createIntegrationTicket(t, r, session, base, "別の根")
		for _, bad := range []string{
			`{"metrics":[{"key":"Mock","name":"x","direction":"higher"}]}`,
			`{"metrics":[{"key":"m","name":"x","direction":"up"}]}`,
			`{"report_every_days":0}`,
		} {
			rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, other), session,
				fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":%s}`, main.ID, evaluator.ID, bad))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s: status = %d, want 422（body=%s）", bad, rec.Code, rec.Body.String())
			}
		}
	})

	if rec := postWithCookie(r, ticketPath+"/delegation", session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":%s}`, main.ID, evaluator.ID, scope)); rec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := bearerPost(r, ticketPath+"/plan", mainToken.Token,
		`{"scope":`+scope+`,"skeleton":{"branches":[{"name":"演習","budget_share":1.0}]},"body_md":"初版","reason_md":"開始"}`); rec.Code != http.StatusCreated {
		t.Fatalf("計画 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := bearerPost(r, ticketPath+"/evaluation", evaluatorToken.Token,
		`{"target_kind":"plan","plan_version":1,"verdict":"pass","findings":[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},{"aspect":"values","verdict":"pass","note_md":""}]}`); rec.Code != http.StatusCreated {
		t.Fatalf("評価 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := postWithCookie(r, ticketPath+"/delegation/approve", session, `{"plan_version":1}`); rec.Code != http.StatusOK {
		t.Fatalf("承認 status = %d（body=%s）", rec.Code, rec.Body.String())
	}

	valuesPath := ticketPath + "/metrics/mock/values"
	t.Run("人と main は値を入れられ、評価と知らない指標は拒まれる", func(t *testing.T) {
		if rec := postWithCookie(r, valuesPath, session, `{"value":58,"note_md":"9月の模試"}`); rec.Code != http.StatusCreated {
			t.Fatalf("PM status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		rec := bearerPost(r, valuesPath, mainToken.Token, `{"value":60.5}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("main status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if v := viewOf(t, rec); v["value"] != 60.5 {
			t.Errorf("value = %v, want 60.5", v["value"])
		}
		if rec := bearerPost(r, valuesPath, evaluatorToken.Token, `{"value":1}`); rec.Code != http.StatusForbidden {
			t.Errorf("評価 status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
		if rec := postWithCookie(r, ticketPath+"/metrics/nope/values", session, `{"value":1}`); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("知らない指標 status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("要約は版と指標の推移を記録から組み立てる", func(t *testing.T) {
		rec := getWithCookie(r, ticketPath+"/summary", session)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got struct {
			Plans   []struct{ Version int } `json:"plans"`
			Metrics []struct {
				Values []struct{ Value float64 } `json:"values"`
			} `json:"metrics"`
			SummaryMd string `json:"summary_md"`
		}
		decodeJSONBody(t, rec, &got)
		if len(got.Metrics) != 1 || len(got.Metrics[0].Values) != 2 {
			t.Fatalf("metrics = %+v, want 1指標に2件", got.Metrics)
		}
		for _, s := range []string{"58 → 60.5", "目標 65", "エージェントが入れた値を含む"} {
			if !strings.Contains(got.SummaryMd, s) {
				t.Errorf("summary_md に %q が無い:\n%s", s, got.SummaryMd)
			}
		}
	})

	checkpoints := func(t *testing.T) (open, all int) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FILTER (WHERE c.status = 'open'), count(*) FROM confirmation c
			 JOIN ticket t ON t.id = c.ticket_id JOIN project p ON p.id = t.project_id
			 WHERE p.key = $1 AND c.reason = 'checkpoint'`, key).Scan(&open, &all); err != nil {
			t.Fatalf("checkpoint を数えられない: %v", err)
		}
		return
	}
	statusPath := ticketPath + "/delegation-status"

	t.Run("周期が過ぎるまでは checkpoint を立てない", func(t *testing.T) {
		getWithCookie(r, statusPath, session)
		if open, _ := checkpoints(t); open != 0 {
			t.Fatalf("checkpoint = %d, want 0", open)
		}
	})

	t.Run("周期が過ぎたら読まれたときに1件だけ立ち、答えるまで main を止める", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`UPDATE delegation_plan p SET decided_at = now() - interval '8 days'
			 FROM delegation d JOIN ticket t ON t.id = d.root_ticket_id JOIN project pr ON pr.id = t.project_id
			 WHERE p.delegation_id = d.id AND pr.key = $1`, key); err != nil {
			t.Fatalf("起点を戻せない: %v", err)
		}
		rec := getWithCookie(r, statusPath, session)
		getWithCookie(r, statusPath, session) // 2回目は立てない
		if open, _ := checkpoints(t); open != 1 {
			t.Fatalf("開いている checkpoint = %d, want 1", open)
		}
		if !strings.Contains(rec.Body.String(), "周期の要約") {
			t.Errorf("delegation-status の確認待ちに要約が無い: %s", rec.Body.String())
		}
		blocked := bearerPost(r, base+"/tickets", mainToken.Token, fmt.Sprintf(`{"type":"task","title":"演習","parent_seq":%d}`, rootSeq))
		if blocked.Code != http.StatusForbidden {
			t.Fatalf("checkpoint 中の起票 status = %d, want 403（body=%s）", blocked.Code, blocked.Body.String())
		}
		var id string
		if err := pool.QueryRow(ctx,
			`SELECT c.id FROM confirmation c JOIN ticket t ON t.id = c.ticket_id JOIN project p ON p.id = t.project_id
			 WHERE p.key = $1 AND c.reason = 'checkpoint' AND c.status = 'open'`, key).Scan(&id); err != nil {
			t.Fatalf("checkpoint を読めない: %v", err)
		}
		if rec := postWithCookie(r, fmt.Sprintf("%s/confirmation/%s/answer", ticketPath, id), session, `{"answer_md":"確認した"}`); rec.Code != http.StatusOK {
			t.Fatalf("回答 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if rec := bearerPost(r, base+"/tickets", mainToken.Token, fmt.Sprintf(`{"type":"task","title":"演習","parent_seq":%d}`, rootSeq)); rec.Code != http.StatusCreated {
			t.Fatalf("回答後の起票 status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		getWithCookie(r, statusPath, session)
		if open, all := checkpoints(t); open != 0 || all != 1 {
			t.Errorf("回答後の checkpoint open=%d all=%d, want 0 / 1（起点が直近の checkpoint に進む）", open, all)
		}
	})
}
