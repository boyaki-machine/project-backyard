package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// アクセスログがテスト出力を埋めないよう、既定ロガーを捨てる。
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

type errBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func do(t *testing.T, method, path string) (*httptest.ResponseRecorder, errBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	NewRouter(Deps{}).ServeHTTP(rec, httptest.NewRequest(method, path, nil))

	var body errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答が 2.5 の JSON 形式でない: %v (%s)", err, rec.Body.String())
	}
	return rec, body
}

// API の未知のパスは 2.5 の形式で 404 を返す。chi の既定（本文なし）ではない。
//
// SPA のフォールバック（Design.md 3.4）が効くのは /api /mcp の外だけであり、
// 綴りを誤った API 呼び出しに index.html を返してはならない。
func TestUnknownAPIPathReturnsApiErrorShape(t *testing.T) {
	for _, path := range []string{"/api/v1/nope", "/api", "/api/", "/mcp/nope"} {
		rec, body := do(t, http.MethodGet, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
		if body.Error.Code != "not_found" {
			t.Errorf("%s: code = %q", path, body.Error.Code)
		}
		if body.Error.Message == "" {
			t.Errorf("%s: message が空", path)
		}
	}
}

// API 以外の未知のパスは SPA の index.html を返す（Design.md 3.4）。
// 画面のルーティングはクライアント側にあり、サーバは経路を知らない。
func TestUnknownScreenPathFallsBackToSPA(t *testing.T) {
	for _, path := range []string{"/", "/projects", "/p/my-app/tickets/31", "/404"} {
		rec := httptest.NewRecorder()
		NewRouter(Deps{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q, want text/html", path, ct)
		}
	}
}

// 画面のパスへの POST に index.html を返さない。
func TestSPAFallbackIsGetOnly(t *testing.T) {
	rec, body := do(t, http.MethodPost, "/projects")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	if body.Error.Code != "method_not_allowed" {
		t.Errorf("code = %q", body.Error.Code)
	}
}

// RequestID ミドルウェアが 404 応答にも効いている（連鎖の外に落ちていない）。
func TestNotFoundCarriesRequestID(t *testing.T) {
	_, body := do(t, http.MethodGet, "/api/v1/nope")
	if _, err := ulid.ParseStrict(body.Error.RequestID); err != nil {
		t.Errorf("request_id が ULID でない: %q (%v)", body.Error.RequestID, err)
	}
}

// 同じパスへの2回のリクエストで request_id が変わる。
func TestRequestIDDiffersPerRequest(t *testing.T) {
	_, a := do(t, http.MethodGet, "/api/v1/nope")
	_, b := do(t, http.MethodGet, "/api/v1/nope")
	if a.Error.RequestID == b.Error.RequestID {
		t.Errorf("request_id が使い回されている: %q", a.Error.RequestID)
	}
}

func TestContentType(t *testing.T) {
	rec, _ := do(t, http.MethodGet, "/api/v1/nope")
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// 認証必須グループに置いたルートは、資格情報が無ければハンドラへ届かず
// 401 unauthenticated になる（Design.md 6.2.2）。
//
// v1.Mount が並べる実際のルートで確かめる。ここが 404 になるようなら
// マウントの経路が切れている。
func TestAuthenticatedRoutesRejectAnonymous(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/me"},
		{http.MethodPost, "/auth/logout"},
	} {
		rec := httptest.NewRecorder()
		NewRouter(Deps{Queries: emptyQuerier{}}).
			ServeHTTP(rec, httptest.NewRequest(route.method, BasePath+route.path, nil))

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: status = %d, want 401（body=%s）",
				route.method, route.path, rec.Code, rec.Body.String())
		}

		var body errBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答が 2.5 の JSON 形式でない: %v (%s)", err, rec.Body.String())
		}
		if body.Error.Code != "unauthenticated" {
			t.Errorf("%s: code = %q, want unauthenticated", route.path, body.Error.Code)
		}
		if body.Error.RequestID == "" {
			t.Errorf("%s: request_id が空。ミドルウェアの順序が崩れている", route.path)
		}
	}
}

// ログインは認証必須グループの外にある（ApiDesign.md 3.1「必要権限：不要」）。
// 資格情報なしで叩いて 401 になるようでは、誰もログインできない。
func TestLoginIsOutsideAuthentication(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, BasePath+"/auth/login",
		strings.NewReader(`{"email":"","password":""}`))
	NewRouter(Deps{Queries: emptyQuerier{}}).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（認証を要求してはならない。body=%s）", rec.Code, rec.Body.String())
	}
}

// /healthcheck は認証必須グループの外にある（ApiDesign.md 2.11）。
func TestHealthcheckIsOutsideAuthentication(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter(Deps{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, HealthPath, nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200（認証を要求してはならない）", rec.Code)
	}
}

// emptyQuerier は認証が必ず失敗する Querier（トークンを1件も持たない）。
type emptyQuerier struct{ gen.Querier }

func (emptyQuerier) FindAccessTokenByHash(context.Context, string) (gen.FindAccessTokenByHashRow, error) {
	return gen.FindAccessTokenByHashRow{}, pgx.ErrNoRows
}

// パスは存在するがメソッドが違う場合は 405 method_not_allowed（ApiDesign.md 2.5.1）。
// /healthcheck は GET のみを受け付ける。
func TestMethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		rec, body := do(t, method, HealthPath)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", method, HealthPath, rec.Code)
		}
		if body.Error.Code != "method_not_allowed" {
			t.Errorf("%s: code = %q", method, body.Error.Code)
		}
		if body.Error.RequestID == "" {
			t.Errorf("%s: request_id が空", method)
		}
	}
}
