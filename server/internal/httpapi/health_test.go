package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func getHealth(t *testing.T, deps Deps) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, HealthPath, nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答が JSON として読めない: %v (%s)", err, rec.Body.String())
	}
	return rec, body
}

// ApiDesign.md 2.11：認証不要、常に 200 と {"status":"OK"}。
func TestHealthDefault(t *testing.T) {
	rec, body := getHealth(t, Deps{Version: "1.4.4"})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if body["status"] != "OK" {
		t.Errorf("status = %v, want OK", body["status"])
	}
	// 既定ではバージョンを返さない（未認証の呼び出し元への情報開示になるため）。
	if _, ok := body["version"]; ok {
		t.Errorf("PB_HEALTH_SHOW_VERSION=false なのに version が出ている: %s", rec.Body.String())
	}
}

func TestHealthWithVersion(t *testing.T) {
	_, body := getHealth(t, Deps{Version: "1.4.4", HealthShowVersion: true})

	if body["status"] != "OK" {
		t.Errorf("status = %v, want OK", body["status"])
	}
	if body["version"] != "1.4.4" {
		t.Errorf("version = %v, want 1.4.4", body["version"])
	}
}

// DB へ接続しない（Deps.Pool が nil でも応答する）。
// liveness に DB を含めない設計の裏返し（Design.md 10.2）。
func TestHealthDoesNotTouchDB(t *testing.T) {
	rec, body := getHealth(t, Deps{Pool: nil})
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if body["status"] != "OK" {
		t.Errorf("status = %v", body["status"])
	}
}

// /api/v1 の外に置く（ApiDesign.md 2.11）。
func TestHealthIsOutsideVersionedAPI(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter(Deps{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+HealthPath, nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("%s%s: status = %d, want 404", BasePath, HealthPath, rec.Code)
	}
}
