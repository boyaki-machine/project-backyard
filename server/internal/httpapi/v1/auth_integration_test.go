package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// ログイン → GET /me → ログアウトを**実際のDBに対して**通す。
//
// 他のテストは gen.Querier をフェイクに差し替えるため、queries/auth.sql と
// queries/authz.sql の SQL は一度も実行されない。列名の綴り、JOIN の向き
// （とくに user_identity.subject = app_user.email）、role_permission の
// 引き当ては、ここでしか検証できない。
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/v1/ -run Integration -v
func TestLoginIntegration(t *testing.T) {
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
	actorID := ulidgen.New()

	// **メールは大小混在で作る。** 小文字で入力しても citext が引き当て、
	// その値で user_identity.subject を引けることを確かめるため
	// （PROGRESS.md「メールアドレスの大小の扱い」）。
	email := "Integration-" + actorID + "@Example.com"

	seedLocalUser(t, ctx, pool, q, actorID, email, testPassword)

	r := routerWithDeps(Deps{Queries: q})

	// ① 小文字で入力してもログインできる。
	rec := call(r, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+strings.ToLower(email)+`","password":"`+testPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ログインの status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	session := cookieOf(rec, auth.SessionCookieName)
	if session == nil {
		t.Fatal("pb_session が返らない")
	}
	if cookieOf(rec, auth.CSRFCookieName) == nil {
		t.Fatal("pb_csrf が返らない")
	}

	// 応答には保存された表記（大小混在）が載る。
	loginView := viewOf(t, rec)
	if got := loginView["actor"].(map[string]any)["email"]; got != email {
		t.Errorf("actor.email = %v, want %q（保存された表記）", got, email)
	}
	// administrator は全権限（DbDesign.md 7.3）。28件の権限カタログが引けている。
	if perms := loginView["permissions"].([]any); len(perms) != 28 {
		t.Errorf("permissions = %d件, want 28（role_permission を引けていない）", len(perms))
	}

	// ② DB には平文が載らない。
	var stored struct {
		hash, prefix string
		expiresAt    *time.Time
		clientInfo   *string
	}
	if err := pool.QueryRow(ctx,
		`SELECT token_hash, token_prefix, expires_at, client_info
		   FROM access_token WHERE actor_id = $1 AND token_type = 'session'`, actorID).
		Scan(&stored.hash, &stored.prefix, &stored.expiresAt, &stored.clientInfo); err != nil {
		t.Fatalf("access_token を読めない: %v", err)
	}
	if stored.hash != auth.HashToken(session.Value) {
		t.Error("token_hash が平文の SHA-256 になっていない")
	}
	if strings.HasPrefix(stored.hash, "pb_") {
		t.Fatal("平文が保存されている")
	}
	if stored.expiresAt == nil {
		t.Error("expires_at が NULL（セッションには必ず期限を設定する）")
	}

	// ③ last_login_at と failed_attempts。
	if at := lastLoginAt(t, pool, actorID); at == nil {
		t.Error("last_login_at が更新されていない")
	}

	// ④ Cookie で GET /me が通る。
	meRec := callWithCookie(r, http.MethodGet, "/api/v1/me", session.Value)
	if meRec.Code != http.StatusOK {
		t.Fatalf("GET /me の status = %d, want 200（body=%s）", meRec.Code, meRec.Body.String())
	}
	meView := viewOf(t, meRec)
	meActor := meView["actor"].(map[string]any)
	if meActor["id"] != actorID || meActor["locale"] != "ja" || meActor["timezone"] != "Asia/Tokyo" {
		t.Errorf("GET /me の actor = %v", meActor)
	}
	if len(meView["permissions"].([]any)) != 28 {
		t.Errorf("GET /me の permissions = %v", meView["permissions"])
	}

	// ⑤ ログアウトで失効し、同じ Cookie は 401 になる。
	outRec := callWithCookie(r, http.MethodPost, "/api/v1/auth/logout", session.Value)
	if outRec.Code != http.StatusNoContent {
		t.Fatalf("ログアウトの status = %d, want 204（body=%s）", outRec.Code, outRec.Body.String())
	}
	if again := callWithCookie(r, http.MethodGet, "/api/v1/me", session.Value); again.Code != http.StatusUnauthorized {
		t.Errorf("ログアウト後の GET /me = %d, want 401", again.Code)
	}

	// ⑥ 監査ログが3件（login.success / logout）残る。
	if got := auditActionsOf(t, pool, actorID); len(got) != 2 ||
		got[0] != "login.success" || got[1] != "logout" {
		t.Errorf("監査記録 = %v, want [login.success logout]", got)
	}

	// ⑦ パスワード違いを5回で 423 になり、locked_until が入る。
	for i := 1; i <= maxFailedAttempts; i++ {
		bad := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+strings.ToLower(email)+`","password":"wrong-password-x"}`)
		want := http.StatusUnauthorized
		if i == maxFailedAttempts {
			want = http.StatusLocked
		}
		if bad.Code != want {
			t.Fatalf("%d回目の失敗 status = %d, want %d（body=%s）", i, bad.Code, want, bad.Body.String())
		}
	}
	attempts, lockedUntil := credentialState(t, pool, actorID)
	if attempts != maxFailedAttempts {
		t.Errorf("failed_attempts = %d, want %d", attempts, maxFailedAttempts)
	}
	if lockedUntil == nil || !lockedUntil.After(time.Now()) {
		t.Errorf("locked_until = %v, want 未来の時刻", lockedUntil)
	}

	// ⑧ ロック中は正しいパスワードでも 423。
	locked := call(r, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+strings.ToLower(email)+`","password":"`+testPassword+`"}`)
	if locked.Code != http.StatusLocked {
		t.Fatalf("ロック中の status = %d, want 423", locked.Code)
	}
	if got := errorOf(t, locked); got.RetryAfterSec <= 0 {
		t.Errorf("retry_after_sec = %d", got.RetryAfterSec)
	}
}

// seedLocalUser は actor / app_user / user_identity / local_credential を作り、
// 後始末を登録する（pb admin create と同じ4テーブル。DbDesign.md 6.2）。
func seedLocalUser(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, q *gen.Queries,
	actorID, email, password string,
) {
	t.Helper()

	if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
		ID: actorID, DisplayName: "ログイン結合テスト",
	}); err != nil {
		t.Fatalf("actor を作れない: %v", err)
	}
	t.Cleanup(func() {
		// audit_log.actor_id は ON DELETE SET NULL なので、actor より先に消す。
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE actor_id = $1`, actorID); err != nil {
			t.Errorf("監査ログの後始末に失敗した: %v", err)
		}
		// actor を消せば app_user / user_identity / local_credential /
		// access_token は CASCADE で落ちる。
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM actor WHERE id = $1`, actorID); err != nil {
			t.Errorf("後始末に失敗した: %v", err)
		}
	})

	if err := q.CreateAdministrator(ctx, gen.CreateAdministratorParams{
		ActorID: actorID, Email: email,
	}); err != nil {
		t.Fatalf("app_user を作れない: %v", err)
	}

	identityID := ulidgen.New()
	if err := q.CreateUserIdentity(ctx, gen.CreateUserIdentityParams{
		ID: identityID, UserID: actorID, ProviderKey: "local", Subject: email,
	}); err != nil {
		t.Fatalf("user_identity を作れない: %v", err)
	}

	phc, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := q.CreateLocalCredential(ctx, gen.CreateLocalCredentialParams{
		IdentityID: identityID, PasswordHash: phc,
	}); err != nil {
		t.Fatalf("local_credential を作れない: %v", err)
	}
}

// callWithCookie は Cookie 認証で1リクエスト投げる。
// 状態変更系は CSRF ミドルウェア（ApiDesign.md 2.4）を通るため、
// ブラウザと同じく pb_csrf Cookie と X-PB-CSRF ヘッダを揃えて送る。
func callWithCookie(r http.Handler, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func lastLoginAt(t *testing.T, pool *pgxpool.Pool, actorID string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT last_login_at FROM app_user WHERE actor_id = $1`, actorID).Scan(&at); err != nil {
		t.Fatalf("last_login_at を読めない: %v", err)
	}
	return at
}

func credentialState(t *testing.T, pool *pgxpool.Pool, actorID string) (int, *time.Time) {
	t.Helper()
	var attempts int
	var lockedUntil *time.Time
	err := pool.QueryRow(context.Background(), `
		SELECT c.failed_attempts, c.locked_until
		  FROM local_credential c
		  JOIN user_identity i ON i.id = c.identity_id
		 WHERE i.user_id = $1`, actorID).Scan(&attempts, &lockedUntil)
	if err != nil {
		t.Fatalf("local_credential を読めない: %v", err)
	}
	return attempts, lockedUntil
}

func auditActionsOf(t *testing.T, pool *pgxpool.Pool, actorID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE actor_id = $1 ORDER BY occurred_at, id`, actorID)
	if err != nil {
		t.Fatalf("監査ログを読めない: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatalf("監査ログの読み出しに失敗した: %v", err)
		}
		out = append(out, action)
	}
	return out
}
