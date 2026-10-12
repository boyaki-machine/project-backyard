/**
 * タイムラインの時間軸（`GuiDesign.md` 5.15「拡大縮小」）。
 *
 * **ガントの `lib/gantt/time.ts` の最小単位は分である**ので、秒の目盛りと壁時計は
 * ここに持つ（ガントの段階表を変えない）。壁時計の年月日時分はガントの `wallParts` を流用する。
 *
 * **1秒の幅（px）を連続量として持ち、段階はその途中の止まり先とする。** 段階は秒・分・時の3つで、
 * それより引くのは表示期間（1日まで）とホイールで行う（pb-320）。
 * この部品は画面の文言を持たない（純粋な関数だけ。単体テストの対象）。
 */
import { wallParts } from '../../lib/gantt/time'

export const SEC = 1000
export const MIN = 60 * SEC
export const HOUR = 60 * MIN
export const DAY = 24 * HOUR

export type ZoomKey = 'sec' | 'min' | 'hour'

/** 段階（1秒の幅 px）。秒：1秒＝4px、分：1分＝8px、時：1時間＝220px */
export const ZOOMS: Readonly<Record<ZoomKey, number>> = { sec: 4, min: 8 / 60, hour: 220 / 3600 }
export const ZOOM_ORDER: readonly ZoomKey[] = ['sec', 'min', 'hour']
/** 引ける下限。**1日＝320px**——狭い画面でも1日が本体に収まる（5.15「拡大縮小」。pb-320） */
export const PPS_MIN = 320 / 86400
export const PPS_MAX = ZOOMS.sec * 2

/** URL の `zoom` を段階に直す。知らない値・無いときは `min`（既定） */
export function zoomOf(raw: unknown): ZoomKey {
  return raw === 'sec' || raw === 'hour' ? raw : 'min'
}

/** いまの幅に一致する段階。どれでもなければ `null`（ボタンを押された形にしない） */
export function zoomAt(pps: number): ZoomKey | null {
  return ZOOM_ORDER.find((k) => Math.abs(Math.log(ZOOMS[k] / pps)) < 0.02) ?? null
}

/** 目盛りの候補（秒）。1・2・5・10・15・30秒、1・2・5・10・15・30分、1・2・3・6・12時間、1日 */
const STEPS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400]

export interface TickScale {
  /** 小目盛りの間隔（秒）。文字が 48px 以上離れる最小 */
  minor: number
  /** 大目盛りの間隔（秒）。文字が 120px 以上離れる最小で、小目盛りの倍数 */
  major: number
  /** 補助線の間隔（秒）。12px 以上離れる最小で、小目盛りを割り切る */
  fine: number
}

export function tickScale(pps: number): TickScale {
  const minor = STEPS.find((s) => s * pps >= 48) ?? 86400
  const major = STEPS.find((s) => s * pps >= 120 && s % minor === 0) ?? 86400
  const fine = STEPS.find((s) => s * pps >= 12 && minor % s === 0) ?? minor
  return { minor, major, fine }
}

/** tz の時差（壁時計 − UTC、ミリ秒。分の単位まで） */
export function offsetOf(tz: string, ms: number): number {
  const p = wallParts(tz, ms)
  return Date.UTC(p.y, p.m - 1, p.d, p.h, p.mi) - Math.floor(ms / MIN) * MIN
}

/**
 * `[t0, t1]` の目盛り（壁時計の `step` 秒の倍数の瞬間）。**tz の壁時計で揃える**——
 * 時差が30分・45分の地域でも、目盛りは現地の `:00` に来る。
 */
export function wallTicks(tz: string, t0: number, t1: number, stepSec: number): number[] {
  const step = stepSec * SEC
  const out: number[] = []
  let off = offsetOf(tz, t0)
  let t = Math.ceil((t0 + off) / step) * step - off
  while (t <= t1 && out.length < 5000) {
    const o = offsetOf(tz, t)
    if (o !== off) {
      // 夏時間の切り替わり。新しい時差で揃え直す
      off = o
      t = Math.ceil((t + off) / step) * step - off
      continue
    }
    out.push(t)
    t += step
  }
  return out
}

/** 壁時計の秒の位置で割り切れるか（大目盛りの判定） */
export function isWallMultiple(tz: string, t: number, stepSec: number): boolean {
  return (t + offsetOf(tz, t)) % (stepSec * SEC) === 0
}

const pad = (n: number) => String(n).padStart(2, '0')

/** `HH:MM`、秒を出すときは `HH:MM:SS`（tz の壁時計） */
export function clock(tz: string, t: number, withSec = false): string {
  const p = wallParts(tz, t)
  const base = `${pad(p.h)}:${pad(p.mi)}`
  // 時差は分の単位なので、秒は UTC の秒と同じ
  return withSec ? `${base}:${pad(Math.floor((((t % MIN) + MIN) % MIN) / SEC))}` : base
}

/** `M/D`（tz の暦） */
export function monthDay(tz: string, t: number): string {
  const p = wallParts(tz, t)
  return `${p.m}/${p.d}`
}

/** 時刻を吸着の単位（秒）に寄せる（四捨五入）。単位は 1〜900秒で、時差（15分の倍数）と揃う */
export function snapTo(t: number, unitSec: number): number {
  const u = unitSec * SEC
  return Math.round(t / u) * u
}

/** 吸着の単位に切り上げる */
export function ceilTo(t: number, unitSec: number): number {
  const u = unitSec * SEC
  return Math.ceil(t / u) * u
}

// ── 描く範囲と表示期間（5.15「時間軸の範囲」「表示期間」。pb-320）──────────

/** 描く範囲は、見えている幅の前後に何画面ぶん足すか */
export const WINDOW_PAGES = 3

/**
 * 描く範囲。**見えている左端 `left` から1画面（`viewMs`）を真ん中に、前後に3画面ずつ**。
 * チケットの有無で切れない
 */
export function windowRange(left: number, viewMs: number): { t0: number; t1: number } {
  return { t0: left - WINDOW_PAGES * viewMs, t1: left + (WINDOW_PAGES + 1) * viewMs }
}

/**
 * 描き直すか。見えている左端が描いた範囲の最初の1画面に入るか、右端が最後の1画面に入ったら
 */
export function needsRewindow(range: { t0: number; t1: number }, left: number, viewMs: number): boolean {
  return left < range.t0 + viewMs || left + viewMs > range.t1 - viewMs
}

export type SpanKey = '1h' | '3h' | '6h' | '12h' | '1d'

/** 表示期間の選択肢（秒）。並びは画面の `[期間 ▾]` の順 */
export const SPANS: Readonly<Record<SpanKey, number>> = { '1h': 3600, '3h': 10800, '6h': 21600, '12h': 43200, '1d': 86400 }
export const SPAN_ORDER: readonly SpanKey[] = ['1h', '3h', '6h', '12h', '1d']

/** URL の `span` を読む。知らない値・無いときは `null` */
export function spanOf(raw: unknown): SpanKey | null {
  return typeof raw === 'string' && raw in SPANS ? (raw as SpanKey) : null
}

/** URL の `at`（エポックミリ秒）を読む。数でなければ `null` */
export function atOf(raw: unknown): number | null {
  if (typeof raw !== 'string' || !/^\d{10,15}$/.test(raw)) return null
  return Number(raw)
}

/** 期間を本体の幅にちょうど収める1秒の幅。引ける下限・寄れる上限で切る */
export function ppsForSpan(span: SpanKey, bodyW: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, bodyW / SPANS[span]))
}

/** 壁時計の `datetime-local` の値（`YYYY-MM-DDTHH:mm`）と瞬間の行き来（tz の壁時計） */
export function toLocalInput(tz: string, t: number): string {
  const p = wallParts(tz, t)
  return `${p.y}-${pad(p.m)}-${pad(p.d)}T${pad(p.h)}:${pad(p.mi)}`
}

export function fromLocalInput(tz: string, v: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(v)
  if (!m) return null
  const guess = Date.UTC(+m[1]!, +m[2]! - 1, +m[3]!, +m[4]!, +m[5]!)
  // 時差を引いて瞬間にする。夏時間の境目では、引いた後の時差で合わせ直す
  let t = guess - offsetOf(tz, guess)
  t = guess - offsetOf(tz, t)
  return t
}
