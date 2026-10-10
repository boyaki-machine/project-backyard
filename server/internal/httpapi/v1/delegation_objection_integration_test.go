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

// PM の事後の異議（Delegation.md 6.4）を**実際のDBに対して**通す。pb-275。
//
// ここでしか確かめられないもの：GetPlanForObjection の LATERAL（合格の評価を拾う）、
// 差し戻しの版の採番と「前の反映済みの版」の選び方、objected_at の一度きりの更新。
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationObjectionIntegration(t *testing.T) {
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
	adminEmail := "deleg-ob-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-ob-" + strings.ToLower(adminID[len(adminID)-8:])
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"事後の異議の結合テスト","workflow_template":"with_review"}`, key))
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
	passFindings := `[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},` +
		`{"aspect":"values","verdict":"pass","note_md":""}]`
	submit := func(t *testing.T, skeleton, bodyMd string) int {
		t.Helper()
		rec := bearerPost(r, ticketPath+"/plan", mainToken.Token,
			`{"scope":{},"skeleton":`+skeleton+`,"body_md":"`+bodyMd+`","reason_md":"変更"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("計画の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		return int(viewOf(t, rec)["version"].(float64))
	}
	pass := func(t *testing.T, version int) {
		t.Helper()
		rec := bearerPost(r, ticketPath+"/evaluation", evaluatorToken.Token,
			fmt.Sprintf(`{"target_kind":"plan","plan_version":%d,"verdict":"pass","findings":%s}`, version, passFindings))
		if rec.Code != http.StatusCreated {
			t.Fatalf("評価 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
	}
	object := func(version int) *httptest.ResponseRecorder {
		return postWithCookie(r, fmt.Sprintf("%s/plan/%d/object", ticketPath, version), session, `{"reason_md":"重点がずれた"}`)
	}
	planStatus := func(t *testing.T, version int) (string, *int) {
		t.Helper()
		var status string
		var revertsTo *int
		if err := pool.QueryRow(ctx,
			`SELECT p.status, p.reverts_to FROM delegation_plan p JOIN delegation d ON d.id = p.delegation_id
			 JOIN ticket t ON t.id = d.root_ticket_id JOIN project pr ON pr.id = t.project_id
			 WHERE pr.key = $1 AND p.version = $2`, key, version).Scan(&status, &revertsTo); err != nil {
			t.Fatalf("v%d を読めない: %v", version, err)
		}
		return status, revertsTo
	}

	one := `{"branches":[{"name":"演習","budget_share":1.0}]}`
	two := `{"branches":[{"name":"演習","budget_share":0.5},{"name":"模試","budget_share":0.5}]}`
	three := `{"branches":[{"name":"演習","budget_share":0.4},{"name":"模試","budget_share":0.3},{"name":"面接","budget_share":0.3}]}`

	if rec := postWithCookie(r, ticketPath+"/delegation", session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{}}`, main.ID, evaluator.ID)); rec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	submit(t, one, "初版")
	pass(t, 1)
	if rec := postWithCookie(r, ticketPath+"/delegation/approve", session, `{"plan_version":1}`); rec.Code != http.StatusOK {
		t.Fatalf("承認 status = %d（body=%s）", rec.Code, rec.Body.String())
	}

	t.Run("PM が承認した初版には異議を出せない", func(t *testing.T) {
		if rec := object(1); rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("評価が合格させた最新の構造の変更への異議は、前の版へ差し戻す", func(t *testing.T) {
		v2 := submit(t, two, "模試を足す")
		if s, _ := planStatus(t, v2); s != "proposed" {
			t.Fatalf("v2 = %s, want proposed（構造の変更）", s)
		}
		pass(t, v2)
		if s, _ := planStatus(t, v2); s != "accepted" {
			t.Fatalf("v2 = %s, want accepted（評価の合格で反映）", s)
		}
		rec := object(v2)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["reverted"] != true {
			t.Fatalf("reverted = %v, want true（body=%s）", v["reverted"], rec.Body.String())
		}
		s, revertsTo := planStatus(t, 3)
		if s != "accepted" || revertsTo == nil || *revertsTo != 1 {
			t.Errorf("v3 = %s reverts_to=%v, want accepted / 1", s, revertsTo)
		}
		var objected int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM evaluation e JOIN delegation_plan p ON p.id = e.plan_id
			 JOIN delegation d ON d.id = p.delegation_id JOIN ticket t ON t.id = d.root_ticket_id
			 JOIN project pr ON pr.id = t.project_id
			 WHERE pr.key = $1 AND e.objected_at IS NOT NULL`, key).Scan(&objected); err != nil {
			t.Fatalf("異議を数えられない: %v", err)
		}
		if objected != 1 {
			t.Errorf("objected_at の立った評価 = %d 件, want 1", objected)
		}
		if rec := object(v2); rec.Code != http.StatusConflict {
			t.Errorf("2回目の異議 status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("後に別の版が反映されていれば、異議だけを記録し差し戻さない", func(t *testing.T) {
		v4 := submit(t, three, "面接を足す")
		pass(t, v4)
		v5 := submit(t, three, "説明の手直し")
		if s, _ := planStatus(t, v5); s != "accepted" {
			t.Fatalf("v5 = %s, want accepted（軽微）", s)
		}
		rec := object(v4)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["reverted"] != false || v["later_version"] != float64(v5) {
			t.Errorf("応答 = %v, want reverted=false later_version=%d", v, v5)
		}
		var maxVersion int
		if err := pool.QueryRow(ctx,
			`SELECT max(p.version) FROM delegation_plan p JOIN delegation d ON d.id = p.delegation_id
			 JOIN ticket t ON t.id = d.root_ticket_id JOIN project pr ON pr.id = t.project_id
			 WHERE pr.key = $1`, key).Scan(&maxVersion); err != nil {
			t.Fatalf("版を数えられない: %v", err)
		}
		if maxVersion != v5 {
			t.Errorf("最新の版 = v%d, want v%d（差し戻しの版を作らない）", maxVersion, v5)
		}
	})

	t.Run("軽微な変更には異議を出せない", func(t *testing.T) {
		if rec := object(5); rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// ── 一覧（pb-274）：版と評価を新しい順に返し、異議の判定を添える ──
	t.Run("版の一覧は新しい順で、異議を出した版は objectable でない", func(t *testing.T) {
		v1x := submit(t, `{"branches":[{"name":"演習","budget_share":0.2},{"name":"模試","budget_share":0.2},{"name":"面接","budget_share":0.2},{"name":"小論文","budget_share":0.4}]}`, "小論文を足す")
		pass(t, v1x)
		rec := getWithCookie(r, ticketPath+"/plans", session)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got struct {
			Items []struct {
				Version        int     `json:"version"`
				Status         string  `json:"status"`
				RevertsTo      *int    `json:"reverts_to"`
				LatestVerdict  *string `json:"latest_verdict"`
				PassObjectedAt *int64  `json:"pass_objected_at"`
				Objectable     bool    `json:"objectable"`
			} `json:"items"`
		}
		decodeJSONBody(t, rec, &got)
		if len(got.Items) != v1x || got.Items[0].Version != v1x {
			t.Fatalf("件数 = %d・先頭 = v%d, want %d・v%d", len(got.Items), got.Items[0].Version, v1x, v1x)
		}
		want := map[int]bool{v1x: true, 4: false, 2: false, 1: false, 5: false}
		for _, it := range got.Items {
			if w, ok := want[it.Version]; ok && it.Objectable != w {
				t.Errorf("v%d objectable = %v, want %v", it.Version, it.Objectable, w)
			}
			if it.Version == 2 && it.PassObjectedAt == nil {
				t.Errorf("v2 の pass_objected_at が無い")
			}
			if it.Version == 3 && (it.RevertsTo == nil || *it.RevertsTo != 1) {
				t.Errorf("v3 reverts_to = %v, want 1", it.RevertsTo)
			}
		}
	})

	t.Run("評価の一覧は新しい順で、異議と評価者を返す", func(t *testing.T) {
		rec := getWithCookie(r, ticketPath+"/evaluations", session)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got struct {
			Items []struct {
				PlanVersion *int   `json:"plan_version"`
				Verdict     string `json:"verdict"`
				ObjectedAt  *int64 `json:"objected_at"`
				Evaluator   struct {
					DisplayName string `json:"display_name"`
				} `json:"evaluator"`
				EvaluatorModel struct {
					ClientKind *string `json:"client_kind"`
				} `json:"evaluator_model"`
			} `json:"items"`
		}
		decodeJSONBody(t, rec, &got)
		if len(got.Items) != 4 || got.Items[0].PlanVersion == nil || *got.Items[0].PlanVersion != 6 {
			t.Fatalf("items = %+v, want 4件で先頭は第6版の評価", got.Items)
		}
		objected := 0
		for _, it := range got.Items {
			if it.ObjectedAt != nil {
				objected++
			}
			if it.Evaluator.DisplayName != "evaluator" || it.EvaluatorModel.ClientKind == nil {
				t.Errorf("評価者 = %q / client_kind = %v", it.Evaluator.DisplayName, it.EvaluatorModel.ClientKind)
			}
		}
		if objected != 2 {
			t.Errorf("異議の立った評価 = %d 件, want 2", objected)
		}
	})

	t.Run("根でないチケットは422", func(t *testing.T) {
		other, _ := createIntegrationTicket(t, r, session, base, "根ではない")
		if rec := getWithCookie(r, fmt.Sprintf("%s/tickets/%d/plans", base, other), session); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
	})
}
