package agentsetup

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderConnectClaudeHoldHooks は承認待ちのフック（DbDesign.md 6.34）を確かめる。
func TestRenderConnectClaudeHoldHooks(t *testing.T) {
	p := testConnectParams()
	p.WorkHoldURL = "http://localhost:8081/api/v1/projects/pb/work-hold"
	c, err := RenderConnect("claude_code", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 2 || c.Files[1].Path != ".claude/settings.local.json" || c.Files[1].Mode != ModeMerge {
		t.Fatalf("files: %v", pathsOf(c.Files))
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Type           string            `json:"type"`
				URL            string            `json:"url"`
				Headers        map[string]string `json:"headers"`
				AllowedEnvVars []string          `json:"allowedEnvVars"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(c.Files[1].Content), &doc); err != nil {
		t.Fatalf("JSON として壊れている: %v\n%s", err, c.Files[1].Content)
	}
	for _, event := range []string{"PermissionRequest", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit"} {
		groups := doc.Hooks[event]
		if len(groups) != 1 || groups[0].Matcher != nil || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", event, groups)
		}
		h := groups[0].Hooks[0]
		// **展開できるのは allowedEnvVars に挙げた変数だけ**（Claude Code の HTTP フック）。
		if h.Type != "http" || h.URL != p.WorkHoldURL || h.Headers["Authorization"] != "Bearer ${PB_TOKEN_MY_LAPTOP}" ||
			len(h.AllowedEnvVars) != 1 || h.AllowedEnvVars[0] != "PB_TOKEN_MY_LAPTOP" {
			t.Fatalf("%s handler: %+v", event, h)
		}
	}
	if len(doc.Hooks) != 4 {
		t.Fatalf("events: %v", doc.Hooks)
	}
	if !strings.Contains(c.Readme, "settings.local.pb-block.json") {
		t.Error("手引きにフックの置き方が無い")
	}
}

// TestRenderConnectCodexHoldHooks は、pb-wait を同梱したときだけ Codex のフックを書くことを確かめる。
func TestRenderConnectCodexHoldHooks(t *testing.T) {
	p := testConnectParams()
	p.WorkHoldURL = "http://localhost:8081/api/v1/projects/pb/work-hold"
	without, err := RenderConnect("codex", p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without.Files[0].Content, "[[hooks.") || len(without.Files) != 1 {
		t.Fatalf("pb-wait を同梱しないのにフックを書いた:\n%s", without.Files[0].Content)
	}
	p.BridgeOS, p.BridgeArch = "darwin", "arm64"
	with, err := RenderConnect("codex", p)
	if err != nil {
		t.Fatal(err)
	}
	content := with.Files[0].Content
	cmd := `command = "pb-wait hook --hold-url http://localhost:8081/api/v1/projects/pb/work-hold --token-env PB_TOKEN_MY_LAPTOP"`
	for _, event := range []string{"PermissionRequest", "PostToolUse", "UserPromptSubmit"} {
		if !strings.Contains(content, "[[hooks."+event+"]]\n\n[[hooks."+event+".hooks]]\ntype = \"command\"\n"+cmd) {
			t.Fatalf("%s のフックが無い:\n%s", event, content)
		}
	}
	// Codex には PostToolUseFailure が無い。
	if strings.Contains(content, "PostToolUseFailure") || strings.Count(content, cmd) != 3 {
		t.Fatalf("フックの数:\n%s", content)
	}
	if !strings.Contains(with.Readme, "/hooks") {
		t.Error("手引きにフックの信頼の手順が無い")
	}
}
