package agentsetup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// testConnectParams は系統B の差し込み値。
//
// **testParams（系統A）と名前を分ける。** 同じパッケージで名前空間を共有するため、
// ファイルを分けても衝突する。
func testConnectParams() ConnectParams {
	return ConnectParams{
		ProjectKey:        "pb",
		ProjectName:       "Project Backyard",
		MCPURL:            "http://localhost:8081/mcp/pb",
		TokenEnvName:      "PB_TOKEN_MY_LAPTOP",
		DisplayName:       "私の Claude Code",
		ClientDisplayName: "Claude Code",
	}
}

// TestConnectSpecsCoverClients は2つのマップの鍵が一致することを確かめる。
//
// **系統A（specs）と系統B（connectSpecs）は別の関心だが、種別の集合は同じである。**
// 片方だけに種別が足されると、**画面は種別を出すのに接続設定が空になる**——
// 症状は「落とせない」で、サーバ側には何も出ない。
func TestConnectSpecsCoverClients(t *testing.T) {
	for _, kind := range SupportedClients() {
		if _, ok := connectSpecs[kind]; !ok {
			t.Errorf("%q は配置ファイルを出せる種別なのに、接続設定の仕様が無い", kind)
		}
	}
	for kind := range connectSpecs {
		if _, ok := specs[kind]; !ok {
			t.Errorf("%q に接続設定の仕様があるのに、配置ファイルの仕様が無い", kind)
		}
	}
}

// TestRenderConnectClaudeCode は .mcp.json を確かめる（Requirements.md 10.8.3）。
func TestRenderConnectClaudeCode(t *testing.T) {
	c, err := RenderConnect("claude_code", testConnectParams())
	if err != nil {
		t.Fatalf("RenderConnect が失敗した: %v", err)
	}

	if len(c.Files) != 1 {
		t.Fatalf("接続設定は1枚のはず: got %d 枚 %v", len(c.Files), pathsOf(c.Files))
	}
	f := c.Files[0]
	if f.Path != ".mcp.json" {
		t.Errorf("置き場: got %q, want %q", f.Path, ".mcp.json")
	}
	if f.Mode != ModeMerge {
		t.Errorf("mode: got %q, want %q（既存の mcpServers を丸ごと置き換えない。4.5.8.4）",
			f.Mode, ModeMerge)
	}
	if f.ClientKind != "claude_code" {
		t.Errorf("client_kind: got %q", f.ClientKind)
	}
	if !c.UsesTokenEnvVar {
		t.Error("Claude Code は環境変数を読む（export 行を出す）")
	}

	// **JSON として壊れていないこと。** 壊れた設定を配ると、症状は
	// クライアント側の「繋がらない」で、PB には何も出ない。
	var doc struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(f.Content), &doc); err != nil {
		t.Fatalf(".mcp.json が JSON として壊れている: %v\n%s", err, f.Content)
	}
	pb, ok := doc.MCPServers["pb"]
	if !ok {
		t.Fatalf("mcpServers に pb が無い: %s", f.Content)
	}
	if pb.Type != "http" {
		t.Errorf("type: got %q, want http（PB は SSE を持たない。Design.md 8.4）", pb.Type)
	}
	if pb.URL != "http://localhost:8081/mcp/pb" {
		t.Errorf("url: got %q", pb.URL)
	}
	// **${…} はクライアントが展開する。** ファイルに実体は残らない。
	if want := "Bearer ${PB_TOKEN_MY_LAPTOP}"; pb.Headers["Authorization"] != want {
		t.Errorf("Authorization: got %q, want %q", pb.Headers["Authorization"], want)
	}
}

// TestRenderConnectCopilot は .vscode/mcp.json を確かめる（Requirements.md 10.8.4）。
//
// **この形式だけ環境変数を使わない。**
func TestRenderConnectCopilot(t *testing.T) {
	p := testConnectParams()
	p.ClientDisplayName = "GitHub Copilot"
	c, err := RenderConnect("copilot", p)
	if err != nil {
		t.Fatalf("RenderConnect が失敗した: %v", err)
	}

	f := c.Files[0]
	if f.Path != ".vscode/mcp.json" {
		t.Errorf("置き場: got %q, want %q", f.Path, ".vscode/mcp.json")
	}
	if c.UsesTokenEnvVar {
		t.Error("Copilot は環境変数を読まない（export 行を出さない。ApiDesign.md 4.5.8.2）")
	}

	var doc struct {
		Inputs []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Password bool   `json:"password"`
		} `json:"inputs"`
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(f.Content), &doc); err != nil {
		t.Fatalf(".vscode/mcp.json が JSON として壊れている: %v\n%s", err, f.Content)
	}
	// **トップレベルは servers であって mcpServers ではない**（VS Code の書式）。
	if _, ok := doc.Servers["pb"]; !ok {
		t.Fatalf("servers に pb が無い: %s", f.Content)
	}
	if len(doc.Inputs) != 1 || doc.Inputs[0].ID != "pb-token" {
		t.Fatalf("inputs に pb-token が無い: %s", f.Content)
	}
	if !doc.Inputs[0].Password {
		t.Error("トークンの入力は password: true にする（画面に出さない）")
	}
	if want := "Bearer ${input:pb-token}"; doc.Servers["pb"].Headers["Authorization"] != want {
		t.Errorf("Authorization: got %q, want %q",
			doc.Servers["pb"].Headers["Authorization"], want)
	}
	// **環境変数名がファイルに漏れていないこと。** 意味を持たない値である。
	if strings.Contains(f.Content, "PB_TOKEN_MY_LAPTOP") {
		t.Errorf("Copilot の設定に環境変数名が入っている: %s", f.Content)
	}
}

// TestRenderConnectCodex は .codex/config.toml を確かめる（Requirements.md 10.8.4.1）。
//
// **ツールの許可が接続設定と同居する。** Claude Code が .claude/settings.json
// （コミットする）に書くものを、Codex はこのファイルに書く。
func TestRenderConnectCodex(t *testing.T) {
	p := testConnectParams()
	p.ClientDisplayName = "OpenAI Codex"
	c, err := RenderConnect("codex", p)
	if err != nil {
		t.Fatalf("RenderConnect が失敗した: %v", err)
	}

	f := c.Files[0]
	if f.Path != ".codex/config.toml" {
		t.Errorf("置き場: got %q, want %q", f.Path, ".codex/config.toml")
	}
	if f.Language != "toml" {
		t.Errorf("language: got %q, want toml", f.Language)
	}
	if !c.UsesTokenEnvVar {
		t.Error("Codex は bearer_token_env_var で環境変数を読む")
	}

	// **変数名そのものを書く**（${…} の展開ではない）。
	for _, want := range []string{
		`[mcp_servers.pb]`,
		`url = "http://localhost:8081/mcp/pb"`,
		`bearer_token_env_var = "PB_TOKEN_MY_LAPTOP"`,
		`default_tools_approval_mode = "prompt"`,
	} {
		if !strings.Contains(f.Content, want) {
			t.Errorf("%q が無い:\n%s", want, f.Content)
		}
	}
	if strings.Contains(f.Content, "${") {
		t.Errorf("Codex は ${…} を展開しない。変数名をそのまま書く:\n%s", f.Content)
	}

	// **読み取り7件だけが auto である。** enabled_tools は使わない
	// ——あれは一覧から消す絞り込みで、write 系のツールごと消してしまう。
	for _, tool := range autoApprovedTools {
		if !strings.Contains(f.Content, "[mcp_servers.pb.tools."+tool+"]") {
			t.Errorf("%s の approval_mode が無い:\n%s", tool, f.Content)
		}
	}
	if strings.Contains(f.Content, "enabled_tools") {
		t.Errorf("enabled_tools は使わない（write 系が一覧から消える）:\n%s", f.Content)
	}
	if got := strings.Count(f.Content, `approval_mode = "auto"`); got != len(autoApprovedTools) {
		t.Errorf(`approval_mode = "auto" の件数: got %d, want %d`, got, len(autoApprovedTools))
	}
}

// TestClaudeSettingsAndCodexShareTheList は、2つの書式が同じ一覧から出ることを確かめる。
//
// **同じ意図を2つの書式で表すので、元を1つにしておかないと片方だけ足して気づかない。**
func TestClaudeSettingsAndCodexShareTheList(t *testing.T) {
	settings := renderClaudeSettings()
	var doc struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(settings), &doc); err != nil {
		t.Fatalf(".claude/settings.json が JSON として壊れている: %v\n%s", err, settings)
	}
	if len(doc.Permissions.Allow) != len(autoApprovedTools) {
		t.Fatalf("許可の件数: got %d, want %d", len(doc.Permissions.Allow), len(autoApprovedTools))
	}
	for i, tool := range autoApprovedTools {
		if want := "mcp__pb__" + tool; doc.Permissions.Allow[i] != want {
			t.Errorf("allow[%d]: got %q, want %q", i, doc.Permissions.Allow[i], want)
		}
	}

	c, err := RenderConnect("codex", testConnectParams())
	if err != nil {
		t.Fatalf("RenderConnect が失敗した: %v", err)
	}
	for _, tool := range autoApprovedTools {
		if !strings.Contains(c.Files[0].Content, tool) {
			t.Errorf("Codex 側に %s が無い（一覧がずれている）", tool)
		}
	}
}

// TestRenderConnectWithoutTemplate は配置ファイルを持たない種別を確かめる
// （ApiDesign.md 4.5.8.3）。
//
// **エラーにしない。** エージェントは既に登録されており、URL も変数名も
// 正しく決まっている——**選び直しを促すために空にするのは、持ち主に対して乱暴である。**
func TestRenderConnectWithoutTemplate(t *testing.T) {
	p := testConnectParams()
	p.ClientDisplayName = "Gemini（CLI / Code Assist）"
	c, err := RenderConnect("gemini", p)
	if err != nil {
		t.Fatalf("配置ファイルを持たない種別で error になった: %v", err)
	}
	if len(c.Files) != 0 {
		t.Errorf("接続設定は出ないはず: got %v", pathsOf(c.Files))
	}
	if c.Readme == "" {
		t.Fatal("手引きは出す（自分で設定するための値を渡す）")
	}
	// **自分で書くのに要る値が全部そろっていること。**
	for _, want := range []string{
		"http://localhost:8081/mcp/pb",
		"PB_TOKEN_MY_LAPTOP",
		"Gemini（CLI / Code Assist）",
		"Project Backyard",
	} {
		if !strings.Contains(c.Readme, want) {
			t.Errorf("手引きに %q が無い", want)
		}
	}
}

// TestConnectReadmeIsPerKind は手引きが種別ごとに違うことを確かめる
// （利用者の判断、2026-09-06）。
//
// **置き場も、改名の要否も、.gitignore の扱いも種別で変わる。**
func TestConnectReadmeIsPerKind(t *testing.T) {
	cases := []struct {
		kind     string
		label    string
		wants    []string
		notWants []string
	}{
		{
			kind:  "claude_code",
			label: "Claude Code",
			// 改名の指示と、貼り先のキーが要る
			wants: []string{".mcp.pb-block.json", "`.mcp.json` へ改名", "mcpServers", "export PB_TOKEN_MY_LAPTOP"},
		},
		{
			kind:  "copilot",
			label: "GitHub Copilot",
			wants: []string{".vscode/mcp.pb-block.json", "servers", "エージェントモード"},
			// **環境変数の節を出さない**（10.8.4）。
			notWants: []string{"export PB_TOKEN_MY_LAPTOP"},
		},
		{
			kind:  "codex",
			label: "OpenAI Codex",
			wants: []string{".codex/config.pb-block.toml", "信頼済み", "export PB_TOKEN_MY_LAPTOP",
				"スラッシュコマンドがありません"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			p := testConnectParams()
			p.ClientDisplayName = tc.label
			c, err := RenderConnect(tc.kind, p)
			if err != nil {
				t.Fatalf("RenderConnect が失敗した: %v", err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(c.Readme, want) {
					t.Errorf("手引きに %q が無い", want)
				}
			}
			for _, no := range tc.notWants {
				if strings.Contains(c.Readme, no) {
					t.Errorf("手引きに %q が入っている", no)
				}
			}
			// **テンプレートの差し込み漏れが残っていないこと。**
			if strings.Contains(c.Readme, "{{") || strings.Contains(c.Readme, "<no value>") {
				t.Errorf("手引きに未差し込みが残っている:\n%s", c.Readme)
			}
			// **文が二重になっていないこと**（実出力を読んで見つけた。手順28b）。
			// onboardRef は種別ごとに品詞が違うので、Go 側で動詞を足すと
			// Claude Code だけ「… を実行します を実行します」になる。
			// **差し込みの前後が日本語として繋がるかは、置換の一致では測れない。**
			for _, dup := range []string{"を実行します を実行します", "を実行しますを実行します"} {
				if strings.Contains(c.Readme, dup) {
					t.Errorf("手引きの文が二重になっている（%q）:\n%s", dup, c.Readme)
				}
			}
			// **平文のトークンを手引きに書かない**（Requirements.md 10.10.1）。
			if strings.Contains(c.Readme, "pb_agt_") {
				t.Errorf("手引きにトークンらしき文字列がある:\n%s", c.Readme)
			}
		})
	}
}

// TestConnectZip は zip の中の名前を確かめる（ApiDesign.md 4.5.8.5）。
//
// **接続設定は改名され、手引きは実名のまま入る**——読むものはすぐ読め、
// 置くものは一手間かかる、が狙いどおりの形である。
func TestConnectZip(t *testing.T) {
	c, err := RenderConnect("claude_code", testConnectParams())
	if err != nil {
		t.Fatalf("RenderConnect が失敗した: %v", err)
	}
	blob, err := ConnectZip(c)
	if err != nil {
		t.Fatalf("ConnectZip が失敗した: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("zip を読めない: %v", err)
	}

	got := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		got = append(got, f.Name)
	}
	want := []string{"PB-README.md", ".mcp.pb-block.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("zip の中身: got %v, want %v", got, want)
	}
	// **README.md にしない。** プロジェクト直下で展開されたときに本物を消す。
	for _, name := range got {
		if name == "README.md" || name == ".mcp.json" {
			t.Errorf("%q は既存を壊しうる名前である", name)
		}
	}
}

// TestConnectZipWithoutTemplate は、接続設定が無くても手引きだけの zip を返すことを確かめる。
func TestConnectZipWithoutTemplate(t *testing.T) {
	c, err := RenderConnect("other", testConnectParams())
	if err != nil {
		t.Fatalf("RenderConnect が失敗した: %v", err)
	}
	blob, err := ConnectZip(c)
	if err != nil {
		t.Fatalf("ConnectZip が失敗した: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("zip を読めない: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != ReadmeName {
		t.Fatalf("手引きだけの zip になっていない: %d 件", len(zr.File))
	}
}

// TestExportLineHasNoPlaintext は export 行に平文が入らないことを確かめる
// （Requirements.md 10.10.1）。
func TestExportLineHasNoPlaintext(t *testing.T) {
	line := ExportLine("PB_TOKEN_MY_LAPTOP")
	if !strings.HasPrefix(line, "export PB_TOKEN_MY_LAPTOP=") {
		t.Errorf("export 行: got %q", line)
	}
	if strings.Contains(line, "pb_agt_") {
		t.Errorf("export 行にトークンらしき文字列がある: %q", line)
	}
}
