/**
 * ガントの Excel 出力（`GuiDesign.md` 5.14「Excel 出力」。pb-223）。
 *
 * **日付は日時の値、塗りは Excel の関数（条件付き書式）で決める。** Excel で開始・終了や
 * 休日を直すと、バー・土日祝・親の期間・判定が追随する（2026-09-30 利用者の判断）。
 * 依存線と blocks の囲みは図形で描く（出力した時点の位置）。
 *
 * **このモジュールは押したときだけ読み込む**（`GanttPage.vue` が動的 `import()` で呼ぶ）。
 * ガントを開いたときの読み込みを増やさないため、画面の側からは静的に import しない。
 *
 * **日付の関数（`time.ts`）と文言の関数（`uiText`）は画面から受け取る**（`env`）。ここで
 * 静的に import すると、それらが引く `vue-i18n` → Vue の本体まで「画面とこのモジュールの
 * 共有物」になり、ビルドが Vue の本体を別のファイル（65KB）へ切り出す——ガントを開いたときの
 * 読み込みが2ファイルに増える（pb-223 で実測）。**`edit.ts` も同じ理由で import しない**
 * （`time.ts` を引く）。型だけの import は消えるので構わない。
 */
import type { Hue } from '../../stores/ui'
import { buildXlsx, cellRef, colName, serial, sheetRef } from '../xlsx'
import type { XAnchor, XBook, XCell, XCol, XCondRule, XRow, XShape, XSheet, XStyle } from '../xlsx'
import { paletteOf, overWhite } from './excelPalette'
import type { GItem, GRow, GanttLink } from './model'
import type { DayKind, GSprint } from './render'
import type * as TimeModule from './time'

/** 画面から受け取る関数。`time.ts` の関数と `uiText` をそのまま渡す */
export interface ExcelEnv {
  time: Pick<typeof TimeModule, 'days' | 'hm' | 'tzShort' | 'wall' | 'wallParts' | 'weekdayName' | 'ymd'>
  text: (source: string, params?: Record<string, string | number>) => string
}

export interface GanttExcelInput {
  env: ExcelEnv
  projectKey: string
  rows: GRow[]
  links: GanttLink[]
  sprints: GSprint[]
  /** 休日（祝日と手動の休日。週末だけの日は含めない）。基準タイムゾーンの `YYYY-MM-DD` */
  holidays: { day: string; name: string }[]
  dayKind: (d: TimeModule.DayTick) => DayKind
  /** 土日祝の塗り方の慣習（5.14「土日祝」）。日本・韓国は east */
  calStyle: 'east' | 'other'
  baseTz: string
  viewTz: string
  now: number
  hue: Hue
  idOf: (seq: number) => string
  /** 状態の表示名（ワークフローの名前） */
  statusText: (it: GItem) => string
  /** 上限で打ち切られていれば、表示した件数と全件数 */
  truncated: { shown: number; total: number } | null
}

/** 期間の上限（5.14。3年） */
export const MAX_DAYS = 1096
/** 前後の余白 */
const MARGIN_DAYS = 7
/** 左の列の数（ID・タイトル・状態・開始・終了・先行・区分・終日） */
const LEFT = 8
const COL = { id: 0, title: 1, status: 2, start: 3, end: 4, pred: 5, cat: 6, allDay: 7 }
/** 見出しの行の数（表題・年月・日・曜日・スプリント／列の見出し） */
const HEAD = 5
/** 本表の行の高さ（pt）。図形の縦の位置はこの値から決める */
const ROW_PT = 18
const EMU_PT = 12700
/** 休日タブの名前付き範囲 */
const HOLIDAY_NAME = 'HolidayList'
const HOLIDAY_ROWS = 1000

const WHITE = '#ffffff'
const FMT_DATE = 'yyyy-mm-dd'
const FMT_TIME = 'yyyy-mm-dd hh:mm'
const DAY_MS = 86_400_000

/** 期間（基準タイムゾーンの日の頭で揃える）。予定の全体＋前後7日、今日を含む */
export function exportRange(env: ExcelEnv, rows: GRow[], now: number, tz: string): { t0: number; t1: number; cut: boolean } {
  const { wall, wallParts } = env.time
  let lo = now
  let hi = now
  for (const r of rows) {
    if (r.kind !== 'ticket') continue
    const it = r.item
    for (const v of [it.s, it.e, it.roll?.s ?? null, it.roll?.e ?? null]) {
      if (v === null) continue
      if (v < lo) lo = v
      if (v > hi) hi = v
    }
  }
  const a = wallParts(tz, lo)
  const b = wallParts(tz, hi - 1)
  const t0 = wall(tz, a.y, a.m, a.d - MARGIN_DAYS)
  let t1 = wall(tz, b.y, b.m, b.d + 1 + MARGIN_DAYS)
  const cap = wall(tz, a.y, a.m, a.d - MARGIN_DAYS + MAX_DAYS)
  const cut = t1 > cap
  if (cut) t1 = cap
  return { t0, t1, cut }
}

/** 瞬間を基準タイムゾーンの壁時計のシリアル値にする（秒は落とす） */
function wallSerial(env: ExcelEnv, tz: string, t: number): number {
  const p = env.time.wallParts(tz, t)
  return serial(Date.UTC(p.y, p.m - 1, p.d, p.h, p.mi))
}

/** 依存に反した配置か（5.14「依存に反する配置」。`edit.ts` の `isViolated` と同じ規則） */
function violated(l: GanttLink, a: GItem, b: GItem): boolean {
  const ty = l.link_type === 'blocks' ? 'FS' : l.link_type
  const endOf = (it: GItem, end: string): number | null => (end === 'S' ? (it.s ?? it.e) : (it.e ?? it.s))
  const from = endOf(a, ty[0]!)
  const to = endOf(b, ty[1]!)
  if (from === null || to === null) return false
  return to < from + l.lag_days * DAY_MS
}

export function buildGanttBook(inp: GanttExcelInput): XBook {
  const { days, hm, tzShort, weekdayName, ymd } = inp.env.time
  // 名前を uiText に揃える——i18n の検査（scripts/check-i18n.mjs）は uiText('…') の日本語を訳の対象として数える
  const uiText = inp.env.text
  const P = paletteOf(inp.hue)
  const { t0, t1, cut } = exportRange(inp.env, inp.rows, inp.now, inp.baseTz)
  const dayList = days(inp.baseTz, t0, t1 - 1).filter((d) => d.t >= t0 && d.t < t1)
  const nDays = dayList.length
  const mainName = `${inp.projectKey} ${uiText('ガント')}`
  const holName = uiText('休日')
  const relName = uiText('関係')
  const M = sheetRef(mainName)

  // ── 本表の列 ──
  const cols: XCol[] = [
    { width: 13 }, { width: 42 }, { width: 12 }, { width: 17 }, { width: 17 }, { width: 22 },
    { width: 11, hidden: true }, { width: 6, hidden: true },
  ]
  for (let i = 0; i < nDays; i++) cols.push({ width: 3.4 })

  const rows: XRow[] = []
  const merges: NonNullable<XSheet['merges']> = []
  const row = (r: number): XRow => {
    while (rows.length <= r) rows.push({ cells: new Map() })
    return rows[r]!
  }
  const put = (r: number, c: number, cell: XCell) => row(r).cells.set(c, cell)
  const text = (color: string): XStyle => ({ font: { color } })

  // ── 1行目：表題・注記・凡例 ──
  const notes: string[] = []
  if (inp.truncated) notes.push(uiText('先頭の {value0}件だけを出力しています（全{value1}件）', { value0: inp.truncated.shown, value1: inp.truncated.total }))
  if (cut) notes.push(uiText('期間が {value0}日を超えるため、先頭の {value0}日だけを出力しています', { value0: MAX_DAYS }))
  put(0, 0, {
    v:
      `${mainName}　${uiText('出力')} ${ymd(inp.viewTz, inp.now)} ${hm(inp.viewTz, inp.now)} ${tzShort(inp.viewTz, inp.now)}` +
      `　${uiText('基準')} ${inp.baseTz}` +
      (notes.length ? `　${notes.join('　')}` : '') +
      `　｜　${uiText('線：FS 実線／SS 破線／FF 点線／SF 一点鎖線（矢印が後行）・blocks は後行と配下をシアンの枠で囲む・赤は依存に反する配置・線・枠・今日の線は出力した時点の位置')}`,
    s: { font: { bold: true, color: P.text } },
  })
  row(0).height = 20

  // ── 2〜4行目：年月・日（日付の値）・曜日（式）──
  put(1, 0, { v: `${uiText('基準')} ${inp.baseTz}`, s: text(P.muted) })
  let m0 = 0
  dayList.forEach((d, i) => {
    const next = dayList[i + 1]
    if (next && next.y === d.y && next.m === d.m) return
    const c0 = LEFT + m0
    const c1 = LEFT + i
    put(1, c0, { v: `${d.y}-${String(d.m).padStart(2, '0')}`, s: { font: { bold: true, color: P.text } } })
    if (c1 > c0) merges.push({ r0: 1, c0, r1: 1, c1 })
    m0 = i + 1
  })
  const center = { h: 'center' as const, v: 'center' as const }
  const wdNames = [0, 1, 2, 3, 4, 5, 6].map((w) => `"${weekdayName(w)}"`).join(',')
  const hol = uiText('祝')
  const holSet = new Set(inp.holidays.map((h) => h.day))
  dayList.forEach((d, i) => {
    const c = LEFT + i
    const dref = cellRef(2, c)
    put(2, c, { v: serial(Date.UTC(d.y, d.m - 1, d.d)), s: { font: { color: P.text }, align: center, numFmt: 'd' } })
    const key = `${d.y}-${String(d.m).padStart(2, '0')}-${String(d.d).padStart(2, '0')}`
    put(3, c, {
      f: `IF(COUNTIF(${HOLIDAY_NAME},${dref})>0,"${hol}",CHOOSE(WEEKDAY(${dref}),${wdNames}))`,
      v: holSet.has(key) ? hol : weekdayName(d.wd),
      s: { font: { color: P.muted }, align: center },
    })
  })

  // ── 5行目：列の見出しとスプリントの帯 ──
  const headCell: XStyle = { font: { bold: true, color: P.text }, fill: { pattern: 'solid', fg: P.head }, border: { bottom: { style: 'thin', color: P.grid } } }
  ;[uiText('ID'), uiText('タイトル'), uiText('状態'), uiText('開始'), uiText('終了'), uiText('先行'), uiText('区分'), uiText('終日')].forEach((v, c) => put(4, c, { v, s: headCell }))
  const taken = new Set<number>()
  for (const sp of inp.sprints) {
    const idx = dayList.map((d, i) => (d.t < sp.e && d.tn > sp.s ? i : -1)).filter((i) => i >= 0)
    if (idx.length === 0 || idx.some((i) => taken.has(i))) continue
    idx.forEach((i) => taken.add(i))
    const color = sp.status === 'active' ? P.neonAct : P.neon
    const side = { style: sp.status === 'active' ? ('medium' as const) : sp.status === 'planned' ? ('dashed' as const) : ('thin' as const), color }
    const c0 = LEFT + idx[0]!
    const c1 = LEFT + idx[idx.length - 1]!
    for (let c = c0; c <= c1; c++) {
      put(4, c, {
        v: c === c0 ? sp.name : undefined,
        s: {
          font: { bold: sp.status === 'active', color: sp.status === 'active' ? P.neonAct : P.muted },
          border: { top: side, bottom: side, ...(c === c0 ? { left: side } : {}), ...(c === c1 ? { right: side } : {}) },
          align: { v: 'center' },
        },
      })
    }
    if (c1 > c0) merges.push({ r0: 4, c0, r1: 4, c1 })
  }

  // ── 6行目〜：チケット ──
  const preds = new Map<number, string[]>()
  for (const l of inp.links) {
    const lag = l.lag_days ? ` ${l.lag_days > 0 ? '+' : ''}${l.lag_days}d` : ''
    const list = preds.get(l.target_seq) ?? []
    list.push(`${l.link_type} ${inp.idOf(l.source_seq)}${lag}`)
    preds.set(l.target_seq, list)
  }
  const rowOfSeq = new Map<number, number>()
  const itemOfSeq = new Map<number, GItem>()
  const grouped = inp.rows.some((r) => r.kind === 'group')

  inp.rows.forEach((gr, n) => {
    const r = HEAD + n
    row(r).height = ROW_PT
    if (gr.kind === 'group') {
      const st: XStyle = { font: { bold: true, color: P.text }, fill: { pattern: 'solid', fg: P.head } }
      put(r, 0, { v: `${gr.label}（${gr.count}）`, s: st })
      for (let c = 1; c <= COL.pred; c++) put(r, c, { s: st })
      merges.push({ r0: r, c0: 0, r1: r, c1: COL.pred })
      return
    }
    const it = gr.item
    if (!rowOfSeq.has(it.seq)) rowOfSeq.set(it.seq, r)
    itemOfSeq.set(it.seq, it)
    if (gr.depth > 0) row(r).outline = gr.depth
    const tone = it.done ? P.muted : P.text
    put(r, COL.id, { v: inp.idOf(it.seq), s: { font: { bold: !it.done, color: tone } } })
    put(r, COL.title, { v: it.title, s: { font: { color: tone }, align: gr.depth > 0 ? { indent: gr.depth } : undefined } })
    put(r, COL.status, { v: `${inp.statusText(it)}${it.overdue ? ' ⚠' : ''}`, s: it.overdue ? text(P.danger) : text(tone) })
    const pr = preds.get(it.seq)
    if (pr) put(r, COL.pred, { v: pr.join(', '), s: text(tone) })

    const dateStyle = (allDay: boolean): XStyle => ({ font: { color: tone }, numFmt: allDay ? FMT_DATE : FMT_TIME, align: { h: 'left' } })
    if (it.s !== null || it.e !== null) {
      // 自分の予定。終日の終了は最後の日（含む）
      if (it.s !== null) put(r, COL.start, { v: wallSerial(inp.env, inp.baseTz, it.s), s: dateStyle(it.allDay) })
      if (it.e !== null) put(r, COL.end, { v: wallSerial(inp.env, inp.baseTz, it.allDay ? it.e - DAY_MS : it.e), s: dateStyle(it.allDay) })
      put(r, COL.cat, { v: it.done ? 'done' : it.cat })
      put(r, COL.allDay, { v: it.allDay ? 1 : 0 })
    } else if (it.roll) {
      // 配下の期間。配下の行が出ていれば MIN / MAX の式、畳んでいれば出力時の値
      let last = n
      if (!grouped && gr.hasKids && !gr.collapsed) {
        while (last + 1 < inp.rows.length) {
          const nx = inp.rows[last + 1]!
          if (nx.kind !== 'ticket' || nx.depth <= gr.depth) break
          last++
        }
      }
      const st = dateStyle(true)
      if (last > n) {
        const range = `${cellRef(r + 1, COL.start)}:${cellRef(HEAD + last, COL.end)}`
        put(r, COL.start, { f: `IF(COUNT(${range})>0,MIN(${range}),"")`, s: st })
        put(r, COL.end, { f: `IF(COUNT(${range})>0,MAX(${range}),"")`, s: st })
      } else {
        put(r, COL.start, { v: wallSerial(inp.env, inp.baseTz, it.roll.s), s: st })
        put(r, COL.end, { v: wallSerial(inp.env, inp.baseTz, it.roll.e - DAY_MS), s: st })
      }
      put(r, COL.cat, { v: 'roll' })
      put(r, COL.allDay, { v: 1 })
    } else {
      put(r, COL.cat, { v: it.done ? 'done' : it.cat })
      put(r, COL.allDay, { v: 1 })
    }
  })

  // ── 条件付き書式 ──
  const lastRow = HEAD + Math.max(inp.rows.length, 1) - 1
  const lastCol = LEFT + nDays - 1
  const day = `${colName(LEFT)}$3` // 見出しの日付（相対の列・固定の行）
  const at = (c: number) => `$${colName(c)}${HEAD + 1}` // 本表の最初の行を起点にした相対の行
  const D = at(COL.start)
  const E = at(COL.end)
  const G = at(COL.cat)
  const H = at(COL.allDay)
  const bar = `${D}<>"",${E}<>"",${day}+1>${D},${day}<${E}+${H}`
  const first = `INT(${D})=${day}`
  const last = `INT(${E}+${H}-1/86400)=${day}`
  /**
   * バーの規則を「1日だけ・始まり・終わり・途中」に分け、**それぞれが罫を全部持つ**
   * （5.14「バー」）。複数の規則の罫を Excel が辺ごとに重ねるかに頼らない。先に並べたものが勝つ。
   */
  const barRules = (cat: string, fill: XStyle['fill'], style: 'thin' | 'dashed', color: string): XCondRule[] => {
    const side = { style, color }
    const end = { style: 'thin' as const, color }
    const base = `${G}="${cat}",${bar}`
    return [
      { formula: `AND(${base},${first},${last})`, style: { fill, border: { top: side, bottom: side, left: end, right: end } } },
      { formula: `AND(${base},${first})`, style: { fill, border: { top: side, bottom: side, left: end } } },
      { formula: `AND(${base},${last})`, style: { fill, border: { top: side, bottom: side, right: end } } },
      { formula: `AND(${base})`, style: { fill, border: { top: side, bottom: side } } },
    ]
  }
  const calendarRules: XCondRule[] = [
    { formula: `COUNTIF(${HOLIDAY_NAME},${day})>0`, style: { fill: { pattern: 'lightUp', fg: P.holLine, bg: P.sun } } },
    { formula: `WEEKDAY(${day},2)=6`, style: { fill: { pattern: 'solid', fg: inp.calStyle === 'east' ? P.sat : P.wkend } } },
    { formula: `WEEKDAY(${day},2)=7`, style: { fill: { pattern: 'solid', fg: inp.calStyle === 'east' ? P.sun : P.wkend } } },
  ]
  const bodyRules: XCondRule[] = [
    // マイルストーン：開始だけは左、終了だけは右の太い縦罫
    { formula: `AND(${G}="done",${D}<>"",${E}="",INT(${D})=${day})`, style: { border: { left: { style: 'thick', color: P.doneLine } } } },
    { formula: `AND(${D}<>"",${E}="",INT(${D})=${day})`, style: { border: { left: { style: 'thick', color: P.bar } } } },
    { formula: `AND(${G}="done",${D}="",${E}<>"",${day}=INT(${E}-(1-${H})/86400))`, style: { border: { right: { style: 'thick', color: P.doneLine } } } },
    { formula: `AND(${D}="",${E}<>"",${day}=INT(${E}-(1-${H})/86400))`, style: { border: { right: { style: 'thick', color: P.bar } } } },
    // バー（区分ごと）。**土日祝より先に置く**——バーに掛かる日はバーの塗りが勝つ
    ...barRules('done', { pattern: 'solid', fg: P.doneFill }, 'thin', P.doneLine),
    ...barRules('review', { pattern: 'solid', fg: P.barFill }, 'dashed', P.bar),
    ...barRules('in_progress', { pattern: 'solid', fg: P.barFill }, 'thin', P.bar),
    ...barRules('todo', { pattern: 'lightUp', fg: overWhite(P.bar, 0.38), bg: WHITE }, 'thin', P.bar),
    { formula: `AND(${G}="roll",${bar})`, style: { border: { bottom: { style: 'thin', color: P.roll } } } },
    ...calendarRules,
  ]
  const cond: NonNullable<XSheet['cond']> = []
  if (nDays > 0) {
    // 見出しの日と曜日の行（日付は $3 の固定なので、同じ式が効く）
    cond.push({ r0: 2, c0: LEFT, r1: 3, c1: lastCol, rules: calendarRules })
    if (inp.rows.length > 0) cond.push({ r0: HEAD, c0: LEFT, r1: lastRow, c1: lastCol, rules: bodyRules })
  }

  // ── 図形：依存線と blocks の囲み ──
  const shapes: XShape[] = []
  const dayIndex = (v: number) => dayList.findIndex((d) => v >= d.t && v < d.tn)
  const mid = (ROW_PT / 2) * EMU_PT
  /** チケットの端の位置（S＝始まりの日の左端、F＝終わりの日の右端）。期間の外なら null */
  const anchorOf = (it: GItem, r: number, end: 'S' | 'F'): XAnchor | null => {
    const s = it.s ?? it.e
    const e = it.e ?? it.s
    if (s === null || e === null) return null
    const i = end === 'S' ? dayIndex(s) : dayIndex(Math.max(s, e - 1))
    if (i < 0) return null
    return { r, c: LEFT + i + (end === 'F' ? 1 : 0), dr: mid }
  }
  /** 帯の日の列（自分の予定、無ければ配下の期間）。期間の外や日付なしは null */
  const spanCols = (it: GItem): { c0: number; c1: number } | null => {
    const s = it.s ?? it.e ?? it.roll?.s ?? null
    const e = it.e ?? it.s ?? it.roll?.e ?? null
    if (s === null || e === null) return null
    const i0 = dayIndex(s)
    const i1 = dayIndex(Math.max(s, e - 1))
    if (i0 < 0 || i1 < 0) return null
    return { c0: LEFT + i0, c1: LEFT + i1 + 1 }
  }
  /** 行の番号（inp.rows の添字）。配下の範囲を求めるのに使う */
  const indexOfSeq = new Map<number, number>()
  inp.rows.forEach((r, n) => {
    if (r.kind === 'ticket' && !indexOfSeq.has(r.item.seq)) indexOfSeq.set(r.item.seq, n)
  })
  const dashOf: Record<string, 'solid' | 'dash' | 'sysDot' | 'dashDot'> = { FS: 'solid', SS: 'dash', FF: 'sysDot', SF: 'dashDot' }
  for (const l of inp.links) {
    const ra = rowOfSeq.get(l.source_seq)
    const rb = rowOfSeq.get(l.target_seq)
    const a = itemOfSeq.get(l.source_seq)
    const b = itemOfSeq.get(l.target_seq)
    if (ra === undefined || rb === undefined || !a || !b) continue
    const bad = violated(l, a, b)
    if (l.link_type === 'blocks') {
      // 止められている後行と、その配下の行をまとめて角丸の四角枠で囲む（5.14「図形」）
      const n = indexOfSeq.get(b.seq)!
      const top = inp.rows[n]!
      let lastN = n
      if (!grouped && top.kind === 'ticket') {
        while (lastN + 1 < inp.rows.length) {
          const nx = inp.rows[lastN + 1]!
          if (nx.kind !== 'ticket' || nx.depth <= top.depth) break
          lastN++
        }
      }
      let c0 = Infinity
      let c1 = -Infinity
      for (let k = n; k <= lastN; k++) {
        const r = inp.rows[k]!
        if (r.kind !== 'ticket') continue
        const sc = spanCols(r.item)
        if (!sc) continue
        c0 = Math.min(c0, sc.c0)
        c1 = Math.max(c1, sc.c1)
      }
      if (!Number.isFinite(c0)) continue
      const color = bad ? P.danger : P.neon
      shapes.push({ kind: 'roundRect', from: { r: rb, c: c0, dr: EMU_PT }, to: { r: HEAD + lastN, c: c1, dr: (ROW_PT - 1) * EMU_PT }, color, width: 2.25 * EMU_PT, dash: 'solid' })
      const from = anchorOf(a, ra, 'F')
      if (from) shapes.push({ kind: 'connector', from, to: { r: rb, c: c0, dr: mid }, color, width: 1.5 * EMU_PT, dash: 'solid', end: 'oval' })
      continue
    }
    const from = anchorOf(a, ra, l.link_type[0] as 'S' | 'F')
    const to = anchorOf(b, rb, l.link_type[1] as 'S' | 'F')
    if (!from || !to) continue
    shapes.push({ kind: 'connector', from, to, color: bad ? P.danger : P.bar, width: 1.25 * EMU_PT, dash: dashOf[l.link_type] ?? 'solid', end: 'triangle' })
  }
  // 今日の線（図形の縦線。出力した日の列の左端。開いた日には追随しない）
  const todayIdx = dayIndex(inp.now)
  if (todayIdx >= 0) {
    shapes.push({ kind: 'connector', geom: 'straight', from: { r: 2, c: LEFT + todayIdx }, to: { r: lastRow + 1, c: LEFT + todayIdx }, color: P.bar, width: 1.5 * EMU_PT, dash: 'solid' })
  }

  const main: XSheet = { name: mainName, cols, rows, merges, freeze: { cols: LEFT, rows: HEAD }, cond, shapes }

  // ── 休日タブ（名前付き範囲 HolidayList）──
  const holRows: XRow[] = [{ cells: new Map<number, XCell>([[0, { v: uiText('日付'), s: headCell }], [1, { v: uiText('名前'), s: headCell }]]) }]
  for (const h of inp.holidays) {
    const [y, m, d] = h.day.split('-').map(Number) as [number, number, number]
    holRows.push({
      cells: new Map<number, XCell>([
        [0, { v: serial(Date.UTC(y, m - 1, d)), s: { numFmt: FMT_DATE, align: { h: 'left' } } }],
        [1, { v: h.name }],
      ]),
    })
  }
  const holidays: XSheet = { name: holName, cols: [{ width: 14 }, { width: 30 }], rows: holRows, freeze: { cols: 0, rows: 1 } }

  // ── 関係タブ ──
  const relRows: XRow[] = [
    {
      cells: new Map<number, XCell>(
        [uiText('先行'), uiText('先行のタイトル'), uiText('種別'), uiText('ずらし（日）'), uiText('後行'), uiText('後行のタイトル'), uiText('判定')].map((v, c) => [c, { v, s: headCell }]),
      ),
    },
  ]
  const relLinks: NonNullable<XSheet['links']> = []
  const ref = (r: number, c: number) => `${M}!$${colName(c)}$${r + 1}`
  const startOf = (r: number) => `IF(${ref(r, COL.start)}<>"",${ref(r, COL.start)},${ref(r, COL.end)}+${ref(r, COL.allDay)})`
  const finishOf = (r: number) => `IF(${ref(r, COL.end)}<>"",${ref(r, COL.end)}+${ref(r, COL.allDay)},${ref(r, COL.start)})`
  const has = (r: number) => `COUNT(${ref(r, COL.start)}:${ref(r, COL.end)})>0`
  const bad = uiText('⚠ 反している')
  for (const l of inp.links) {
    const ra = rowOfSeq.get(l.source_seq)
    const rb = rowOfSeq.get(l.target_seq)
    const a = itemOfSeq.get(l.source_seq)
    const b = itemOfSeq.get(l.target_seq)
    if (ra === undefined || rb === undefined || !a || !b) continue
    const ty = l.link_type === 'blocks' ? 'FS' : l.link_type
    const from = ty[0] === 'S' ? startOf(ra) : finishOf(ra)
    const to = ty[1] === 'S' ? startOf(rb) : finishOf(rb)
    const rr = relRows.length
    relRows.push({
      cells: new Map<number, XCell>([
        [0, { v: inp.idOf(a.seq), s: { font: { color: P.bar } } }],
        [1, { v: a.title }],
        [2, { v: l.link_type }],
        [3, { v: l.lag_days }],
        [4, { v: inp.idOf(b.seq), s: { font: { color: P.bar } } }],
        [5, { v: b.title }],
        [6, { f: `IF(AND(${has(ra)},${has(rb)}),IF(${to}<${from}+${l.lag_days},"${bad}",""),"")`, v: violated(l, a, b) ? bad : '', s: text(P.danger) }],
      ]),
    })
    relLinks.push({ r: rr, c: 0, location: `${M}!A${ra + 1}` }, { r: rr, c: 4, location: `${M}!A${rb + 1}` })
  }
  const relations: XSheet = {
    name: relName,
    cols: [{ width: 14 }, { width: 40 }, { width: 8 }, { width: 11 }, { width: 14 }, { width: 40 }, { width: 14 }],
    rows: relRows,
    freeze: { cols: 0, rows: 1 },
    links: relLinks,
  }

  return {
    sheets: [main, holidays, relations],
    names: { [HOLIDAY_NAME]: `${sheetRef(holName)}!$A$2:$A$${HOLIDAY_ROWS}` },
    font: 'Yu Gothic',
  }
}

/** ガントを xlsx にする。ファイル名は `<キー>-gantt-<YYYYMMDD>.xlsx`（見る人の今日） */
export async function buildGanttXlsx(inp: GanttExcelInput): Promise<{ blob: Blob; name: string }> {
  const blob = await buildXlsx(buildGanttBook(inp))
  return { blob, name: `${inp.projectKey}-gantt-${inp.env.time.ymd(inp.viewTz, inp.now).replace(/-/g, '')}.xlsx` }
}
