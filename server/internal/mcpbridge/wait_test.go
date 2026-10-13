package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeWaitServer は pb_wait に答える偽の PB MCP。answers の順に応答を返し、
// 受け取った since を記録する。応答は SDK と同じく SSE で返す。
func fakeWaitServer(t *testing.T, answers []string) (*httptest.Server, *[]string, string) {
	t.Helper()
	var sinces []string
	n := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("要求を解釈できない: %v", err)
		}
		if req.Method != "tools/call" || req.Params.Name != "pb_wait" || req.Params.Arguments["seq"] != float64(226) {
			t.Errorf("要求 = %s, want tools/call pb_wait seq=226", body)
		}
		since, _ := req.Params.Arguments["since"].(string)
		sinces = append(sinces, since)
		text := answers[min(n, len(answers)-1)]
		n++
		result, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", result)
	}))
	t.Cleanup(server.Close)
	pemText := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caFile := filepath.Join(t.TempDir(), "pb-ca.pem")
	if err := os.WriteFile(caFile, pemText, 0o600); err != nil {
		t.Fatal(err)
	}
	return server, &sinces, caFile
}

// 変化が無い間は cursor を引き継いで pb_wait を繰り返し、変化があったら結果を書いて終わる（pb-303）。
func TestWaitRepeatsUntilChanged(t *testing.T) {
	server, sinces, caFile := fakeWaitServer(t, []string{
		`{"changed":false,"cursor":"c1"}`,
		`{"changed":false,"cursor":"c2"}`,
		`{"changed":true,"cursor":"c3","next":"変化があった"}`,
	})
	t.Setenv("PB_WAIT_TEST_TOKEN", "test-token")
	var out bytes.Buffer
	result, err := Wait(context.Background(), &out, Config{URL: server.URL, TokenEnvName: "PB_WAIT_TEST_TOKEN", CAFile: caFile},
		WaitOptions{Seq: 226, Since: "c0", Max: time.Hour})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result != WaitChanged {
		t.Errorf("result = %v, want WaitChanged", result)
	}
	if got := strings.Join(*sinces, ","); got != "c0,c1,c2" {
		t.Errorf("since の並び = %s, want c0,c1,c2（前の cursor を引き継ぐ）", got)
	}
	if !strings.Contains(out.String(), `"cursor":"c3"`) || !strings.Contains(out.String(), "変化があった") {
		t.Errorf("出力 = %s, want 最後の pb_wait の応答", out.String())
	}
}

// 上限を過ぎたら、変化なしとして最後の cursor を書いて終わる。
func TestWaitStopsAtMax(t *testing.T) {
	server, sinces, caFile := fakeWaitServer(t, []string{`{"changed":false,"cursor":"c9"}`})
	t.Setenv("PB_WAIT_TEST_TOKEN", "test-token")
	var out bytes.Buffer
	result, err := Wait(context.Background(), &out, Config{URL: server.URL, TokenEnvName: "PB_WAIT_TEST_TOKEN", CAFile: caFile},
		WaitOptions{Seq: 226, Max: 0})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result != WaitTimedOut {
		t.Errorf("result = %v, want WaitTimedOut", result)
	}
	if len(*sinces) != 1 || (*sinces)[0] != "" {
		t.Errorf("since = %v, want 1回だけ・省略", *sinces)
	}
	if !strings.Contains(out.String(), `"cursor":"c9"`) {
		t.Errorf("出力 = %s, want 最後の cursor", out.String())
	}
}

// pb_wait が失敗を返したら、繰り返さずに失敗として終わる（拒まれたまま待ち続けない）。
func TestWaitFailsOnToolError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"チケット 226 が見つかりません"}]}}`))
	}))
	defer server.Close()
	pemText := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caFile := filepath.Join(t.TempDir(), "pb-ca.pem")
	if err := os.WriteFile(caFile, pemText, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PB_WAIT_TEST_TOKEN", "test-token")
	_, err := Wait(context.Background(), io.Discard, Config{URL: server.URL, TokenEnvName: "PB_WAIT_TEST_TOKEN", CAFile: caFile},
		WaitOptions{Seq: 226, Max: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "見つかりません") {
		t.Errorf("err = %v, want pb_wait の失敗の文", err)
	}
}

func TestWaitRequiresSeq(t *testing.T) {
	t.Setenv("PB_WAIT_TEST_TOKEN", "test-token")
	_, err := Wait(context.Background(), io.Discard, Config{URL: "https://pb.example.test/mcp/pb", TokenEnvName: "PB_WAIT_TEST_TOKEN"},
		WaitOptions{Max: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "--seq") {
		t.Errorf("err = %v, want --seq の指定を求める", err)
	}
}

// 評価役が待つ間に main が止まっていれば、繰り返さずに終わる（Delegation.md 10.4。pb-310）。
func TestWaitStopsWhenMainStopped(t *testing.T) {
	server, sinces, caFile := fakeWaitServer(t, []string{
		`{"changed":false,"cursor":"c1"}`,
		`{"changed":false,"main_stopped":true,"cursor":"c1","next":"main が止まっている"}`,
	})
	t.Setenv("PB_WAIT_TEST_TOKEN", "test-token")
	var out bytes.Buffer
	result, err := Wait(context.Background(), &out, Config{URL: server.URL, TokenEnvName: "PB_WAIT_TEST_TOKEN", CAFile: caFile},
		WaitOptions{Seq: 226, Max: time.Hour})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result != WaitMainStopped {
		t.Errorf("result = %v, want WaitMainStopped", result)
	}
	if len(*sinces) != 2 || !strings.Contains(out.String(), "main が止まっている") {
		t.Errorf("呼び出し = %d 回・出力 = %s, want 2回で止まり、止まる指示を書く", len(*sinces), out.String())
	}
}
