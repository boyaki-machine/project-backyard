/**
 * タイムラインの時間軸（`GuiDesign.md` 5.15「拡大縮小」）。
 *
 * **ガントの `lib/gantt/time.ts` の最小単位は分である**ので、秒の目盛りと壁時計は
 * ここに持つ（ガントの段階表を変えない）。壁時計の年月日時分はガントの `wallParts` を流用する。
 *
 * **1秒の幅（px）を連続量として持ち、段階はその途中の止まり先とする。** 日以上の段階を持たない。
 * この部品は画面の文言を持たない（純粋な関数だけ。単体テストの対象）。
 */
import { wallParts } from '../../lib/gantt/time'

export const SEC = 1000
export const MIN = 60 * SEC
export const HOUR = 60 * MIN

export type ZoomKey = 'sec' | 'min' | 'hour'

/** 段階（1秒の幅 px）。秒：1秒＝4px、分：1分＝8px、時：1時間＝220px */
export const ZOOMS: Readonly<Record<ZoomKey, number>> = { sec: 4, min: 8 / 60, hour: 220 / 3600 }
export const ZOOM_ORDER: readonly ZoomKey[] = ['sec', 'min', 'hour']
export const PPS_MIN = ZOOMS.hour / 2
export const PPS_MAX = ZOOMS.sec * 2

/** URL の `zoom` を段階に直す。知らない値・無いときは `min`（既定） */
export function zoomOf(raw: unknown): ZoomKey {
  return raw === 'sec' || raw === 'hour' ? raw : 'min'
}

/** いまの幅に一致する段階。どれでもなければ `null`（ボタンを押された形にしない） */
export function zoomAt(pps: number): ZoomKey | null {
  return ZOOM_ORDER.find((k) => Math.abs(Math.log(ZOOMS[k] / pps)) < 0.02) ?? null
}

/** 目盛りの候補（秒）。1・2・5・10・15・30秒、1・2・5・10・15・30分、1・2時間 */
const STEPS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200]

export interface TickScale {
  /** 小目盛りの間隔（秒）。文字が 48px 以上離れる最小 */
  minor: number
  /** 大目盛りの間隔（秒）。文字が 120px 以上離れる最小で、小目盛りの倍数 */
  major: number
  /** 補助線の間隔（秒）。12px 以上離れる最小で、小目盛りを割り切る */
  fine: number
}

export function tickScale(pps: number): TickScale {
  const minor = STEPS.find((s) => s * pps >= 48) ?? 7200
  const major = STEPS.find((s) => s * pps >= 120 && s % minor === 0) ?? Math.max(minor, 7200)
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

/**
 * 時間軸の範囲。**描くバーの最も早い時刻から最も遅い時刻までに前後 30分を足し、「いま」を必ず含める**。
 * バーが無ければ「いま」の前後 1時間。
 */
export function axisRange(times: readonly number[], now: number): { t0: number; t1: number } {
  if (times.length === 0) return { t0: now - HOUR, t1: now + HOUR }
  let lo = now
  let hi = now
  for (const t of times) {
    if (t < lo) lo = t
    if (t > hi) hi = t
  }
  return { t0: lo - 30 * MIN, t1: hi + 30 * MIN }
}
