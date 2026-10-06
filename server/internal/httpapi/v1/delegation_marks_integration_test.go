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

// 一覧の自律実行の印（Delegation.md 8.2）を**実際のDBに対して**通す。pb-276。
//
// ここでしか確かめられないもの：ListDelegationMarks の再帰（根から孫まで）と、
// 確認待ちが立っただけで一覧の ETag が変わること（updated_at は動かない）。
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationMarksIntegration(t *testing.T) {
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
	adminEmail := "deleg-mk-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-mk-" + strings.ToLower(adminID[len(adminID)-8:])
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"自律実行の印の結合テスト","workflow_template":"with_review"}`, key))
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
	outsideSeq, _ := createIntegrationTicket(t, r, session, base, "普段の仕事")
	ticketPath := fmt.Sprintf("%s/tickets/%d", base, rootSeq)

	type mark struct {
		RootSeq           int    `json:"root_seq"`
		IsRoot            bool   `json:"is_root"`
		Status            string `json:"status"`
		OpenConfirmations int    `json:"open_confirmations"`
	}
	list := func(t *testing.T) (map[int]*mark, string) {
		t.Helper()
		rec := getWithCookie(r, base+"/tickets?per_page=100", session)
		if rec.Code != http.StatusOK {
			t.Fatalf("一覧 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got struct {
			Items []struct {
				Seq        int   `json:"seq"`
				Delegation *mark `json:"delegation"`
			} `json:"items"`
		}
		decodeJSONBody(t, rec, &got)
		out := map[int]*mark{}
		for _, it := range got.Items {
			out[it.Seq] = it.Delegation
		}
		return out, rec.Header().Get("ETag")
	}

	t.Run("委任が無ければ印は null", func(t *testing.T) {
		m, _ := list(t)
		if m[rootSeq] != nil || m[outsideSeq] != nil {
			t.Fatalf("印 = %+v / %+v, want null", m[rootSeq], m[outsideSeq])
		}
	})

	if rec := postWithCookie(r, ticketPath+"/delegation", session,
		fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{}}`, main.ID, evaluator.ID)); rec.Code != http.StatusCreated {
		t.Fatalf("委任の作成 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := bearerPost(r, ticketPath+"/plan", mainToken.Token,
		`{"scope":{},"skeleton":{"branches":[{"name":"演習","budget_share":1.0}]},"body_md":"初版","reason_md":"開始"}`); rec.Code != http.StatusCreated {
		t.Fatalf("計画 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := bearerPost(r, ticketPath+"/evaluation", evaluatorToken.Token,
		`{"target_kind":"plan","plan_version":1,"verdict":"pass","findings":[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},{"aspect":"values","verdict":"pass","note_md":""}]}`); rec.Code != http.StatusCreated {
		t.Fatalf("評価 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if rec := postWithCookie(r, ticketPath+"/delegation/approve", session, `{"plan_version":1}`); rec.Code != http.StatusOK {
		t.Fatalf("承認 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	createChild := func(t *testing.T, parent int, title string) int {
		t.Helper()
		rec := bearerPost(r, base+"/tickets", mainToken.Token, fmt.Sprintf(`{"type":"task","title":%q,"parent_seq":%d}`, title, parent))
		if rec.Code != http.StatusCreated {
			t.Fatalf("起票 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		return int(viewOf(t, rec)["seq"].(float64))
	}
	child := createChild(t, rootSeq, "演習")
	grand := createChild(t, child, "演習の1回目")

	var etagBefore string
	t.Run("根と子孫に印が付き、木の外は null", func(t *testing.T) {
		m, etag := list(t)
		etagBefore = etag
		if r := m[rootSeq]; r == nil || !r.IsRoot || r.Status != "approved" || r.RootSeq != rootSeq {
			t.Errorf("根の印 = %+v, want is_root・approved", r)
		}
		for _, s := range []int{child, grand} {
			if c := m[s]; c == nil || c.IsRoot || c.RootSeq != rootSeq {
				t.Errorf("#%d の印 = %+v, want 子孫・root_seq=%d", s, c, rootSeq)
			}
		}
		if m[outsideSeq] != nil {
			t.Errorf("木の外の印 = %+v, want null", m[outsideSeq])
		}
	})

	t.Run("確認待ちが立つと数が載り、ETag が変わる", func(t *testing.T) {
		if rec := bearerPost(r, ticketPath+"/confirmation", mainToken.Token,
			`{"reason":"hearing","question_md":"目的は？"}`); rec.Code != http.StatusCreated {
			t.Fatalf("確認待ち status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		m, etag := list(t)
		if m[rootSeq].OpenConfirmations != 1 || m[grand].OpenConfirmations != 1 {
			t.Errorf("open_confirmations = %d / %d, want 1 / 1", m[rootSeq].OpenConfirmations, m[grand].OpenConfirmations)
		}
		if etag == etagBefore {
			t.Errorf("ETag が変わらない（%s）", etag)
		}
	})

	t.Run("一時停止は状態に出て、取り下げると印が消える", func(t *testing.T) {
		if rec := postWithCookie(r, ticketPath+"/delegation/pause", session, `{}`); rec.Code != http.StatusOK {
			t.Fatalf("停止 status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if m, _ := list(t); m[child].Status != "paused" {
			t.Errorf("子の印の状態 = %s, want paused", m[child].Status)
		}
		if rec := postWithCookie(r, ticketPath+"/delegation/withdraw", session, `{"execution_mode":"human_only"}`); rec.Code != http.StatusOK {
			t.Fatalf("取り下げ status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		m, _ := list(t)
		for _, s := range []int{rootSeq, child, grand} {
			if m[s] != nil {
				t.Errorf("#%d の印 = %+v, want null（終了した委任）", s, m[s])
			}
		}
	})
}
