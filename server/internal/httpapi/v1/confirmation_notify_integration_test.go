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

// 確認待ちの通知（Delegation.md 8.2、DbDesign.md 6.33.1）を**実際のDBに対して**通す。
//
// 確かめること：
//   - 確認待ちが立つと、**委任を作った人だけ**の未読数が1増える（別の管理者には届かない）
//   - 回答・委任の取り下げで、その通知が取り下げられる（受け入れは evaluation_integration_test.go）
//   - PB 自身が立てた確認待ちは、通知元が PB になる
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestConfirmationNotificationIntegration(t *testing.T) {
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

	pmID := ulidgen.New()
	pmEmail := "deleg-notice-pm-" + pmID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, pmID, pmEmail, auth.SystemRoleAdministrator)
	pmSession := loginAs(t, r, pmEmail)

	// 委任を作っていない管理者。delegation.manage を持つが、通知は届かない。
	otherID := ulidgen.New()
	otherEmail := "deleg-notice-other-" + otherID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, otherID, otherEmail, auth.SystemRoleAdministrator)
	otherSession := loginAs(t, r, otherEmail)

	key := "deleg-ntc-" + strings.ToLower(pmID[len(pmID)-8:])
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key)
	})
	rec := postWithCookie(r, "/api/v1/projects", pmSession,
		fmt.Sprintf(`{"key":%q,"name":"確認待ちの通知の結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	var createdAgents []string
	t.Cleanup(func() {
		bg := context.Background()
		// プロジェクトを先に消す（委任がエージェントを RESTRICT で参照する。pb-287）。
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
		rec := postWithCookie(r, "/api/v1/me/agents", pmSession,
			fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":"claude_code"}`, name, key))
		if rec.Code != http.StatusCreated {
			t.Fatalf("エージェント登録の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got agentJSON
		decodeJSONBody(t, rec, &got)
		createdAgents = append(createdAgents, got.ID)
		return got
	}
	main := register("通知の main")
	evaluator := register("通知の評価")
	var mainToken issuedAgentTokenJSON
	decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+main.ID+"/tokens", pmSession,
		`{"expires_in_days":30}`), &mainToken)

	rootSeq, _ := createIntegrationTicket(t, r, pmSession, base, "文化祭の準備")
	delegPath := fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq)
	startDelegation := func(t *testing.T) string {
		t.Helper()
		rec := postWithCookie(r, delegPath, pmSession,
			fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"準備する"}}`, main.ID, evaluator.ID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("委任の作成 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var id string
		if err := pool.QueryRow(ctx,
			`UPDATE delegation SET status = 'approved' WHERE status <> 'ended' AND root_ticket_id =
			   (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)
			 RETURNING id`,
			key, rootSeq).Scan(&id); err != nil {
			t.Fatalf("委任を承認済みにできない: %v", err)
		}
		return id
	}
	delegationID := startDelegation(t)

	confirmPath := fmt.Sprintf("%s/tickets/%d/confirmation", base, rootSeq)
	raise := func(t *testing.T, reason, question string) string {
		t.Helper()
		rec := bearerPost(r, confirmPath, mainToken.Token,
			fmt.Sprintf(`{"reason":%q,"question_md":%q}`, reason, question))
		if rec.Code != http.StatusCreated {
			t.Fatalf("確認待ちの提出 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		return viewOf(t, rec)["id"].(string)
	}
	unread := func(t *testing.T, session string) int {
		t.Helper()
		rec := getWithCookie(r, "/api/v1/me/notifications/unread-count", session)
		if rec.Code != http.StatusOK {
			t.Fatalf("未読数の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var v struct{ Count int }
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("未読数を読めない: %v", err)
		}
		return v.Count
	}
	type noticeRow struct {
		Kind, Link, LinkLabel, SourceName string
		SourceID                          *string
	}
	noticeOf := func(t *testing.T, confirmationID string) (noticeRow, int) {
		t.Helper()
		var n noticeRow
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification WHERE subject_key = $1`,
			"delegation_confirmation:"+confirmationID).Scan(&count); err != nil {
			t.Fatalf("通知を数えられない: %v", err)
		}
		if count == 1 {
			if err := pool.QueryRow(ctx,
				`SELECT kind, link, link_label, source_name, source_id FROM notification WHERE subject_key = $1`,
				"delegation_confirmation:"+confirmationID,
			).Scan(&n.Kind, &n.Link, &n.LinkLabel, &n.SourceName, &n.SourceID); err != nil {
				t.Fatalf("通知を読めない: %v", err)
			}
		}
		return n, count
	}

	t.Run("確認待ちが立つと委任を作った人だけに届き、回答で取り下がる", func(t *testing.T) {
		pmBefore, otherBefore := unread(t, pmSession), unread(t, otherSession)

		id := raise(t, "scope_change", "範囲を広げてよいですか")
		if got := unread(t, pmSession); got != pmBefore+1 {
			t.Errorf("PM の未読数 = %d, want %d", got, pmBefore+1)
		}
		if got := unread(t, otherSession); got != otherBefore {
			t.Errorf("PM 以外の未読数 = %d, want %d（届いてはいけない）", got, otherBefore)
		}
		n, count := noticeOf(t, id)
		if count != 1 {
			t.Fatalf("通知の件数 = %d, want 1", count)
		}
		wantLink := fmt.Sprintf("/p/%s/tickets/%d#confirmations", key, rootSeq)
		if n.Kind != "delegation.confirmation" || n.Link != wantLink ||
			n.LinkLabel != fmt.Sprintf("%s-%d 文化祭の準備", key, rootSeq) ||
			n.SourceID == nil || *n.SourceID != main.ID || n.SourceName != "通知の main" {
			t.Errorf("通知 = %+v, want kind=delegation.confirmation link=%s 通知元=main", n, wantLink)
		}

		rec := postWithCookie(r, fmt.Sprintf("%s/%s/answer", confirmPath, id), pmSession, `{"answer_md":"広げてよい"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("回答 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if _, count := noticeOf(t, id); count != 0 {
			t.Errorf("回答後の通知 = %d 件, want 0（取り下げる）", count)
		}
		if got := unread(t, pmSession); got != pmBefore {
			t.Errorf("回答後の PM の未読数 = %d, want %d", got, pmBefore)
		}
	})

	t.Run("PB が立てた確認待ちは、通知元が PB になる", func(t *testing.T) {
		// 評価できない（evaluation_unable）と同じ口。raised_by は呼び出しの文脈の actor。
		var id string
		err := store.NewTxRunner(pool).RunInTx(ctx, func(tq gen.Querier) error {
			var rootID string
			if err := pool.QueryRow(ctx, `SELECT root_ticket_id FROM delegation WHERE id = $1`, delegationID).Scan(&rootID); err != nil {
				return err
			}
			if err := raiseSystemConfirmation(ctx, tq, delegationID, rootID,
				confirmationReasonEvaluationUnable, "判断できません", evaluator.ID); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatalf("PB の確認待ちを立てられない: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT id FROM confirmation WHERE delegation_id = $1 AND reason = 'evaluation_unable' AND status = 'open'`,
			delegationID).Scan(&id); err != nil {
			t.Fatalf("確認待ちを読めない: %v", err)
		}
		n, count := noticeOf(t, id)
		if count != 1 || n.SourceID != nil || n.SourceName != "PB" {
			t.Errorf("通知 = %+v（%d 件）, want 1件・通知元 PB", n, count)
		}
		rec := postWithCookie(r, fmt.Sprintf("%s/%s/answer", confirmPath, id), pmSession, `{"answer_md":"続けてよい"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("回答 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("委任を取り下げると、開いている確認待ちの通知も取り下がる", func(t *testing.T) {
		hearing := raise(t, "hearing", "目的を教えてください")
		gate := raise(t, "charter_change", "規約を変えてよいですか")
		for _, id := range []string{hearing, gate} {
			if _, count := noticeOf(t, id); count != 1 {
				t.Fatalf("取り下げ前の通知 = %d 件, want 1", count)
			}
		}
		rec := postWithCookie(r, delegPath+"/withdraw", pmSession, `{"execution_mode":"agent_draft"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("取り下げ status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		for _, id := range []string{hearing, gate} {
			if _, count := noticeOf(t, id); count != 0 {
				t.Errorf("取り下げ後の通知（%s） = %d 件, want 0", id, count)
			}
		}
	})
}
