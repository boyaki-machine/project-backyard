// バンドルに入った npm パッケージのライセンスを JSON に書き出す。`make licenses` から呼ぶ。
//
// **package.json の依存ではなく、バンドルに実際に入ったものだけを拾う。** vite・typescript の
// ようにビルドにしか使わないものは配布物に入らない。拾うのは Vite 8 の build.license で、
// バンドルに入ったモジュールの持ち主のパッケージと、その LICENSE の本文を返す。
//
// **ビルドの道具が注入するコードは、ここで足す。** build.license は、ビルドの中で作られる
// モジュール（ID が \0 で始まる仮想モジュール）を数えない。だが中身は道具のコード
// （preload の補助・SFC の補助関数・バンドラの実行時の補助）で、バンドルに入る。
// 持ち主を virtualOwners で対応づけ、**知らない仮想モジュールが現れたら失敗する**
// ——黙って漏らさず、対応づけを足すかどうかを人に決めさせる。
//
// **本番のビルドとは別の一時ディレクトリへビルドする。** client/dist に JSON を残すと、
// 実行ファイルへ埋め込まれて配信されてしまう。
//
// 使い方: node client/scripts/npm-licenses.mjs <出力する JSON>
// 出力は [{ name, version, identifier, text }] の配列。npm ci 済みであること。
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { build } from 'vite'

const out = process.argv[2]
if (!out) {
  console.error('使い方: node client/scripts/npm-licenses.mjs <出力する JSON>')
  process.exit(2)
}

const root = fileURLToPath(new URL('..', import.meta.url))

// 仮想モジュールの ID と、そのコードの持ち主のパッケージ。from は解決の起点
// （rolldown は client の直接の依存ではなく、vite が使うものを数える）。
const virtualOwners = [
  { id: /^\0vite\//, pkg: 'vite', from: '.' },
  { id: /^\0plugin-vue:/, pkg: '@vitejs/plugin-vue', from: '.' },
  { id: /^\0rolldown\//, pkg: 'rolldown', from: 'vite' },
]

// **Vite の LICENSE.md は、自身の MIT のあとに、Vite が内部に抱える依存のライセンスを続ける。**
// そちらはビルド時に動くコードで、バンドルには入らない。見出しから先を落とす。
const bundledDepsHeading = '\n# Licenses of bundled dependencies'

const virtualIds = new Set()
const collectVirtual = {
  name: 'pb:collect-virtual-modules',
  generateBundle(_, bundle) {
    for (const chunk of Object.values(bundle)) {
      if (chunk.type !== 'chunk') continue
      for (const id of chunk.moduleIds) if (id.startsWith('\0')) virtualIds.add(id)
    }
  },
}

function packageDir(pkg, from) {
  const base = from === '.' ? join(root, 'package.json') : packageDir(from, '.') + '/package.json'
  return dirname(createRequire(base).resolve(`${pkg}/package.json`))
}

function toolEntry(pkg, from) {
  const dir = packageDir(pkg, from)
  const { name, version, license } = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'))
  const file = readdirSync(dir).find((f) => /^(licen[cs]e|copying)/i.test(f))
  let text = file ? readFileSync(join(dir, file), 'utf8') : ''
  const cut = text.indexOf(bundledDepsHeading)
  if (cut >= 0) text = text.slice(0, cut)
  return { name, version, identifier: license, text: text.trim() }
}

// process.exit は finally を飛ばして一時ディレクトリを残すので、失敗は印だけ付けて抜ける。
let failed = false
const dir = mkdtempSync(join(tmpdir(), 'pb-npm-licenses-'))
try {
  await build({
    root,
    logLevel: 'error',
    plugins: [collectVirtual],
    build: {
      outDir: dir,
      emptyOutDir: true,
      license: { fileName: 'licenses.json' },
    },
  })
  const entries = JSON.parse(readFileSync(join(dir, 'licenses.json'), 'utf8'))

  const unknown = [...virtualIds].filter((id) => !virtualOwners.some((o) => o.id.test(id)))
  if (unknown.length > 0) {
    console.error('NG: 持ち主の分からない仮想モジュールがバンドルに入った')
    for (const id of unknown) console.error('    ' + JSON.stringify(id))
    console.error('    直す: client/scripts/npm-licenses.mjs の virtualOwners に持ち主を足す')
    failed = true
  }
  for (const o of virtualOwners) {
    if (![...virtualIds].some((id) => o.id.test(id))) continue
    const e = toolEntry(o.pkg, o.from)
    if (!entries.some((x) => x.name === e.name && x.version === e.version)) entries.push(e)
  }

  if (!failed) writeFileSync(out, JSON.stringify(entries, null, 2))
} finally {
  rmSync(dir, { recursive: true, force: true })
}
if (failed) process.exit(1)
