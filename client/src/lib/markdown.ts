/**
 * Markdown の描画（`GuiDesign.md` 5.5「説明欄」、`Design.md` 3.1）。
 *
 * **`markdown-it` の出力を `dompurify` に通してから返す。** `markdown-it` は
 * 既定で安全側に倒れている（`html: false` と `javascript:` 等を弾くリンク検証）が、
 * **本文の書き手は人だけではない**——チケットの説明はエージェントが MCP 経由で
 * 書き込む一次資料であり（`Requirements.md` 10章）、描画の直前にもう一段通す。
 * **MCP 経由の入力を信用しない**方針は `Requirements.md` 10.10.6 と同じ根である。
 *
 * 対応構文は **`markdown-it` の既定（CommonMark ＋ 表・取り消し線）に限る。**
 * `Requirements.md` 7章が挙げるチェックボックス（`- [x]`）・`#123` 形式のタスク
 * 参照記法・画像のペーストは**プラグインか独自の規則が要るので Phase 1 では
 * 実装しない**（5.5「説明欄」）。`- [x]` は箇条書きの項目になり、
 * **`[x]` はそのまま文字として残る**（実測）。
 */
import DOMPurify from 'dompurify'
import MarkdownIt from 'markdown-it'

/**
 * **`html: false`**（生の HTML を通さない）。`dompurify` があっても外さない
 * ——多層で守るほうが、片方の既定が将来変わったときに壊れにくい。
 *
 * `linkify` は生の URL を自動でリンクにする。`typographer` は入れない
 * （引用符や省略記号を勝手に置き換えると、コード片を地の文に書いたときに壊れる）。
 *
 * **`breaks: true`**（段落の中の改行を `<br>` にする。`GuiDesign.md` 5.5、pb-36）。
 * CommonMark どおりの `false` では、日本語で Enter を打った位置に半角空白が見えていた。
 * 漢字・かなの間だけ詰める案は、Enter で改行したつもりの文が1行につながるので採らない。
 */
const md = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
})

/**
 * **見出しを3段下げる**（`#` → `<h4>`）。
 *
 * `GuiDesign.md` 9.2 は「**ページヘッダの `<h1>` が唯一のページ見出しとなる**」と
 * 定める。本文の `#` をそのまま `<h1>` にすると、**利用者が書いた文字が
 * ページの見出し階層に割り込む**——チケット詳細では `<h1>`（バックログ）→
 * `<h2>`（チケットID）→ `<h3>`（説明）と続くので、本文はその下から始める。
 *
 * **見た目は `base.css` の `.markdown-body` が作る**ので、段を下げても
 * 「概要」が小さくなるわけではない。**`h6` で頭打ちにする**（HTML に `h7` は無い）。
 */
md.core.ruler.push('pb_demote_headings', (state) => {
  for (const token of state.tokens) {
    if (token.type !== 'heading_open' && token.type !== 'heading_close') continue
    const level = Number(token.tag.slice(1))
    token.tag = `h${Math.min(level + 3, 6)}`
  }
  return true
})

/**
 * **リンクは新しいタブで開く**（5.5「コードと参考リンク」と同じ規則）。
 * `rel="noopener nofollow"` を必ず添える——`noopener` は開いた先から
 * `window.opener` を触られないため、`nofollow` は本文の書き手が人だけでは
 * ないためである。
 */
md.renderer.rules.link_open = (tokens, idx, options, _env, self) => {
  const token = tokens[idx]!
  token.attrSet('target', '_blank')
  token.attrSet('rel', 'noopener nofollow')
  return self.renderToken(tokens, idx, options)
}

/**
 * Markdown ソースを描画用の HTML にする。
 *
 * **戻り値はそのまま `v-html` に渡してよい**（この関数を通っていることが
 * 安全性の根拠なので、呼び出し側で別経路の HTML を混ぜない）。
 */
export function renderMarkdown(source: string): string {
  if (source === '') return ''
  return DOMPurify.sanitize(md.render(source), {
    // `target="_blank"` は既定の許可属性に含まれないため明示する。
    // これを落とすと上の `link_open` が無意味になる。
    ADD_ATTR: ['target'],
  })
}
