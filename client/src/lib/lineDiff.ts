/**
 * 行単位の差分（計画の版の見比べ。`GuiDesign.md` 5.5「自律実行」。pb-274）。
 *
 * **依存を足さない。** 骨格（JSON を整形したもの）と説明は数十〜数百行なので、
 * 最長共通部分列の表（行数の積）で足りる。大きすぎるときは比べずに丸ごと出す。
 */
export type DiffLine = { kind: 'same' | 'add' | 'del'; text: string }

/** 比べる行数の積の上限。超えたら、古い側を全部削除・新しい側を全部追加として返す */
const MAX_CELLS = 400_000

export function lineDiff(before: string, after: string): DiffLine[] {
  const a = before === '' ? [] : before.split('\n')
  const b = after === '' ? [] : after.split('\n')
  if (a.length * b.length > MAX_CELLS) {
    return [
      ...a.map((text) => ({ kind: 'del' as const, text })),
      ...b.map((text) => ({ kind: 'add' as const, text })),
    ]
  }
  // lcs[i][j] = a[i..] と b[j..] の最長共通部分列の長さ
  const lcs: number[][] = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0))
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: 'same', text: a[i] })
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: 'del', text: a[i++] })
    } else {
      out.push({ kind: 'add', text: b[j++] })
    }
  }
  while (i < a.length) out.push({ kind: 'del', text: a[i++] })
  while (j < b.length) out.push({ kind: 'add', text: b[j++] })
  return out
}
