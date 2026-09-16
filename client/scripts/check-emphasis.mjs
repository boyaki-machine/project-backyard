// 描画しても `**` が残る段落（閉じない強調記号）を数える。`make docs-emphasis` から呼ぶ。
// 数え方の正本は Testing.md 7.7。
//
// - 段落単位で見る。強調は行をまたぐので、行単位では開き側と閉じ側を切り離して誤判定する
// - コードフェンスとコードスパンは描画器が別のトークンにするので、text だけを見れば除ける
// - フランキング規則を自分で実装しない。CJK の扱いは描画器の出力だけが信用できる（PB #39）
//
// 使い方: node client/scripts/check-emphasis.mjs <file.md>...
// 1段落でも残れば終了コード 1、引数が無ければ 2。
import { readFileSync } from 'node:fs'
import MarkdownIt from 'markdown-it'

const files = process.argv.slice(2)
if (files.length === 0) {
  console.error('使い方: node client/scripts/check-emphasis.mjs <file.md>...')
  process.exit(2)
}

const md = new MarkdownIt()
let found = 0

for (const file of files) {
  const tokens = md.parse(readFileSync(file, 'utf8'), {})
  // 表のセルの inline は位置を持たないので、直前に位置を持っていたトークン（表の行）で代える
  let line = '?'
  for (const token of tokens) {
    if (token.map) line = token.map[0] + 1
    // inline は段落・見出し・表のセル1つ分の中身
    if (token.type !== 'inline') continue
    const left = token.children.some((c) => c.type === 'text' && c.content.includes('**'))
    if (!left) continue
    found++
    console.log(`${file}:${line}: ${token.content.replace(/\s*\n\s*/g, ' ').slice(0, 60)}`)
  }
}

if (found > 0) {
  console.log(`NG: 描画しても ** が残る段落が ${found} 件ある（${files.length} ファイル）`)
  console.log('    約物を強調の外へ出して直す（Testing.md 7.7）')
  process.exit(1)
}
console.log(`OK: 閉じない強調記号は無い（${files.length} ファイル）`)
