/**
 * 日時の表示（`ApiDesign.md` 2.2 は ISO8601 UTC で返す）。
 *
 * ここでは端末のローカル時刻へ直して出す。`app_user.timezone` の反映は
 * 自分の設定（手順15）で扱う。**その時にここだけを直せばよい**ように、
 * 画面ごとに書かず1か所に置く。
 *
 * 不正な値はそのまま返す。表示のために例外を投げない。
 */

function parts(iso: string): { y: number; mo: number; d: number; h: number; mi: number } | null {
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return null
  return {
    y: t.getFullYear(),
    mo: t.getMonth() + 1,
    d: t.getDate(),
    h: t.getHours(),
    mi: t.getMinutes(),
  }
}

const p2 = (n: number) => String(n).padStart(2, '0')

/** `2026-08-11 09:12` */
export function formatDateTime(iso: string): string {
  const t = parts(iso)
  if (t === null) return iso
  return `${t.y}-${p2(t.mo)}-${p2(t.d)} ${p2(t.h)}:${p2(t.mi)}`
}

/** `2026-08-11`。時刻に意味がない項目（参加日など）で使う */
export function formatDate(iso: string): string {
  const t = parts(iso)
  if (t === null) return iso
  return `${t.y}-${p2(t.mo)}-${p2(t.d)}`
}

/**
 * `2026-08-11`。**`date` 列（時刻を持たない日付）専用**（`ApiDesign.md` 9.12）。
 *
 * **`formatDate` に通してはならない。** `new Date('2026-08-19')` は仕様上
 * **UTC の 0時**として解釈される一方、`getDate()` は端末のローカル時刻を返す。
 * UTC より西の地域では前日へずれる（`America/New_York` で `2026-08-18` になる）。
 * スプリントの期限は「その日」であって「その瞬間」ではないので、
 * **タイムゾーンの変換を通さずそのまま出す。**
 *
 * サーバは `YYYY-MM-DD` で返す（`apitime.go` の `Date`）。形が違えばそのまま返す。
 */
export function formatPlainDate(date: string): string {
  return /^\d{4}-\d{2}-\d{2}$/.test(date) ? date : date
}
