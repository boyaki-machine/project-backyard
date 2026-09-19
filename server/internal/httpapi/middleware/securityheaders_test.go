package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 4件すべてが付くこと（ApiDesign.md 2.12 の表と一対一）。
func TestSecurityHeadersAreSet(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy が付いていない")
	}
}

// **HSTS を出さない**（Design.md 6.6.2）。PB は http でも動くので、
// 一度受け取ったブラウザが http で開けなくなると締め出しになる。
func TestSecurityHeadersDoesNotSetHSTS(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q, want 空", got)
	}
}

// CSP の各ディレクティブ。**画面が壊れる値を黙って入れないための検査である**
// ——`script-src` を緩めていないこと、QR と Vue の :style に要る分だけを
// 許していることを、文字列として固定する。
func TestContentSecurityPolicyDirectives(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")

	for _, want := range []string{
		"default-src 'self'",
		// **ここに 'unsafe-inline' や 'unsafe-eval' が入ったら落とす。**
		// スクリプトの実行を止めるのはこのディレクティブである。
		"script-src 'self'",
		// Vue の :style 束縛と CodeMirror に要る（Design.md 6.6.2）。
		"style-src 'self' 'unsafe-inline'",
		// TOTP の QR が data:image/png で来る（ApiDesign.md 4.6.2）。
		"img-src 'self' data:",
		"frame-ancestors 'none'",
		"object-src 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP に %q が無い: %s", want, csp)
		}
	}

	// **script-src が緩んでいないことを、含まれないほうからも見る。**
	// 上の Contains だけだと "script-src 'self' 'unsafe-inline'" でも通る。
	if i := strings.Index(csp, "script-src"); i >= 0 {
		rest := csp[i:]
		if j := strings.Index(rest, ";"); j >= 0 {
			rest = rest[:j]
		}
		if strings.Contains(rest, "unsafe") {
			t.Errorf("script-src が緩んでいる: %q", rest)
		}
	}
}

// 応答の中身にかかわらず付く（API の JSON にも、エラーにも）。
func TestSecurityHeadersSetBeforeHandlerWrites(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal_error"}}`))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("エラー応答に nosniff が付いていない")
	}
}
