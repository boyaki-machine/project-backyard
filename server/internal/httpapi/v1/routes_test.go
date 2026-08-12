package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
)

// ルート定義に CSRF とレート制限が並んでいることを、ハンドラではなく
// ルータ越しに確かめる（Design.md 6.4.4「必要権限はルート定義に宣言する」）。
// ミドルウェア単体の検証は middleware パッケージ側にある。

// **Cookie 認証の POST に X-PB-CSRF が無ければ 403**（ApiDesign.md 2.4）。
func TestLogoutRequiresCSRF(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	// pb_csrf も X-PB-CSRF も付けない。
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "csrf_failed" {
		t.Errorf("code = %q, want csrf_failed", got)
	}
	if len(q.revoked) != 0 {
		t.Error("CSRF に失敗したのにトークンを失効させた")
	}
}

// GET は状態を変えないので CSRF を要求しない。
func TestMeDoesNotRequireCSRF(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
}

// Bearer 認証には CSRF を要求しない（ApiDesign.md 2.4）。CLI から
// pb_csrf を用意させることになってしまうため。
func TestLogoutWithBearerSkipsCSRF(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
}

// ログインは IP あたり loginRateLimit 回/分（ApiDesign.md 2.9）。
//
// **成否によらず数える。** 本文を壊した 400 を並べているのは、ハンドラの
// 結果と無関係に「送った回数」で制限されることを固定するためである
// （ロックの副作用も入らない）。
func TestLoginIsRateLimitedPerIP(t *testing.T) {
	q := newFake(t)
	r := router(q)

	for i := 1; i <= loginRateLimit; i++ {
		rec := call(r, http.MethodPost, "/api/v1/auth/login", `{"email":`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%d回目 status = %d, want 400（上限は %d）", i, rec.Code, loginRateLimit)
		}
	}

	over := call(r, http.MethodPost, "/api/v1/auth/login", `{"email":`)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("%d回目 status = %d, want 429（body=%s）",
			loginRateLimit+1, over.Code, over.Body.String())
	}

	got := errorOf(t, over)
	if got.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", got.Code)
	}
	if got.RetryAfterSec <= 0 {
		t.Errorf("retry_after_sec = %d, want 1以上", got.RetryAfterSec)
	}
	if h := over.Header().Get("Retry-After"); h == "" {
		t.Error("Retry-After ヘッダが無い（ApiDesign.md 2.9）")
	}
	if h := over.Header().Get("X-RateLimit-Limit"); h != "10" {
		t.Errorf("X-RateLimit-Limit = %q, want 10", h)
	}
}

// ログインの制限は他のエンドポイントを巻き込まない（キーも上限も別）。
func TestLoginRateLimitDoesNotAffectMe(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	r := router(q)

	for range loginRateLimit + 1 {
		call(r, http.MethodPost, "/api/v1/auth/login", `{"email":`)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me の status = %d, want 200（ログインの制限に巻き込まれている）", rec.Code)
	}
}

// 認証済みリクエストには X-RateLimit ヘッダが付く（アクターあたり 600回/分）。
func TestAuthenticatedRequestsCarryRateLimitHeaders(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	rec := authed(q, http.MethodGet, "/api/v1/me", token)
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "600" {
		t.Errorf("X-RateLimit-Limit = %q, want 600", got)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "599" {
		t.Errorf("X-RateLimit-Remaining = %q, want 599", got)
	}
}
