/**
 * ガントの時間軸（`GuiDesign.md` 5.14「時間軸のヘッダ」「拡大縮小」）。
 *
 * **日の区切りはタイムゾーンで決まる。** 基準の段はプロジェクトの基準タイムゾーン、
 * 見る人の段は `app_user.timezone` で区切るので、どちらも「その地域の0時の瞬間」を
 * エポックミリ秒で求めてから位置に直す。`Date` のローカル時刻は使わない——端末の
 * タイムゾーンが混ざると、同じ画面に3つめの暦が現れる。
 *
 * 見本（`docs/design/mock/gantt.html`）の関数を型付きで移したもの。**描き方を変えるときは
 * 5.14 を先に直す**（見本は見え方を確かめるためのもので、正本は 5.14 である）。
 */
import { uiLocaleTag } from '../../locales/ui'

export const DAY = 86_400_000

export interface WallParts {
  y: number
  m: number
  d: number
  h: number
  mi: number
}

const partFormats = new Map<string, Intl.DateTimeFormat>()

/** その瞬間の、tz での壁時計（年月日時分） */
export function wallParts(tz: string, ms: number): WallParts {
  let f = partFormats.get(tz)
  if (f === undefined) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      hourCycle: 'h23',
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: 'numeric',
      minute: 'numeric',
    })
    partFormats.set(tz, f)
  }
  const o: Record<string, string> = {}
  for (const p of f.formatToParts(ms)) o[p.type] = p.value
  return { y: Number(o.year), m: Number(o.month), d: Number(o.day), h: Number(o.hour) % 24, mi: Number(o.minute) }
}

/** tz の時差（壁時計 − UTC。分の単位まで） */
function offsetOf(tz: string, ms: number): number {
  const p = wallParts(tz, ms)
  return Date.UTC(p.y, p.m - 1, p.d, p.h, p.mi) - Math.floor(ms / 60_000) * 60_000
}

const wallCache = new Map<string, number>()

/**
 * tz の壁時計 `y-m-d h:mi` の瞬間。**時差を2回測る**——夏時間の切り替わりの前後では、
 * 1回目に引いた先で時差が変わっていることがある（`datetime.ts` の `planInstant` と同じ）。
 *
 * 月と日は範囲外でもよい（`d = 32` は翌月へ繰り上がる）。**日の頭は何度も引くので覚えておく。**
 */
export function wall(tz: string, y: number, m: number, d: number, h = 0, mi = 0): number {
  const g = Date.UTC(y, m - 1, d, h, mi)
  const key = `${tz}|${g}`
  const hit = wallCache.get(key)
  if (hit !== undefined) return hit
  let t = g - offsetOf(tz, g)
  t = g - offsetOf(tz, t)
  if (wallCache.size > 50_000) wallCache.clear()
  wallCache.set(key, t)
  return t
}

export interface DayTick {
  /** その日の0時の瞬間 */
  t: number
  /** 翌日の0時の瞬間（夏時間の日は 24時間でない） */
  tn: number
  y: number
  m: number
  d: number
  /** 曜日（0＝日曜） */
  wd: number
}

/** `[ta, tb]` に掛かる tz の日を並べる（前後に1日はみ出す） */
export function days(tz: string, ta: number, tb: number): DayTick[] {
  const p = wallParts(tz, ta)
  const out: DayTick[] = []
  for (let i = -1; out.length < 4000; i++) {
    const t = wall(tz, p.y, p.m, p.d + i)
    if (t > tb) break
    const dt = new Date(Date.UTC(p.y, p.m - 1, p.d + i))
    out.push({
      t,
      tn: wall(tz, p.y, p.m, p.d + i + 1),
      y: dt.getUTCFullYear(),
      m: dt.getUTCMonth() + 1,
      d: dt.getUTCDate(),
      wd: dt.getUTCDay(),
    })
  }
  return out
}

const zoneNameFormats = new Map<string, Intl.DateTimeFormat>()

/** `GMT+9` / `PDT` のような短い名前 */
export function tzShort(tz: string, ms: number): string {
  let f = zoneNameFormats.get(tz)
  if (f === undefined) {
    f = new Intl.DateTimeFormat('en-US', { timeZone: tz, timeZoneName: 'short' })
    zoneNameFormats.set(tz, f)
  }
  return f.formatToParts(ms).find((p) => p.type === 'timeZoneName')?.value ?? tz
}

// ── 拡大縮小（5.14「拡大縮小」）─────────────────────────────

export interface ZoomLevel {
  key: 'minute' | 'hour' | 'day' | 'week' | 'month' | 'quarter'
  ppd: number
}

/** 段階。**1日の幅（px）の止まり先**であり、表示の単位そのものではない */
export const ZOOM_LEVELS: readonly ZoomLevel[] = [
  { key: 'minute', ppd: 2880 },
  { key: 'hour', ppd: 480 },
  { key: 'day', ppd: 44 },
  { key: 'week', ppd: 16 },
  { key: 'month', ppd: 5 },
  { key: 'quarter', ppd: 1.6 },
]

export const PPD_MIN = 1.2
export const PPD_MAX = 5760
export const PPD_DAY = 44

/** いまの幅に最も近い段階。どれからも離れていれば `-1`（ボタンを押された形にしない） */
export function nearestLevel(ppd: number): number {
  let best = 0
  let bd = Infinity
  ZOOM_LEVELS.forEach((l, i) => {
    const d = Math.abs(Math.log(l.ppd / ppd))
    if (d < bd) {
      bd = d
      best = i
    }
  })
  return bd < 0.25 ? best : -1
}

/** 段階を1つ進めた先（`dir` が +1 で拡大）。端なら `null` */
export function stepLevel(ppd: number, dir: 1 | -1): ZoomLevel | null {
  if (dir > 0) return [...ZOOM_LEVELS].reverse().find((l) => l.ppd > ppd * 1.03) ?? null
  return ZOOM_LEVELS.find((l) => l.ppd < ppd * 0.97) ?? null
}

// ── 目盛り ───────────────────────────────────────────────────

type TickUnit = ['day'] | ['week'] | ['min', number] | ['month', number] | ['year']
type LabelKind = 'hm' | 'dwd' | 'd' | 'md' | 'm' | 'q' | 'date' | 'ym' | 'y'

export interface Scale {
  maj: TickUnit
  min: TickUnit
  /** 小目盛りの文字 */
  ml: LabelKind
  /** 大目盛りの文字 */
  jl: LabelKind
}

/** 1日の幅から目盛りを選ぶ。**切り替わりは 1200 / 150 / 20 / 7 / 2.2px**（5.14） */
export function scaleOf(ppd: number): Scale {
  const pph = ppd / 24
  if (ppd >= 1200) return { maj: ['day'], min: ['min', 60], ml: 'hm', jl: 'date' }
  if (ppd >= 150) {
    // 文字が 36px 以上離れる最小の間隔
    const st = [1, 2, 3, 6, 12].find((h) => h * pph >= 36) ?? 12
    return { maj: ['day'], min: ['min', st * 60], ml: 'hm', jl: 'date' }
  }
  if (ppd >= 20) return { maj: ['month', 1], min: ['day'], ml: ppd >= 38 ? 'dwd' : 'd', jl: 'ym' }
  if (ppd >= 7) return { maj: ['month', 1], min: ['week'], ml: 'md', jl: 'ym' }
  if (ppd >= 2.2) return { maj: ['year'], min: ['month', 1], ml: 'm', jl: 'y' }
  return { maj: ['year'], min: ['month', 3], ml: 'q', jl: 'y' }
}

export interface Tick {
  t: number
  y: number
  m: number
  d: number
  h: number
  mi: number
  wd: number
}

/** 目盛りの位置（tz の暦で区切る）。**週の頭は月曜**（ISO 8601） */
export function ticks(u: TickUnit, tz: string, ta: number, tb: number): Tick[] {
  const [unit] = u
  const dayTick = (d: DayTick): Tick => ({ t: d.t, y: d.y, m: d.m, d: d.d, h: 0, mi: 0, wd: d.wd })
  if (unit === 'day') return days(tz, ta, tb).map(dayTick)
  if (unit === 'week') return days(tz, ta - 7 * DAY, tb).filter((d) => d.wd === 1).map(dayTick)
  if (unit === 'min') {
    const step = u[1] as number
    const out: Tick[] = []
    for (const d of days(tz, ta, tb)) {
      for (let m = 0; m < 1440; m += step) {
        const t = wall(tz, d.y, d.m, d.d, 0, m)
        if (t >= d.tn) break
        if (t < ta - step * 60_000 || t > tb) continue
        out.push({ t, y: d.y, m: d.m, d: d.d, h: Math.floor(m / 60), mi: m % 60, wd: d.wd })
      }
    }
    return out
  }
  const p = wallParts(tz, ta)
  const out: Tick[] = []
  if (unit === 'month') {
    const step = u[1] as number
    for (let i = -3; out.length < 400; i++) {
      const dt = new Date(Date.UTC(p.y, p.m - 1 + i, 1))
      const y = dt.getUTCFullYear()
      const m = dt.getUTCMonth() + 1
      const t = wall(tz, y, m, 1)
      if (t > tb) break
      if ((m - 1) % step) continue
      out.push({ t, y, m, d: 1, h: 0, mi: 0, wd: dt.getUTCDay() })
    }
    return out
  }
  for (let y = p.y; out.length < 400; y++) {
    const t = wall(tz, y, 1, 1)
    if (t > tb) break
    out.push({ t, y, m: 1, d: 1, h: 0, mi: 0, wd: new Date(Date.UTC(y, 0, 1)).getUTCDay() })
  }
  return out
}

const pad = (n: number) => String(n).padStart(2, '0')

let names: { locale: string; wd: string[]; mon: string[] } | null = null

/** 曜日と月の短い名前。**表示言語に従う**（日本語は `火` / `10月`、英語は `Tue` / `Oct`） */
function localNames(): { wd: string[]; mon: string[] } {
  const locale = uiLocaleTag()
  if (names?.locale !== locale) {
    const wdf = new Intl.DateTimeFormat(locale, { weekday: 'short', timeZone: 'UTC' })
    const mf = new Intl.DateTimeFormat(locale, { month: 'short', timeZone: 'UTC' })
    names = {
      locale,
      // 1970-01-04 は日曜
      wd: Array.from({ length: 7 }, (_, i) => wdf.format(Date.UTC(1970, 0, 4 + i))),
      mon: Array.from({ length: 12 }, (_, i) => mf.format(Date.UTC(2001, i, 1))),
    }
  }
  return names
}

export function weekdayName(wd: number): string {
  return localNames().wd[wd] ?? ''
}

export function tickLabel(kind: LabelKind, x: Tick): string {
  switch (kind) {
    case 'hm':
      return `${pad(x.h)}:${pad(x.mi)}`
    case 'dwd':
      return `${x.d}(${weekdayName(x.wd)})`
    case 'd':
      return `${x.d}`
    case 'md':
      return `${x.m}/${pad(x.d)}`
    case 'm':
      return localNames().mon[x.m - 1] ?? `${x.m}`
    case 'q':
      return `Q${(x.m - 1) / 3 + 1}`
    case 'date':
      return `${x.y}-${pad(x.m)}-${pad(x.d)}(${weekdayName(x.wd)})`
    case 'ym':
      return `${x.y}-${pad(x.m)}`
    case 'y':
      return `${x.y}`
  }
}

/** `MM/DD`（tz の暦） */
export function md(tz: string, t: number): string {
  const p = wallParts(tz, t)
  return `${pad(p.m)}/${pad(p.d)}`
}

/** `YYYY-MM-DD`（tz の暦） */
export function ymd(tz: string, t: number): string {
  const p = wallParts(tz, t)
  return `${p.y}-${pad(p.m)}-${pad(p.d)}`
}

/** `HH:MM`（tz の暦） */
export function hm(tz: string, t: number): string {
  const p = wallParts(tz, t)
  return `${pad(p.h)}:${pad(p.mi)}`
}

export { pad }
