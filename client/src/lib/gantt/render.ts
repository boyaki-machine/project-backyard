/**
 * ガントの本体を SVG の文字列に描く（`GuiDesign.md` 5.14）。
 *
 * **描画ライブラリを入れず、SVG を文字列で組んで1回で差し替える**（判断の記録
 * 「ガントは描画ライブラリを入れず、SVG で自前で描く」）。Vue のテンプレートで要素を
 * 1つずつ持つと、2,000行・4,000本の依存でスクロールのたびに差分を取ることになる。
 *
 * **見えている範囲だけを描く**（5.14「大量の行」）。横は目盛り・塗り・帯を見えている
 * 時間に、縦は行を見えている行に絞る。依存線は、両端の行が見えている範囲を上下
 * どちらか一方へ外れているものを描かない。
 *
 * 描く順は **土日祝 → 格子 → スプリントの枠 → 「今」の線 → 依存線 → バー → ドラッグの影と
 * 取っ手 → 端の札 → ヘッダ → カーソル**（5.14「描く順」）。行に属するもの（格子の行・依存線・帯・札）は
 * ヘッダの下へ潜るよう、ヘッダより下に切り抜く。
 *
 * 見本（`docs/design/mock/gantt.html`）の `render` を移したもの。純粋な関数にして
 * あるので、画面（`GanttPage.vue`）はポインタと拡大縮小の状態だけを持てばよい。
 */
import { DAY, days, hm, md, scaleOf, tickLabel, ticks, tzShort, wallParts, weekdayName, pad } from './time'
import type { DayTick } from './time'
import { spanOf } from './model'
import type { GItem, GRow, GanttLink } from './model'
import { HANDLE_R, handlesOf, isViolated } from './edit'
import type { End, Plan } from './edit'

/** 行の高さ（5.14。バックログの 40px より詰める） */
export const RH = 28
/** スプリント帯の高さ */
export const LANE = 16
/** 左右へ 28px の破線（マイルストーン） */
export const MS_TAIL = 28

export type DayKind = '' | 'sat' | 'sun' | 'hol'

export interface GSprint {
  name: string
  s: number
  e: number
  status: 'planned' | 'active' | 'completed'
}

export interface RenderText {
  /** `配下` */
  under: string
  /** `日付なし` */
  noDate: string
  /** `{n}日` */
  days: (n: number) => string
}

export interface RenderInput {
  W: number
  H: number
  /** 横・縦のスクロール量 */
  sl: number
  st: number
  ppd: number
  T0: number
  T1: number
  rows: GRow[]
  rowOf: Map<number, number>
  items: Map<number, GItem>
  links: GanttLink[]
  sprints: GSprint[]
  dayKind: (d: DayTick) => DayKind
  holidayName: (d: DayTick) => string | null
  baseTz: string
  viewTz: string
  now: number
  hover: number | null
  sel: number | null
  cursor: { x: number; y: number } | null
  /** 帯を見せられる右端（浮かせた詳細ペインの左まで） */
  freeRight: number
  idOf: (seq: number) => string
  text: RenderText
  /** 編集（5.14「編集」）。無ければ閲覧だけの描画になる */
  edit?: EditView
}

/**
 * 編集中の見え方（5.14「送り方と見え方」「依存を引く」）。
 *
 * **`override` の予定で帯と依存線を描き直す**——ドラッグ中も、応答を待つ間も、
 * 行と依存は新しい位置で描く（違反の印もそこで判定する）。元の位置は `ghost` の
 * チケットだけ薄く残す。
 */
export interface EditView {
  override: Map<number, Plan>
  /** 掴んでいるチケット（元の位置を薄い影で残す） */
  ghost: number | null
  /** 応答を待っているチケット（新しい位置を破線で描く） */
  pending: Set<number>
  /** ドラッグの札（新しい期間と「時刻付きになります」） */
  tag: { seq: number; text: string } | null
  /** 依存の取っ手を出すチケット。`hot` はポインタが載っている取っ手 */
  handles: { seq: number; hot: End | null } | null
  /** 依存を引いている線。`ok` が偽なら落とせない相手の上 */
  link: { x0: number; y0: number; x1: number; y1: number; ok: boolean; text: string; ring: { x: number; y: number } | null } | null
}

export interface EdgeHit {
  x: number
  y: number
  w: number
  h: number
  seq: number
}

export interface Geometry {
  /** 見る人の段を出すか（基準と違い、1日が 20px 以上のとき） */
  showV: boolean
  /** 時間軸ヘッダの高さ（スプリント帯を含まない） */
  hdrH: number
  /** 最初の行の上端 */
  top: number
}

export function geometry(baseTz: string, viewTz: string, ppd: number, now: number): Geometry {
  // 名前が違っても、いまの壁時計が同じ（時差が同じ）なら段は要らない
  const a = wallParts(baseTz, now)
  const b = wallParts(viewTz, now)
  const same = baseTz === viewTz || (a.d === b.d && a.h === b.h && a.mi === b.mi)
  const showV = !same && ppd >= 20
  const hdrH = 34 + (showV ? 16 : 0)
  return { showV, hdrH, top: hdrH + LANE }
}

// ── 図形 ─────────────────────────────────────────────────────

const f1 = (v: number) => (Math.round(v * 10) / 10).toString()
const escMap: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }
export const esc = (s: string) => s.replace(/[&<>"]/g, (c) => escMap[c]!)
const L = (x1: number, y1: number, x2: number, y2: number, c: string) =>
  `<line class="${c}" x1="${f1(x1)}" y1="${f1(y1)}" x2="${f1(x2)}" y2="${f1(y2)}"/>`
const R = (x: number, y: number, w: number, h: number, c: string, extra = '') =>
  `<rect class="${c}" x="${f1(x)}" y="${f1(y)}" width="${f1(Math.max(0, w))}" height="${f1(h)}" ${extra}/>`
const TX = (x: number, y: number, s: string, c: string, anchor?: string) =>
  `<text class="${c}" x="${f1(x)}" y="${f1(y)}"${anchor ? ` text-anchor="${anchor}"` : ''}>${esc(s)}</text>`

/** 文字の幅の見積り（等幅の半角 0.61em、全角 1em） */
export const estW = (s: string, fs: number) =>
  [...s].reduce((w, c) => w + (c.charCodeAt(0) > 255 ? fs : fs * 0.61), 0)

function barSVG(x0: number, x1: number, y: number, cat: string): string {
  let o = R(x0, y - 6, x1 - x0, 12, `b ${cat}`)
  if (cat !== 'done') {
    // 四隅の外側 2px に 4px の鉤括弧
    const a = 4
    const l = x0 - 2
    const r = x1 + 2
    const t = y - 8
    const b = y + 8
    o += `<path class="br" d="M${f1(l)},${t + a}V${t}H${f1(l + a)}M${f1(r - a)},${t}H${f1(r)}V${t + a}M${f1(l)},${b - a}V${b}H${f1(l + a)}M${f1(r - a)},${b}H${f1(r)}V${b - a}"/>`
  }
  return o
}

function msSVG(kind: 'start' | 'end', x: number, y: number, cat: string): string {
  const dn = cat === 'done' ? ' done' : ''
  const k = kind === 'start' ? 1 : -1
  return (
    `<path class="ms-tail${dn}" d="M${f1(x)},${y}H${f1(x + k * MS_TAIL)}"/>` +
    `<path class="ms${dn}" d="M${f1(x + k * 5)},${y - 7}H${f1(x)}V${y + 7}H${f1(x + k * 5)}"/>` +
    `<circle class="ms-dot${dn}" cx="${f1(x)}" cy="${y}" r="1.8"/>`
  )
}

/** 配下の期間：製図の寸法線（両端の縦線と、外を向いた矢印） */
function rollSVG(x0: number, x1: number, y: number, done: boolean): string {
  const dn = done ? ' done' : ''
  let o = `<path class="roll${dn}" d="M${f1(x0)},${y - 8}V${y + 8}M${f1(x1)},${y - 8}V${y + 8}"/>`
  if (x1 - x0 >= 16) {
    o += `<path class="roll${dn}" d="M${f1(x0 + 1)},${y}H${f1(x1 - 1)}"/>`
    o += `<path class="roll-h${dn}" d="M${f1(x0 + 0.5)},${y}L${f1(x0 + 7)},${y - 3}L${f1(x0 + 7)},${y + 3}ZM${f1(x1 - 0.5)},${y}L${f1(x1 - 7)},${y - 3}L${f1(x1 - 7)},${y + 3}Z"/>`
  }
  return o
}

function calSVG(kind: DayKind, x: number, y: number, w: number, h: number): string {
  if (kind === 'sat') return R(x, y, w, h, 'c-sat')
  if (kind === 'sun') return R(x, y, w, h, 'c-sun')
  return R(x, y, w, h, 'c-holbg') + R(x, y, w, h, 'c-hol')
}

interface Pos {
  xs: number
  xf: number
  y: number
  top: number
}

/** 依存線（5.14「依存線」）。a が先行、b が後行 */
function depSVG(link: GanttLink, a: Pos, b: Pos, hi: boolean, bad: boolean): string {
  const type = link.link_type
  const blk = type === 'blocks'
  const ty = blk ? 'FS' : type
  const se = ty[0]
  const te = ty[1]
  const x0 = se === 'F' ? a.xf : a.xs
  const y0 = a.y
  const x1 = x0 + (se === 'F' ? 1 : -1) * 9
  const xt = te === 'S' ? b.xs : b.xf
  const yt = b.y
  const ap = te === 'S' ? -1 : 1
  const xq = xt + ap * 9
  const tip = xt + ap * (blk ? 2 : 5)
  let d: string
  let tagX: number
  let tagY: number
  const simple = ty === 'FF' || ty === 'SS' || (ty === 'FS' && x1 <= xq) || (ty === 'SF' && x1 >= xq)
  if (simple) {
    const xm = ty === 'FF' ? Math.max(x1, xq) : ty === 'SS' ? Math.min(x1, xq) : x1
    d = `M${f1(x0)},${y0}H${f1(xm)}V${yt}H${f1(tip)}`
    tagX = xm
    tagY = (y0 + yt) / 2
  } else {
    // 逆向きは、相手の行の境目で折り返す
    const ymid = yt > y0 ? b.top : b.top + RH
    d = `M${f1(x0)},${y0}H${f1(x1)}V${ymid}H${f1(xq)}V${yt}H${f1(tip)}`
    tagX = x1
    tagY = (y0 + ymid) / 2
  }
  const head =
    ap === -1
      ? `M${f1(xt)},${yt}L${f1(xt - 6)},${yt - 3.5}L${f1(xt - 6)},${yt + 3.5}Z`
      : `M${f1(xt)},${yt}L${f1(xt + 6)},${yt - 3.5}L${f1(xt + 6)},${yt + 3.5}Z`
  const ai = link.origin === 'ai_suggested'
  const words: string[] = []
  // 依存に反する配置は札に ⚠ を足す（FS も札を出す）。色だけに頼らない（8.9）
  if (bad) words.push('⚠')
  if (ai) words.push('AI')
  if (ty !== 'FS') words.push(ty)
  if (link.lag_days) words.push(`${link.lag_days > 0 ? '+' : ''}${link.lag_days}d`)
  let tag = ''
  if (words.length) {
    const s = words.join(' ')
    const w = estW(s, 9.5) + 6
    tag = R(tagX - w / 2, tagY - 6.5, w, 13, 'd-tagr', 'rx="2"') + TX(tagX, tagY + 3.4, s, 'd-tagt', 'middle')
  }
  const end = blk
    ? `<path class="d-stop" d="M${f1(xt - ap * 1.5)},${yt - 6}V${yt + 6}"/>`
    : `<path class="d-h" d="${head}"/>`
  return `<g class="dep${ai ? ' ai' : ''}${blk ? ' blk' : ''}${hi ? ' hi' : ''}${bad ? ' bad' : ''}"><path class="d" d="${d}"/><circle class="d-s" cx="${f1(x0)}" cy="${y0}" r="2.4"/>${end}${tag}</g>`
}

/** 帯の形だけ（影に使う）。日付が無ければ何も描かない */
function itemShape(it: GItem, y: number, X: (t: number) => number, cat: string): string {
  if (it.s !== null && it.e !== null) {
    const x0 = X(it.s)
    const x1 = Math.max(X(it.e), x0 + 3)
    return barSVG(x0, x1, y, cat)
  }
  if (it.s !== null) return msSVG('start', X(it.s), y, cat)
  if (it.e !== null) return msSVG('end', X(it.e), y, cat)
  return ''
}

/** 取っ手・ドラッグの札・引いている線（5.14「編集」） */
function editOverlay(
  ed: EditView,
  items: Map<number, GItem>,
  rowOf: Map<number, number>,
  rowY: (i: number) => number,
  X: (t: number) => number,
  fr: number,
): string {
  let o = ''
  if (ed.handles) {
    const it = items.get(ed.handles.seq)
    const i = rowOf.get(ed.handles.seq)
    if (it && i !== undefined && (it.s !== null || it.e !== null)) {
      const xs = X((it.s ?? it.e)!)
      const xf = Math.max(X((it.e ?? it.s)!), it.s !== null && it.e !== null ? xs + 3 : xs)
      const y = rowY(i) + RH / 2
      for (const h of handlesOf(xs, xf, { s: it.s !== null, e: it.e !== null })) {
        o += `<circle class="hdl${ed.handles.hot === h.end ? ' hot' : ''}" cx="${f1(h.x)}" cy="${f1(y)}" r="${HANDLE_R}"/>`
      }
    }
  }
  if (ed.tag) {
    const it = items.get(ed.tag.seq)
    const i = rowOf.get(ed.tag.seq)
    if (it && i !== undefined && (it.s !== null || it.e !== null)) {
      const w = estW(ed.tag.text, 10.5) + 12
      const x = Math.max(4, Math.min(X((it.s ?? it.e)!), fr - w - 4))
      const y = rowY(i) - 12
      o += R(x, y, w, 15, 'dtag', 'rx="3"') + TX(x + 6, y + 11, ed.tag.text, 'dtag-t')
    }
  }
  if (ed.link) {
    const l = ed.link
    const cls = l.ok ? 'ldrag' : 'ldrag no'
    o += L(l.x0, l.y0, l.x1, l.y1, cls)
    if (l.ring) o += `<circle class="lring${l.ok ? '' : ' no'}" cx="${f1(l.ring.x)}" cy="${f1(l.ring.y)}" r="5.5"/>`
    if (l.text) {
      const w = estW(l.text, 10.5) + 12
      let x = l.x1 + 12
      if (x + w > fr - 4) x = l.x1 - 12 - w
      const y = l.y1 + 10
      o += R(x, y, w, 16, l.ok ? 'dtag' : 'dtag no', 'rx="3"') + TX(x + 6, y + 11.5, l.text, l.ok ? 'dtag-t' : 'dtag-t no')
    }
  }
  return o
}

/** 帯の右に出す期間（5.14「帯」）。終日は基準の日付、時刻付きは見る人の時刻 */
export function rangeLabel(it: GItem, baseTz: string, viewTz: string): string {
  if (!it.allDay) {
    const one = (t: number) => `${md(viewTz, t)} ${hm(viewTz, t)}`
    const zone = tzShort(viewTz, (it.s ?? it.e)!)
    if (it.s !== null && it.e !== null) {
      const sameDay = md(viewTz, it.s) === md(viewTz, it.e)
      return `${one(it.s)}–${sameDay ? hm(viewTz, it.e) : one(it.e)} ${zone}`
    }
    return it.s !== null ? `${one(it.s)}– ${zone}` : `–${one(it.e!)} ${zone}`
  }
  if (it.s !== null && it.e !== null) return `${md(baseTz, it.s)}–${md(baseTz, it.e - 1)}`
  return it.s !== null ? `${md(baseTz, it.s)}–` : `–${md(baseTz, it.e! - 1)}`
}

export function render(inp: RenderInput): { svg: string; edges: EdgeHit[] } {
  const { W, H, sl, st, ppd, T0, T1, rows, rowOf, baseTz, viewTz, now, freeRight: fr } = inp
  const ed = inp.edit
  // 編集中の予定で描き直す（行の item は元の値のままなので、ここで差し替える）
  let items = inp.items
  if (ed && ed.override.size > 0) {
    items = new Map(inp.items)
    for (const [seq, p] of ed.override) {
      const it = inp.items.get(seq)
      if (it) items.set(seq, { ...it, s: p.s, e: p.e, allDay: p.allDay, roll: p.s === null && p.e === null ? it.roll : null })
    }
  }
  const live = (it: GItem): GItem => items.get(it.seq) ?? it
  const ta = Math.max(T0 - DAY, T0 + (sl / ppd) * DAY)
  const tb = Math.min(T1 + DAY, T0 + ((sl + W) / ppd) * DAY)
  const X = (t: number) => ((t - T0) / DAY) * ppd - sl
  const { showV, hdrH, top } = geometry(baseTz, viewTz, ppd, now)
  const rowY = (i: number) => top + i * RH - st
  // 見えている行（上下に1行ずつ余分に取る）
  const i0 = Math.max(0, Math.floor(st / RH) - 1)
  const i1 = Math.min(rows.length - 1, Math.ceil((st + H - top) / RH) + 1)
  const sc = scaleOf(ppd)
  let o = ''

  o += `<defs><pattern id="g-hatch" width="5" height="5" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line class="hatch" x1="0" y1="0" x2="0" y2="5"/></pattern>`
  o += `<pattern id="g-hol" width="8" height="8" patternUnits="userSpaceOnUse" patternTransform="rotate(-45)"><line class="hol-line" x1="0" y1="0" x2="0" y2="8"/></pattern>`
  o += `<clipPath id="g-body"><rect x="0" y="${top}" width="${W}" height="${Math.max(0, H - top)}"/></clipPath></defs>`

  // 1. 土日祝（本体とヘッダの小目盛りの行）。**基準タイムゾーンの暦で区切る**（プロジェクトの暦の日付）
  const dayList = ppd >= 2.2 ? days(baseTz, ta, tb) : []
  for (const d of dayList) {
    const k = inp.dayKind(d)
    if (!k) continue
    const x = X(d.t)
    const w = X(d.tn) - x
    const name = inp.holidayName(d)
    o += `<g>${name ? `<title>${esc(name)}</title>` : ''}${calSVG(k, x, hdrH, w, H - hdrH)}</g>`
  }

  // 2. 格子（行に属するものは切り抜く）
  let body = ''
  for (let i = i0; i <= i1; i++) {
    const r = rows[i]!
    const y = rowY(i)
    if (r.kind === 'group') body += R(0, y, W, RH, 'rowgrp')
    else if (r.item.seq === inp.sel) body += R(0, y, W, RH, 'rowsel')
    else if (r.item.seq === inp.hover) body += R(0, y, W, RH, 'rowhi')
  }
  for (let i = i0; i <= Math.min(rows.length, i1 + 1); i++) body += L(0, rowY(i), W, rowY(i), 'g-row')
  o += `<g clip-path="url(#g-body)">${body}</g>`

  const minT = ticks(sc.min, baseTz, ta, tb)
  const majT = ticks(sc.maj, baseTz, ta, tb)
  const majSet = new Set(majT.map((m) => m.t))
  const bodyTop = top
  for (const m of minT) {
    if (majSet.has(m.t)) continue
    const x = X(m.t)
    if (x >= -1 && x <= W + 1) o += L(x, bodyTop, x, H, 'g-min')
  }
  let cross = ''
  for (const m of majT) {
    const x = X(m.t)
    if (x < -1 || x > W + 1) continue
    o += L(x, bodyTop, x, H, 'g-maj')
    // 大目盛りの十字（4行ごと）
    for (let i = i0 - (i0 % 4); i <= i1 + 1; i += 4) {
      const y = rowY(i)
      cross += `M${f1(x - 3.5)},${f1(y)}H${f1(x + 3.5)}M${f1(x)},${f1(y - 3.5)}V${f1(y + 3.5)}`
    }
  }
  if (cross) o += `<path class="g-cross" clip-path="url(#g-body)" d="${cross}"/>`
  // 見る人の0時（本体まで点線）
  if (showV) {
    for (const d of days(viewTz, ta, tb)) {
      const x = X(d.t)
      if (x >= 0 && x <= W) o += L(x, bodyTop, x, H, 'g-vmid')
    }
  }
  // 30分・15分の極薄の点線
  if (ppd >= 1200) {
    const step = ppd >= 3600 ? 15 : 30
    for (const m of ticks(['min', step], baseTz, ta, tb)) {
      if (m.mi === 0) continue
      const x = X(m.t)
      if (x >= 0 && x <= W) o += L(x, bodyTop, x, H, 'g-sub')
    }
  }

  // 3. スプリント（ネオンの枠）
  for (const sp of inp.sprints) {
    const x0 = X(sp.s) + 2
    const x1 = X(sp.e) - 2
    if (x1 < -10 || x0 > W + 10 || x1 <= x0) continue
    const cls = sp.status === 'active' ? ' act' : sp.status === 'planned' ? ' plan' : ''
    o += R(x0, hdrH + 3, x1 - x0, H - hdrH - 7, 'spr' + cls, 'rx="8"')
    const lab = `${sp.name} · ${md(baseTz, sp.s)}–${md(baseTz, sp.e - 1)}`
    const avail = x1 - Math.max(x0, 0) - 16
    const s = estW(lab, 10.5) <= avail ? lab : estW(sp.name, 10.5) <= avail ? sp.name : ''
    if (s) o += TX(Math.max(x0, 0) + 10, hdrH + 12, s, 'sprl' + (sp.status === 'active' ? ' act' : ''))
  }

  // 4. 「今」の線
  const xn = X(now)
  if (xn >= -2 && xn <= W + 2) o += L(xn, hdrH, xn, H, 'g-now')

  // 帯の位置
  const posOf = (it: GItem): Pos | null => {
    const i = rowOf.get(it.seq)
    if (i === undefined || (it.s === null && it.e === null)) return null
    const xs = it.s !== null ? X(it.s) : X(it.e!)
    let xf = it.e !== null ? X(it.e) : X(it.s!)
    if (it.s !== null && it.e !== null && xf - xs < 3) xf = xs + 3
    return { xs, xf, y: rowY(i) + RH / 2, top: rowY(i) }
  }
  const spanPos = (it: GItem, i: number) => {
    const sp = spanOf(it)
    if (!sp) return null
    const xs = sp.s !== null ? X(sp.s) : X(sp.e!)
    let xf = sp.e !== null ? X(sp.e) : X(sp.s!)
    if (xf - xs < 3 && sp.s !== null && sp.e !== null) xf = xs + 3
    return { xs, xf, y: rowY(i) + RH / 2, roll: sp.roll }
  }

  // 画面の外にある帯（端の札）。見えている行だけ
  const edgeOf = new Map<number, 'L' | 'R'>()
  for (let i = i0; i <= i1; i++) {
    const r = rows[i]!
    if (r.kind !== 'ticket') continue
    const ri = live(r.item)
    const p = spanPos(ri, i)
    if (!p) continue
    const lo = p.roll || ri.s !== null ? p.xs : p.xs - MS_TAIL
    const hi = !p.roll && ri.e === null ? p.xf + MS_TAIL : p.xf
    if (hi < 0) edgeOf.set(i, 'L')
    else if (lo > fr) edgeOf.set(i, 'R')
  }

  let rowsSvg = ''
  // 対応線（ポインタを載せた行と選んだ行）
  for (const seq of [inp.hover, inp.sel]) {
    if (seq === null) continue
    const i = rowOf.get(seq)
    const it = items.get(seq)
    if (i === undefined || it === undefined || i < i0 || i > i1) continue
    const p = spanPos(it, i)
    if (!p) continue
    const e = edgeOf.get(i)
    const xe = e === 'R' ? fr - 118 : e === 'L' ? null : p.xs - (!p.roll && it.s === null ? MS_TAIL + 2 : 4)
    if (xe !== null && xe > 6) rowsSvg += L(0, p.y, xe, p.y, 'leader')
  }

  // 5. 依存線
  const focus = inp.hover ?? inp.sel
  const yTop = top - RH
  for (const l of inp.links) {
    const sa = items.get(l.source_seq)
    const tb2 = items.get(l.target_seq)
    if (!sa || !tb2) continue
    const ia = rowOf.get(sa.seq)
    const ib = rowOf.get(tb2.seq)
    if (ia === undefined || ib === undefined) continue
    // 縦：両端が同じ側に外れていれば描かない
    if ((ia < i0 && ib < i0) || (ia > i1 && ib > i1)) continue
    const a = posOf(sa)
    const b = posOf(tb2)
    if (!a || !b) continue
    if (Math.max(a.y, b.y) < yTop || Math.min(a.y, b.y) > H + RH) continue
    if (Math.max(a.xs, a.xf, b.xs, b.xf) < -40 || Math.min(a.xs, a.xf, b.xs, b.xf) > W + 40) continue
    rowsSvg += depSVG(l, a, b, focus === l.source_seq || focus === l.target_seq, isViolated(l, sa, tb2))
  }

  // 6. バー・マイルストーン・寸法線と、その右の文字
  for (let i = i0; i <= i1; i++) {
    const r = rows[i]!
    if (r.kind !== 'ticket') continue
    const it = live(r.item)
    const cat = it.done ? 'done' : it.cat
    const y = rowY(i) + RH / 2
    // 掴んでいるチケットは、元の位置に薄い影を残す（5.14「送り方と見え方」）
    if (ed && ed.ghost === it.seq) {
      const g = inp.items.get(it.seq)
      if (g) rowsSvg += `<g class="ghost">${itemShape(g, y, X, cat)}</g>`
    }
    if (edgeOf.has(i)) continue
    const pend = ed?.pending.has(it.seq) ? ' pend' : ''
    let xEnd: number
    if (it.s !== null && it.e !== null) {
      const x0 = X(it.s)
      let x1 = X(it.e)
      if (x1 - x0 < 3) x1 = x0 + 3
      if (x1 < -60 || x0 > W + 60) continue
      rowsSvg += `<g class="bar${pend}">${barSVG(x0, x1, y, cat)}</g>`
      xEnd = x1 + 7
    } else if (it.s !== null) {
      const x = X(it.s)
      rowsSvg += `<g class="bar${pend}">${msSVG('start', x, y, cat)}</g>`
      xEnd = x + MS_TAIL + 5
    } else if (it.e !== null) {
      const x = X(it.e)
      rowsSvg += `<g class="bar${pend}">${msSVG('end', x, y, cat)}</g>`
      xEnd = x + 6
    } else if (it.roll) {
      const x0 = X(it.roll.s)
      const x1 = X(it.roll.e)
      if (x1 < -60 || x0 > W + 60) continue
      rowsSvg += rollSVG(x0, x1, y, it.done)
      // 文字は線の上に地を敷いて載せ、見えている区間の中央に寄せる（線が画面より長くても読める）
      const id = inp.idOf(it.seq)
      const sub = ` ${inp.text.under} ${md(baseTz, it.roll.s)}–${md(baseTz, it.roll.e - 1)}`
      const w = estW(id + sub, 10.5) + 12
      const va = Math.max(x0, 0)
      const vb = Math.min(x1, fr)
      let lx = vb - va >= w + 24 ? (va + vb) / 2 - w / 2 : x1 + 6
      if (lx === x1 + 6 && lx + w > fr) lx = Math.max(va + 10, 4)
      rowsSvg +=
        R(lx, y - 7, w, 14, 'roll-bg', 'rx="2"') +
        `<text class="lbl${it.done ? ' done' : ''}" x="${f1(lx + 6)}" y="${y + 3.5}"><tspan class="lid">${esc(id)}</tspan><tspan class="lsub">${esc(sub)}</tspan></text>`
      continue
    } else continue
    if (it.overdue) {
      rowsSvg += TX(xEnd, y + 4, '⚠', 'od')
      xEnd += 15
    }
    rowsSvg += `<text class="lbl${it.done ? ' done' : ''}" x="${f1(xEnd)}" y="${y + 3.5}"><tspan class="lid">${esc(inp.idOf(it.seq))}</tspan> ${esc(rangeLabel(it, baseTz, viewTz))}</text>`
  }

  // 7. 端の札（画面の外にある帯）。左は終わりの日付、右は始まりの日付
  const edges: EdgeHit[] = []
  for (const [i, e] of edgeOf) {
    const r = rows[i]!
    if (r.kind !== 'ticket') continue
    const sp = spanOf(live(r.item))!
    const y = rowY(i) + RH / 2
    const when = e === 'L'
      ? sp.e !== null ? md(baseTz, sp.e - 1) : md(baseTz, sp.s!)
      : sp.s !== null ? md(baseTz, sp.s) : md(baseTz, sp.e! - 1)
    const id = inp.idOf(r.item.seq)
    const s = e === 'L' ? `◂ ${id} ${when}` : `${id} ${when} ▸`
    const w = estW(s, 10) + 12
    const x = e === 'L' ? 4 : fr - 4 - w
    if (y < top || y > H) continue
    edges.push({ x, y: y - 8, w, h: 16, seq: r.item.seq })
    rowsSvg += `<g class="edge-g">${R(x, y - 8, w, 16, 'edge', 'rx="3"')}${TX(x + 6, y + 3.5, s, 'edge-t')}</g>`
  }
  // 取っ手・ドラッグの札・引いている線（5.14「編集」）
  if (ed) rowsSvg += editOverlay(ed, items, rowOf, rowY, X, fr)
  o += `<g clip-path="url(#g-body)">${rowsSvg}</g>`

  // ── ヘッダ ──
  o += R(0, 0, W, hdrH, 'h-bg')
  for (const d of dayList) {
    const k = inp.dayKind(d)
    if (!k) continue
    const x = X(d.t)
    const w = X(d.tn) - x
    o += calSVG(k, x, 16, w, 18)
  }
  majT.forEach((m, k) => {
    const x = X(m.t)
    const nx = k + 1 < majT.length ? X(majT[k + 1]!.t) : W + 400
    if (nx < 0 || x > W) return
    if (x >= -1) o += L(x, 0, x, 34, 'h-tmaj')
    const s = tickLabel(sc.jl, m)
    const w = estW(s, 11)
    let lx = Math.max(x + 5, 5)
    if (lx + w > nx - 5) lx = nx - 5 - w
    o += TX(lx, 12, s, 'h-maj')
    if (x >= -4) o += `<path class="g-cross" d="M${f1(x - 3.5)},34H${f1(x + 3.5)}M${f1(x)},30.5V37.5"/>`
  })
  minT.forEach((m, k) => {
    const x = X(m.t)
    if (x < -120 || x > W + 2) return
    const nx = k + 1 < minT.length ? X(minT[k + 1]!.t) : x + (minT.length > 1 ? X(minT[1]!.t) - X(minT[0]!.t) : 80)
    if (!majSet.has(m.t)) o += L(x, 16, x, 34, 'h-tick')
    const s = tickLabel(sc.ml, m)
    if (nx - x >= estW(s, 10.5) + 7) o += TX(x + 4, 29, s, 'h-min')
  })
  o += L(0, 34, W, 34, 'h-line')
  if (showV) {
    o += R(0, 34.5, W, 15.5, 'h-vbg')
    for (const d of days(viewTz, ta, tb)) {
      const x = X(d.t)
      const nx = X(d.tn)
      if (nx < 0 || x > W) continue
      o += L(x, 34, x, 50, 'h-vtick')
      const s = ppd >= 64 ? `${pad(d.m)}/${pad(d.d)}(${weekdayName(d.wd)})` : ppd >= 34 ? `${pad(d.m)}/${pad(d.d)}` : `${d.d}`
      const lx = Math.max(x + 3, 3)
      if (nx - lx >= estW(s, 10) + 3) o += TX(lx, 46, s, 'h-v')
    }
    o += L(0, 50, W, 50, 'h-line')
  }
  o += L(0, hdrH + 0.5, W, hdrH + 0.5, 'h-line')
  o += L(0, top - 0.5, W, top - 0.5, 'h-lane')
  // 「今」：時刻の札は最上段（年月の行）に置き、日付の行と重ねない
  if (xn >= -40 && xn <= W + 40) {
    const tm = hm(viewTz, now)
    const w = estW(tm, 10) + 10
    o += L(xn, 14.5, xn, hdrH, 'g-now') + R(xn - w / 2, 1.5, w, 13, 'now-box', 'rx="2"') + TX(xn, 11.5, tm, 'h-nowt', 'middle')
    o += `<path class="h-now" d="M${f1(xn - 4)},16H${f1(xn + 4)}L${f1(xn)},21Z"/>`
  }

  // カーソル：縦の点線・位置の日付・チケットの札
  const cur = inp.cursor
  if (cur && cur.y >= top && cur.x <= fr) {
    const cx = cur.x
    const t = T0 + ((sl + cx) / ppd) * DAY
    const p = wallParts(baseTz, t)
    o += L(cx, top, cx, H, 'cur-v')
    const step = ppd >= 1200 ? 5 : 15
    const wd = new Date(Date.UTC(p.y, p.m - 1, p.d)).getUTCDay()
    const rd = ppd >= 150
      ? `${pad(p.m)}/${pad(p.d)} ${pad(p.h)}:${pad(p.mi - (p.mi % step))}`
      : `${pad(p.m)}/${pad(p.d)}(${weekdayName(wd)})`
    const rw = estW(rd, 10) + 10
    o += R(Math.min(Math.max(cx - rw / 2, 2), fr - rw - 2), H - 17, rw, 14, 'curr', 'rx="2"')
    o += TX(Math.min(Math.max(cx, rw / 2 + 2), fr - rw / 2 - 2), H - 7, rd, 'curr-t', 'middle')
    const hv = inp.hover !== null ? items.get(inp.hover) : undefined
    if (hv) {
      const ttl = hv.title.length > 26 ? hv.title.slice(0, 25) + '…' : hv.title
      const sp = spanOf(hv)
      const id = inp.idOf(hv.seq)
      const sub = !sp
        ? inp.text.noDate
        : sp.roll
          ? `${inp.text.under} ${md(baseTz, sp.s!)}–${md(baseTz, sp.e! - 1)}`
          : `${rangeLabel(hv, baseTz, viewTz)}${hv.allDay && sp.s !== null && sp.e !== null ? ` · ${inp.text.days(Math.round((sp.e - sp.s) / DAY))}` : ''}`
      const idw = estW(id, 11)
      const w = Math.max(idw + estW(ttl, 11.5) + 8, estW(sub, 10.5)) + 16
      let tx = cx + 14
      let ty = cur.y + 14
      if (tx + w > fr - 4) tx = cx - 14 - w
      tx = Math.max(4, Math.min(tx, fr - 4 - w))
      if (ty + 38 > H - 20) ty = cur.y - 14 - 38
      o += R(tx, ty, w, 38, 'tip', 'rx="4"') + TX(tx + 8, ty + 15, id, 'tip-id') + TX(tx + 16 + idw, ty + 15, ttl, 'tip-t') + TX(tx + 8, ty + 30, sub, 'tip-d')
    }
  }
  return { svg: o, edges }
}
