package v1

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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

// 承認済みの委任の main は、木の中ならチケットごとに1件ずつ作業記録を同時に開ける（pb-344）。
//
// サブエージェントは main のトークンで動くので、子を並行して消化すると同じエージェントの記録が
// 重なる。「PJ内1件」のままでは2件目が 409 で拒まれ、子に実績時間が入らなかった（#226）。
// 子ごとに記録を分けるので、合計の作業時間は壁時計の時間を超えうる。
func TestDelegationMainParallelWorkIntegration(t *testing.T) {
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
	adminEmail := "deleg-par-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-par-" + strings.ToLower(adminID[len(adminID)-8:])
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
		fmt.Sprintf(`{"key":%q,"name":"並行する作業記録の結合テスト","workflow_template":"simple"}`, key)); rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	register := func(name string, extra ...string) (agentJSON, string) {
		t.Helper()
		rec := postWithCookie(r, "/api/v1/me/agents", session,
			fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":"claude_code"}`, name, key))
		if rec.Code != http.StatusCreated {
			t.Fatalf("エージェント登録の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got agentJSON
		decodeJSONBody(t, rec, &got)
		createdAgents = append(createdAgents, got.ID)
		body := `{"expires_in_days":30}`
		if len(extra) > 0 {
			scopes := append(append([]string{}, agentDefaultScopes...), extra...)
			body = fmt.Sprintf(`{"expires_in_days":30,"scopes":["%s"]}`, strings.Join(scopes, `","`))
		}
		var token issuedAgentTokenJSON
		decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+got.ID+"/tokens", session, body), &token)
		return got, token.Token
	}
	main, mainToken := register("main")
	evaluator, evaluatorToken := register("evaluator", "delegation.evaluate")

	rec := postWithCookie(r, base+"/tickets", session,
		fmt.Sprintf(`{"type":"task","title":"文化祭モード","assignee_id":%q}`, adminID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("根の起票 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	rootSeq := int(viewOf(t, rec)["seq"].(float64))
	outsideSeq, _ := createIntegrationTicket(t, r, session, base, "委任の外の仕事")
	root := fmt.Sprintf("%s/tickets/%d", base, rootSeq)

	// 委任を作り、計画を評価に通して承認する（main が木の中に起票できるのは承認済みから）。
	for _, s := range []struct {
		path, token, body string
		cookie            bool
		want              int
	}{
		{root + "/delegation", session, fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{}}`, main.ID, evaluator.ID), true, http.StatusCreated},
		{root + "/plan", mainToken, `{"scope":{},"skeleton":{"branches":[{"name":"実装","budget_share":1.0}]},"body_md":"初版","reason_md":"開始"}`, false, http.StatusCreated},
		{root + "/evaluation", evaluatorToken, `{"target_kind":"plan","plan_version":1,"verdict":"pass","findings":[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},{"aspect":"values","verdict":"pass","note_md":""}]}`, false, http.StatusCreated},
		{root + "/delegation/approve", session, `{"plan_version":1}`, true, http.StatusOK},
	} {
		var rec *httptest.ResponseRecorder
		if s.cookie {
			rec = postWithCookie(r, s.path, s.token, s.body)
		} else {
			rec = bearerPost(r, s.path, s.token, s.body)
		}
		if rec.Code != s.want {
			t.Fatalf("%s status = %d, want %d（body=%s）", s.path, rec.Code, s.want, rec.Body.String())
		}
	}
	child := func(title string) int {
		t.Helper()
		rec := bearerPost(r, base+"/tickets", mainToken, fmt.Sprintf(`{"type":"task","title":%q,"parent_seq":%d}`, title, rootSeq))
		if rec.Code != http.StatusCreated {
			t.Fatalf("子の起票 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		return int(viewOf(t, rec)["seq"].(float64))
	}
	childA, childB := child("サーバの実装"), child("画面の実装")

	start := func(seq int, requestID string) *httptest.ResponseRecorder {
		return bearerPost(r, fmt.Sprintf("%s/tickets/%d/work-sessions", base, seq), mainToken,
			fmt.Sprintf(`{"request_id":%q,"note":%q}`, requestID, requestID))
	}
	works := map[int]string{}
	t.Run("木の中なら、チケットごとに同時に開ける", func(t *testing.T) {
		for _, s := range []struct {
			seq  int
			name string
		}{{rootSeq, "main"}, {childA, "sub-1"}, {childB, "sub-2"}} {
			rec := start(s.seq, s.name)
			if rec.Code != http.StatusOK {
				t.Fatalf("#%d の開始 status = %d, want 200（body=%s）", s.seq, rec.Code, rec.Body.String())
			}
			var w testWorkView
			decodeJSONBody(t, rec, &w)
			works[s.seq] = w.ID
			time.Sleep(20 * time.Millisecond)
		}
	})

	t.Run("同じチケットに2件目は開けない", func(t *testing.T) {
		rec := start(childA, "sub-1-again")
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "このチケットで開いている作業") {
			t.Errorf("同じ子での2件目 status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("委任の外では開けない", func(t *testing.T) {
		if rec := start(outsideSeq, "outside"); rec.Code != http.StatusForbidden {
			t.Errorf("委任の外での開始 status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("開いている記録をすべて返し、代表はこのチケットのもの", func(t *testing.T) {
		v := viewOf(t, bearerGet(r, fmt.Sprintf("%s/tickets/%d", base, childA), mainToken))
		wc, _ := v["work_context"].(map[string]any)
		list, _ := wc["open_works"].([]any)
		if len(list) != 3 {
			t.Fatalf("open_works = %v, want 3件", wc["open_works"])
		}
		if wc["open_work_id"] != works[childA] || wc["open_work_seq"] != float64(childA) {
			t.Errorf("代表 = %v / %v, want #%d の記録", wc["open_work_id"], wc["open_work_seq"], childA)
		}
	})

	t.Run("それぞれ閉じると子ごとに時間が入り、合計が壁時計を超える", func(t *testing.T) {
		for _, seq := range []int{childA, childB, rootSeq} {
			rec := bearerPost(r, fmt.Sprintf("%s/tickets/%d/work-sessions/%s/events", base, seq, works[seq]), mainToken,
				fmt.Sprintf(`{"request_id":"finish-%d","action":"finish","result":"completed"}`, seq))
			if rec.Code != http.StatusOK {
				t.Fatalf("#%d の終了 status = %d（body=%s）", seq, rec.Code, rec.Body.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		var sum, span int64
		if err := pool.QueryRow(ctx, `
			SELECT sum(active_ms)::bigint, (extract(epoch FROM max(ended_at) - min(started_at)) * 1000)::bigint
			FROM work_session WHERE id = ANY($1)`, []string{works[rootSeq], works[childA], works[childB]},
		).Scan(&sum, &span); err != nil {
			t.Fatalf("作業記録を集計できない: %v", err)
		}
		if sum <= span {
			t.Errorf("作業時間の合計 %dms が壁時計 %dms を超えない", sum, span)
		}
		for _, seq := range []int{childA, childB} {
			v := viewOf(t, bearerGet(r, fmt.Sprintf("%s/tickets/%d", base, seq), mainToken))
			if h, _ := v["actual_hours"].(float64); h <= 0 {
				t.Errorf("#%d の actual_hours = %v, want 正の値", seq, v["actual_hours"])
			}
		}
	})
}
