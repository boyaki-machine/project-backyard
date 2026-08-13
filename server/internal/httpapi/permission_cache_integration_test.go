package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 実効権限のセッションキャッシュを**実際のDBに対して**通す
// （Design.md 6.4.5、マイグレーション 0012）。
//
// フェイクの Querier では、0012 で足した列も SaveTokenPermissionCache /
// InvalidateActorPermissionCache の SQL も一度も実行されない。とくに
// 「無効化がアクター単位で全トークンに効く」ことは実データでしか確かめられない。
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/ -run Integration -v
func TestPermissionCacheIntegration(t *testing.T) {
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

	actorID := ulidgen.New()
	sessionTokenID := ulidgen.New()
	apiTokenID := ulidgen.New()

	t.Cleanup(func() {
		bg := context.Background()
		if _, err := pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = $1`, actorID); err != nil {
			t.Errorf("audit_log の後始末に失敗した: %v", err)
		}
		// access_token は actor の ON DELETE CASCADE で落ちる。
		if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, actorID); err != nil {
			t.Errorf("actor の後始末に失敗した: %v", err)
		}
	})

	if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
		ID: actorID, DisplayName: "権限キャッシュのテスト",
	}); err != nil {
		t.Fatalf("actor を作れない: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_user (actor_id, email, system_role) VALUES ($1, $2, $3)`,
		actorID, "permcache-"+actorID+"@example.com", auth.SystemRoleOperator); err != nil {
		t.Fatalf("app_user を作れない: %v", err)
	}

	// 同じアクターに2本。無効化がトークン単位ではなくアクター単位で効くことを
	// 見るためである（PC とスマートフォン、あるいはAPIトークンに相当）。
	newToken := func(tokenID, tokenType, prefix string) string {
		plaintext, err := auth.NewToken(prefix)
		if err != nil {
			t.Fatalf("トークンを作れない: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO access_token (id, actor_id, token_type, token_hash, token_prefix, expires_at)
			 VALUES ($1, $2, $3, $4, $5, now() + interval '14 days')`,
			tokenID, actorID, tokenType, auth.HashToken(plaintext), auth.TokenPrefix(plaintext)); err != nil {
			t.Fatalf("access_token を作れない: %v", err)
		}
		return plaintext
	}
	sessionToken := newToken(sessionTokenID, auth.TokenTypeSession, auth.SessionTokenPrefix)
	newToken(apiTokenID, auth.TokenTypeAPI, auth.APITokenPrefix)

	// 認証ミドルウェアごと通す。キャッシュは access_token の行に載っており、
	// それを読むのは Authenticate だからである。
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(q))
		r.With(middleware.RequirePermission(q, "user.manage")).
			Get("/admin/users", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
	})

	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
		req.Header.Set("Authorization", "Bearer "+sessionToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// cacheOf は access_token に入っているキャッシュを読む。
	cacheOf := func(t *testing.T, tokenID string) ([]string, bool) {
		t.Helper()
		var raw []byte
		var cachedAt *time.Time
		if err := pool.QueryRow(ctx,
			`SELECT cached_permissions, permissions_cached_at FROM access_token WHERE id = $1`,
			tokenID).Scan(&raw, &cachedAt); err != nil {
			t.Fatalf("キャッシュを読めない: %v", err)
		}
		if cachedAt == nil {
			return nil, false
		}
		permissions, err := auth.DecodeCachedPermissions(raw)
		if err != nil {
			t.Fatalf("キャッシュを解釈できない: %v", err)
		}
		return permissions, true
	}

	setRole := func(t *testing.T, role string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`UPDATE app_user SET system_role = $2 WHERE actor_id = $1`, actorID, role); err != nil {
			t.Fatalf("system_role を変えられない: %v", err)
		}
	}

	t.Run("初回のリクエストでキャッシュが書かれる", func(t *testing.T) {
		if _, ok := cacheOf(t, sessionTokenID); ok {
			t.Fatal("リクエスト前からキャッシュがある")
		}

		// オペレータは user.manage を持たない（DbDesign.md 7.3）。
		if w := call(); w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", w.Code, w.Body.String())
		}

		got, ok := cacheOf(t, sessionTokenID)
		if !ok {
			t.Fatal("キャッシュが書かれていない")
		}
		if auth.HasPermission(got, "user.manage") {
			t.Errorf("キャッシュ = %v。オペレータが user.manage を持っている", got)
		}
		if !auth.HasPermission(got, "project.view") {
			t.Errorf("キャッシュ = %v。オペレータの権限（DbDesign.md 7.3 の12件）が入っていない", got)
		}

		// 叩いていないトークンのキャッシュは空のまま。書き込みはトークン単位。
		if _, ok := cacheOf(t, apiTokenID); ok {
			t.Error("叩いていないトークンにまでキャッシュが書かれている")
		}
	})

	t.Run("ロールを変えただけではキャッシュが古いまま残る", func(t *testing.T) {
		setRole(t, auth.SystemRoleAdministrator)

		// **これが 6.4.5 が無効化を要求する理由そのものである。**
		// アドミニストレータに昇格しても、キャッシュがある限り 403 のままになる。
		if w := call(); w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403。キャッシュが効いていない（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("無効化すると次のリクエストで権限が変わる", func(t *testing.T) {
		// api トークン側にもキャッシュを作っておく。アクター単位で消えることの確認用。
		// 直前のサブテストで administrator へ変えてあるので、書き込みの条件
		// （@system_role の照合）に合わせる。
		middleware.SaveSystemPermissionCache(ctx, q, apiTokenID,
			auth.SystemRoleAdministrator, []string{"project.view"})
		if _, ok := cacheOf(t, apiTokenID); !ok {
			t.Fatal("検証の前提が崩れている（api トークンにキャッシュが無い）")
		}

		if err := q.InvalidateActorPermissionCache(ctx, actorID); err != nil {
			t.Fatalf("InvalidateActorPermissionCache: %v", err)
		}

		// **アクターの全トークンから消えること。** 1本だけ消しても、
		// 残った経路から古い権限で通れてしまう。
		for _, id := range []string{sessionTokenID, apiTokenID} {
			if _, ok := cacheOf(t, id); ok {
				t.Errorf("トークン %s のキャッシュが残っている", id)
			}
		}

		if w := call(); w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204。無効化後も古い権限のままになっている（body=%s）",
				w.Code, w.Body.String())
		}

		got, ok := cacheOf(t, sessionTokenID)
		if !ok {
			t.Fatal("キャッシュが書き直されていない")
		}
		if !auth.HasPermission(got, "user.manage") {
			t.Errorf("キャッシュ = %v。新しいロールで計算し直されていない", got)
		}
	})

	t.Run("TTL を過ぎたキャッシュは使われない", func(t *testing.T) {
		setRole(t, auth.SystemRoleOperator)

		// 無効化を経ずに、キャッシュだけを古くする。role_permission のシードを
		// マイグレーションで変えた場合（当該ユーザーの無効化では届かない変更）に
		// 相当する状況である。
		if _, err := pool.Exec(ctx,
			`UPDATE access_token SET permissions_cached_at = now() - $2::interval WHERE id = $1`,
			sessionTokenID, (auth.PermissionCacheTTL + time.Minute).String()); err != nil {
			t.Fatalf("permissions_cached_at を戻せない: %v", err)
		}

		if w := call(); w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403。期限切れのキャッシュを使っている（body=%s）",
				w.Code, w.Body.String())
		}

		got, _ := cacheOf(t, sessionTokenID)
		if auth.HasPermission(got, "user.manage") {
			t.Errorf("キャッシュ = %v。計算し直した結果で上書きされていない", got)
		}
	})

	// **書き戻しが無効化を追い越さないこと。**
	//
	//   1. リクエストR が旧ロールの権限を計算する
	//   2. 管理者がロールを変更し、キャッシュを無効化する
	//   3. リクエストR が書き戻そうとする ← ここで落ちなければ旧権限が TTL ぶん復活する
	//
	// 手順10 で無効化を呼ぶようになると実際に起こりうる順序である。
	t.Run("ロールが変わった後の書き戻しは落ちる", func(t *testing.T) {
		setRole(t, auth.SystemRoleOperator)
		if err := q.InvalidateActorPermissionCache(ctx, actorID); err != nil {
			t.Fatalf("InvalidateActorPermissionCache: %v", err)
		}

		// 1. オペレータとして計算した、という状況を作る。
		stale := []string{"project.view", "ticket.view"}

		// 2. 昇格し、無効化する。
		setRole(t, auth.SystemRoleAdministrator)
		if err := q.InvalidateActorPermissionCache(ctx, actorID); err != nil {
			t.Fatalf("InvalidateActorPermissionCache: %v", err)
		}

		// 3. 遅れて届いた書き戻し。
		middleware.SaveSystemPermissionCache(ctx, q, sessionTokenID, auth.SystemRoleOperator, stale)

		if got, ok := cacheOf(t, sessionTokenID); ok {
			t.Errorf("キャッシュ = %v。無効化を追い越して書き込まれている", got)
		}

		// 現在のロールで書けば通る（条件が厳しすぎて常に落ちるわけではない）。
		middleware.SaveSystemPermissionCache(ctx, q, sessionTokenID,
			auth.SystemRoleAdministrator, []string{"user.manage"})
		if _, ok := cacheOf(t, sessionTokenID); !ok {
			t.Error("現在のロールでの書き戻しまで落ちている")
		}

		// 後片付け。次のサブテストの件数を狂わせない。
		if err := q.InvalidateActorPermissionCache(ctx, actorID); err != nil {
			t.Fatalf("InvalidateActorPermissionCache: %v", err)
		}
		setRole(t, auth.SystemRoleOperator)
	})

	t.Run("拒否は permission.denied に残る", func(t *testing.T) {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE actor_id = $1 AND action = 'permission.denied'`,
			actorID).Scan(&n); err != nil {
			t.Fatalf("audit_log を読めない: %v", err)
		}
		// 403 を返した回数（初回・ロール変更後・TTL 超過後）。
		if n != 3 {
			t.Errorf("permission.denied = %d件, want 3", n)
		}
	})
}
