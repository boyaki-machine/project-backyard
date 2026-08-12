package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oklog/ulid/v2"
)

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

// 未知のパスは 2.5 の形式で 404 を返す。chi の既定（本文なし）ではない。
func TestUnknownPathReturnsApiErrorShape(t *testing.T) {
	for _, path := range []string{"/api/v1/nope", "/", "/nope"} {
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
