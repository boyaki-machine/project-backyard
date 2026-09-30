/**
 * ガントの編集（`GuiDesign.md` 5.14「編集」）の計算。
 *
 * **画面の状態を持たない純粋な関数だけを置く。** ポインタの追跡と送信は
 * `GanttPage.vue`、描き方は `render.ts` が持つ。ここが決めるのは、吸着・動かした
 * 結果の予定・依存の種別・輪・違反の判定である。
 */
import { DAY, wall, wallParts } from './time'
import type { GItem, GanttLink } from './model'

const MIN = 60_000

/** 端（S＝開始、F＝終了）。依存の種別の1文字目と2文字目に当たる */
export type End = 'S' | 'F'

/** 依存の種別（ドラッグで作れる4種） */
export type DepType = 'FS' | 'SS' | 'FF' | 'SF'

/** 依存を辿る種別（`ApiDesign.md` 9.10.1 の輪の判定と同じ5つ） */
const DEP_TYPES = new Set(['FS', 'SS', 'FF', 'SF', 'blocks'])

// ── 吸着（5.14「吸着」）──────────────────────────────────────

/**
 * 吸着の区切り。**基準タイムゾーンの壁時計で区切る**——日なら基準の0時、1時間なら
 * 基準の毎正時。`dir` は寄せる向き（最寄り・前・後）。`unit` が `null` なら吸着を外し、
 * 1分に丸める（`Alt` を押している間）。
 */
export function snapTime(t: number, tz: string, unit: number | null, dir: 'near' | 'floor' | 'ceil' = 'near'): number {
  if (unit === null) {
    const k = t / MIN
    return (dir === 'floor' ? Math.floor(k) : dir === 'ceil' ? Math.ceil(k) : Math.round(k)) * MIN
  }
  const p = wallParts(tz, t)
  const day0 = wall(tz, p.y, p.m, p.d)
  if (unit >= 1440) {
    const day1 = wall(tz, p.y, p.m, p.d + 1)
    if (t === day0) return day0
    if (dir === 'floor') return day0
    if (dir === 'ceil') return day1
    return t - day0 < day1 - t ? day0 : day1
  }
  // その日の0時からの分（壁時計）。秒以下は、壁時計の分の頭からの差で足す
  const head = wall(tz, p.y, p.m, p.d, p.h, p.mi)
  const m = p.h * 60 + p.mi + (t - head) / MIN
  const k = m / unit
  const n = dir === 'floor' ? Math.floor(k) : dir === 'ceil' ? Math.ceil(k) : Math.round(k)
  return wall(tz, p.y, p.m, p.d, 0, n * unit)
}

/** 基準タイムゾーンの0時ちょうどか */
export function isMidnight(t: number, tz: string): boolean {
  const p = wallParts(tz, t)
  return p.h === 0 && p.mi === 0 && wall(tz, p.y, p.m, p.d) === t
}

// ── 動かした結果（5.14「吸着」「終日と時刻付き」）────────────────

export type EditKind = 'move' | 'start' | 'end' | 'create'

export interface Plan {
  s: number | null
  e: number | null
  allDay: boolean
}

export interface EditOp {
  kind: EditKind
  /** 掴んだときの予定（新規では null） */
  s0: number | null
  e0: number | null
  allDay0: boolean
  /** 掴んだ瞬間のポインタの時刻 */
  t0: number
}

/**
 * ポインタの時刻 `t` での予定。`unit` は吸着の単位（分）、`null` で吸着を外す。
 *
 * - 移動：**掴んだ側の端（バーの中なら開始）を寄せ、長さを保つ**
 * - 端の変更：動かしている端を寄せ、**長さは1単位（吸着を外せば1分）より短くしない**
 * - 新規：なぞった範囲を区切りで包む（前は切り下げ、後ろは切り上げ）
 *
 * **終日は、両端が基準の0時に揃うときだけ終日のまま**にする。時刻付きは時刻付きのまま
 * （逆向きには切り替えない）。新規は0時に揃えば終日。
 */
export function planAt(op: EditOp, t: number, tz: string, unit: number | null): Plan {
  const len = (unit ?? 1) * MIN
  const shift = t - op.t0
  let s = op.s0
  let e = op.e0
  if (op.kind === 'create') {
    const a = Math.min(op.t0, t)
    const b = Math.max(op.t0, t)
    s = snapTime(a, tz, unit, 'floor')
    e = snapTime(b, tz, unit, 'ceil')
    if (e - s < len) e = unit !== null && unit >= 1440 ? snapTime(s + DAY / 2, tz, unit, 'ceil') : s + len
  } else if (op.kind === 'move') {
    if (op.s0 !== null) {
      s = snapTime(op.s0 + shift, tz, unit)
      e = op.e0 !== null ? s + (op.e0 - op.s0) : null
    } else if (op.e0 !== null) {
      e = snapTime(op.e0 + shift, tz, unit)
    }
  } else if (op.kind === 'start' && op.s0 !== null) {
    s = snapTime(op.s0 + shift, tz, unit)
    if (op.e0 !== null && s > op.e0 - len) s = op.e0 - len
  } else if (op.kind === 'end' && op.e0 !== null) {
    e = snapTime(op.e0 + shift, tz, unit)
    if (op.s0 !== null && e < op.s0 + len) e = op.s0 + len
  }
  const startsAllDay = op.kind === 'create' ? true : op.allDay0
  const allDay = startsAllDay && (s === null || isMidnight(s, tz)) && (e === null || isMidnight(e, tz))
  return { s, e, allDay }
}

// ── 掴む場所（5.14「編集」の表）──────────────────────────────

/** バーの端の当たり（端から内側 5px・外側 3px） */
const EDGE_IN = 5
const EDGE_OUT = 3

/**
 * 帯のどこを指しているか。`xs` / `xf` は帯の両端の画面上の位置（マイルストーンは同じ値）。
 * `tail` はマイルストーンの破線の長さ。当たらなければ `null`。
 */
export function grabAt(
  x: number,
  xs: number,
  xf: number,
  has: { s: boolean; e: boolean },
  tail: number,
): EditKind | null {
  if (has.s && has.e) {
    if (x < xs - EDGE_OUT || x > xf + EDGE_OUT) return null
    const w = xf - xs
    // 細いバーは端どうしが重なる。真ん中より左なら開始、右なら終了
    if (w < EDGE_IN * 2 + 2) return x < xs + w / 2 ? 'start' : 'end'
    if (x <= xs + EDGE_IN) return 'start'
    if (x >= xf - EDGE_IN) return 'end'
    return 'move'
  }
  if (has.s) return x >= xs - 6 && x <= xs + tail ? 'move' : null
  if (has.e) return x >= xf - tail && x <= xf + 6 ? 'move' : null
  return null
}

/** 依存の取っ手の位置（帯の両端の外側 9px。依存線の出る点と同じ） */
export const HANDLE_OFFSET = 9
export const HANDLE_R = 3.5

export function handlesOf(xs: number, xf: number, has: { s: boolean; e: boolean }): { end: End; x: number }[] {
  const out: { end: End; x: number }[] = []
  if (has.s) out.push({ end: 'S', x: xs - HANDLE_OFFSET })
  if (has.e) out.push({ end: 'F', x: xf + HANDLE_OFFSET })
  return out
}

/**
 * 依存の入る側（5.14「依存を引く」）。**帯の左半分なら開始、右半分なら終了**——
 * ポインタが指した位置で決める（`dnd.ts` と同じ原則）。マイルストーンは持つ端に決まる。
 */
export function endAt(x: number, xs: number, xf: number, has: { s: boolean; e: boolean }): End {
  if (!has.s) return 'F'
  if (!has.e) return 'S'
  return x < (xs + xf) / 2 ? 'S' : 'F'
}

export function depTypeOf(from: End, to: End): DepType {
  return `${from}${to}` as DepType
}

// ── 依存の輪と違反（5.14「依存の循環」「依存に反する配置」）────────

/**
 * `source → target` を足すと輪になるか。**相手から自分へ辿れるか**で見る
 * （`ApiDesign.md` 9.10.1 と同じ判定。5種を種別を問わず1つのグラフとして辿る）。
 * 取得済みの依存の上だけで判定するので、絞り込みで落ちたチケットを経由する輪は
 * 見えない——そのときはサーバが `link_cycle` で弾く。
 */
export function wouldCycle(links: GanttLink[], source: number, target: number): boolean {
  if (source === target) return true
  const next = new Map<number, number[]>()
  for (const l of links) {
    if (!DEP_TYPES.has(l.link_type)) continue
    const list = next.get(l.source_seq)
    if (list) list.push(l.target_seq)
    else next.set(l.source_seq, [l.target_seq])
  }
  const seen = new Set<number>([target])
  const stack = [target]
  while (stack.length > 0) {
    const cur = stack.pop()!
    for (const n of next.get(cur) ?? []) {
      if (n === source) return true
      if (seen.has(n)) continue
      seen.add(n)
      stack.push(n)
    }
  }
  return false
}

/**
 * 依存に反した配置か。条件は種別の2文字で決まり、`lag_days` は暦日（24時間 × 日数）。
 * **片方の端しか無ければ、線を描くときと同じく在る方の端で比べる。** どちらかに
 * 日付が無ければ判定しない（線も描かない）。`blocks` は FS と同じ。
 */
export function isViolated(link: GanttLink, a: GItem | undefined, b: GItem | undefined): boolean {
  if (!a || !b || !DEP_TYPES.has(link.link_type)) return false
  const ty = link.link_type === 'blocks' ? 'FS' : link.link_type
  const endOf = (it: GItem, end: string): number | null =>
    end === 'S' ? (it.s ?? it.e) : (it.e ?? it.s)
  const from = endOf(a, ty[0]!)
  const to = endOf(b, ty[1]!)
  if (from === null || to === null) return false
  return to < from + link.lag_days * DAY
}
