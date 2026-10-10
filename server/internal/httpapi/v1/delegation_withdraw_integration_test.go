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

// 委任の取り下げと、取り下げた根での始め直し（Delegation.md 4.3「取り下げ」）を
// **実際のDBに対して**通す。pb-281。
//
// ここでしか確かめられないもの：
//
//   - **0058 の部分一意索引**（uq_delegation_root_active）——終了した委任が
//     あっても2件目を作れ、終了していない2件目は 409 になること
//   - 根から引く問い合わせが**最新の1件**を返すこと（始め直した後の停止・
//     delegation-status が、前の終了した委任を拾わない）
//   - 取り下げの1トランザクション（委任・根の実行モード・確認待ち・コメント）
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationWithdrawIntegration(t *testing.T) {
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
	adminEmail := "deleg-wd-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-wd-" + strings.ToLower(adminID[len(adminID)-8:])
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"委任の取り下げの結合テスト","workflow_template":"with_review"}`, key))
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
	createBody := fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{}}`, main.ID, evaluator.ID)

	// 委任を作り、確認待ちを1件開き、計画 → 評価の合格 → PM の承認で実行中にし、子を1件作る。
	if rec := postWithCookie(r, ticketPath+"/delegation", session, createBody); rec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := bearerPost(r, ticketPath+"/confirmation", mainToken.Token,
		`{"reason":"hearing","question_md":"目的を教えてください"}`); rec.Code != http.StatusCreated {
		t.Fatalf("確認待ち status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := bearerPost(r, ticketPath+"/plan", mainToken.Token,
		`{"scope":{},"skeleton":{"branches":[{"name":"演習","budget_share":1.0}]},"body_md":"初版","reason_md":"開始"}`); rec.Code != http.StatusCreated {
		t.Fatalf("計画の提出 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	passFindings := `[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},` +
		`{"aspect":"values","verdict":"pass","note_md":""}]`
	if rec := bearerPost(r, ticketPath+"/evaluation", evaluatorToken.Token,
		`{"target_kind":"plan","plan_version":1,"verdict":"pass","findings":`+passFindings+`}`); rec.Code != http.StatusCreated {
		t.Fatalf("評価 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := postWithCookie(r, ticketPath+"/delegation/approve", session, `{"plan_version":1}`); rec.Code != http.StatusOK {
		t.Fatalf("承認 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	rec = bearerPost(r, base+"/tickets", mainToken.Token,
		fmt.Sprintf(`{"type":"task","title":"演習","parent_seq":%d}`, rootSeq))
	if rec.Code != http.StatusCreated {
		t.Fatalf("子の起票 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	childSeq := int(viewOf(t, rec)["seq"].(float64))
	childPath := fmt.Sprintf("%s/tickets/%d", base, childSeq)
	childModeBefore := viewOf(t, getWithCookie(r, childPath, session))["execution_mode"]

	t.Run("戻す先の実行モードが無い・不正なら422", func(t *testing.T) {
		for _, body := range []string{`{}`, `{"execution_mode":"delegated"}`} {
			rec := postWithCookie(r, ticketPath+"/delegation/withdraw", session, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s: status = %d, want 422（body=%s）", body, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("main のトークンでは取り下げられない", func(t *testing.T) {
		rec := bearerPost(r, ticketPath+"/delegation/withdraw", mainToken.Token, `{"execution_mode":"agent_draft"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("実行中から取り下げると、委任は終了し根は従来のモードへ戻る", func(t *testing.T) {
		rec := postWithCookie(r, ticketPath+"/delegation/withdraw", session,
			`{"execution_mode":"agent_draft","reason_md":"粒度が大きすぎた"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if s := viewOf(t, rec)["status"]; s != "ended" {
			t.Errorf("status = %v, want ended", s)
		}
		if m := viewOf(t, getWithCookie(r, ticketPath, session))["execution_mode"]; m != "agent_draft" {
			t.Errorf("根の execution_mode = %v, want agent_draft", m)
		}
		if m := viewOf(t, getWithCookie(r, childPath, session))["execution_mode"]; m != childModeBefore {
			t.Errorf("子の execution_mode = %v, want %v（変えない）", m, childModeBefore)
		}
		var open, withdrawn int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FILTER (WHERE c.status = 'open'), count(*) FILTER (WHERE c.status = 'withdrawn')
			 FROM confirmation c JOIN ticket t ON t.id = c.ticket_id
			 JOIN project p ON p.id = t.project_id WHERE p.key = $1`, key).Scan(&open, &withdrawn); err != nil {
			t.Fatalf("確認待ちを数えられない: %v", err)
		}
		if open != 0 || withdrawn != 1 {
			t.Errorf("確認待ち open=%d withdrawn=%d, want 0 / 1", open, withdrawn)
		}
		var comments int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM comment c JOIN ticket t ON t.id = c.ticket_id
			 JOIN project p ON p.id = t.project_id
			 WHERE p.key = $1 AND t.seq = $2 AND c.kind = 'decision' AND c.body_md LIKE '%自律実行をやめた%粒度が大きすぎた%'`,
			key, rootSeq).Scan(&comments); err != nil {
			t.Fatalf("コメントを数えられない: %v", err)
		}
		if comments != 1 {
			t.Errorf("取り下げのコメント = %d 件, want 1", comments)
		}
	})

	t.Run("終了した委任には計画を出せず、もう一度は取り下げられない", func(t *testing.T) {
		rec := bearerPost(r, ticketPath+"/plan", mainToken.Token,
			`{"scope":{},"skeleton":{"branches":[]},"body_md":"再","reason_md":"再"}`)
		if rec.Code < 400 {
			t.Fatalf("計画 status = %d, want 4xx（body=%s）", rec.Code, rec.Body.String())
		}
		rec = postWithCookie(r, ticketPath+"/delegation/withdraw", session, `{"execution_mode":"human_only"}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("2回目の取り下げ status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("delegation-statusは根に終了を添える", func(t *testing.T) {
		v := viewOf(t, getWithCookie(r, ticketPath+"/delegation-status", session))
		if v["in_delegation"] != false || v["status"] != "ended" || v["root_seq"] != float64(rootSeq) {
			t.Errorf("delegation-status = %v, want in_delegation=false status=ended root_seq=%d", v, rootSeq)
		}
		// 木の外になった子には、終了を添えない（根だけ）。
		cv := viewOf(t, getWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation-status", base, childSeq), session))
		if cv["in_delegation"] != false || cv["status"] != nil {
			t.Errorf("子の delegation-status = %v, want in_delegation=false だけ", cv)
		}
	})

	t.Run("取り下げた根で、もう一度自律実行を始められる", func(t *testing.T) {
		rec := postWithCookie(r, ticketPath+"/delegation", session, createBody)
		if rec.Code != http.StatusCreated {
			t.Fatalf("始め直し status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		if s := viewOf(t, rec)["status"]; s != "planning" {
			t.Errorf("status = %v, want planning", s)
		}
		// 終了していない2件目は 409（uq_delegation_root_active の名前の一致）。
		if rec := postWithCookie(r, ticketPath+"/delegation", session, createBody); rec.Code != http.StatusConflict {
			t.Fatalf("2件目 status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
		// 根から引く問い合わせが新しい委任を拾う：状態は計画中、停止もできる。
		v := viewOf(t, getWithCookie(r, ticketPath+"/delegation-status", session))
		if v["in_delegation"] != true || v["status"] != "planning" {
			t.Errorf("delegation-status = %v, want in_delegation=true status=planning", v)
		}
		if rec := postWithCookie(r, ticketPath+"/delegation/pause", session, `{}`); rec.Code != http.StatusOK {
			t.Fatalf("停止 status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		// 一時停止からも取り下げられる。
		rec = postWithCookie(r, ticketPath+"/delegation/withdraw", session, `{"execution_mode":"human_only"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("一時停止からの取り下げ status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM delegation d JOIN ticket t ON t.id = d.root_ticket_id
			 JOIN project p ON p.id = t.project_id WHERE p.key = $1 AND d.status = 'ended'`, key).Scan(&n); err != nil {
			t.Fatalf("委任を数えられない: %v", err)
		}
		if n != 2 {
			t.Errorf("終了した委任 = %d 件, want 2（前の委任も履歴として残る）", n)
		}
	})
}
