package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 認可の判定を**実際のDBに対して**通す（Design.md 6.4.1 / 6.4.4 / 6.4.5）。
//
// middleware パッケージの単体テストはフェイクの Querier で動くため、
// queries/authz.sql の SQL も DbDesign.md 7.3 のシードも一度も実行されない。
// 「オペレータが user.manage を持たない」「project_member 起点では非メンバーと
// 不在を区別できない」といった、**実際のデータに依存する事実**はここでしか
// 検証できない。
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/ -run Integration -v
func TestAuthorizationIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	// Cleanup は LIFO。プールを先に登録し、後片付けの DELETE より後に閉じる。
	t.Cleanup(pool.Close)

	q := gen.New(pool)

	operatorID := ulidgen.New()
	adminID := ulidgen.New()
	projectID := ulidgen.New()
	projectKey := "authz-" + strings.ToLower(projectID[len(projectID)-6:])

	t.Cleanup(func() {
		bg := context.Background()
		// audit_log.actor_id は ON DELETE SET NULL なので、actor を消しても
		// 行は残る。検証で書いた permission.denied を先に消す。
		if _, err := pool.Exec(bg,
			`DELETE FROM audit_log WHERE actor_id = ANY($1)`,
			[]string{operatorID, adminID}); err != nil {
			t.Errorf("audit_log の後始末に失敗した: %v", err)
		}
		// project を消せば project_member も CASCADE で落ちる。
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE id = $1`, projectID); err != nil {
			t.Errorf("project の後始末に失敗した: %v", err)
		}
		if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = ANY($1)`,
			[]string{operatorID, adminID}); err != nil {
			t.Errorf("actor の後始末に失敗した: %v", err)
		}
	})

	// アクター2人。app_user の system_role だけが違う。
	for _, u := range []struct{ id, name, role string }{
		{operatorID, "認可テスト（オペレータ）", auth.SystemRoleOperator},
		{adminID, "認可テスト（アドミニストレータ）", auth.SystemRoleAdministrator},
	} {
		if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
			ID: u.id, DisplayName: u.name,
		}); err != nil {
			t.Fatalf("actor を作れない: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO app_user (actor_id, email, system_role) VALUES ($1, $2, $3)`,
			u.id, "authz-"+u.id+"@example.com", u.role); err != nil {
			t.Fatalf("app_user を作れない: %v", err)
		}
	}

	// プロジェクトを1つ。**メンバーは登録しない**（非メンバーの検証のため）。
	if _, err := pool.Exec(ctx,
		`INSERT INTO project (id, key, name) VALUES ($1, $2, $3)`,
		projectID, projectKey, "認可テスト"); err != nil {
		t.Fatalf("project を作れない: %v", err)
	}

	// system_role はDBから読み直す。Principal に手で入れると、
	// app_user への書き込みと読み出しがずれても気づけない。
	principalFor := func(actorID string) *auth.Principal {
		row, err := q.GetActorProfile(ctx, actorID)
		if err != nil {
			t.Fatalf("GetActorProfile: %v", err)
		}
		return &auth.Principal{
			ActorID:     row.ActorID,
			ActorKind:   row.Kind,
			DisplayName: row.DisplayName,
			Email:       row.Email.String,
			SystemRole:  row.SystemRole.String,
			TokenID:     ulidgen.New(),
			TokenType:   auth.TokenTypeSession,
			Source:      auth.SourceCookie,
		}
	}

	// ルータは検証用に組み立てる。手順6の時点では権限を要求する実ルートが
	// 1つも無い（/admin/users は手順9）ため、必要権限の宣言だけをここで再現する。
	r := chi.NewRouter()
	r.With(middleware.RequirePermission(q, "user.manage")).
		Get("/admin/users", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	r.With(middleware.RequireProjectPermission(q, "project.view")).
		Get("/projects/{key}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	r.With(middleware.RequireProjectPermission(q, "project.edit")).
		Patch("/projects/{key}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})

	call := func(p *auth.Principal, method, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req = req.WithContext(auth.NewPrincipalContext(req.Context(), p))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	operator := principalFor(operatorID)
	admin := principalFor(adminID)

	if operator.SystemRole != auth.SystemRoleOperator || admin.SystemRole != auth.SystemRoleAdministrator {
		t.Fatalf("system_role が読み戻せていない: operator=%q admin=%q",
			operator.SystemRole, admin.SystemRole)
	}

	t.Run("オペレータは user.manage を持たない", func(t *testing.T) {
		w := call(operator, http.MethodGet, "/admin/users")
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("アドミニストレータは全権限を持つ", func(t *testing.T) {
		w := call(admin, http.MethodGet, "/admin/users")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("非メンバーのオペレータにはプロジェクトが見えない", func(t *testing.T) {
		// オペレータはシステムロールとして project.view を持つ（DbDesign.md 7.3）。
		// それでも 404 になることが「到達可否で切る」判定の要点。
		w := call(operator, http.MethodGet, "/projects/"+projectKey)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("非メンバーのアドミニストレータには見える", func(t *testing.T) {
		w := call(admin, http.MethodGet, "/projects/"+projectKey)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("存在しないプロジェクトも 404", func(t *testing.T) {
		w := call(admin, http.MethodGet, "/projects/no-such-project")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("閲覧者として参加すると見えるが編集はできない", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO project_member (project_id, actor_id, role_key) VALUES ($1, $2, 'project_viewer')`,
			projectID, operatorID); err != nil {
			t.Fatalf("project_member を作れない: %v", err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(),
				`DELETE FROM project_member WHERE project_id = $1 AND actor_id = $2`,
				projectID, operatorID); err != nil {
				t.Errorf("project_member の後始末に失敗した: %v", err)
			}
		})

		if w := call(operator, http.MethodGet, "/projects/"+projectKey); w.Code != http.StatusNoContent {
			t.Errorf("GET status = %d, want 204（body=%s）", w.Code, w.Body.String())
		}
		// project_viewer もオペレータも project.edit を持たない。
		// 到達はできるので 404 ではなく 403。
		if w := call(operator, http.MethodPatch, "/projects/"+projectKey); w.Code != http.StatusForbidden {
			t.Errorf("PATCH status = %d, want 403（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("拒否が permission.denied として残る", func(t *testing.T) {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM audit_log
			WHERE actor_id = $1 AND action = 'permission.denied' AND result = 'failure'`,
			operatorID).Scan(&n); err != nil {
			t.Fatalf("audit_log を読めない: %v", err)
		}
		// 上の4件：user.manage / 非メンバーの GET / 参加後の PATCH。
		// 件数そのものより「記録されている」ことを見る。
		if n == 0 {
			t.Fatal("permission.denied が1件も記録されていない（Design.md 6.4.5）")
		}

		var targetType, targetID *string
		if err := pool.QueryRow(ctx, `
			SELECT target_type, target_id FROM audit_log
			WHERE actor_id = $1 AND action = 'permission.denied'
			  AND detail->>'required_permission' = 'project.edit'
			ORDER BY occurred_at DESC LIMIT 1`,
			operatorID).Scan(&targetType, &targetID); err != nil {
			t.Fatalf("project.edit の拒否記録を読めない: %v", err)
		}
		if targetType == nil || *targetType != "project" {
			t.Errorf("target_type = %v, want project", targetType)
		}
		if targetID == nil || *targetID != projectID {
			t.Errorf("target_id = %v, want %s", targetID, projectID)
		}
	})
}
