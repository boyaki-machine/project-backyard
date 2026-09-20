// 系統B——各人が自分の端末へ置く接続設定（Requirements.md 10.9.1、
// ApiDesign.md 4.5.8、手順28b）。
//
// **同じパッケージに置くのは、系統A と同じ clientSpec の知識を使うためである**
// ——どのクライアントが何と呼ばれ、参画をどう起動するか（onboardRef）は両方で要る。
// **ただし specs には足さない。** あちらは「リポジトリにコミットするもの」の仕様で、
// こちらは「手元にしか残らないもの」の仕様である。**混ぜると、Git に入る／入らないの
// 境目が構造から消える**——それは 10.8.1 が三層に分けた理由そのものだった。
//
// **鍵のずれはテストが確かめる。** 系統A を持つ種別は必ず系統B も持ち
// （TestConnectSpecsCoverClients）、系統B の鍵はカタログに在る
// （TestConnectSpecsAreInCatalog）。**逆は成り立たない**——claude_desktop は
// 接続設定だけを持つ（pb-58。作業フォルダが無いのでコミットする先が無い）。
package agentsetup

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"text/template"
)

//go:embed templates/connect/*.md
var connectFS embed.FS

// autoApprovedTools は確認なしで走らせる読み取りツール（Requirements.md 10.8.2）。
//
// **/pb-onboard は読み取りしかしないので、これを入れておくと一息に走る**（10.8.5）。
// **write 系は入れない**——起票・遷移・文書更新は、利用者が1回ずつ見て通す。
//
// **系統A の .claude/settings.json と、系統B の .codex/config.toml の両方がこれを読む。**
// **Claude Code は許可を別のファイル（コミットする）に書き、Codex は接続設定と同じ
// ファイル（コミットしない）に書く**——同じ意図を2つの書式で表すことになるので、
// **元の一覧は1つにしておく**（Requirements.md 10.8.4.1 の非対称）。
var autoApprovedTools = []string{
	"pb_get_project",
	"pb_list_docs",
	"pb_get_doc",
	"pb_list_tasks",
	"pb_get_task",
	"pb_get_context",
	"pb_list_transitions",
}

// mcpServerName は接続設定に書くサーバ名。
//
// **手順ファイルが mcp__pb__* で権限を書くので、ここを変えると許可が効かなくなる。**
const mcpServerName = "pb"

// ConnectParams は接続設定へ差し込む値（ApiDesign.md 4.5.8.1）。
type ConnectParams struct {
	ProjectKey  string
	ProjectName string
	// MCPURL は接続先（Design.md 8.3）。**画面が出す文字列と同じもの**を使う
	// ——別々に組み立てると、画面の表示と生成物が黙ってずれる。
	MCPURL string
	// TokenEnvName は接頭付きの実際の変数名（ApiDesign.md 4.5.1）。
	// **フォールバックの計算はサーバの1か所**で済ませてあり、ここは受け取るだけ。
	TokenEnvName string
	// DisplayName は本人が付けたエージェントの名前（手引きの宛名に使う）。
	DisplayName string
	// ClientDisplayName はカタログの表示名（ApiDesign.md 4.5.7）。
	// **配置ファイルを持たない種別でも手引きは出す**ので、常に要る。
	ClientDisplayName string
	// Transport is "direct" (the Codex HTTP MCP client) or "bridge" (a local
	// stdio process).  Only Codex supports the latter.
	Transport string
}

const (
	TransportDirect = "direct"
	TransportBridge = "bridge"
)

// Connect は系統B の成果物一式（ApiDesign.md 4.5.8）。
type Connect struct {
	// Files は接続設定。**0枚か1枚**である——PB が書式を持たない種別（gemini /
	// other）では空。**has_setup_template では決まらない**（ApiDesign.md 4.5.8.3）
	// ——あれは系統A の有無で、claude_desktop は偽のまま接続設定を持つ。
	Files []File
	// Readme は zip にだけ入れる手引き（4.5.8.5）。
	//
	// **Files に入れない。** 画面が同じ内容を節として描いており、files[] に入れると
	// 「これも置くファイルだ」と読まれる。
	Readme string
	// UsesTokenEnvVar は export 行を出すか（4.5.8.2）。
	//
	// **偽になる種別が2つあり、理由が違う**（ApiDesign.md 4.5.8.2）。Copilot は
	// ${input:pb-token} を使って環境変数を読まず、Claude Desktop は**GUI アプリ
	// なのでシェルの環境が届かない**（トークンは設定ファイルの env に書く）。
	UsesTokenEnvVar bool
}

// ReadmeName は zip に入れる手引きのファイル名（ApiDesign.md 4.5.8.5）。
//
// **README.md にしない。** プロジェクト直下で展開されたときに本物を消す
// ——それは接続設定を merge にした理由そのものである。
const ReadmeName = "PB-README.md"

// connectSpec はクライアント種別ごとの接続設定の仕様。
type connectSpec struct {
	// configPath は置き場（DbDesign.md 8.2.1.1 が言う「client_kind が決めるもの」）。
	configPath string
	// usesTokenEnvVar は環境変数を読むか。**Copilot と Claude Desktop が偽**
	// （10.8.4、10.8.4.2）。**同じ偽でも渡し方が違う**ので、画面は書き分ける。
	usesTokenEnvVar bool
	// render は設定ファイルの中身を組み立てる。
	render func(p ConnectParams) (string, error)
	// language は画面がハイライトに使う。
	language string
	// readme は手引きのテンプレート名（templates/connect/ の中）。
	readme string
}

var connectSpecs = map[string]connectSpec{
	"claude_code": {
		configPath:      ".mcp.json",
		usesTokenEnvVar: true,
		render:          renderClaudeMCP,
		language:        "json",
		readme:          "claude_code.md",
	},
	"copilot": {
		configPath: ".vscode/mcp.json",
		// **VS Code が初回に入力を求め、以降は安全に保存する**（10.8.4）。
		// ワークスペースごとに別のトークンを持てるので、同じ端末で複数の
		// プロジェクトを開いても衝突しない。
		usesTokenEnvVar: false,
		render:          renderCopilotMCP,
		language:        "json",
		readme:          "copilot.md",
	},
	"codex": {
		configPath:      ".codex/config.toml",
		usesTokenEnvVar: true,
		render:          renderCodexConfig,
		language:        "toml",
		readme:          "codex.md",
	},
	// **系統A（specs）に対応する行が無い唯一の種別である**（pb-58）。
	// Claude Desktop は作業フォルダを持たないので、リポジトリにコミットする
	// 配置ファイルの置き場が無い（Requirements.md 10.9.1）。**接続設定だけが在る。**
	"claude_desktop": {
		configPath: "claude_desktop_config.json",
		// **環境変数を読まない。** GUI アプリはシェルから起動しないので、
		// ~/.zshrc に書いた export は届かない（Copilot と同じく export_line は null）。
		// **ただし理由が違う**——あちらはクライアントが入力を求めるのに対し、
		// こちらは**設定ファイルの env にトークンそのものを書く。**
		usesTokenEnvVar: false,
		render:          renderClaudeDesktopConfig,
		language:        "json",
		readme:          "claude_desktop.md",
	},
}

// ExportLine は環境変数へトークンを置く行を組み立てる（ApiDesign.md 4.5.8.2）。
//
// **値はプレースホルダである。** 平文は発行の応答にしか存在せず、
// 何度でも開ける画面に置くのは Requirements.md 10.10.1 に反する。
//
// **単引用符で囲む。** トークンは pb_agt_ + Base62 なのでシェルの特殊文字を含まないが、
// **利用者が貼り替える先**であり、囲っておかないと貼った値に記号が混じったときだけ
// 静かに壊れる。
func ExportLine(tokenEnvName string) string {
	return fmt.Sprintf("export %s='ここに発行したトークンを貼る'", tokenEnvName)
}

// RenderConnect は1件のエージェントぶんの接続設定を組み立てる（ApiDesign.md 4.5.8）。
//
// **配置ファイルを持たない種別でも error にしない**（4.5.8.3）。Files を空にして、
// 手引きに「この値で自分で設定してください」と書く——**エージェントは既に登録されており、
// URL も変数名も正しく決まっている。** 5.7.1 が未対応の種別を 422 で拒むのは
// **これから選ぶ**ものだからで、こちらは**既に選ばれた結果**である。
func RenderConnect(kind string, p ConnectParams) (Connect, error) {
	if p.Transport == "" {
		p.Transport = TransportDirect
	}
	if p.Transport != TransportDirect && p.Transport != TransportBridge {
		return Connect{}, fmt.Errorf("未知の接続方式: %s", p.Transport)
	}
	if p.Transport == TransportBridge && kind != "codex" {
		return Connect{}, fmt.Errorf("stdio ブリッジは Codex でのみ使えます")
	}
	spec, ok := connectSpecs[kind]
	if !ok {
		readme, err := renderReadme("none.md", kind, connectSpec{}, p)
		if err != nil {
			return Connect{}, err
		}
		// **環境変数は勧める側で出す。** 自分で書く人にとって、変数名は
		// PB が決めた事実であって選択肢ではない。
		return Connect{Files: nil, Readme: readme, UsesTokenEnvVar: true}, nil
	}

	content, err := spec.render(p)
	if err != nil {
		return Connect{}, err
	}
	readme, err := renderReadme(spec.readme, kind, spec, p)
	if err != nil {
		return Connect{}, err
	}

	return Connect{
		Files: []File{{
			Path:       spec.configPath,
			ClientKind: kind,
			// **常に merge である**（ApiDesign.md 4.5.8.4）。既存の構造へ
			// 該当キーだけを足すものであり、**zip では別名になる**。
			Mode:     ModeMerge,
			Language: spec.language,
			Content:  content,
		}},
		Readme:          readme,
		UsesTokenEnvVar: spec.usesTokenEnvVar,
	}, nil
}

// readmeParams は手引きへ差し込む値。
type readmeParams struct {
	ConnectParams
	// ConfigPath は置き場。**空になりうる**（配置ファイルを持たない種別）。
	ConfigPath string
	// ZipEntryName は zip の中での名前。**改名を促すために出す。**
	ZipEntryName string
	ExportLine   string
	// OnboardRef は参画をどう起動するか。**系統A の specs から借りる**
	// ——同じ文言を2か所に置くと必ずずれる（Codex はスラッシュコマンドを持たない）。
	OnboardRef string
}

// renderReadme は templates/connect/<name> を差し込む。
func renderReadme(name, kind string, spec connectSpec, p ConnectParams) (string, error) {
	rp := readmeParams{
		ConnectParams: p,
		ConfigPath:    spec.configPath,
		ExportLine:    ExportLine(p.TokenEnvName),
		OnboardRef:    "参画の手順",
	}
	if spec.configPath != "" {
		rp.ZipEntryName = blockName(spec.configPath)
	}
	// **系統A が持つ「起動の言い方」をそのまま使う**（Codex は誘発、他はコマンド）。
	// **無い種別（gemini / other）では既定の日本語のまま**にする。
	//
	// **動詞を足さない。** onboardRef は種別ごとに品詞が違う（Claude Code は
	// 「`/pb-onboard`」だが Codex は「…と伝える（…）」で動詞を含む）ので、
	// **文として整えるのはテンプレート側の仕事である**——ここで
	// 「を実行します」を継ぎ足したら Claude Code だけ二重になった（実出力で発見）。
	if a, ok := specs[kind]; ok {
		rp.OnboardRef = a.onboardRef
	}

	raw, err := connectFS.ReadFile(path.Join("templates/connect", name))
	if err != nil {
		return "", fmt.Errorf("手引き %s を読めない: %w", name, err)
	}
	tmpl, err := template.New(name).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("手引き %s を解釈できない: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, rp); err != nil {
		return "", fmt.Errorf("手引き %s を組み立てられない: %w", name, err)
	}
	return buf.String(), nil
}

// ── 種別ごとの設定ファイル ──────────────────────────────────

// mcpServerEntry は .mcp.json / .vscode/mcp.json のサーバ1件。
type mcpServerEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// renderClaudeMCP は .mcp.json を組み立てる（Requirements.md 10.8.3）。
//
// **encoding/json で組み立てる。** 文字列を継ぎ足すと、**壊れた JSON を配っても
// 誰も気づかない**——症状はクライアント側の「繋がらない」であって、PB には出ない。
//
// **${…} はクライアントが起動時に展開する。** ファイルに実体は残らない。
func renderClaudeMCP(p ConnectParams) (string, error) {
	doc := struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}{
		MCPServers: map[string]mcpServerEntry{
			mcpServerName: {
				Type: "http",
				URL:  p.MCPURL,
				Headers: map[string]string{
					"Authorization": fmt.Sprintf("Bearer ${%s}", p.TokenEnvName),
				},
			},
		},
	}
	return marshalConfig(doc)
}

// renderCopilotMCP は .vscode/mcp.json を組み立てる（Requirements.md 10.8.4）。
//
// **トップレベルは servers であって mcpServers ではない**（VS Code の書式）。
// **inputs で初回に入力を求め、以降は VS Code が安全に保存する**ので、
// **この形式だけ環境変数を使わない。**
func renderCopilotMCP(p ConnectParams) (string, error) {
	type input struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Password    bool   `json:"password"`
	}
	doc := struct {
		Inputs  []input                   `json:"inputs"`
		Servers map[string]mcpServerEntry `json:"servers"`
	}{
		Inputs: []input{{
			ID:          "pb-token",
			Type:        "promptString",
			Description: fmt.Sprintf("Project Backyard（%s）のアクセストークン", p.ProjectName),
			Password:    true,
		}},
		Servers: map[string]mcpServerEntry{
			mcpServerName: {
				Type: "http",
				URL:  p.MCPURL,
				Headers: map[string]string{
					"Authorization": "Bearer ${input:pb-token}",
				},
			},
		},
	}
	return marshalConfig(doc)
}

// desktopCommandPlaceholder / desktopPathPlaceholder は利用者が貼り替える欄
// （ApiDesign.md 4.5.8.6）。**PB は相手の端末に node がどこにあるかを知らない。**
//
// **export 行と同じ作法である**——PB が決められる値は入れ、決められない値は
// 「ここに貼る」と書いて渡す。
const (
	desktopCommandPlaceholder = "ここに npx の絶対パスを貼る（which npx で分かる）"
	desktopPathPlaceholder    = "ここに node のあるディレクトリを貼る（dirname $(which node)）:/usr/bin:/bin"
	desktopTokenPlaceholder   = "ここに発行したトークンを貼る"
)

// mcpRemotePackage は stdio と Streamable HTTP を繋ぐ橋（Requirements.md 10.8.4.2）。
//
// **Claude Desktop の設定ファイルは stdio のサーバしか書けない**ので、
// HTTP の PB へ繋ぐには橋が要る。**0.8.5 で実測した**（pb-58）。
const mcpRemotePackage = "mcp-remote"

// renderClaudeDesktopConfig は claude_desktop_config.json を組み立てる
// （Requirements.md 10.8.4.2、手順は pb-58 で実測）。
//
// **カスタムコネクタでは繋がらない。** あちらの接続は利用者の端末からではなく
// Anthropic のクラウドから届くので、手元の PB には到達しない（https も要る）。
// **残る経路がこのファイルだけである。**
//
// **command と PATH は placeholder にする。** 実測で分かった罠が2つあり、どちらも
// 「GUI アプリはシェルの PATH を継承しない」に帰着する。`"npx"` とだけ書くと
// **起動できず**（No such file or directory）、絶対パスにしても **npx 自身が node を
// PATH から探して落ちる**（env: node: No such file or directory）。
// **env の PATH に node のディレクトリを足して初めて通る。**
//
// **トークンは env に置き、ヘッダから ${…} で参照する**（mcp-remote が展開する）。
// **平文がファイルに残ることは避けられない**——シェルの環境変数は GUI アプリに届かない。
//
// **コロンの後に空白を入れない**（`Authorization:Bearer ${…}`）。mcp-remote が
// 引数の空白で割れる形を避けるための書き方である。
func renderClaudeDesktopConfig(p ConnectParams) (string, error) {
	type desktopServer struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	doc := struct {
		MCPServers map[string]desktopServer `json:"mcpServers"`
	}{
		MCPServers: map[string]desktopServer{
			mcpServerName: {
				Command: desktopCommandPlaceholder,
				Args: []string{
					"-y", mcpRemotePackage, p.MCPURL,
					"--header", fmt.Sprintf("Authorization:Bearer ${%s}", p.TokenEnvName),
				},
				Env: map[string]string{
					"PATH":         desktopPathPlaceholder,
					p.TokenEnvName: desktopTokenPlaceholder,
				},
			},
		},
	}
	return marshalConfig(doc)
}

// renderCodexConfig は .codex/config.toml を組み立てる（Requirements.md 10.8.4.1）。
//
// **TOML を標準ライブラリで書けないので文字列で組む。** 差し込む値は URL・変数名・
// ツール名（すべて PB が決めた値）で、利用者の自由入力は入らない。
//
// **ツールの許可も同居する。** Claude Code が .claude/settings.json（コミットする）に
// 書くものを、Codex はこのファイル（コミットしない）に書く——**だから系統B が配る**。
// **enabled_tools は使わない**——あれは一覧から消す絞り込みで、**write 系のツールを
// 消してしまう**。欲しいのは「読み取り7件を確認なしに、残りは毎回聞く」なので、
// default_tools_approval_mode = "prompt" ＋ 個別の auto にする。
func renderCodexConfig(p ConnectParams) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.%s]\n", mcpServerName)
	if p.Transport == TransportBridge {
		b.WriteString("command = \"pb-mcp-bridge\"\n")
		fmt.Fprintf(&b, "args = [\"--url\", %q, \"--token-env\", %q]\n", p.MCPURL, p.TokenEnvName)
	} else {
		fmt.Fprintf(&b, "url = %q\n", p.MCPURL)
		fmt.Fprintf(&b, "bearer_token_env_var = %q\n", p.TokenEnvName)
	}
	b.WriteString("default_tools_approval_mode = \"prompt\"\n")
	for _, tool := range autoApprovedTools {
		fmt.Fprintf(&b, "\n[mcp_servers.%s.tools.%s]\n", mcpServerName, tool)
		b.WriteString("approval_mode = \"auto\"\n")
	}
	return b.String(), nil
}

// marshalConfig は設定ファイルを2スペース字下げの JSON にする。
//
// **末尾に改行を付ける。** 利用者が手で継ぎ足す先であり、改行の無いファイルは
// エディタによって扱いが揺れる。
func marshalConfig(v any) (string, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("接続設定を組み立てられない: %w", err)
	}
	return string(raw) + "\n", nil
}
