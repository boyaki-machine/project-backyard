package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/maintenance"
)

// next は「素通しされた」ことが分かる印を返す。
func next() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("passed"))
	})
}

func exempt(r *http.Request) bool {
	return r.URL.Path == "/healthcheck" ||
		(r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/restore")
}

// TestMaintenanceOffPassesThrough は、保守モードでなければ何も変えないことを見る。
func TestMaintenanceOffPassesThrough(t *testing.T) {
	flag := maintenance.New(nil)
	h := Maintenance(flag, exempt)(next())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d（素通しされていない）", rec.Code, http.StatusTeapot)
	}
}

// TestMaintenanceResponses は、人と機械で応答を分けることを見る（ApiDesign.md 11.13）。
//
// **ステータスはどちらも 503 である。** ブラウザはステータスに関係なく本文を描くので
// 画面は成立し、200 にするとエージェントと監視が成功と読む。
func TestMaintenanceResponses(t *testing.T) {
	flag := maintenance.New(nil)
	leave, err := flag.Enter("テスト")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer leave()
	h := Maintenance(flag, exempt)(next())

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantJSON   bool
		wantPass   bool
	}{
		{"API は 503 とエラー形式", http.MethodGet, "/api/v1/me", http.StatusServiceUnavailable, true, false},
		{"MCP も 503 とエラー形式", http.MethodPost, "/mcp/pb", http.StatusServiceUnavailable, true, false},
		{"画面は 503 と静的な HTML", http.MethodGet, "/p/pb/backlog", http.StatusServiceUnavailable, false, false},
		{"ルートも 503 と静的な HTML", http.MethodGet, "/", http.StatusServiceUnavailable, false, false},
		{"ヘルスチェックは素通し", http.MethodGet, "/healthcheck", http.StatusTeapot, false, true},
		{"取り込みの口は素通し", http.MethodPost, "/api/v1/admin/restore", http.StatusTeapot, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantPass {
				if rec.Body.String() != "passed" {
					t.Errorf("素通しされていない: %q", rec.Body.String())
				}
				return
			}
			if tt.wantJSON {
				var got struct {
					Error struct {
						Code          string `json:"code"`
						Message       string `json:"message"`
						RetryAfterSec int    `json:"retry_after_sec"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("JSON ではない: %v（%s）", err, rec.Body.String())
				}
				if got.Error.Code != "maintenance" {
					t.Errorf("code = %q, want maintenance", got.Error.Code)
				}
				if got.Error.Message == "" {
					t.Error("message が空（そのまま画面に出せる日本語であること。2.5）")
				}
				// **Retry-After を返さない**（11.13）。見積もれる数字が無い。
				if got.Error.RetryAfterSec != 0 {
					t.Errorf("retry_after_sec = %d, want 0", got.Error.RetryAfterSec)
				}
				if h := rec.Header().Get("Retry-After"); h != "" {
					t.Errorf("Retry-After ヘッダが付いている: %q", h)
				}
				return
			}
			// 画面向け：**単一の静的な HTML で、SPA を積まない**
			body := rec.Body.String()
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", ct)
			}
			if !strings.Contains(body, "メンテナンス中") {
				t.Errorf("「メンテナンス中」が出ていない: %q", body)
			}
			if strings.Contains(body, "<script") {
				t.Error("SPA を積んでいる（保守モードでは Vue を読み込まない。11.13）")
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Error("Cache-Control: no-store が無い（終わったあとも返り続けうる）")
			}
		})
	}
}

// TestMaintenanceEnterIsExclusive は、取り込みが2つ同時に走らないことを見る。
func TestMaintenanceEnterIsExclusive(t *testing.T) {
	flag := maintenance.New(nil)
	leave, err := flag.Enter("1つ目")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if _, err := flag.Enter("2つ目"); err == nil {
		t.Fatal("2つ目が入れてしまった（同時の取り込みを防げない）")
	}
	leave()
	if flag.On() {
		t.Error("leave のあとも保守モードのままである")
	}
	// **leave は何度呼んでも安全である**（defer と明示の両方で呼ばれうる）
	leave()
	leave2, err := flag.Enter("3つ目")
	if err != nil {
		t.Fatalf("出たあとに入れない: %v", err)
	}
	leave2()
}
