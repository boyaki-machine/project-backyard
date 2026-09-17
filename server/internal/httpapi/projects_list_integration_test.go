package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/project"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// GET /api/v1/projects のトークンによる絞り込み（ApiDesign.md 5.1。pb-38）。
//
// **mcp_integration_test.go と同じ作り**（所有者＋エージェント＋2プロジェクト）。
// 所有者を**両方の**プロジェクトのメンバーにする——片方だけだと、絞り込みが
// 効いていなくても1件になり、トークンの project_id を見ているのかメンバーシップを
// 見ているのかを区別できない。
func TestProjectsListTokenScope(t *testing.T) {
	dsn := os.Getenv("PB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	rec := audit.FromCLI("projects list integration test")

	ownerID := ulidgen.New()
	agentID := ulidgen.New()
	projectID := ulidgen.New()
	otherProjectID := ulidgen.New()
	suffix := strings.ToLower(projectID[len(projectID)-6:])
	projectKey := "plist-" + suffix
	otherKey := "plist-other-" + suffix

	t.Cleanup(func() {
		bg := context.Background()
		if _, err := pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = ANY($1)`,
			[]string{ownerID, agentID}); err != nil {
			t.Errorf("audit_log の後始末に失敗した: %v", err)
		}
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE id = ANY($1)`,
			[]string{projectID, otherProjectID}); err != nil {
			t.Errorf("project の後始末に失敗した: %v", err)
		}
		// **エージェントを先に消す**——owner_actor_id が app_user を参照している。
		if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, agentID); err != nil {
			t.Errorf("エージェントの後始末に失敗した: %v", err)
		}
		if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, ownerID); err != nil {
			t.Errorf("所有者の後始末に失敗した: %v", err)
		}
	})

	// ── 所有者（オペレータ）と、その人が両方に参加する2プロジェクト ──────
	if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
		ID: ownerID, DisplayName: "一覧テスト（所有者）",
	}); err != nil {
		t.Fatalf("所有者の actor を作れない: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_user (actor_id, email, system_role) VALUES ($1, $2, $3)`,
		ownerID, "plist-"+ownerID+"@example.com", auth.SystemRoleOperator); err != nil {
		t.Fatalf("所有者の app_user を作れない: %v", err)
	}
	for _, p := range []struct{ id, key, name string }{
		{projectID, projectKey, "一覧テスト"},
		{otherProjectID, otherKey, "一覧テスト（別プロジェクト）"},
	} {
		if err := project.Create(ctx, q, rec, project.CreateParams{
			ID: p.id, Key: p.key, Name: p.name,
			WorkflowTemplate: "simple",
			CreatedBy:        pgtype.Text{String: ownerID, Valid: true},
		}); err != nil {
			t.Fatalf("プロジェクト %s を作れない: %v", p.key, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO project_member (project_id, actor_id, role_key) VALUES ($1, $2, 'project_member')`,
			p.id, ownerID); err != nil {
			t.Fatalf("project_member を作れない: %v", err)
		}
	}

	// ── エージェントと、トークン ───────────────────────────────
	if err := q.CreateAgentActor(ctx, gen.CreateAgentActorParams{
		ID: agentID, DisplayName: "一覧テスト（エージェント）",
	}); err != nil {
		t.Fatalf("エージェントの actor を作れない: %v", err)
	}
	if err := q.CreateAgent(ctx, gen.CreateAgentParams{
		ActorID:      agentID,
		OwnerActorID: ownerID,
		ProjectID:    pgtype.Text{String: projectID, Valid: true},
		ClientKind:   "claude_code",
	}); err != nil {
		t.Fatalf("agent を作れない: %v", err)
	}

	// **空のスコープは「絞り込みなし」である**（Design.md 6.4.1）。
	issue := func(actorID, tokenType, plaintext, project string) {
		t.Helper()
		if err := q.CreateAccessToken(ctx, gen.CreateAccessTokenParams{
			ID:        ulidgen.New(),
			ActorID:   actorID,
			TokenType: tokenType,
			TokenHash: auth.HashToken(plaintext),
			Name:      pgtype.Text{String: plaintext, Valid: true},
			ProjectID: pgtype.Text{String: project, Valid: project != ""},
			Scopes:    json.RawMessage(`[]`),
		}); err != nil {
			t.Fatalf("トークンを発行できない: %v", err)
		}
	}
	agentBound := auth.AgentTokenPrefix + "plist-bound-" + suffix
	issue(agentID, auth.TokenTypeAgent, agentBound, projectID)
	agentAll := auth.AgentTokenPrefix + "plist-all-" + suffix
	issue(agentID, auth.TokenTypeAgent, agentAll, "")
	humanBound := auth.APITokenPrefix + "plist-bound-" + suffix
	issue(ownerID, auth.TokenTypeAPI, humanBound, projectID)
	humanAll := auth.APITokenPrefix + "plist-all-" + suffix
	issue(ownerID, auth.TokenTypeAPI, humanAll, "")

	r := NewRouter(Deps{Pool: pool, Version: "plist-test"})

	// 自分が作ったキー（plist-…）だけを数える。開発 DB の他のプロジェクトに左右されない
	list := func(t *testing.T, token string) (keys []string, roles []string, total int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/projects?status=all&per_page=100", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /projects = %d, body=%s", w.Code, w.Body.String())
		}
		var body struct {
			Items []struct {
				Key    string `json:"key"`
				MyRole string `json:"my_role"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		for _, it := range body.Items {
			if strings.HasSuffix(it.Key, suffix) && strings.HasPrefix(it.Key, "plist-") {
				keys = append(keys, it.Key)
				roles = append(roles, it.MyRole)
			}
		}
		return keys, roles, body.Total
	}

	t.Run("エージェントのトークンがプロジェクトに紐づくと、その1件だけが返る", func(t *testing.T) {
		keys, roles, total := list(t, agentBound)
		if len(keys) != 1 || keys[0] != projectKey {
			t.Fatalf("keys = %v, want [%s]", keys, projectKey)
		}
		// **total も1件に絞られている**——一覧だけ絞って件数を絞らないと、ページャが食い違う
		if total != 1 {
			t.Errorf("total = %d, want 1", total)
		}
		// **my_role は所有者のロール**（委譲。5.4 と同じ）
		if roles[0] != "project_member" {
			t.Errorf("my_role = %q, want project_member", roles[0])
		}
	})

	t.Run("プロジェクトに紐づかないエージェントのトークンは、所有者のメンバーシップで両方が返る", func(t *testing.T) {
		keys, _, _ := list(t, agentAll)
		if len(keys) != 2 {
			t.Fatalf("keys = %v, want 2件", keys)
		}
	})

	t.Run("人のトークンもプロジェクトに紐づくと、その1件だけが返る", func(t *testing.T) {
		keys, _, total := list(t, humanBound)
		if len(keys) != 1 || keys[0] != projectKey || total != 1 {
			t.Fatalf("keys = %v total = %d, want [%s] と 1", keys, total, projectKey)
		}
	})

	t.Run("プロジェクトに紐づかない人のトークンは、これまでどおり両方が返る", func(t *testing.T) {
		keys, _, _ := list(t, humanAll)
		if len(keys) != 2 {
			t.Fatalf("keys = %v, want 2件", keys)
		}
	})
}
