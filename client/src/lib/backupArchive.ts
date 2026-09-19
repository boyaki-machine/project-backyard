/**
 * 書庫（tar.gz）の先頭にある `meta.json` をブラウザで読む（`GuiDesign.md` 5.12.2）。pb-147。
 *
 * **書庫の中身を、押す前に読んで見せるために要る。** ファイル名は利用者が変えられるので、
 * どれを戻そうとしているのかが分からないまま押させない。
 *
 * **先頭だけを読む。** `meta.json` は書庫の1件目に置かれている（`DbDesign.md` 9.1.1）ので、
 * **全体を展開しない**——数百 MB の書庫でも、読むのは数十 KB で足りる。
 *
 * **読めなくても誤りにしない。** 取り込みそのものはサーバが検査する（422）。ここは
 * 「押す前に見せる」ためだけの読み取りであり、**読めなければ見せないだけである。**
 */

/** `meta.json` のうち、画面が使う項目 */
export type BackupMeta = {
  format_version: number
  migration_version: number
  created_at: string
  pb_version: string
}

/** tar の見出しは 512 バイト固定 */
const TAR_BLOCK = 512

/**
 * 展開して読む先頭の量。
 *
 * **`meta.json` は表の数だけ件数を持つ**（44 表で 2KB 程度）。64KB あれば、
 * 表が増えても当分足りる。
 */
const HEAD_BYTES = 64 * 1024

/**
 * 書庫の先頭から `meta.json` を読む。読めなければ `null` を返す。
 */
export async function readBackupMeta(file: File): Promise<BackupMeta | null> {
  try {
    // **圧縮前の先頭 64KB を取るために、圧縮後の先頭を多めに読む。**
    // gzip は縮むので、同じ量を読めば足りる。
    const head = file.slice(0, HEAD_BYTES)
    const plain = await inflateHead(head, HEAD_BYTES)
    if (plain.byteLength < TAR_BLOCK) return null

    const name = readString(plain, 0, 100)
    if (name !== 'meta.json') return null

    // 大きさは 124 バイト目から 12 バイトの8進数（末尾は NUL か空白）
    const size = parseInt(readString(plain, 124, 12).trim() || '0', 8)
    if (!Number.isFinite(size) || size <= 0) return null
    if (plain.byteLength < TAR_BLOCK + size) return null

    const body = new TextDecoder().decode(plain.subarray(TAR_BLOCK, TAR_BLOCK + size))
    const meta = JSON.parse(body) as Partial<BackupMeta>
    if (
      typeof meta.format_version !== 'number' ||
      typeof meta.migration_version !== 'number' ||
      typeof meta.created_at !== 'string' ||
      typeof meta.pb_version !== 'string'
    ) {
      return null
    }
    return meta as BackupMeta
  } catch {
    // **壊れた gzip も、tar でないものも、ここへ落ちる。**
    return null
  }
}

/** gzip の先頭を、最大 limit バイトまで展開する */
async function inflateHead(blob: Blob, limit: number): Promise<Uint8Array> {
  // `DecompressionStream` が無い環境では読めない（見せないだけなので落とさない）
  if (typeof DecompressionStream === 'undefined') return new Uint8Array(0)

  const reader = blob.stream().pipeThrough(new DecompressionStream('gzip')).getReader()
  const chunks: Uint8Array[] = []
  let total = 0
  try {
    while (total < limit) {
      const { done, value } = await reader.read()
      if (done) break
      chunks.push(value)
      total += value.byteLength
    }
  } catch {
    // **途中で切れても、そこまでで読む。** 先頭だけを取っているので、
    // 末尾が欠けた gzip として例外になるのが普通である。
  } finally {
    await reader.cancel().catch(() => undefined)
  }

  const out = new Uint8Array(total)
  let at = 0
  for (const c of chunks) {
    out.set(c, at)
    at += c.byteLength
  }
  return out
}

/** NUL 終端の固定長文字列を読む */
function readString(buf: Uint8Array, offset: number, length: number): string {
  const slice = buf.subarray(offset, offset + length)
  const end = slice.indexOf(0)
  return new TextDecoder().decode(end === -1 ? slice : slice.subarray(0, end))
}
