package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// capture は AccessLog が出した行を JSON として読み取る。
func capture(t *testing.T, level slog.Level, quiet []string, h http.Handler, req *http.Request) []map[string]any {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})))
	defer slog.SetDefault(prev)

	RequestID(AccessLog(quiet...)(h)).ServeHTTP(httptest.NewRecorder(), req)

	var lines []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("ログ行が JSON として読めない: %v (%s)", err, raw)
		}
		lines = append(lines, m)
	}
	return lines
}

func okHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

// Design.md 10.1：1リクエスト1行。項目は request_id / method / path /
// status / duration_ms / bytes / ip。
func TestAccessLogFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects?page=2&q=tanaka@example.com", nil)
	req.RemoteAddr = "127.0.0.1:54321"

	lines := capture(t, slog.LevelInfo, nil, okHandler(http.StatusOK, "hello"), req)
	if len(lines) != 1 {
		t.Fatalf("ログ行数 = %d, want 1: %v", len(lines), lines)
	}
	got := lines[0]

	if got["msg"] != "request" {
		t.Errorf("msg = %v", got["msg"])
	}
	if got["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", got["level"])
	}
	if got["method"] != "GET" {
		t.Errorf("method = %v", got["method"])
	}
	if got["status"] != float64(200) {
		t.Errorf("status = %v, want 200", got["status"])
	}
	if got["bytes"] != float64(len("hello")) {
		t.Errorf("bytes = %v, want 5", got["bytes"])
	}
	if got["ip"] != "127.0.0.1" {
		t.Errorf("ip = %v, want 127.0.0.1（ポートを落とす）", got["ip"])
	}
	if id, _ := got["request_id"].(string); len(id) != 26 {
		t.Errorf("request_id = %v", got["request_id"])
	}
	if _, ok := got["duration_ms"]; !ok {
		t.Error("duration_ms が無い")
	}
}

// クエリ文字列は記録しない（検索語やメールアドレスがログに残るため）。
func TestAccessLogOmitsQueryString(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects?q=tanaka@example.com", nil)
	lines := capture(t, slog.LevelInfo, nil, okHandler(http.StatusOK, ""), req)

	if got := lines[0]["path"]; got != "/api/v1/projects" {
		t.Errorf("path = %v, want /api/v1/projects", got)
	}
	if bytes.Contains([]byte(lines[0]["path"].(string)), []byte("tanaka")) {
		t.Error("クエリ文字列がログに残っている")
	}
}

// 5xx は ERROR、それ以外は INFO。
func TestAccessLogLevelByStatus(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{200, "INFO"},
		{404, "INFO"},
		{422, "INFO"},
		{500, "ERROR"},
	}
	for _, tt := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		lines := capture(t, slog.LevelInfo, nil, okHandler(tt.status, ""), req)
		if len(lines) != 1 {
			t.Fatalf("status %d: ログ行数 = %d", tt.status, len(lines))
		}
		if lines[0]["level"] != tt.want {
			t.Errorf("status %d: level = %v, want %s", tt.status, lines[0]["level"], tt.want)
		}
	}
}

// quietPaths に挙げたパスは DEBUG。既定レベル（info）では出ない。
func TestAccessLogQuietPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthcheck", nil)

	lines := capture(t, slog.LevelInfo, []string{"/healthcheck"}, okHandler(http.StatusOK, ""), req)
	if len(lines) != 0 {
		t.Errorf("info レベルで /healthcheck のログが出ている: %v", lines)
	}

	req = httptest.NewRequest(http.MethodGet, "/healthcheck", nil)
	lines = capture(t, slog.LevelDebug, []string{"/healthcheck"}, okHandler(http.StatusOK, ""), req)
	if len(lines) != 1 {
		t.Fatalf("debug レベルで出ていない: %v", lines)
	}
	if lines[0]["level"] != "DEBUG" {
		t.Errorf("level = %v, want DEBUG", lines[0]["level"])
	}
}

// quietPaths でも失敗は隠さない。メソッド違いや probe の設定誤りに気づけなくなるため。
func TestAccessLogQuietPathStillLogsFailures(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusMethodNotAllowed, "INFO"},
		{http.StatusInternalServerError, "ERROR"},
	}
	for _, tt := range cases {
		req := httptest.NewRequest(http.MethodPost, "/healthcheck", nil)
		lines := capture(t, slog.LevelInfo, []string{"/healthcheck"}, okHandler(tt.status, ""), req)

		if len(lines) != 1 {
			t.Fatalf("status %d: /healthcheck の失敗が握りつぶされている: %v", tt.status, lines)
		}
		if lines[0]["level"] != tt.want {
			t.Errorf("status %d: level = %v, want %s", tt.status, lines[0]["level"], tt.want)
		}
	}
}

// WriteHeader を呼ばないハンドラでも 200 として記録する。
func TestAccessLogImplicit200(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	lines := capture(t, slog.LevelInfo, nil, h, req)

	if lines[0]["status"] != float64(200) {
		t.Errorf("status = %v, want 200", lines[0]["status"])
	}
	if lines[0]["bytes"] != float64(2) {
		t.Errorf("bytes = %v, want 2", lines[0]["bytes"])
	}
}
