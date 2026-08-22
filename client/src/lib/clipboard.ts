/**
 * 1回だけ表示する秘密（初期パスワード・アクセストークン）のコピー。
 *
 * `navigator.clipboard` は安全なコンテキスト（https / localhost）でしか使えず、
 * 権限や利用者の設定で失敗もする。**失敗したら選択だけ済ませて ⌘C に委ねる**。
 * どちらの値もダイアログを閉じると二度と出せないので、コピーできなかったことを
 * 理由に何も渡さない状態にはしない（`GuiDesign.md` 5.6.1 / 5.8.1）。
 *
 * **`GeneratedPasswordDialog` と `IssuedTokenDialog` で共有する。** 本文の作りは
 * 違う（前者は他人に渡すもの、後者は自分のもの）が、この作法は同じである。
 */

/** コピーの結果。`manual` は「自分で選択してコピーしてほしい」状態 */
export type CopyState = 'idle' | 'ok' | 'manual'

/**
 * 要素の中身を選択状態にする。
 *
 * コピーに失敗したときの受け皿。選択しておけば ⌘C（Ctrl+C）で拾える。
 */
export function selectContents(el: HTMLElement | null) {
  if (!el) return
  const range = document.createRange()
  range.selectNodeContents(el)
  const selection = window.getSelection()
  selection?.removeAllRanges()
  selection?.addRange(range)
}

/**
 * 値をクリップボードへ書く。失敗したら `el` を選択して `'manual'` を返す。
 *
 * **例外を投げない。** 呼び出し側は返った状態をそのまま画面に出せばよい。
 */
export async function copySecret(value: string, el: HTMLElement | null): Promise<CopyState> {
  try {
    if (!navigator.clipboard) throw new Error('clipboard unavailable')
    await navigator.clipboard.writeText(value)
    return 'ok'
  } catch {
    selectContents(el)
    return 'manual'
  }
}
