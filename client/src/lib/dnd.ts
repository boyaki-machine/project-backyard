/**
 * ドラッグ&ドロップの落とし先（`GuiDesign.md` 6.8）。
 *
 * **ポインタが指した位置で落とし先が決まる。** 掴んだ行がどこから来たかには
 * 依らない——「越えた向き」で決める方式は、行の下側を指しても上に入ることが
 * あり、**出した目印と着地が食い違う**（5.4「ドロップ先の見せ方」）。
 *
 * **判定の正本をここに1つ置く**（pb-28 で切り出した）。同じ規則を使う場所が
 * 3つある——バックログ（5.4）、文書ツリー（5.10）、タグ（5.9.4）。写しを持つと、
 * 同じ操作が画面によって違う場所へ着地する。
 *
 * | 割り方 | 使う場面 | 上 | 中央 | 下 |
 * |---|---|---|---|---|
 * | 2分割（既定） | 親子を持たない一覧（5.9.4） | 上半分＝`before` | — | 下半分＝`after` |
 * | 3分割（`inside: true`） | 親子を1回のドロップで決める木（5.4 / 5.10） | 上 1/4＝`before` | 中央 1/2＝`inside` | 下 1/4＝`after` |
 *
 * **3分割が要るのは、兄弟の順序と親子の2軸を1回のドロップで決めるときだけ**で
 * ある。タグは `sort_order` しか持たないので（`ApiDesign.md` 9.11.1）、中央に
 * 割り当てる行き先が無い。
 */

/** 落とし先。`inside` は「その行の子へ」で、3分割のときだけ返る */
export type DropZone = 'before' | 'after' | 'inside'

/**
 * ポインタが行のどこを指しているか。
 *
 * **測るのは `e.currentTarget` の高さ**なので、呼ぶのは行そのものに張った
 * ハンドラ（`dragover` / `drop`）からである。要素が取れないときは `after` を
 * 返す——ハンドラを張った要素が `currentTarget` に入るので実際には起こらないが、
 * 落とし先を1つに決めておかないと呼び出し側が分岐を持つことになる。
 */
export function zoneOf(e: DragEvent, options: { inside?: boolean } = {}): DropZone {
  const el = e.currentTarget as HTMLElement | null
  if (el === null) return 'after'
  const r = el.getBoundingClientRect()
  const y = e.clientY - r.top

  if (options.inside === true) {
    if (y < r.height / 4) return 'before'
    if (y > (r.height * 3) / 4) return 'after'
    return 'inside'
  }

  return y < r.height / 2 ? 'before' : 'after'
}
