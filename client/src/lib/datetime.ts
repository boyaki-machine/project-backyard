/**
 * 日時の表示（`ApiDesign.md` 2.2 は ISO8601 UTC で返す）。
 *
 * **`app_user.timezone` に従って出す**（`GuiDesign.md` 7.5。手順19b）。
 * 未設定なら端末のローカル時刻。auth ストアがセッション確定時に `setTimezone`
 * を呼ぶので、**画面側は何も意識しなくてよい**。
 *
 * 従うのは `timestamptz`（その瞬間）を出す2つだけである。`date` 列を出す
 * `formatPlainDate` と、それと比べる `todayPlainDate` は**タイムゾーンを
 * 通さない**——`date` 列はそもそもタイムゾーンを持たない値であり、変換を
 * 通すこと自体が誤りになる（7.5 の表）。
 *
 * 不正な値はそのまま返す。表示のために例外を投げない。
 */

/**
 * 表示に使うタイムゾーン。`null` は端末のローカル。
 *
 * **モジュールの変数に置いてある。** 画面ごとに引き回すと、渡し忘れた場所
 * だけ端末のローカルで出てしまい、同じ画面に2つの時計が並ぶ。
 */
let timezone: string | null = null

/**
 * 表示タイムゾーンを設定する（`GET /me` の `actor.timezone`）。
 *
 * **不正な値は無視してローカルへ落とす。** `Intl.DateTimeFormat` は知らない
 * タイムゾーン名で例外を投げるので、ここで1回だけ試して確かめる。設定画面が
 * 弾いた後の値しか来ない想定だが、**日時がまったく出ない画面を作るよりは
 * ずれた時計を出すほうがよい**。
 */
export function setTimezone(tz: string | null | undefined): void {
  if (tz === null || tz === undefined || tz === '') {
    timezone = null
    return
  }
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz }).format(new Date())
    timezone = tz
  } catch {
    timezone = null
  }
}

/** いま設定されている表示タイムゾーン（検証と設定画面のため） */
export function currentTimezone(): string | null {
  return timezone
}

/**
 * `timestamptz` を年月日時分に分解する。**`timezone` が設定されていればそれで、
 * 無ければ端末のローカルで**読む。
 *
 * **`Intl.DateTimeFormat` の `formatToParts` を使う。** `toLocaleString` の
 * 文字列を切り出すと、ロケールごとの区切りに依存して壊れる。
 */
function parts(iso: string): { y: string; mo: string; d: string; h: string; mi: string } | null {
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return null
  if (timezone === null) {
    return {
      y: String(t.getFullYear()),
      mo: p2(t.getMonth() + 1),
      d: p2(t.getDate()),
      h: p2(t.getHours()),
      mi: p2(t.getMinutes()),
    }
  }
  const f = new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    // **24時間表記を明示する。** en-CA の既定は12時間で、`hour` が `01` の
    // ように出て午前・午後が落ちる。
    hour12: false,
  })
  const got: Record<string, string> = {}
  for (const p of f.formatToParts(t)) got[p.type] = p.value
  if (!got.year || !got.month || !got.day) return null
  return {
    y: got.year,
    mo: got.month,
    d: got.day,
    // `hour12: false` でも 24 を返す実装があるため 0 に丸める。
    h: got.hour === '24' ? '00' : (got.hour ?? '00'),
    mi: got.minute ?? '00',
  }
}

const p2 = (n: number) => String(n).padStart(2, '0')

/** `2026-08-11 09:12` */
export function formatDateTime(iso: string): string {
  const t = parts(iso)
  if (t === null) return iso
  return `${t.y}-${t.mo}-${t.d} ${t.h}:${t.mi}`
}

/** `2026-08-11`。時刻に意味がない項目（参加日など）で使う */
export function formatDate(iso: string): string {
  const t = parts(iso)
  if (t === null) return iso
  return `${t.y}-${t.mo}-${t.d}`
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
 * **`app_user.timezone` にも従わない**（`GuiDesign.md` 7.5）。従わせると、
 * 同じ期限が読み手ごとに違う日付になる。
 *
 * サーバは `YYYY-MM-DD` で返す（`apitime.go` の `Date`）。形が違えばそのまま返す。
 */
export function formatPlainDate(date: string): string {
  return /^\d{4}-\d{2}-\d{2}$/.test(date) ? date : date
}

/**
 * 端末のローカル時刻での今日（`YYYY-MM-DD`）。
 *
 * **`toISOString().slice(0, 10)` を使ってはならない。** UTC へ直すため、
 * UTC より東の地域（Asia/Tokyo を含む）では日付が1日進む。期限超過の判定
 * （`GuiDesign.md` 5.4）は「利用者にとっての今日」と `date` 列を比べるもの
 * なので、ローカルの年月日から組み立てる。
 *
 * **`app_user.timezone` には従わない**（`formatPlainDate` と同じ層。7.5）。
 * サーバの集計も `CURRENT_DATE` で数え、利用者の設定を混ぜない
 * （`ApiDesign.md` 9.13.1）。
 *
 * 比較相手はサーバが返す `YYYY-MM-DD` であり、文字列のまま辞書順で比べられる。
 */
export function todayPlainDate(): string {
  const t = new Date()
  return `${t.getFullYear()}-${p2(t.getMonth() + 1)}-${p2(t.getDate())}`
}

/**
 * `YYYY-MM-DD` に日数を足す（負も可）。形が違えば `null`。
 *
 * **タイムゾーンを通さない。** 暦の上の日付の計算であり、UTC の0時で組み立てて
 * UTC で読むので、どの地域でも同じ答えになる（月末・年末の繰り上がりは `Date.UTC` が行う）。
 */
export function addDaysPlainDate(date: string, days: number): string | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (m === null) return null
  const t = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + days))
  return `${t.getUTCFullYear()}-${p2(t.getUTCMonth() + 1)}-${p2(t.getUTCDate())}`
}

/**
 * その日の0時の瞬間を ISO8601 UTC で返す（`GuiDesign.md` 5.13。pb-66）。形が違えば `null`。
 *
 * **日の境界は `app_user.timezone` で作る**（未設定なら端末のローカル）。チケット検索の期間は
 * `timestamptz`（完了日時・着手日時）を絞るもので、一覧は完了日を `formatDate`（同じ
 * タイムゾーン）で出している。**境界を別の基準で作ると、見えている日付と絞り込みの日付が
 * ずれる。** サーバは日付を解釈しない（`ApiDesign.md` 9.2.1「検索の条件」）。
 *
 * **時差を2回測る。** UTC の0時を仮に置いて時差を引くと、夏時間の切り替わりの前後では
 * 引いた先で時差が変わっていることがある。
 */
export function startOfDayInstant(date: string): string | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (m === null) return null
  const y = Number(m[1])
  const mo = Number(m[2]) - 1
  const d = Number(m[3])
  if (timezone === null) {
    const t = new Date(y, mo, d)
    return Number.isNaN(t.getTime()) ? null : t.toISOString()
  }
  const guess = Date.UTC(y, mo, d)
  let t = guess - offsetMs(guess)
  t = guess - offsetMs(t)
  return new Date(t).toISOString()
}

/** その瞬間の、設定タイムゾーンでの時差（壁時計 − UTC。ミリ秒。分の単位まで） */
function offsetMs(utcMs: number): number {
  const p = parts(new Date(utcMs).toISOString())
  if (p === null) return 0
  const wall = Date.UTC(Number(p.y), Number(p.mo) - 1, Number(p.d), Number(p.h), Number(p.mi))
  return wall - Math.floor(utcMs / 60_000) * 60_000
}
