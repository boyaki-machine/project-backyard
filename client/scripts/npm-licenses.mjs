// バンドルに入った npm パッケージのライセンスを JSON に書き出す。`make licenses` から呼ぶ。
//
// **package.json の依存ではなく、バンドルに実際に入ったものだけを拾う。** vite・typescript の
// ようにビルドにしか使わないものは配布物に入らない。拾うのは Vite 8 の build.license で、
// バンドルに入ったモジュールの持ち主のパッケージと、その LICENSE の本文を返す。
//
// **本番のビルドとは別の一時ディレクトリへビルドする。** client/dist に JSON を残すと、
// 実行ファイルへ埋め込まれて配信されてしまう。
//
// 使い方: node client/scripts/npm-licenses.mjs <出力する JSON>
// 出力は [{ name, version, identifier, text }] の配列。npm ci 済みであること。
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { build } from 'vite'

const out = process.argv[2]
if (!out) {
  console.error('使い方: node client/scripts/npm-licenses.mjs <出力する JSON>')
  process.exit(2)
}

const root = fileURLToPath(new URL('..', import.meta.url))
const dir = mkdtempSync(join(tmpdir(), 'pb-npm-licenses-'))
try {
  await build({
    root,
    logLevel: 'error',
    build: {
      outDir: dir,
      emptyOutDir: true,
      license: { fileName: 'licenses.json' },
    },
  })
  writeFileSync(out, readFileSync(join(dir, 'licenses.json')))
} finally {
  rmSync(dir, { recursive: true, force: true })
}
