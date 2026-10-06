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

// 確認待ち（Delegation.md 8章・12.2）を**実際のDBに対して**通す。pb-249。
//
// 単体テストはフェイクを差し替えるので queries/confirmation.sql は一度も
// 実行されない。**ここでしか確かめられないものが2つある。**
//
//   - **reason の CHECK 制約が実在すること**（main が立てられない理由を
//     アプリ側の分岐だけでなく DB 制約としても拒む）
//   - **HasOpenConfirmation が実際の行を見て、他の書き込み口（起票）を
//     実DBの状態から正しく止めること**
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestConfirmationIntegration(t *testing.T) {
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
	adminEmail := "deleg-confirm-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-cfm-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key)
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"確認待ちの結合テスト","workflow_template":"simple"}`, key))
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
	if _, err := pool.Exec(ctx,
		`UPDATE delegation SET status = 'approved' WHERE root_ticket_id =
		   (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)`,
		key, rootSeq); err != nil {
		t.Fatalf("委任を承認済みにできない: %v", err)
	}

	confirmPath := fmt.Sprintf("%s/tickets/%d/confirmation", base, rootSeq)
	createTicketPath := base + "/tickets"

	t.Run("hearingは関門に数えず、起票は止まらない", func(t *testing.T) {
		rec := bearerPost(r, confirmPath, mainToken.Token,
			`{"reason":"hearing","question_md":"目的を教えてください"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("確認待ちの提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}

		rec = bearerPost(r, createTicketPath, mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"演習1","parent_seq":%d}`, rootSeq))
		if rec.Code != http.StatusCreated {
			t.Fatalf("起票（hearing中） status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("scope_changeが開いているあいだ起票は止まり、答えると再開する", func(t *testing.T) {
		rec := bearerPost(r, confirmPath, mainToken.Token,
			`{"reason":"scope_change","question_md":"範囲を広げてよいですか"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("確認待ちの提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		confirmID := v["id"].(string)

		rec = bearerPost(r, createTicketPath, mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"演習2","parent_seq":%d}`, rootSeq))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("起票（確認待ち中） status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}

		answerPath := fmt.Sprintf("%s/%s/answer", confirmPath, confirmID)
		rec = postWithCookie(r, answerPath, session, `{"answer_md":"広げてよい"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("回答 status = %d（body=%s）", rec.Code, rec.Body.String())
		}

		rec = bearerPost(r, createTicketPath, mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"演習2","parent_seq":%d}`, rootSeq))
		if rec.Code != http.StatusCreated {
			t.Fatalf("起票（回答後） status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}

		// 2本目の答えは拒む。
		rec = postWithCookie(r, answerPath, session, `{"answer_md":"もう一度"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("2本目の回答 status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("mainはevaluation_unableを立てられない", func(t *testing.T) {
		rec := bearerPost(r, confirmPath, mainToken.Token,
			`{"reason":"evaluation_unable","question_md":"判断できません"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
	})
}
