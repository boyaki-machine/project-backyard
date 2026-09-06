// Package agentsetup はリポジトリに置く配置ファイルを組み立てる
// （Requirements.md 10.8、ApiDesign.md 5.7、手順28a）。
//
// **生成するのはコミットする層だけである。** 三層分離（10.8.1）のうち、
// 手順（コマンド・スキル）と常時コンテキスト（マーカーブロック）と .gitignore を出す。
// **接続設定（.mcp.json / .vscode/mcp.json / .codex/config.toml）は出さない**
// ——履歴管理の対象外なので、リポジトリに置くものを作るこの経路には置き場がない。
// あれは各人が /me/agents から受け取る（10.9.1 の系統B。手順28b）。
//
// **手順の本文は1つしか持たない。** 10.8.5 が「同一内容とし、フロントマターのみ
// 各クライアントの形式に合わせる」と定めている。**写しを3つ置けば必ずずれる**ので、
// templates/body/ に本文を1枚だけ置き、フロントマターだけを Go 側で組み立てる。
package agentsetup

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
	"text/template"
)

// WorkflowVersion は生成する手順ファイルに埋める版番号（Requirements.md 10.9.3）。
//
// **Phase 2 では常に 1 である。** 埋めるところまでが手順28a の範囲で、
// 「PB 側が古いと判断して警告を返す」経路はまだ無い（10.8.8）。**種を蒔いておかないと、
// 後から突き合わせる材料が作れない**ため、値だけ先に置く。
//
// **手順ファイルの本文を変えたら、ここを上げる。**
const WorkflowVersion = 1

//go:embed templates/body/*.md
var bodyFS embed.FS

// Mode は置き方（ApiDesign.md 5.7.1）。
//
// **これが無いと、画面が「上書きしてよいファイル」と「壊してはいけないファイル」を
// 同じ見た目で並べる。** 追記であることは受け渡しの形に現れていなければならない。
type Mode string

const (
	// ModeCreate はそのまま置く。
	ModeCreate Mode = "create"
	// ModeAppend は既存の末尾へ追記する（マーカーの内側だけが PB の管理範囲）。
	ModeAppend Mode = "append"
	// ModeMerge は既存の構造へ該当キーだけを足す。
	//
	// **JSON は追記できないので append と分けている。** このリポジトリの
	// .claude/settings.json が130行あり、丸ごと置き換えると Bash の許可設定が
	// 全部消える——**実装中に自分のリポジトリで踏んだ**ので、モードを1つ足した。
	ModeMerge Mode = "merge"
)

// File は生成した1枚（ApiDesign.md 5.7.1 の files[]）。
type File struct {
	Path string `json:"path"`
	// ClientKind はどのクライアント向けか。**共通のもの（.gitignore）は空**。
	ClientKind  string `json:"client_kind"`
	Mode        Mode   `json:"mode"`
	Language    string `json:"language"`
	MarkerBegin string `json:"marker_begin,omitempty"`
	MarkerEnd   string `json:"marker_end,omitempty"`
	Content     string `json:"content"`
}

// Params は差し込む値。
type Params struct {
	ProjectKey  string
	ProjectName string
	// BaseURL は PB の公開 URL（ApiDesign.md 5.7.1）。**接続設定を出さない
	// 28a では使わないが、画面が「エージェントはこの URL へ接続します」と
	// 出すために応答へ載せる。**
	BaseURL string
}

// markerBegin / markerEnd は常時コンテキストのマーカー（Requirements.md 10.8.8）。
//
// **PB による再生成で手書き部分を破壊しないための境界である。**
var (
	markerBegin = fmt.Sprintf("<!-- PB:BEGIN v%d", WorkflowVersion)
	markerEnd   = "<!-- PB:END -->"
)

// command は配る手順1つ分。
type command struct {
	name string
	// description は各クライアントのフロントマターに入る。
	//
	// **Codex ではこれが起動条件そのものになる**（スキルは description による
	// 誘発で立ち上がる。Requirements.md 10.8.2）ので、「いつ使うか」を書く。
	description string
	// argumentHint はチケット番号を取るコマンドにだけ入る（Claude Code）。
	argumentHint string
	// allowedTools は Claude Code のフロントマター。読み取りだけのコマンドを
	// 絞っておくと、確認を挟まず一息に走る（10.8.5）。
	allowedTools string
}

var commands = map[string]command{
	"pb-onboard": {
		name:         "pb-onboard",
		description:  "PB のプロジェクトに参画する（規約と担当を読む）。参画した直後、および間が空いたときに使う",
		allowedTools: "mcp__pb__pb_get_project, mcp__pb__pb_list_docs, mcp__pb__pb_get_doc, mcp__pb__pb_list_tasks",
	},
	"pb-implement": {
		name:         "pb-implement",
		description:  "PB のチケットを実装する。チケット番号を指定して着手するときに使う",
		argumentHint: "<ticket-id>",
		allowedTools: "mcp__pb__*, Bash(git *), Read, Edit, Write",
	},
	"pb-refine": {
		name:         "pb-refine",
		description:  "PB のチケット記述を、エージェントが自律実行できる水準まで引き上げる",
		argumentHint: "<ticket-id>",
	},
}

// clientSpec はクライアント種別ごとの置き場と書式（DbDesign.md 8.2.1.1）。
//
// **値が決めるのは設定ファイルの置き場である。** エディタではない——同じ VS Code でも
// Claude 拡張と GitHub Copilot で分かれる。
type clientSpec struct {
	kind string
	// commands は配る手順の名前。**pb-refine は Claude Code だけ**
	// （10.8.2 が他の2種別の行を挙げていない。補助コマンドなので、入口が
	// 揃っていなくても /pb-onboard と /pb-implement の通しは壊れない）。
	commands []string
	// commandPath は手順ファイルの置き場を返す。
	commandPath func(name string) string
	// frontMatter はフロントマターを返す（末尾に改行を含む）。
	frontMatter func(c command) string
	// contextPath は常時コンテキストの貼り先。
	contextPath string
	// ticketRef は本文の「チケット {{.TicketRef}}」に入る。
	//
	// **Claude Code だけが引数の記法を持つ。** Copilot と Codex には
	// リポジトリにコミットできる引数付きスラッシュコマンドが無いので、
	// **推測で記法を書かず、日本語で指す**（実機で確かめられていないため）。
	ticketRef string
	// onboardRef / implementRef は文中でコマンドを指す書き方。
	onboardRef   string
	implementRef string
	// ignorePaths は .gitignore に足す接続設定のパス。
	ignorePaths []string
}

var specs = map[string]clientSpec{
	"claude_code": {
		kind:        "claude_code",
		commands:    []string{"pb-onboard", "pb-implement", "pb-refine"},
		commandPath: func(n string) string { return ".claude/commands/" + n + ".md" },
		frontMatter: func(c command) string {
			var b strings.Builder
			b.WriteString("---\n")
			fmt.Fprintf(&b, "description: %s\n", c.description)
			if c.argumentHint != "" {
				fmt.Fprintf(&b, "argument-hint: %s\n", c.argumentHint)
			}
			if c.allowedTools != "" {
				fmt.Fprintf(&b, "allowed-tools: %s\n", c.allowedTools)
			}
			b.WriteString("---\n")
			return b.String()
		},
		contextPath:  "CLAUDE.md",
		ticketRef:    "#$1",
		onboardRef:   "`/pb-onboard`",
		implementRef: "`/pb-implement <id>`",
		ignorePaths:  []string{".mcp.json"},
	},
	"copilot": {
		kind:        "copilot",
		commands:    []string{"pb-onboard", "pb-implement"},
		commandPath: func(n string) string { return ".github/prompts/" + n + ".prompt.md" },
		frontMatter: func(c command) string {
			return fmt.Sprintf("---\nmode: agent\ndescription: %s\n---\n", c.description)
		},
		contextPath:  ".github/copilot-instructions.md",
		ticketRef:    "（利用者が指定した番号）",
		onboardRef:   "`pb-onboard` プロンプト",
		implementRef: "`pb-implement` プロンプト",
		ignorePaths:  []string{".vscode/mcp.json"},
	},
	"codex": {
		kind:        "codex",
		commands:    []string{"pb-onboard", "pb-implement"},
		commandPath: func(n string) string { return ".agents/skills/" + n + "/SKILL.md" },
		frontMatter: func(c command) string {
			return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n", c.name, c.description)
		},
		contextPath: "AGENTS.md",
		ticketRef:   "（利用者が指定した番号）",
		// **Codex にはスラッシュコマンドが無い**（Requirements.md 10.8.2）。
		// リポジトリにコミットできるのはスキルで、起動は description による誘発である。
		onboardRef:   "「PB に参画して」と伝える（`pb-onboard` スキルが立ち上がる）",
		implementRef: "「PB のチケット <番号> を実装して」と伝える（`pb-implement` スキルが立ち上がる）",
		ignorePaths:  []string{".codex/config.toml"},
	},
}

// claudeSettings は .claude/settings.json へ足す許可（10.8.2）。
//
// **read 系だけを自動承認にする。** /pb-onboard は読み取りしかしないので、
// これを入れておくと確認を挟まず一息に走る（10.8.5）。**write 系は入れない**
// ——起票・遷移・文書更新は、利用者が1回ずつ見て通す。
const claudeSettings = `{
  "permissions": {
    "allow": [
      "mcp__pb__pb_get_project",
      "mcp__pb__pb_list_docs",
      "mcp__pb__pb_get_doc",
      "mcp__pb__pb_list_tasks",
      "mcp__pb__pb_get_task",
      "mcp__pb__pb_get_context",
      "mcp__pb__pb_list_transitions"
    ]
  }
}
`

// SupportedClients は配置ファイルを出せる種別を返す（昇順）。
//
// **DBの has_setup_template と一致していなければならない**（DbDesign.md 8.2.1.1）。
// 一致は agentsetup_test.go が 0023 のマイグレーションと突き合わせて確かめる
// ——**「テンプレートを持っていると名乗ったのに出ない」を、テストで捕まえる。**
func SupportedClients() []string {
	out := make([]string, 0, len(specs))
	for k := range specs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// bodyParams は本文テンプレートへ渡す値。
type bodyParams struct {
	ProjectKey      string
	ProjectName     string
	BaseURL         string
	WorkflowVersion int
	TicketRef       string
	OnboardRef      string
	ImplementRef    string
}

// Render は選ばれた種別ぶんの配置ファイルを組み立てる（ApiDesign.md 5.7.1）。
//
// **並びは「種別ごとに、手順 → 常時コンテキスト」、最後に共通の .gitignore。**
// 画面はこの順でそのまま並べる（並べ替えを画面に持たせない）。
//
// 未知の種別、または配置ファイルを持たない種別は error にする。**選んだ先に
// 何も出ない選択肢を通さない。**
func Render(clients []string, p Params) ([]File, error) {
	if len(clients) == 0 {
		return nil, fmt.Errorf("クライアントが1件も指定されていない")
	}

	files := make([]File, 0, len(clients)*4+1)
	ignore := make([]string, 0, len(clients)+1)

	for _, kind := range clients {
		spec, ok := specs[kind]
		if !ok {
			return nil, fmt.Errorf("配置ファイルを持たないクライアント種別: %q", kind)
		}

		bp := bodyParams{
			ProjectKey:      p.ProjectKey,
			ProjectName:     p.ProjectName,
			BaseURL:         p.BaseURL,
			WorkflowVersion: WorkflowVersion,
			TicketRef:       spec.ticketRef,
			OnboardRef:      spec.onboardRef,
			ImplementRef:    spec.implementRef,
		}

		for _, name := range spec.commands {
			c := commands[name]
			body, err := renderBody(name+".md", bp)
			if err != nil {
				return nil, err
			}
			// **版番号はフロントマターの直後に置く**（Requirements.md 10.9.3）。
			// 手順ファイルの先頭にあることが陳腐化検出の前提である。
			content := spec.frontMatter(c) +
				fmt.Sprintf("\n<!-- pb-workflow-version: %d -->\n\n", WorkflowVersion) +
				body
			files = append(files, File{
				Path:       spec.commandPath(name),
				ClientKind: kind,
				Mode:       ModeCreate,
				Language:   "markdown",
				Content:    content,
			})
		}

		if kind == "claude_code" {
			files = append(files, File{
				Path:       ".claude/settings.json",
				ClientKind: kind,
				Mode:       ModeMerge,
				Language:   "json",
				Content:    claudeSettings,
			})
		}

		block, err := renderBody("context-block.md", bp)
		if err != nil {
			return nil, err
		}
		files = append(files, File{
			Path:        spec.contextPath,
			ClientKind:  kind,
			Mode:        ModeAppend,
			Language:    "markdown",
			MarkerBegin: markerBegin,
			MarkerEnd:   markerEnd,
			Content:     block,
		})

		ignore = append(ignore, spec.ignorePaths...)
	}

	files = append(files, File{
		Path:       ".gitignore",
		ClientKind: "",
		Mode:       ModeAppend,
		Language:   "text",
		Content:    renderGitignore(ignore),
	})
	return files, nil
}

// renderBody は templates/body/<name> を差し込む。
func renderBody(name string, bp bodyParams) (string, error) {
	raw, err := bodyFS.ReadFile(path.Join("templates/body", name))
	if err != nil {
		return "", fmt.Errorf("テンプレート %s を読めない: %w", name, err)
	}
	// **html/template を使わない。** JSON と Markdown をエスケープされると壊れる。
	// 差し込む値はプロジェクトキー（DBの CHECK で `^[a-z0-9][a-z0-9-]{1,19}$`）と
	// PB が持つ定数だけで、利用者の自由入力は入らない。
	tmpl, err := template.New(name).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("テンプレート %s を解釈できない: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, bp); err != nil {
		return "", fmt.Errorf("テンプレート %s を組み立てられない: %w", name, err)
	}
	return buf.String(), nil
}

// renderGitignore は .gitignore への追記を組み立てる（Requirements.md 10.8.1）。
//
// **「これは各自の環境。共有しない」を必ず添える。** 経験の浅い参加者が
// 「自分のエージェントに関するファイルがコミットされる」と読むこと自体が事故のもとで、
// **どちらがどちらかを .gitignore そのものに書いておく**のが手当てである（利用者の指摘）。
func renderGitignore(paths []string) string {
	var b strings.Builder
	b.WriteString("# Project Backyard — 接続設定は各自の環境。共有しない\n")
	b.WriteString("# （ネットワーク事情で書き換えて使うため、コミットすると上書き合戦になる）\n")
	for _, p := range paths {
		b.WriteString(p + "\n")
	}
	b.WriteString("\n# トークンを置く direnv のファイル。実体が入るので必ず除外する\n")
	b.WriteString(".envrc\n")
	return b.String()
}

// Zip は Render の結果を zip にまとめる（ApiDesign.md 5.7.2）。
//
// **mode が create でないものは別名で入れる。** 展開した瞬間に既存の CLAUDE.md を
// 消す zip を配らない——`CLAUDE.md.pb-block.md` のように接尾を付けて、
// 人が中身を見てから貼れるようにする。
func Zip(files []File) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		name := f.Path
		if f.Mode != ModeCreate {
			name = blockName(f.Path)
		}
		e, err := w.Create(name)
		if err != nil {
			return nil, fmt.Errorf("zip に %s を作れない: %w", name, err)
		}
		if _, err := e.Write([]byte(f.Content)); err != nil {
			return nil, fmt.Errorf("zip へ %s を書けない: %w", name, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("zip を閉じられない: %w", err)
	}
	return buf.Bytes(), nil
}

// blockName は追記・マージ用の別名を作る（CLAUDE.md → CLAUDE.pb-block.md）。
//
// **path.Ext をそのまま使えない。** ドットで始まる名前（.gitignore）では
// Ext が ".gitignore" を返すので、素朴に足すと ".gitignore.pb-block.gitignore"
// になる。**最後のドットが先頭にあるときは拡張子ではない**ので分けない。
func blockName(p string) string {
	base := path.Base(p)
	if i := strings.LastIndex(base, "."); i > 0 {
		dir := p[:len(p)-len(base)]
		return dir + base[:i] + ".pb-block" + base[i:]
	}
	return p + ".pb-block"
}
