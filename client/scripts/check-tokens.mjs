// 定義されていないデザイントークン（--pb-*）を数える。`make css-tokens` から呼ぶ（pb-102）。
//
// **CSS の未定義変数はエラーにならず、その宣言ごと無効になる。** 色は継承値のまま、
// 余白は 0 のままになり、型検査もビルドも通るので、読むまで気づかない。
//
// - 定義は `--pb-xxx:` の宣言。正本は styles/tokens.css だが、部品が自分で置く変数も数える
// - `var(--pb-xxx, 既定値)` のように既定値を持つ参照は、未定義でも宣言が効くので数えない
//
// 使い方: node client/scripts/check-tokens.mjs [client/src]
// 1件でも残れば終了コード 1。依存を使わないので npm ci は要らない。
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const root = process.argv[2] ?? 'client/src'

function walk(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return name === 'node_modules' ? [] : walk(path)
    return /\.(vue|css|ts)$/.test(name) ? [path] : []
  })
}

const files = walk(root)
const defined = new Set()
const refs = []

for (const file of files) {
  const lines = readFileSync(file, 'utf8').split('\n')
  lines.forEach((line, i) => {
    for (const m of line.matchAll(/(--pb-[a-z0-9-]+)\s*:/g)) defined.add(m[1])
    for (const m of line.matchAll(/var\(\s*(--pb-[a-z0-9-]+)\s*\)/g)) {
      refs.push({ name: m[1], at: `${relative('.', file)}:${i + 1}` })
    }
  })
}

const missing = refs.filter((r) => !defined.has(r.name))
for (const r of missing) console.log(`${r.at}: ${r.name}`)

if (missing.length > 0) {
  const names = new Set(missing.map((r) => r.name))
  console.log(`NG: 定義されていないトークンが ${names.size} 種 ${missing.length} か所ある（${files.length} ファイル）`)
  console.log('    styles/tokens.css にある名前へ寄せる（GuiDesign.md 8.5）')
  process.exit(1)
}
console.log(`OK: 使っているトークンはすべて定義されている（${files.length} ファイル、${defined.size} 種）`)
