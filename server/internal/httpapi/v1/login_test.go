package v1

import (
	"errors"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
)

// 正しい資格情報で 200 と、3.1 の応答本体・Cookie 2種が返る。
func TestLoginSuccess(t *testing.T) {
	q := newFake(t)
	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	view := viewOf(t, rec)
	actor, ok := view["actor"].(map[string]any)
	if !ok {
		t.Fatalf("actor が無い: %v", view)
	}
	if actor["id"] != testActorID || actor["email"] != testEmail {
		t.Errorf("actor = %v", actor)
	}
	if actor["kind"] != auth.ActorKindUser {
		t.Errorf("kind = %v, want user", actor["kind"])
	}
	if actor["system_role"] != auth.SystemRoleAdministrator {
		t.Errorf("system_role = %v", actor["system_role"])
	}
	if actor["must_change_password"] != false {
		t.Errorf("must_change_password = %v", actor["must_change_password"])
	}
	if view["expires_at"] == nil {
		t.Error("expires_at が null（セッションには必ず期限を設定する）")
	}

	// permissions はシステムロール由来の実効権限（ApiDesign.md 4.1）。
	perms, _ := view["permissions"].([]any)
	if len(perms) != 3 {
		t.Errorf("permissions = %v, want 3件", view["permissions"])
	}

	// projects は所属が無くても null ではなく空配列で返る。
	if _, ok := view["projects"].([]any); !ok {
		t.Errorf("projects = %v, want []", view["projects"])
	}
}

// 発行したトークンは DB にハッシュだけが載り、平文は Cookie にしか現れない
// （Design.md 6.2.1「平文トークンは Cookie にのみ存在し、DB にもログにも残さない」）。
func TestLoginStoresOnlyHash(t *testing.T) {
	q := newFake(t)
	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)

	if len(q.created) != 1 {
		t.Fatalf("発行された access_token = %d件, want 1", len(q.created))
	}
	created := q.created[0]

	session := cookieOf(rec, auth.SessionCookieName)
	if session == nil {
		t.Fatal("pb_session が発行されていない")
	}
	if !strings.HasPrefix(session.Value, auth.SessionTokenPrefix) {
		t.Errorf("pb_session = %q, want %q 始まり", session.Value, auth.SessionTokenPrefix)
	}
	if created.TokenHash != auth.HashToken(session.Value) {
		t.Error("token_hash が平文の SHA-256 になっていない")
	}
	if created.TokenHash == session.Value {
		t.Fatal("平文がそのまま保存されている")
	}
	if created.TokenType != auth.TokenTypeSession {
		t.Errorf("token_type = %q", created.TokenType)
	}
	if created.TokenPrefix.String != auth.TokenPrefix(session.Value) {
		t.Errorf("token_prefix = %q", created.TokenPrefix.String)
	}
	if string(created.Scopes) != "[]" {
		t.Errorf("scopes = %s, want []（セッションは絞り込みなし）", created.Scopes)
	}
	if created.ExpiresAt == nil {
		t.Error("expires_at が NULL。セッションには必ず期限を設定する")
	}
}

// Cookie の属性が ApiDesign.md 3.1 のとおりであること。
func TestLoginCookieAttributes(t *testing.T) {
	q := newFake(t)
	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)

	wantMaxAge := int(SessionMaxAge / time.Second)
	if wantMaxAge != 1209600 {
		t.Fatalf("SessionMaxAge = %d秒, want 1209600（ApiDesign.md 3.1）", wantMaxAge)
	}

	session := cookieOf(rec, auth.SessionCookieName)
	if session == nil {
		t.Fatal("pb_session が無い")
	}
	if !session.HttpOnly {
		t.Error("pb_session に HttpOnly が無い")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("pb_session の SameSite = %v, want Lax", session.SameSite)
	}
	if session.Path != "/" || session.MaxAge != wantMaxAge {
		t.Errorf("pb_session の Path=%q MaxAge=%d", session.Path, session.MaxAge)
	}
	if session.Secure {
		t.Error("PB_COOKIE_SECURE=false なのに Secure が付いた")
	}

	csrf := cookieOf(rec, auth.CSRFCookieName)
	if csrf == nil {
		t.Fatal("pb_csrf が無い（ApiDesign.md 2.4）")
	}
	if csrf.HttpOnly {
		t.Error("pb_csrf に HttpOnly が付いている（JS から読めなくなる）")
	}
	if csrf.MaxAge != wantMaxAge {
		t.Errorf("pb_csrf の MaxAge = %d, want %d（pb_session と揃える）", csrf.MaxAge, wantMaxAge)
	}
	if csrf.Value == session.Value {
		t.Error("pb_csrf と pb_session が同じ値")
	}
}

// PB_COOKIE_SECURE=true で Secure が付く（Design.md 6.2.1 手順7）。
func TestLoginCookieSecure(t *testing.T) {
	q := newFake(t)
	r := routerWithDeps(Deps{Queries: q,
		Settings: config.LiveWith(config.Row{Key: config.KeyCookieSecure, Value: "true"})})
	rec := call(r, http.MethodPost, "/api/v1/auth/login",
		`{"email":"tanaka@example.com","password":"`+testPassword+`"}`)

	for _, name := range []string{auth.SessionCookieName, auth.CSRFCookieName} {
		if c := cookieOf(rec, name); c == nil || !c.Secure {
			t.Errorf("%s に Secure が付かない", name)
		}
	}
}

// 未登録のメールでも 401 invalid_credentials（存在を漏らさない）。
func TestLoginUnknownEmail(t *testing.T) {
	q := newFake(t)
	q.loginErr = notFoundErr

	rec := postLogin(q, `{"email":"nobody@example.com","password":"`+testPassword+`"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := errorOf(t, rec); got.Code != "invalid_credentials" {
		t.Errorf("code = %q", got.Code)
	}
	if len(q.created) != 0 {
		t.Error("トークンが発行された")
	}
	if got := q.auditActions(); len(got) != 1 || got[0] != "login.failure" {
		t.Errorf("監査記録 = %v, want [login.failure]", got)
	}
}

// パスワード違いは 401 で、failed_attempts が1増える（Design.md 6.2.1 手順5）。
func TestLoginWrongPasswordIncrementsAttempts(t *testing.T) {
	q := newFake(t)
	q.loginRow.FailedAttempts = 1

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"wrong-password-x"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(q.failures) != 1 {
		t.Fatalf("RecordLoginFailure が %d回", len(q.failures))
	}
	if q.failures[0].FailedAttempts != 2 {
		t.Errorf("failed_attempts = %d, want 2", q.failures[0].FailedAttempts)
	}
	if q.failures[0].LockedUntil != nil {
		t.Error("2回目でロックがかかった")
	}
	if got := q.auditActions(); len(got) != 1 || got[0] != "login.failure" {
		t.Errorf("監査記録 = %v", got)
	}
}

// 5回目の失敗で15分ロックし、その応答から 423 になる（Design.md 6.3）。
func TestLoginLocksAfterFiveFailures(t *testing.T) {
	q := newFake(t)
	q.loginRow.FailedAttempts = maxFailedAttempts - 1

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"wrong-password-x"}`)

	if rec.Code != http.StatusLocked {
		t.Fatalf("status = %d, want 423", rec.Code)
	}
	got := errorOf(t, rec)
	if got.Code != "account_locked" {
		t.Errorf("code = %q", got.Code)
	}
	if got.RetryAfterSec <= 0 || got.RetryAfterSec > int(lockDuration/time.Second) {
		t.Errorf("retry_after_sec = %d, want 0 < n <= %d", got.RetryAfterSec, int(lockDuration/time.Second))
	}
	if h := rec.Header().Get("Retry-After"); h != strconv.Itoa(got.RetryAfterSec) {
		t.Errorf("Retry-After ヘッダ = %q, want %d", h, got.RetryAfterSec)
	}

	if len(q.failures) != 1 || q.failures[0].LockedUntil == nil {
		t.Fatalf("locked_until が設定されていない: %+v", q.failures)
	}
	if q.failures[0].FailedAttempts != maxFailedAttempts {
		t.Errorf("failed_attempts = %d, want %d", q.failures[0].FailedAttempts, maxFailedAttempts)
	}
}

// ロック中は照合すらせず 423 を返す（Design.md 6.2.1 手順4）。
func TestLoginWhileLocked(t *testing.T) {
	q := newFake(t)
	q.loginRow.LockedUntil = tsp(time.Now().Add(10 * time.Minute))

	// 正しいパスワードでも通さない。
	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)

	if rec.Code != http.StatusLocked {
		t.Fatalf("status = %d, want 423", rec.Code)
	}
	if len(q.created) != 0 {
		t.Error("ロック中にトークンが発行された")
	}
	if len(q.failures) != 0 {
		t.Error("ロック中の試行で failed_attempts が増えた")
	}
	if got := errorOf(t, rec).RetryAfterSec; got < 590 || got > 601 {
		t.Errorf("retry_after_sec = %d, want 約600", got)
	}
}

// ロック期限が切れていれば、失敗回数は 0 起点に戻る（6.3 の「5回連続」）。
func TestLoginResetsAttemptsAfterLockExpired(t *testing.T) {
	q := newFake(t)
	q.loginRow.FailedAttempts = maxFailedAttempts
	q.loginRow.LockedUntil = tsp(time.Now().Add(-time.Minute))

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"wrong-password-x"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（ロックは明けている）", rec.Code)
	}
	if len(q.failures) != 1 || q.failures[0].FailedAttempts != 1 {
		t.Fatalf("failed_attempts = %+v, want 1", q.failures)
	}
	if q.failures[0].LockedUntil != nil {
		t.Error("1回の失敗で再ロックされた")
	}
}

// 成功時に failed_attempts が 0 に戻る（Design.md 6.2.1 手順5）。
func TestLoginSuccessResetsFailures(t *testing.T) {
	q := newFake(t)
	q.loginRow.FailedAttempts = 3

	postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)

	if len(q.resets) != 1 || q.resets[0] != testIdentity {
		t.Errorf("ResetLoginFailure = %v", q.resets)
	}
	if len(q.touchedLogin) != 1 || q.touchedLogin[0] != testActorID {
		t.Errorf("last_login_at が更新されていない: %v", q.touchedLogin)
	}
	if got := q.auditActions(); len(got) != 1 || got[0] != "login.success" {
		t.Errorf("監査記録 = %v, want [login.success]", got)
	}
	if q.audits[0].TokenID.String != q.created[0].ID {
		t.Error("監査ログの token_id が発行したトークンと違う")
	}
}

// 無効化されたアカウントは 401 で、認証失敗と区別しない（ApiDesign.md 3.1）。
func TestLoginInactiveActor(t *testing.T) {
	q := newFake(t)
	q.loginRow.IsActive = false

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := errorOf(t, rec); got.Code != "invalid_credentials" {
		t.Errorf("code = %q, want invalid_credentials（無効を区別してはならない）", got.Code)
	}
	if len(q.created) != 0 {
		t.Error("無効なアカウントにトークンが発行された")
	}
}

// 旧世代のハッシュはログイン成功時に作り直される（Design.md 6.2.1 手順5）。
func TestLoginRehashesStaleHash(t *testing.T) {
	q := newFake(t)
	q.loginRow.PasswordHash = weakHash(t, testPassword)

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.rehashes) != 1 {
		t.Fatalf("RehashPassword が %d回, want 1", len(q.rehashes))
	}
	if auth.NeedsRehash(q.rehashes[0].PasswordHash) {
		t.Error("再ハッシュ後も旧世代のパラメータのまま")
	}
}

// 現行パラメータのハッシュは作り直さない。
func TestLoginDoesNotRehashCurrentHash(t *testing.T) {
	q := newFake(t)
	postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)
	if len(q.rehashes) != 0 {
		t.Errorf("不要な再ハッシュが走った: %+v", q.rehashes)
	}
}

// 入力の形式誤りは 422 validation_failed で、フィールドごとの details が付く。
func TestLoginValidation(t *testing.T) {
	tests := map[string]struct {
		body  string
		field string
	}{
		"メール未入力":   {`{"email":"","password":"` + testPassword + `"}`, "email"},
		"メール形式が不正": {`{"email":"not-an-email","password":"` + testPassword + `"}`, "email"},
		"パスワード未入力": {`{"email":"tanaka@example.com","password":""}`, "password"},
		"メールが長すぎる": {`{"email":"` + longEmail() + `","password":"` + testPassword + `"}`, "email"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			q := newFake(t)
			rec := postLogin(q, tt.body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rec.Code)
			}
			got := errorOf(t, rec)
			if got.Code != "validation_failed" {
				t.Fatalf("code = %q", got.Code)
			}
			if len(got.Details) == 0 || got.Details[0].Field != tt.field {
				t.Errorf("details = %+v, want field=%q", got.Details, tt.field)
			}
			if len(q.loginedBy) != 0 {
				t.Error("検証に落ちた入力でDBを引いている")
			}
		})
	}
}

// 本文が JSON でなければ 400 bad_request。
func TestLoginBrokenJSON(t *testing.T) {
	rec := postLogin(newFake(t), `{"email":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := errorOf(t, rec); got.Code != "bad_request" {
		t.Errorf("code = %q", got.Code)
	}
}

// 表示名つきのメールでもアドレス部で引く（pb admin create と同じ正規化）。
func TestLoginAcceptsDisplayNameForm(t *testing.T) {
	q := newFake(t)
	postLogin(q, `{"email":"\"田中\" <tanaka@example.com>","password":"`+testPassword+`"}`)

	if len(q.loginedBy) != 1 || q.loginedBy[0] != testEmail {
		t.Errorf("検索に使われたメール = %v, want %q", q.loginedBy, testEmail)
	}
}

// 大文字小文字は変換せず、入力された表記のままDBへ渡す
// （照合は app_user.email の citext に委ねる）。
func TestLoginKeepsEmailCase(t *testing.T) {
	q := newFake(t)
	postLogin(q, `{"email":"Tanaka@Example.com","password":"`+testPassword+`"}`)

	if len(q.loginedBy) != 1 || q.loginedBy[0] != "Tanaka@Example.com" {
		t.Errorf("検索に使われたメール = %v（小文字化してはならない）", q.loginedBy)
	}
}

// DB障害は 401 ではなく 500（利用者が再ログインを試み続けないように）。
func TestLoginDatabaseError(t *testing.T) {
	q := newFake(t)
	q.loginErr = errors.New("connection refused")

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// 失敗回数を記録できないときは通さない（総当たりが無制限になるため）。
func TestLoginFailsClosedWhenAttemptsCannotBeRecorded(t *testing.T) {
	q := newFake(t)
	q.failErr = errors.New("write failed")

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"wrong-password-x"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// トークンを発行できなければ Cookie を出さない。
func TestLoginTokenCreationFailure(t *testing.T) {
	q := newFake(t)
	q.createErr = errors.New("insert failed")

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if cookieOf(rec, auth.SessionCookieName) != nil {
		t.Error("発行に失敗したのに pb_session が返った")
	}
}

// must_change_password が応答に載る（ApiDesign.md 3.1「フロントは変更画面へ誘導」）。
func TestLoginMustChangePassword(t *testing.T) {
	q := newFake(t)
	q.loginRow.MustChange = true

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（要変更でもログインは成立する）", rec.Code)
	}
	actor := viewOf(t, rec)["actor"].(map[string]any)
	if actor["must_change_password"] != true {
		t.Errorf("must_change_password = %v, want true", actor["must_change_password"])
	}
}

// retryAfterSec は端数を切り上げる（0 を返すと「今すぐ再試行してよい」と読める）。
func TestRetryAfterSecRoundsUp(t *testing.T) {
	now := time.Now()
	if got := retryAfterSec(now.Add(1500*time.Millisecond), now); got != 2 {
		t.Errorf("retryAfterSec = %d, want 2", got)
	}
	if got := retryAfterSec(now.Add(-time.Second), now); got != 0 {
		t.Errorf("過去の期限 = %d, want 0", got)
	}
}

// ── 補助 ────────────────────────────────

// weakHash は旧世代のパラメータで実際に生成した PHC 文字列を返す。
//
// パラメータ部だけを書き換えると、キーが元のパラメータで導出されたままに
// なり照合に失敗する。再ハッシュは**照合に成功した後**の処理なので、
// 本物として通る弱いハッシュが要る。
func weakHash(t *testing.T, password string) string {
	t.Helper()
	phc, err := argon2id.CreateHash(password, &argon2id.Params{
		Memory: 32 * 1024, Iterations: 2, Parallelism: 2, SaltLength: 16, KeyLength: 32,
	})
	if err != nil {
		t.Fatalf("CreateHash: %v", err)
	}
	if !auth.NeedsRehash(phc) {
		t.Fatal("弱いはずのハッシュが再ハッシュ対象になっていない")
	}
	return phc
}

// longEmail は上限を超える長さのメールアドレスを返す。
func longEmail() string {
	return strings.Repeat("a", maxEmailLength) + "@example.com"
}
