/**
 * 日時の表示（`ApiDesign.md` 2.2 はエポックミリ秒で返す。pb-224）。
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
import { uiLocaleTag } from '../locales/ui'

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
function parts(
  ms: number,
  tz: string | null = timezone,
): { y: string; mo: string; d: string; h: string; mi: string; s: string } | null {
  const t = new Date(ms)
  if (Number.isNaN(t.getTime())) return null
  if (tz === null) {
    return {
      y: String(t.getFullYear()),
      mo: p2(t.getMonth() + 1),
      d: p2(t.getDate()),
      h: p2(t.getHours()),
      mi: p2(t.getMinutes()),
      s: p2(t.getSeconds()),
    }
  }
  const f = new Intl.DateTimeFormat('en-CA', {
    timeZone: tz,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
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
    s: got.second ?? '00',
  }
}

const p2 = (n: number) => String(n).padStart(2, '0')

/** `2026-08-11 09:12` */
export function formatDateTime(ms: number): string {
  const t = parts(ms)
  if (t === null) return String(ms)
  if (uiLocaleTag() === 'en-US') return `${t.mo}/${t.d}/${t.y} ${hour12(t.h)}:${t.mi}:${t.s} ${ampm(t.h)}`
  return `${t.y}-${t.mo}-${t.d} ${t.h}:${t.mi}`
}

/** 監査ログの時刻。保存されたミリ秒を表示し、設定タイムゾーンも反映する。 */
export function formatAuditDateTime(ms: number): string {
  const t = parts(ms)
  if (t === null) return String(ms)
  const milli = String(new Date(ms).getUTCMilliseconds()).padStart(3, '0')
  if (uiLocaleTag() === 'en-US') return `${t.mo}/${t.d}/${t.y} ${hour12(t.h)}:${t.mi}:${t.s}.${milli} ${ampm(t.h)}`
  return `${t.y}-${t.mo}-${t.d} ${t.h}:${t.mi}:${t.s}.${milli}`
}

function hour12(hour: string): number {
  return Number(hour) % 12 || 12
}

function ampm(hour: string): 'AM' | 'PM' {
  return Number(hour) < 12 ? 'AM' : 'PM'
}

/**
 * `<time datetime>` に入れる ISO8601（UTC）。**画面に出す文字列ではない**——表示は
 * `formatDateTime` / `formatDate` を使う。
 */
export function isoOf(ms: number): string {
  const t = new Date(ms)
  return Number.isNaN(t.getTime()) ? '' : t.toISOString()
}

/** `2026-08-11`。時刻に意味がない項目（参加日など）で使う */
export function formatDate(ms: number): string {
  const t = parts(ms)
  if (t === null) return String(ms)
  if (uiLocaleTag() === 'en-US') return `${t.mo}/${t.d}/${t.y}`
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
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  return match && uiLocaleTag() === 'en-US' ? `${match[2]}/${match[3]}/${match[1]}` : date
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
 * その日の0時の瞬間をエポックミリ秒で返す（`GuiDesign.md` 5.13）。形が違えば `null`。
 *
 * **日の境界は `app_user.timezone` で作る**（未設定なら端末のローカル）。チケット検索の期間は
 * `timestamptz`（完了日時・着手日時）を絞るもので、一覧は完了日を `formatDate`（同じ
 * タイムゾーン）で出している。**境界を別の基準で作ると、見えている日付と絞り込みの日付が
 * ずれる。** サーバは日付を解釈しない（`ApiDesign.md` 9.2.1「検索の条件」）。
 *
 * **時差を2回測る。** UTC の0時を仮に置いて時差を引くと、夏時間の切り替わりの前後では
 * 引いた先で時差が変わっていることがある。
 */
export function startOfDayInstant(date: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (m === null) return null
  const y = Number(m[1])
  const mo = Number(m[2]) - 1
  const d = Number(m[3])
  if (timezone === null) {
    const t = new Date(y, mo, d).getTime()
    return Number.isNaN(t) ? null : t
  }
  const guess = Date.UTC(y, mo, d)
  let t = guess - offsetMs(guess)
  t = guess - offsetMs(t)
  return t
}

/** その瞬間の、tz（既定は設定タイムゾーン）での時差（壁時計 − UTC。ミリ秒。分の単位まで） */
function offsetMs(utcMs: number, tz: string | null = timezone): number {
  const p = parts(utcMs, tz)
  if (p === null) return 0
  const wall = Date.UTC(Number(p.y), Number(p.mo) - 1, Number(p.d), Number(p.h), Number(p.mi))
  return wall - Math.floor(utcMs / 60_000) * 60_000
}

// ── 予定日時（pb-217。`GuiDesign.md` 7.5「予定日時の出し方」）──────────────
//
// 予定は `start_at` / `due_at`（エポックミリ秒の半開区間）＋ `all_day` で届く
// （`ApiDesign.md` 9.3.1）。**終日はプロジェクトの基準タイムゾーンの日付**で扱い、
// 見る人のタイムゾーン（`setTimezone`）を通さない——「9/30締切」はプロジェクトの暦の上の
// 日付であり、瞬間として変換すると UTC より西の地域で前日へずれる。

/**
 * 終日の値を基準タイムゾーン `tz` の日付（`YYYY-MM-DD`）にする。
 * **終わり（`isEnd`）は1日戻す**——`due_at` は締切日の翌日の0時である。
 */
export function planDate(ms: number, tz: string, isEnd = false): string {
  const p = parts(isEnd ? ms - 1 : ms, tz)
  return p === null ? '' : `${p.y}-${p.mo}-${p.d}`
}

/**
 * 基準タイムゾーン `tz` の日付を、その0時の瞬間（エポックミリ秒）にする。
 * **終わり（`isEnd`）は翌日の0時**——締切日を含めるための半開区間の終わりである。
 * 形が違えば `null`。
 */
export function planInstant(date: string, tz: string, isEnd = false): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (m === null) return null
  const guess = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + (isEnd ? 1 : 0))
  // 時差を2回測る（startOfDayInstant と同じ。夏時間の切り替わりの前後で時差が変わる）
  let t = guess - offsetMs(guess, tz)
  t = guess - offsetMs(t, tz)
  return t
}

/**
 * 予定の1端を出す。終日なら基準タイムゾーンの日付、時刻付きなら見る人のタイムゾーンの日時。
 * 値が無ければ空文字。
 */
export function formatPlan(ms: number | null | undefined, allDay: boolean, tz: string, isEnd = false): string {
  if (ms === null || ms === undefined) return ''
  return allDay ? formatPlainDate(planDate(ms, tz, isEnd)) : formatDateTime(ms)
}

/** 期限を過ぎたか（`due_at <= いま`。終日も時刻付きも同じ式。`ApiDesign.md` 9.2.1 の `overdue`） */
export function isPastDue(dueAt: number | null | undefined): boolean {
  return dueAt !== null && dueAt !== undefined && dueAt <= Date.now()
}

/**
 * `input[type=datetime-local]` の値（`YYYY-MM-DDTHH:mm`）を、**見る人のタイムゾーン**
 * （`setTimezone`。未設定なら端末）で作る。時刻付きの予定の入力に使う（7.5）。
 */
export function localInputOf(ms: number): string {
  const p = parts(ms)
  return p === null ? '' : `${p.y}-${p.mo}-${p.d}T${p.h}:${p.mi}`
}

/** `localInputOf` の逆。見る人のタイムゾーンの壁時計を瞬間（エポックミリ秒）へ直す。形が違えば `null` */
export function instantOfLocalInput(value: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value)
  if (m === null) return null
  const guess = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]))
  let t = guess - offsetMs(guess)
  t = guess - offsetMs(t)
  return t
}
