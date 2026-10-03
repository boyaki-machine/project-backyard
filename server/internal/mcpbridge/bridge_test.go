package mcpbridge

import (
	"bytes"
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
		w.Header().Set("Content-Type", "application/json")
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
	contentType, response, err := forward(context.Background(), client, server.URL, "test-token", []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("contentType = %q", contentType)
	}
	if !strings.Contains(string(response), `"result"`) {
		t.Errorf("response = %s", response)
	}
}

// pb-262: 公式MCP SDKは既定で text/event-stream を返す。bridge が受け取った
// 応答を生のまま1行として中継すると、SSEの複数行構造がCodexにはJSON-RPCとして
// 読めなくなる（この regression の再現には pb-mcp-bridge を介した通信が要る）。
func TestRunDecodesServerSentEventResponses(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// 進捗通知（idなし）に続けて、本来の応答が別イベントで届く。
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n"))
		_, _ = w.Write([]byte(": ok\n\n"))
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n"))
	}))
	defer server.Close()

	cert := server.Certificate()
	pemText := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	caFile := filepath.Join(t.TempDir(), "pb-ca.pem")
	if err := os.WriteFile(caFile, pemText, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{URL: server.URL, TokenEnvName: "PB_BRIDGE_TEST_TOKEN", CAFile: caFile}
	t.Setenv("PB_BRIDGE_TEST_TOKEN", "test-token")

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	var out bytes.Buffer
	if err := Run(context.Background(), in, &out, cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d line(s), want 2: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"notifications/progress"`) {
		t.Errorf("line[0] = %s", lines[0])
	}
	if !strings.Contains(lines[1], `"result"`) || !strings.Contains(lines[1], `"ok":true`) {
		t.Errorf("line[1] = %s", lines[1])
	}
}

func TestDecodeMessages(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		want        []string
	}{
		{"JSONはそのまま1件", "application/json", `{"jsonrpc":"2.0","id":1,"result":{}}`,
			[]string{`{"jsonrpc":"2.0","id":1,"result":{}}`}},
		{"charset付きのJSONもそのまま1件", "application/json; charset=utf-8", `{"jsonrpc":"2.0","id":1,"result":{}}`,
			[]string{`{"jsonrpc":"2.0","id":1,"result":{}}`}},
		{"SSEはイベントごとに1件", "text/event-stream",
			"event: message\ndata: {\"a\":1}\n\nevent: message\ndata: {\"b\":2}\n\n",
			[]string{`{"a":1}`, `{"b":2}`}},
		{"SSEのコメント行は無視する", "text/event-stream", ": ok\n\nevent: message\ndata: {\"a\":1}\n\n",
			[]string{`{"a":1}`}},
		{"本文が空なら0件", "application/json", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeMessages(tc.contentType, []byte(tc.body))
			if err != nil {
				t.Fatalf("decodeMessages: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d message(s), want %d: %q", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if string(got[i]) != w {
					t.Errorf("message[%d] = %s, want %s", i, got[i], w)
				}
			}
		})
	}
}
