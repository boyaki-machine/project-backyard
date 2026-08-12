package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
)

const csrfToken = "csrf-token-01K2F8QW3H7YRJ4M5N6P7Q8R9S"

// serveCSRF は RequireCSRF を通したリクエストを実行し、
// 応答と「ハンドラまで届いたか」を返す。
func serveCSRF(r *http.Request) (*httptest.ResponseRecorder, bool) {
	reached := false
	h := RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, reached
}

// cookieRequest は Cookie 認証を通ったリクエストを作る。
func cookieRequest(method string, source auth.CredentialSource) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/auth/logout", nil)
	p := &auth.Principal{ActorID: "01K2F8QW3H7YRJ4M5N6P7Q8R9S", Source: source}
	return r.WithContext(auth.NewPrincipalContext(r.Context(), p))
}

// Cookie とヘッダが一致すれば通る（ApiDesign.md 2.4）。
func TestCSRFAllowsMatchingToken(t *testing.T) {
	r := cookieRequest(http.MethodPost, auth.SourceCookie)
	r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrfToken})
	r.Header.Set(auth.CSRFHeaderName, csrfToken)

	w, reached := serveCSRF(r)
	if !reached {
		t.Fatalf("一致しているのにハンドラへ届かない（status=%d body=%s）", w.Code, w.Body.String())
	}
}

// **X-PB-CSRF が無ければ 403 csrf_failed。** 手順5b の検証項目そのもの。
func TestCSRFRejectsMissingHeader(t *testing.T) {
	r := cookieRequest(http.MethodPost, auth.SourceCookie)
	r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrfToken})

	w, reached := serveCSRF(r)
	if reached {
		t.Fatal("ヘッダが無いのにハンドラへ届いた")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if got := errorCode(t, w); got != "csrf_failed" {
		t.Errorf("code = %q, want csrf_failed", got)
	}
}

// Cookie 側が無い場合も同じ 403。理由は応答で区別しない。
func TestCSRFRejectsMissingCookie(t *testing.T) {
	r := cookieRequest(http.MethodPost, auth.SourceCookie)
	r.Header.Set(auth.CSRFHeaderName, csrfToken)

	w, reached := serveCSRF(r)
	if reached {
		t.Fatal("Cookie が無いのにハンドラへ届いた")
	}
	if got := errorCode(t, w); got != "csrf_failed" {
		t.Errorf("code = %q, want csrf_failed", got)
	}
}

// 値が食い違えば 403。ヘッダだけを推測しても通らない。
func TestCSRFRejectsMismatch(t *testing.T) {
	r := cookieRequest(http.MethodPost, auth.SourceCookie)
	r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrfToken})
	r.Header.Set(auth.CSRFHeaderName, csrfToken+"x")

	w, reached := serveCSRF(r)
	if reached {
		t.Fatal("不一致なのにハンドラへ届いた")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// 空文字どうしを「一致」と読まない。Cookie が空なら通さない。
func TestCSRFRejectsEmptyValues(t *testing.T) {
	r := cookieRequest(http.MethodPost, auth.SourceCookie)
	r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: ""})
	r.Header.Set(auth.CSRFHeaderName, "")

	if _, reached := serveCSRF(r); reached {
		t.Fatal("空の CSRF トークンを一致とみなした")
	}
}

// 状態を変えないメソッドは対象外（ApiDesign.md 2.4）。
func TestCSRFSkipsSafeMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		r := cookieRequest(method, auth.SourceCookie)
		if _, reached := serveCSRF(r); !reached {
			t.Errorf("%s が CSRF で弾かれた", method)
		}
	}
}

// 列挙されている状態変更系はすべて対象になる。
func TestCSRFCoversUnsafeMethods(t *testing.T) {
	for _, method := range []string{
		http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete,
	} {
		r := cookieRequest(method, auth.SourceCookie)
		if _, reached := serveCSRF(r); reached {
			t.Errorf("%s が CSRF の対象外になっている", method)
		}
	}
}

// **Bearer 認証では要求しない**（ApiDesign.md 2.4）。
// Authorization ヘッダは他サイトからのリクエストに自動では付かない。
func TestCSRFSkipsBearer(t *testing.T) {
	r := cookieRequest(http.MethodPost, auth.SourceBearer)
	if _, reached := serveCSRF(r); !reached {
		t.Fatal("Bearer 認証に CSRF を要求した")
	}
}

// 未認証（Principal が無い）は素通しする。認証を通っていないリクエストは
// Authenticate が先に 401 で弾いており、ここへは届かない。
func TestCSRFSkipsUnauthenticated(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	if _, reached := serveCSRF(r); !reached {
		t.Fatal("未認証のリクエストを CSRF で弾いた（ログインが通らなくなる）")
	}
}
