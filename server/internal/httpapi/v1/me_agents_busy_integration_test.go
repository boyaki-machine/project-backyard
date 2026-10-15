package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// GET /me/agents の open_works・delegations（ApiDesign.md 4.5.1）を**実際のDBに対して**通す。
//
// 確かめること：開いている作業記録だけが載り、閉じたものは載らない。終了していない委任の
// 役割が載り、取り下げて終了したら消える。
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestMeAgentsBusyIntegration(t *testing.T) {
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
	adminEmail := "agents-busy-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "agt-busy-" + strings.ToLower(adminID[len(adminID)-8:])
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"いまの仕事の結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	var createdAgents []string
	t.Cleanup(func() {
		bg := context.Background()
		// プロジェクトを先に消す（委任・作業記録がエージェントを RESTRICT で参照する。pb-287）。
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
		if strings.Contains(rec.Body.String(), `"open_works"`) {
			t.Errorf("登録の応答に open_works が載っている（一覧だけが返す。body=%s）", rec.Body.String())
		}
		var got agentJSON
		decodeJSONBody(t, rec, &got)
		createdAgents = append(createdAgents, got.ID)
		return got
	}
	worker := register("作業中")
	main := register("main")
	evaluator := register("評価")

	workSeq, workID := createIntegrationTicket(t, r, session, base, "受付画面")
	rootSeq, _ := createIntegrationTicket(t, r, session, base, "文化祭の準備")

	// 作業記録は REST の着手（エージェントのトークン）を経ずに DB へ置く。開いた記録の
	// 読み方だけを確かめたいので、計時の規則（Design.md 8.5.7）はここでは通さない。
	var projectID string
	if err := pool.QueryRow(ctx, `SELECT id FROM project WHERE key = $1`, key).Scan(&projectID); err != nil {
		t.Fatalf("プロジェクトを読めない: %v", err)
	}
	openSession := ulidgen.New()
	now := time.Now()
	if _, err := pool.Exec(ctx,
		`INSERT INTO work_session (id, project_id, ticket_id, actor_id, state, started_at, changed_at)
		 VALUES ($1, $2, $3, $4, 'running', $5, $5)`, openSession, projectID, workID, worker.ID, now); err != nil {
		t.Fatalf("作業記録を置けない: %v", err)
	}
	// 閉じた記録は載らない。
	if _, err := pool.Exec(ctx,
		`INSERT INTO work_session (id, project_id, ticket_id, actor_id, state, started_at, changed_at, ended_at)
		 VALUES ($1, $2, $3, $4, 'finished', $5, $5, $5)`, ulidgen.New(), projectID, workID, main.ID, now.Add(-time.Hour)); err != nil {
		t.Fatalf("閉じた作業記録を置けない: %v", err)
	}

	rec = postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, rootSeq), session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{}}`, main.ID, evaluator.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", rec.Code, rec.Body.String())
	}

	type busy struct {
		works  []string
		delegs []string
	}
	read := func(t *testing.T) map[string]busy {
		t.Helper()
		rec := getWithCookie(r, "/api/v1/me/agents", session)
		if rec.Code != http.StatusOK {
			t.Fatalf("一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got agentListJSON
		decodeJSONBody(t, rec, &got)
		out := map[string]busy{}
		for _, it := range got.Items {
			if it.OpenWorks == nil || it.Delegations == nil {
				t.Errorf("%s: open_works / delegations が null（空の配列を返す。body=%s）", it.DisplayName, rec.Body.String())
			}
			var b busy
			for _, w := range it.OpenWorks {
				b.works = append(b.works, fmt.Sprintf("%d %s", w.TicketSeq, w.TicketTitle))
			}
			for _, d := range it.Delegations {
				b.delegs = append(b.delegs, fmt.Sprintf("%d %s %s", d.RootSeq, d.Role, d.Status))
			}
			out[it.ID] = b
		}
		return out
	}

	t.Run("開いた作業記録と、終了していない委任の役割が載る", func(t *testing.T) {
		got := read(t)
		if w := got[worker.ID].works; len(w) != 1 || w[0] != fmt.Sprintf("%d 受付画面", workSeq) {
			t.Errorf("作業中の open_works = %v, want [%d 受付画面]", w, workSeq)
		}
		if w := got[main.ID].works; len(w) != 0 {
			t.Errorf("main の open_works = %v, want 空（閉じた記録は載らない）", w)
		}
		if d := got[main.ID].delegs; len(d) != 1 || d[0] != fmt.Sprintf("%d main planning", rootSeq) {
			t.Errorf("main の delegations = %v, want [%d main planning]", d, rootSeq)
		}
		if d := got[evaluator.ID].delegs; len(d) != 1 || d[0] != fmt.Sprintf("%d evaluator planning", rootSeq) {
			t.Errorf("評価の delegations = %v, want [%d evaluator planning]", d, rootSeq)
		}
		if d := got[worker.ID].delegs; len(d) != 0 {
			t.Errorf("作業中の delegations = %v, want 空", d)
		}
	})

	t.Run("取り下げて終了した委任は載らない", func(t *testing.T) {
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation/withdraw", base, rootSeq), session,
			`{"execution_mode":"agent_draft"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("取り下げ status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		got := read(t)
		if d := got[main.ID].delegs; len(d) != 0 {
			t.Errorf("取り下げ後の main の delegations = %v, want 空", d)
		}
	})
}
