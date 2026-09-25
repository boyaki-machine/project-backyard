package agentsetup

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 「HTTPS の証明書を信頼させる」手順（ApiDesign.md 4.5.8.1b、pb-202）のテスト。

// developmentMDPath は手順の事実の正本（Development.md 14.6「クライアントに信頼させる」の表）。
const developmentMDPath = "../../../docs/Development.md"

// caTrustDocRow は 14.6 の表の1行を、手順を引く鍵へ対応づけたもの。
type caTrustDocRow struct {
	kind, transport string
	// idents は「手当て」の欄に `…` で書かれた変数名・設定名。
	idents []string
	// verification は「確かめたこと」の欄から読んだ判定。
	verification CATrustVerification
}

// caTrustDocRowKeys は表の行の書き出しから、手順を引く鍵を決める。
//
// **表の行を足したら、ここにも足す。** 足さない行は読み飛ばさず落とす（下の Fatalf）
// ——黙って飛ばすと、表に書いた手当てが画面に出ないまま通る。
var caTrustDocRowKeys = []struct {
	prefix, kind, transport string
}{
	{"| Claude Code（", "claude_code", TransportDirect},
	{"| Codex（stdio ブリッジ）", "codex", TransportBridge},
	{"| Codex（HTTPS 直結）", "codex", TransportDirect},
	{"| Claude Desktop（", "claude_desktop", TransportDirect},
	{"| その他の Node 製", "gemini", TransportDirect},
	{"| Copilot（", "copilot", TransportDirect},
	// curl は PB が接続設定を配る相手ではない
	{"| curl", "", ""},
}

var (
	backtickRe = regexp.MustCompile("`([^`]+)`")
	// identRe は環境変数名（大文字）か、ドット区切りの設定名（`http.systemCertificates`）。
	// **`env` のような設定のキーは拾わない**——「同上」の行では置き場が変わる（設定の env → シェル）。
	identRe = regexp.MustCompile(`^(?:[A-Z][A-Z0-9_]+|[a-z]+(?:\.[A-Za-z]+)+)$`)
)

// readCATrustDocRows は Development.md 14.6 の表を読む。
func readCATrustDocRows(t *testing.T) []caTrustDocRow {
	t.Helper()
	raw, err := os.ReadFile(developmentMDPath)
	if err != nil {
		t.Fatalf("Development.md を読めない: %v", err)
	}
	doc := string(raw)
	start := strings.Index(doc, "## 14.6 ")
	if start < 0 {
		t.Fatal("Development.md に 14.6 が無い")
	}
	sec := doc[start:]
	tbl := strings.Index(sec, "### クライアントに信頼させる")
	if tbl < 0 {
		t.Fatal("14.6 に「クライアントに信頼させる」が無い")
	}
	sec = sec[tbl:]
	if end := strings.Index(sec[1:], "\n### "); end >= 0 {
		sec = sec[:end+1]
	}

	var rows []caTrustDocRow
	var prevCare, prevCheck string
	for _, line := range strings.Split(sec, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| クライアント") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 3 {
			t.Fatalf("表の列が3つでない: %s", line)
		}
		care, check := strings.TrimSpace(cells[1]), strings.TrimSpace(cells[2])
		if strings.HasPrefix(care, "同上") {
			care = prevCare
		}
		if check == "同上" {
			check = prevCheck
		}
		prevCare, prevCheck = care, check

		matched := false
		for _, k := range caTrustDocRowKeys {
			if !strings.HasPrefix(line, k.prefix) {
				continue
			}
			matched = true
			if k.kind == "" {
				break
			}
			row := caTrustDocRow{kind: k.kind, transport: k.transport}
			for _, m := range backtickRe.FindAllStringSubmatch(care, -1) {
				id, _, _ := strings.Cut(m[1], "=")
				if identRe.MatchString(id) {
					row.idents = append(row.idents, id)
				}
			}
			switch {
			case strings.HasPrefix(check, "**確認**"):
				row.verification = CATrustVerified
			case check == "**実機未確認**":
				row.verification = CATrustUnverified
			default:
				row.verification = CATrustPartial
			}
			rows = append(rows, row)
			break
		}
		if !matched {
			t.Fatalf("14.6 の表に対応づけていない行がある（caTrustDocRowKeys に足す）: %s", line)
		}
	}
	if len(rows) == 0 {
		t.Fatal("14.6 の表から1行も読めなかった")
	}
	return rows
}

// TestCATrustMatchesDevelopmentMD は、画面と手引きに出す手順が Development.md 14.6 の表と
// 食い違わないことを確かめる（pb-202 の完了条件）。
//
// **期待値をここに書かない。** 表を直したらテストが追従し、手順の側が追いついていなければ落ちる。
func TestCATrustMatchesDevelopmentMD(t *testing.T) {
	for _, row := range readCATrustDocRows(t) {
		t.Run(row.kind+"/"+row.transport, func(t *testing.T) {
			p := testConnectParams()
			p.MCPURL = "https://localhost:8443/mcp/pb"
			p.Transport = row.transport
			c, err := RenderConnect(row.kind, p)
			if err != nil {
				t.Fatalf("RenderConnect が失敗した: %v", err)
			}
			if c.CATrust.Verification != row.verification {
				t.Errorf("確かめたこと: got %q, 14.6 の表は %q", c.CATrust.Verification, row.verification)
			}
			for _, id := range row.idents {
				// 語として現れること（`NODE_EXTRA_CA_CERTS` が `NODE_EXTRA_CA_CERTS_X` で通らないように）
				re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_.])` + regexp.QuoteMeta(id) + `(?:[^A-Za-z0-9_]|$)`)
				if !re.MatchString(c.CATrust.BodyMD) {
					t.Errorf("14.6 の表にある %q が手順に無い:\n%s", id, c.CATrust.BodyMD)
				}
			}
		})
	}
}

// TestCATrustSpecsCoverConnectSpecs は、接続設定を持つ種別がすべて専用の手順を持つことを確かめる。
//
// **無いと none.md の一般論へ黙って落ちる**——症状は「書いてある手当てが効かない」で、
// サーバには何も出ない。
func TestCATrustSpecsCoverConnectSpecs(t *testing.T) {
	for kind := range connectSpecs {
		if _, ok := caTrustSpecs[kind]; !ok {
			t.Errorf("%s の「CA を信頼させる」手順が caTrustSpecs に無い", kind)
		}
	}
	if caTrustSpecFor("codex", TransportBridge).name != "codex_bridge.md" {
		t.Error("Codex のブリッジが専用の手順を引いていない")
	}
}

// TestCATrustIsAlwaysRenderedAndSharedWithReadme は、手順が http でも組み立てられ、
// 手引きに同じ本文が入ることを確かめる（4.5.8.1b）。
//
// **画面と zip が同じ文を使う**のがサーバに持たせた理由である。
func TestCATrustIsAlwaysRenderedAndSharedWithReadme(t *testing.T) {
	cases := []struct{ kind, transport string }{
		{"claude_code", TransportDirect},
		{"copilot", TransportDirect},
		{"codex", TransportDirect},
		{"codex", TransportBridge},
		{"claude_desktop", TransportDirect},
		{"gemini", TransportDirect},
		{"other", TransportDirect},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.transport, func(t *testing.T) {
			p := testConnectParams()
			p.Transport = tc.transport
			c, err := RenderConnect(tc.kind, p)
			if err != nil {
				t.Fatalf("RenderConnect が失敗した: %v", err)
			}
			if c.CATrust.BodyMD == "" || c.CATrust.Verification == "" {
				t.Fatalf("http でも手順を返す: %+v", c.CATrust)
			}
			// 前置き（公開 CA なら不要）が先頭に付く
			if !strings.HasPrefix(c.CATrust.BodyMD, "**公開 CA の証明書なら") {
				t.Errorf("前置きが先頭に無い:\n%s", c.CATrust.BodyMD)
			}
			if !strings.Contains(c.Readme, c.CATrust.BodyMD) {
				t.Errorf("手引きに画面と同じ本文が無い:\n%s", c.Readme)
			}
			if !strings.Contains(c.Readme, "**実機での確認**："+c.CATrust.Verification.label()) {
				t.Errorf("手引きに確かめたかの札が無い:\n%s", c.Readme)
			}
			// **画面は見出しを3段下げて描く**が、節の中に見出しを置くと畳んだ節の外と区別できない。
			for _, line := range strings.Split(c.CATrust.BodyMD, "\n") {
				if strings.HasPrefix(line, "#") {
					t.Errorf("手順の本文に見出しがある: %s", line)
				}
			}
			// **段落を折り返さない。** 画面の Markdown は改行を <br> にする（lib/markdown.ts の
			// breaks: true）ので、テンプレートで折り返した位置で文が切れて見える（実画面で発見）。
			inCode, prevText := false, false
			for _, line := range strings.Split(c.CATrust.BodyMD, "\n") {
				if strings.HasPrefix(line, "```") {
					inCode, prevText = !inCode, false
					continue
				}
				text := !inCode && strings.TrimSpace(line) != ""
				if text && prevText {
					t.Errorf("段落が折り返されている（画面では文の途中で改行になる）: %s", line)
				}
				prevText = text
			}
			for _, bad := range []string{"{{", "<no value>", "pb_agt_"} {
				if strings.Contains(c.CATrust.BodyMD, bad) {
					t.Errorf("手順に %q が残っている:\n%s", bad, c.CATrust.BodyMD)
				}
			}
		})
	}
}

// TestCATrustCodexDirectPointsToBridge は、Codex の直接接続の手順がブリッジを正とすることを
// 確かめる（pb-202 で決めた。直接接続は実機未確認）。
func TestCATrustCodexDirectPointsToBridge(t *testing.T) {
	p := testConnectParams()
	c, err := RenderConnect("codex", p)
	if err != nil {
		t.Fatalf("RenderConnect が失敗した: %v", err)
	}
	for _, want := range []string{"ローカル stdio ブリッジ", "CODEX_CA_CERTIFICATE", "確かめたこと：ありません"} {
		if !strings.Contains(c.CATrust.BodyMD, want) {
			t.Errorf("直接接続の手順に %q が無い:\n%s", want, c.CATrust.BodyMD)
		}
	}
}
