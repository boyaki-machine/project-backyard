package mcp

import (
	"encoding/json"
	"testing"
)

// 自律実行が無効なら、そのツールを tools/list に出さない（Delegation.md 1.3。pb-353）。
func TestToolsListHidesDelegationToolsWhenDisabled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{{"無効", false}, {"有効", true}} {
		t.Run(tc.name, func(t *testing.T) {
			enabled := tc.enabled
			h := New(&fakeREST{}, "v0").WithDelegationGate(func() bool { return enabled })
			res := decodeRPC(t, callMCP(t, h, agentPrincipal(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			if res.Error != nil {
				t.Fatalf("tools/list が失敗した: %+v", res.Error)
			}
			b, _ := json.Marshal(res.Result)
			var out struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatalf("応答を読めない: %v", err)
			}
			got := map[string]bool{}
			for _, tl := range out.Tools {
				got[tl.Name] = true
			}
			for name := range delegationToolNames {
				if got[name] != tc.enabled {
					t.Errorf("%s が出ている = %v, want %v", name, got[name], tc.enabled)
				}
			}
			// 自律実行に関わらないツールは常に出る。
			for _, name := range []string{"pb_get_task", "pb_post_note", "pb_submit_result", "pb_wait"} {
				if !got[name] {
					t.Errorf("%s が出ていない", name)
				}
			}
		})
	}
}
