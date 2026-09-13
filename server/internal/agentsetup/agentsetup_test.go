package agentsetup

import (
	"archive/zip"
	"bytes"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// testParams は各テストで使う差し込み値。
func testParams() Params {
	return Params{ProjectKey: "pb", ProjectName: "Project Backyard", BaseURL: "http://localhost:8081"}
}

// pathsOf は File のパスだけを取り出す（比較を読みやすくするため）。
func pathsOf(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// findFile は path で1件引く。無ければテストを落とす。
func findFile(t *testing.T, files []File, p string) File {
	t.Helper()
	for _, f := range files {
		if f.Path == p {
			return f
		}
	}
	t.Fatalf("%q が生成されていない。生成されたのは %v", p, pathsOf(files))
	return File{}
}

// TestRenderClaudeCode は Claude Code の一式を確かめる（Requirements.md 10.8.2）。
func TestRenderClaudeCode(t *testing.T) {
	files, err := Render([]string{"claude_code"}, testParams())
	if err != nil {
		t.Fatalf("Render が失敗した: %v", err)
	}

	want := []string{
		".claude/commands/pb-onboard.md",
		".claude/commands/pb-implement.md",
		".claude/commands/pb-refine.md",
		".claude/settings.json",
		"CLAUDE.md",
		".gitignore",
	}
	got := pathsOf(files)
	if len(got) != len(want) {
		t.Fatalf("枚数が違う: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("並びが違う[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	// **接続設定は出さない**（ApiDesign.md 5.7。履歴管理の対象外なので置き場がない）。
	for _, f := range files {
		if f.Path == ".mcp.json" {
			t.Error(".mcp.json が生成されている。系統A は接続設定を出さない")
		}
	}

	onboard := findFile(t, files, ".claude/commands/pb-onboard.md")
	if onboard.Mode != ModeCreate {
		t.Errorf("pb-onboard.md の mode: got %q, want %q", onboard.Mode, ModeCreate)
	}
	if !strings.HasPrefix(onboard.Content, "---\ndescription: ") {
		t.Errorf("フロントマターで始まっていない:\n%s", firstLines(onboard.Content, 3))
	}
	if !strings.Contains(onboard.Content, "allowed-tools: mcp__pb__pb_get_project") {
		t.Error("pb-onboard に read 系の allowed-tools が入っていない（10.8.5）")
	}
	// **版番号は手順ファイルの先頭に埋まっていなければならない**（10.9.3）。
	if !strings.Contains(onboard.Content, "<!-- pb-workflow-version: "+strconv.Itoa(WorkflowVersion)+" -->") {
		t.Error("版番号が埋まっていない")
	}

	impl := findFile(t, files, ".claude/commands/pb-implement.md")
	if !strings.Contains(impl.Content, "チケット #$1 の実装") {
		t.Error("Claude Code の pb-implement が引数記法 #$1 を使っていない")
	}
	if !strings.Contains(impl.Content, "argument-hint: <ticket-id>") {
		t.Error("argument-hint が入っていない")
	}
	// **存在しないツールを書かない**（Requirements.md 10.8.6 を実装に合わせて改訂した）。
	if strings.Contains(impl.Content, "pb_propose_subtasks") {
		t.Error("Phase 3 のツール pb_propose_subtasks を手順ファイルに書いている")
	}
	// **実装値と綴りを合わせる**（0006 の CHECK は human_only / red）。
	if strings.Contains(impl.Content, "human-only") {
		t.Error("execution_mode の綴りが実装（human_only）と違う")
	}

	settings := findFile(t, files, ".claude/settings.json")
	if settings.Mode != ModeMerge {
		t.Errorf(".claude/settings.json の mode: got %q, want %q（丸ごと置き換えると既存の許可が消える）",
			settings.Mode, ModeMerge)
	}

	block := findFile(t, files, "CLAUDE.md")
	if block.Mode != ModeAppend {
		t.Errorf("CLAUDE.md の mode: got %q, want %q", block.Mode, ModeAppend)
	}
	if block.MarkerBegin == "" || block.MarkerEnd == "" {
		t.Error("追記のファイルにマーカーが入っていない（Requirements.md 10.8.8）")
	}
	if !strings.HasPrefix(block.Content, block.MarkerBegin) {
		t.Errorf("本文が marker_begin で始まっていない:\n%s", firstLines(block.Content, 2))
	}
	if !strings.Contains(block.Content, block.MarkerEnd) {
		t.Error("本文に marker_end が無い")
	}
	// **利用者の要望（2026-09-06）**：貼る前・貼った後のチェックを生成物に埋める。
	if !strings.Contains(block.Content, "git pull") || !strings.Contains(block.Content, "git diff") {
		t.Error("常時コンテキストに反映時のチェック（git pull / git diff）が無い")
	}
	if !strings.Contains(block.Content, "プロジェクトキー: `pb`") {
		t.Error("プロジェクトキーが差し込まれていない")
	}
}

// TestRenderAllClients は3種別を同時に選んだときの置き場を確かめる。
//
// **3種別とも置き場が違う**（DbDesign.md 8.2.1.1）。ここが崩れると、
// 同じ人が複数のクライアントを使うときに渡すものが間違う。
func TestRenderAllClients(t *testing.T) {
	files, err := Render([]string{"claude_code", "copilot", "codex"}, testParams())
	if err != nil {
		t.Fatalf("Render が失敗した: %v", err)
	}
	want := map[string]string{
		".claude/commands/pb-onboard.md":         "claude_code",
		".claude/commands/pb-implement.md":       "claude_code",
		".claude/commands/pb-refine.md":          "claude_code",
		".claude/settings.json":                  "claude_code",
		"CLAUDE.md":                              "claude_code",
		".github/prompts/pb-onboard.prompt.md":   "copilot",
		".github/prompts/pb-implement.prompt.md": "copilot",
		".github/copilot-instructions.md":        "copilot",
		".agents/skills/pb-onboard/SKILL.md":     "codex",
		".agents/skills/pb-implement/SKILL.md":   "codex",
		"AGENTS.md":                              "codex",
		".gitignore":                             "",
	}
	if len(files) != len(want) {
		t.Fatalf("枚数が違う: got %d (%v), want %d", len(files), pathsOf(files), len(want))
	}
	for _, f := range files {
		kind, ok := want[f.Path]
		if !ok {
			t.Errorf("想定外のファイル %q", f.Path)
			continue
		}
		if f.ClientKind != kind {
			t.Errorf("%s の client_kind: got %q, want %q", f.Path, f.ClientKind, kind)
		}
	}

	// **.gitignore は選んだ種別ぶんの接続設定を並べる**（Requirements.md 10.8.1）。
	ig := findFile(t, files, ".gitignore")
	for _, p := range []string{".mcp.json", ".vscode/mcp.json", ".codex/config.toml", ".envrc"} {
		if !strings.Contains(ig.Content, p) {
			t.Errorf(".gitignore に %q が無い", p)
		}
	}
	if !strings.Contains(ig.Content, "各自の環境") {
		t.Error(".gitignore に「各自の環境。共有しない」の説明が無い（利用者の指摘）")
	}

	// **Codex にはスラッシュコマンドが無い**（Requirements.md 10.8.2）ので、
	// 引数記法を書いてはならない。
	skill := findFile(t, files, ".agents/skills/pb-implement/SKILL.md")
	if strings.Contains(skill.Content, "$1") {
		t.Error("Codex のスキルに Claude Code の引数記法 $1 が入っている")
	}
	if !strings.HasPrefix(skill.Content, "---\nname: pb-implement\n") {
		t.Errorf("SKILL.md のフロントマターに name が無い:\n%s", firstLines(skill.Content, 3))
	}

	prompt := findFile(t, files, ".github/prompts/pb-onboard.prompt.md")
	if !strings.HasPrefix(prompt.Content, "---\nmode: agent\n") {
		t.Errorf("Copilot のプロンプトに mode: agent が無い:\n%s", firstLines(prompt.Content, 3))
	}
}

// TestRenderBodyIsShared は手順の本文が3種別で同一であることを確かめる。
//
// **10.8.5 が「同一内容とし、フロントマターのみ各クライアントの形式に合わせる」と
// 定めている。** 本文が分岐し始めたら、写しが3つに増えたということである。
func TestRenderBodyIsShared(t *testing.T) {
	files, err := Render([]string{"claude_code", "copilot", "codex"}, testParams())
	if err != nil {
		t.Fatalf("Render が失敗した: %v", err)
	}
	bodies := map[string]string{}
	for _, p := range []string{
		".claude/commands/pb-onboard.md",
		".github/prompts/pb-onboard.prompt.md",
		".agents/skills/pb-onboard/SKILL.md",
	} {
		f := findFile(t, files, p)
		bodies[p] = afterFrontMatter(f.Content)
	}
	// pb-onboard の本文は ImplementRef だけがクライアントで変わる。
	// **その1行を落として比べる**——それ以外が違ったら本文が分岐している。
	var prev, prevPath string
	for p, b := range bodies {
		norm := dropLinesContaining(b, "チケットに着手するときは")
		if prev != "" && norm != prev {
			t.Errorf("pb-onboard の本文が %s と %s で違う（10.8.5 は同一内容と定める）", prevPath, p)
		}
		prev, prevPath = norm, p
	}
}

// TestRenderRejectsUnknownClient は未知・テンプレート無しの種別を弾く。
//
// **選んだ先に何も出ない選択肢を通さない**（DbDesign.md 8.2.1.1）。
func TestRenderRejectsUnknownClient(t *testing.T) {
	for _, kind := range []string{"gemini", "other", "", "claude-code"} {
		if _, err := Render([]string{kind}, testParams()); err == nil {
			t.Errorf("種別 %q が通ってしまった", kind)
		}
	}
	if _, err := Render(nil, testParams()); err == nil {
		t.Error("クライアント0件が通ってしまった")
	}
}

// TestSupportedClientsMatchesMigration は Go の specs と 0023 の
// has_setup_template を突き合わせる。
//
// **「テンプレートを持っている」と DB が名乗ったのに Render が出せない、という
// 食い違いを捕まえる。** 片方だけ足したときに 422 ではなく 500 になるので、
// 気づける場所をここに置く。
func TestSupportedClientsMatchesMigration(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0023_agent_setup.sql")
	if err != nil {
		t.Fatalf("マイグレーションを読めない: %v", err)
	}
	re := regexp.MustCompile(`(?s)SET has_setup_template = true\s+WHERE key IN \(([^)]*)\)`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatal("0023 に has_setup_template を立てる UPDATE が見つからない")
	}
	var fromSQL []string
	for _, part := range strings.Split(string(m[1]), ",") {
		fromSQL = append(fromSQL, strings.Trim(strings.TrimSpace(part), "'"))
	}
	sort.Strings(fromSQL)

	fromGo := SupportedClients()
	if strings.Join(fromSQL, ",") != strings.Join(fromGo, ",") {
		t.Errorf("0023 と specs が食い違う:\n  SQL: %v\n  Go : %v", fromSQL, fromGo)
	}
}

// TestZipBlockNames は zip の中の名前を確かめる（ApiDesign.md 5.7.2）。
//
// **展開した瞬間に既存の CLAUDE.md を消す zip を配らない。**
func TestZipBlockNames(t *testing.T) {
	files, err := Render([]string{"claude_code"}, testParams())
	if err != nil {
		t.Fatalf("Render が失敗した: %v", err)
	}
	blob, err := Zip(files)
	if err != nil {
		t.Fatalf("Zip が失敗した: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("zip を開けない: %v", err)
	}
	got := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		got = append(got, f.Name)
	}
	sort.Strings(got)
	want := []string{
		".claude/commands/pb-implement.md",
		".claude/commands/pb-onboard.md",
		".claude/commands/pb-refine.md",
		".claude/settings.pb-block.json",
		".gitignore.pb-block",
		"CLAUDE.pb-block.md",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("zip の中身が違う:\n got %v\nwant %v", got, want)
	}
}

// ── テスト用の小さな道具 ──────────────────────────────────

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// afterFrontMatter は先頭の --- ブロックと版番号の行を落とす。
func afterFrontMatter(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return s
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return dropLinesContaining(strings.Join(lines[i+1:], "\n"), "pb-workflow-version")
		}
	}
	return s
}

func dropLinesContaining(s, needle string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
