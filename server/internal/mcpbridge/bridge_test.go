package mcpbridge

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRequiresHTTPSAndTokenEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want bool
	}{
		{"HTTPS", Config{URL: "https://pb.example.test/mcp/pb", TokenEnvName: "PB_TOKEN"}, true},
		{"HTTPは拒む", Config{URL: "http://localhost:8081/mcp/pb", TokenEnvName: "PB_TOKEN"}, false},
		{"トークン名なしは拒む", Config{URL: "https://pb.example.test/mcp/pb"}, false},
		{"URLなしは拒む", Config{TokenEnvName: "PB_TOKEN"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validate(tc.cfg)
			if (err == nil) != tc.want {
				t.Fatalf("validate() error = %v, want success=%v", err, tc.want)
			}
		})
	}
}

func TestForwardUsesAdditionalCAAndBearerToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got, want := r.Method, http.MethodPost; got != want {
			t.Errorf("method = %s, want %s", got, want)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"jsonrpc":"2.0","id":1,"method":"ping"}` {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer server.Close()

	cert := server.Certificate()
	pemText := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	caFile := filepath.Join(t.TempDir(), "pb-ca.pem")
	if err := os.WriteFile(caFile, pemText, 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := newClient(caFile)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	response, err := forward(context.Background(), client, server.URL, "test-token", []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if !strings.Contains(string(response), `"result"`) {
		t.Errorf("response = %s", response)
	}
}
