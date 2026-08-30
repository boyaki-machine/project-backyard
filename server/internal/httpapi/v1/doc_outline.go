// 文書の見出し（ApiDesign.md 10.2 の outline、10.3 の ?section=）。
//
// **見出しはどこにも保存しない**（DbDesign.md 8.1.3）。常に現在の本文から作るので、
// 見出しを改名しても壊れる参照が生まれない。
//
// **抽出規則は CommonMark の ATX 見出しに合わせる。** 10.2 は「見出しテキスト
// そのもの」としか書いていないが、画面の描画は markdown-it（CommonMark 準拠）で
// 行うため（client/src/lib/markdown.ts）、**ここが CommonMark からずれると
// 「目次にある章が本文では見出しに見えない」という食い違いが起きる。**
//
//	#〜###### のあとに空白1つ以上          → 見出し（#見出し は見出しではない）
//	先頭の空白は3つまで                    → 4つ以上はコードブロック
//	末尾の閉じ # 列（## 命名 ##）は落とす
//	フェンス（``` / ~~~）の内側は拾わない  → コード例に混ざる # を見出しにしない
//	Setext（=== / ---）は拾わない          → 10.2 が level を「## が 2」と定義している
package v1

import (
	"strconv"
	"strings"
)

// docHeading は本文から取り出した見出し1件。
type docHeading struct {
	// Section は 10.2 の section。見出しテキストそのもので、同名が2つ以上
	// あるときだけ2つ目以降に #2 / #3 … が付く。
	Section string
	// Level は Markdown の見出しレベル（## が 2）。
	Level int
	// line は本文の何行目にあるか（0始まり）。?section= の切り出しに使う。
	line int
}

// docOutlineItem は 10.2 / 10.3 が返す1件。
type docOutlineItem struct {
	Section string `json:"section"`
	Level   int    `json:"level"`
}

// 見出しの最大レベル（CommonMark は h1〜h6）。
const maxHeadingLevel = 6

// フェンスの最小の長さ（CommonMark は3つ以上）。
const minFenceLen = 3

// parseHeadings は本文から見出しを出現順に取り出す。
//
// **同名の見出しは2つ目以降に #2 を付ける**（ApiDesign.md 10.2）。
// 付けるのは「同じ Section 文字列が既に出たとき」だけで、1つ目には付かない。
func parseHeadings(body string) []docHeading {
	var out []docHeading
	seen := map[string]int{}

	var fence string // 開いているフェンスの印（``` か ~~~）。空なら閉じている

	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")

		if marker, ok := fenceMarker(trimmed); ok {
			// 開いているフェンスは同じ種類の印でしか閉じられない（CommonMark）。
			switch {
			case fence == "":
				fence = marker
			case fence == marker:
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}

		text, level, ok := atxHeading(trimmed)
		if !ok {
			continue
		}

		seen[text]++
		section := text
		if n := seen[text]; n > 1 {
			section = text + "#" + strconv.Itoa(n)
		}
		out = append(out, docHeading{Section: section, Level: level, line: i})
	}
	return out
}

// outlineItems は 10.2 / 10.3 の outline を組み立てる。
//
// **常に空でないスライスを返す**（nil にしない）。JSON の null と [] を
// 取り違えさせないためで、2.6 の items と同じ扱いである。
func outlineItems(body string) []docOutlineItem {
	headings := parseHeadings(body)
	items := make([]docOutlineItem, 0, len(headings))
	for _, h := range headings {
		items = append(items, docOutlineItem{Section: h.Section, Level: h.Level})
	}
	return items
}

// sectionNames は 10.3 の available_sections。見出しの出現順に並べる。
func sectionNames(body string) []string {
	headings := parseHeadings(body)
	names := make([]string, 0, len(headings))
	for _, h := range headings {
		names = append(names, h.Section)
	}
	return names
}

// extractSection は ?section= が指す章を切り出す（ApiDesign.md 10.3）。
//
// **見出し行から、同じか上のレベルの次の見出しの直前までを含む。** 下位の
// 見出し（## 命名 の中の ### 接頭辞）は章の一部として付いてくる。
//
// 見つからなければ ok=false を返す。呼び出し側が available_sections を添えて
// 404 を返す。
func extractSection(body, section string) (string, bool) {
	headings := parseHeadings(body)
	idx := -1
	for i, h := range headings {
		if h.Section == section {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false
	}

	lines := strings.Split(body, "\n")
	start := headings[idx].line
	end := len(lines)
	for _, h := range headings[idx+1:] {
		if h.Level <= headings[idx].Level {
			end = h.line
			break
		}
	}

	// 章の末尾に付いてくる空行を落とす。次の見出しの直前までを取ると、
	// 段落を区切るための空行が必ず1行以上入るためである。
	out := lines[start:end]
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n"), true
}

// atxHeading は ATX 見出しなら (テキスト, レベル, true) を返す。
//
// CommonMark 4.2 に従う——先頭の空白は3つまで、# は1〜6個、そのあとに
// 空白が1つ以上要る（`#見出し` は見出しではない）。末尾の閉じ # 列は落とす。
// `#` だけの行は空の見出しで、Section が空文字になると ?section= で指せない
// ため拾わない。
func atxHeading(line string) (string, int, bool) {
	i := 0
	for i < len(line) && i < 4 && line[i] == ' ' {
		i++
	}
	if i >= 4 || i >= len(line) || line[i] != '#' {
		return "", 0, false
	}

	level := 0
	for i < len(line) && line[i] == '#' {
		level++
		i++
	}
	if level > maxHeadingLevel {
		return "", 0, false
	}
	// # の直後は行末か空白でなければならない。
	if i < len(line) && line[i] != ' ' && line[i] != '\t' {
		return "", 0, false
	}

	text := strings.TrimSpace(line[i:])
	// 閉じ # 列（## 命名 ##）。# だけで構成された末尾の語を落とす。
	if fields := strings.Fields(text); len(fields) > 0 {
		if last := fields[len(fields)-1]; strings.Trim(last, "#") == "" {
			text = strings.TrimSpace(text[:len(text)-len(last)])
		}
	}
	if text == "" {
		return "", 0, false
	}
	return text, level, true
}

// fenceMarker はフェンスの開始・終了行なら印（``` か ~~~）を返す。
//
// **印の長さは見ない。** 開いたフェンスより短い印では閉じられないのが
// CommonMark の規則だが、ここが判定するのは「見出しを拾ってよい区間か」
// だけであり、短い印で早く閉じても拾う見出しは変わらない。
func fenceMarker(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) >= 4 {
		return "", false // 4つ以上の空白はコードブロック
	}
	for _, m := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, m) && countRun(trimmed, rune(m[0])) >= minFenceLen {
			return m, true
		}
	}
	return "", false
}

// countRun は先頭から続く同じ文字の数を数える。
func countRun(s string, c rune) int {
	n := 0
	for _, r := range s {
		if r != c {
			break
		}
		n++
	}
	return n
}
