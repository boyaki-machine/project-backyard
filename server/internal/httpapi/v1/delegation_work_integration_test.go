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

// 委任の外で開いた作業記録を、委任の main になった後でも閉じられる（pb-306）。
//
// #226 の試行で、main になったエージェントが別のチケット（#288）で開いていた記録を
// resume も finish もできず（403「委任の範囲外の作業は記録できません」）、パックの
// 「継続するか abandon で終了せよ」と食い違った。PM が画面から閉じて解消した。
//
// **閉じる操作は範囲を広げない**ので、自分の記録の finish / abandon は委任の境界を
// 検査しない。委任の外での新しい着手は、これまでどおり拒む。
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationMainClosesOutsideWorkIntegration(t *testing.T) {
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
	adminEmail := "deleg-work-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-wk-" + strings.ToLower(adminID[len(adminID)-8:])
	var createdAgents []string
	t.Cleanup(func() {
		bg := context.Background()
		// **プロジェクトを先に消す**（委任と作業記録がエージェントを RESTRICT で参照する）。
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
	if rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"委任と作業記録の結合テスト","workflow_template":"simple"}`, key)); rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	register := func(name string) (agentJSON, string) {
		t.Helper()
		rec := postWithCookie(r, "/api/v1/me/agents", session,
			fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":"claude_code"}`, name, key))
		if rec.Code != http.StatusCreated {
			t.Fatalf("エージェント登録の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got agentJSON
		decodeJSONBody(t, rec, &got)
		createdAgents = append(createdAgents, got.ID)
		var token issuedAgentTokenJSON
		decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+got.ID+"/tokens", session, `{"expires_in_days":30}`), &token)
		return got, token.Token
	}
	main, mainToken := register("main")
	evaluator, _ := register("evaluator")

	// **エージェントが進めるには、担当が自分の所有者（admin）であることが要る**（9.6 の検証6）。
	ticket := func(title string) int {
		t.Helper()
		rec := postWithCookie(r, base+"/tickets", session,
			fmt.Sprintf(`{"type":"task","title":%q,"assignee_id":%q}`, title, adminID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("起票 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		return int(viewOf(t, rec)["seq"].(float64))
	}
	outsideSeq := ticket("委任の外の観察")
	otherSeq := ticket("委任の外の別の仕事")
	rootSeq := ticket("文化祭モード")
	outside := fmt.Sprintf("%s/tickets/%d", base, outsideSeq)

	// 委任の外で記録を開く（まだ main ではない）。
	rec := bearerPost(r, outside+"/work-sessions", mainToken, `{"request_id":"outside-start"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("委任の外での開始 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	var work testWorkView
	decodeJSONBody(t, rec, &work)

	// そのエージェントを main にして委任を作る。
	if rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq), session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{}}`, main.ID, evaluator.ID)); rec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", rec.Code, rec.Body.String())
	}

	t.Run("根の詳細で、開いている記録が別のチケットのものだと分かる", func(t *testing.T) {
		v := viewOf(t, bearerGet(r, fmt.Sprintf("%s/tickets/%d", base, rootSeq), mainToken))
		wc, _ := v["work_context"].(map[string]any)
		if wc == nil || wc["open_work_id"] != work.ID || wc["open_work_seq"] != float64(outsideSeq) {
			t.Errorf("work_context = %v, want open_work_id=%s・open_work_seq=%d", wc, work.ID, outsideSeq)
		}
	})

	t.Run("委任の外での新しい着手は、これまでどおり拒む", func(t *testing.T) {
		rec := bearerPost(r, fmt.Sprintf("%s/tickets/%d/work-sessions", base, otherSeq), mainToken, `{"request_id":"other-start"}`)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "委任の範囲外") {
			t.Errorf("委任の外での開始 status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("委任の外で開いた記録を、main になった後でも閉じられる", func(t *testing.T) {
		rec := bearerPost(r, outside+"/work-sessions/"+work.ID+"/events", mainToken,
			`{"request_id":"outside-finish","action":"finish","result":"partial","incomplete":true,"note":"委任の前の観察を閉じる"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("委任の外の記録の終了 status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		var state string
		if err := pool.QueryRow(ctx, `SELECT state FROM work_session WHERE id = $1`, work.ID).Scan(&state); err != nil {
			t.Fatalf("作業記録を読めない: %v", err)
		}
		if state != "finished" {
			t.Errorf("作業記録の状態 = %q, want finished", state)
		}
		v := viewOf(t, bearerGet(r, fmt.Sprintf("%s/tickets/%d", base, rootSeq), mainToken))
		if wc, _ := v["work_context"].(map[string]any); wc == nil || wc["open_work_id"] != nil || wc["open_work_seq"] != nil {
			t.Errorf("閉じた後の work_context = %v, want 開いている記録なし", wc)
		}
	})
}
