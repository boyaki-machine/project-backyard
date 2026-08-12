package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 認証経路を**実際のDBに対して**通す。
//
// 他のテストは gen.Querier を差し替えたフェイクで動くため、queries/auth.sql の
// SQL そのものは一度も実行されない。列名の綴りや JOIN の向き、last_used_at の
// 間引き条件（Design.md 6.2.2）はここでしか検証できない。
//
// PB_TEST_DATABASE_URL が無ければスキップする。CI や DB を立てていない環境で
// make test を落とさないため。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/ -run Integration -v
func TestAuthenticateIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	// Cleanup は LIFO で走る。プールを先に登録し、後片付けの DELETE より
	// あとに閉じられるようにする（defer にするとプールが先に閉じてしまう）。
	t.Cleanup(pool.Close)

	q := gen.New(pool)

	// 検証用のアクターとトークンを作り、終わったら消す。
	actorID := ulidgen.New()
	tokenID := ulidgen.New()
	plaintext, err := auth.NewToken(auth.SessionTokenPrefix)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
		ID: actorID, DisplayName: "結合テスト",
	}); err != nil {
		t.Fatalf("actor を作れない: %v", err)
	}
	t.Cleanup(func() {
		// actor を消せば access_token も app_user も CASCADE で落ちる。
		if _, err := pool.Exec(context.Background(), `DELETE FROM actor WHERE id = $1`, actorID); err != nil {
			t.Errorf("後始末に失敗した: %v", err)
		}
	})

	if err := q.CreateAdministrator(ctx, gen.CreateAdministratorParams{
		ActorID: actorID, Email: "integration-" + actorID + "@example.com",
	}); err != nil {
		t.Fatalf("app_user を作れない: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO access_token (id, actor_id, token_type, token_hash, token_prefix, expires_at, scopes)
		VALUES ($1, $2, 'session', $3, $4, now() + interval '14 days', '["ticket:read"]')`,
		tokenID, actorID, auth.HashToken(plaintext), auth.TokenPrefix(plaintext))
	if err != nil {
		t.Fatalf("access_token を作れない: %v", err)
	}

	// ルータ全体ではなく Authenticate だけを通す。ここで確かめたいのは
	// queries/auth.sql の SQL とミドルウェアの判定であり、ルート定義ではない
	// （/api/v1 の疎通は v1 パッケージの結合テストが見る）。
	var got *auth.Principal
	r := chi.NewRouter()
	r.Use(middleware.Authenticate(q))
	r.Get("/probe", func(w http.ResponseWriter, req *http.Request) {
		got = auth.PrincipalFromContext(req.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	call := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// ① 有効なトークンで通り、actor と app_user の値が載る。
	if rec := call(plaintext); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if got == nil {
		t.Fatal("プリンシパルがコンテキストに載っていない")
	}
	if got.ActorID != actorID || got.DisplayName != "結合テスト" {
		t.Errorf("actor = %+v", got)
	}
	if got.SystemRole != auth.SystemRoleAdministrator {
		t.Errorf("system_role = %q（LEFT JOIN app_user が効いていない）", got.SystemRole)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "ticket:read" {
		t.Errorf("scopes = %v（jsonb の読み出し）", got.Scopes)
	}

	// ② last_used_at が記録される。
	first := lastUsedAt(t, pool, tokenID)
	if first == nil {
		t.Fatal("last_used_at が更新されていない")
	}

	// ③ 直後の再リクエストでは更新されない（1分粒度の間引き。Design.md 6.2.2）。
	if rec := call(plaintext); rec.Code != http.StatusNoContent {
		t.Fatalf("2回目の status = %d", rec.Code)
	}
	if second := lastUsedAt(t, pool, tokenID); second == nil || !second.Equal(*first) {
		t.Errorf("1分以内に last_used_at が再更新された: %v → %v", first, second)
	}

	// ④ 1分より前に巻き戻すと、次のリクエストで更新される。
	if _, err := pool.Exec(ctx,
		`UPDATE access_token SET last_used_at = now() - interval '2 minutes' WHERE id = $1`, tokenID); err != nil {
		t.Fatalf("last_used_at を巻き戻せない: %v", err)
	}
	if rec := call(plaintext); rec.Code != http.StatusNoContent {
		t.Fatalf("3回目の status = %d", rec.Code)
	}
	third := lastUsedAt(t, pool, tokenID)
	if third == nil || !third.After(time.Now().Add(-time.Minute)) {
		t.Errorf("1分経過後に last_used_at が更新されなかった: %v", third)
	}

	// ⑤ 失効させると 401 になる。
	if _, err := pool.Exec(ctx,
		`UPDATE access_token SET revoked_at = now() WHERE id = $1`, tokenID); err != nil {
		t.Fatalf("失効させられない: %v", err)
	}
	if rec := call(plaintext); rec.Code != http.StatusUnauthorized {
		t.Errorf("失効後の status = %d, want 401", rec.Code)
	}

	// ⑥ 存在しないトークンも 401（pgx.ErrNoRows の扱い）。
	if rec := call("pb_sess_no-such-token"); rec.Code != http.StatusUnauthorized {
		t.Errorf("未知トークンの status = %d, want 401", rec.Code)
	}
}

func lastUsedAt(t *testing.T, pool *pgxpool.Pool, tokenID string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT last_used_at FROM access_token WHERE id = $1`, tokenID).Scan(&at); err != nil {
		t.Fatalf("last_used_at を読めない: %v", err)
	}
	return at
}
