package mcpbridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHookEventNameSkipsLargeFields(t *testing.T) {
	// PostToolUse はツールの出力を丸ごと含む。イベント名がその後ろにあっても拾う。
	big := strings.Repeat("x", 300<<10)
	body := `{"session_id":"s","tool_response":{"stdout":"` + big + `"},"hook_event_name":"PostToolUse"}`
	if got := HookEventName(strings.NewReader(body)); got != "PostToolUse" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"", "[]", `{"hook_event_name":1}`, `{"other":"x"}`, "not json"} {
		if got := HookEventName(strings.NewReader(bad)); got != "" {
			t.Fatalf("%q: got %q", bad, got)
		}
	}
}

func TestHookForwardsOnlyTheEventName(t *testing.T) {
	var got struct {
		auth string
		body map[string]string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got.body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("PB_TOKEN_HOOK_TEST", "pb_agt_x")
	in := strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_input":{"command":"rm -rf /"}}`)
	if err := Hook(context.Background(), in, HookConfig{HoldURL: srv.URL + "/api/v1/projects/pb/work-hold", TokenEnvName: "PB_TOKEN_HOOK_TEST"}); err != nil {
		t.Fatal(err)
	}
	// ツールの入力（コマンドの中身）は PB へ送らない。
	if got.auth != "Bearer pb_agt_x" || len(got.body) != 1 || got.body["hook_event_name"] != "PermissionRequest" {
		t.Fatalf("got %+v", got)
	}
	// 空のトークンでは送らずに誤りを返す（呼び出し側は終了コード0で終わる）。
	if err := Hook(context.Background(), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), HookConfig{HoldURL: srv.URL, TokenEnvName: "PB_TOKEN_UNSET_X"}); err == nil {
		t.Fatal("empty token accepted")
	}
}
