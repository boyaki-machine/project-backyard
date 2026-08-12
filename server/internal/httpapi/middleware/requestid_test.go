package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

func TestRequestIDIsULIDAndUnique(t *testing.T) {
	var seen []string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, apierr.RequestIDFromContext(r.Context()))
	}))

	for range 3 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}

	if len(seen) != 3 {
		t.Fatalf("ハンドラが呼ばれた回数 = %d", len(seen))
	}
	for _, id := range seen {
		// audit_log.id / activity.request_id は char(26)（DbDesign.md 6.8）。
		if len(id) != 26 {
			t.Errorf("request_id の長さ = %d, want 26 (%q)", len(id), id)
		}
		if _, err := ulid.ParseStrict(id); err != nil {
			t.Errorf("ULID として解釈できない: %q (%v)", id, err)
		}
	}
	if seen[0] == seen[1] || seen[1] == seen[2] {
		t.Errorf("request_id が重複している: %v", seen)
	}
}

// クライアントが渡したヘッダを採用しない（監査ログの識別子を外部に決めさせない）。
func TestRequestIDIgnoresClientSuppliedHeader(t *testing.T) {
	var got string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = apierr.RequestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "00000000000000000000000000")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got == "00000000000000000000000000" {
		t.Error("クライアントが指定した request_id が採用されている")
	}
}
