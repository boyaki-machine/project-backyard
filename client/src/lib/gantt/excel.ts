/**
 * ガントの Excel 出力（`GuiDesign.md` 5.14「Excel 出力」。pb-223）。
 *
 * **画面が持つ行・帯・暦をそのまま使い、セル1つ＝1日で塗る**（`Requirements.md` 5章の
 * 「共通レイアウトエンジン型」）。日付軸は `time.ts`、行と帯は `model.ts` と同じ値である。
 *
 * **このモジュールは押したときだけ読み込む**（`GanttPage.vue` が動的 `import()` で呼ぶ）。
 * ガントを開いたときの読み込みを増やさないため、画面の側からは静的に import しない。
 *
 * **日付の関数（`time.ts`）と文言の関数（`uiText`）は画面から受け取る**（`env`）。ここで
 * 静的に import すると、それらが引く `vue-i18n` → Vue の本体まで「画面とこのモジュールの
 * 共有物」になり、ビルドが Vue の本体を別のファイル（65KB）へ切り出す——ガントを開いたときの
 * 読み込みが2ファイルに増える（pb-223 で実測）。型だけの import は消えるので構わない。
 */
import type { Hue } from '../../stores/ui'
import { buildXlsx } from '../xlsx'
import type { XBorder, XCell, XCol, XRow, XSheet, XStyle } from '../xlsx'
import { paletteOf, overWhite } from './excelPalette'
import type { GItem, GRow, GanttLink } from './model'
import type { DayKind, GSprint } from './render'
import type * as TimeModule from './time'
import type { DayTick } from './time'

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
  dayKind: (d: DayTick) => DayKind
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
/** 左の列の数（ID・タイトル・状態・開始・終了・先行） */
const LEFT = 6
/** 見出しの行の数（表題・年月・日・曜日・スプリント／列の見出し） */
const HEAD = 5

const WHITE = '#ffffff'

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

function dateText(env: ExcelEnv, t: number, allDay: boolean, end: boolean, baseTz: string, viewTz: string): string {
  const { hm, tzShort, ymd } = env.time
  if (allDay) return ymd(baseTz, end ? t - 1 : t)
  return `${ymd(viewTz, t)} ${hm(viewTz, t)} ${tzShort(viewTz, t)}`
}

/** 列の既定の書式に、セル自身の書式を重ねる（塗りと罫は自身が優先。無い側は列のまま） */
function over(col: XStyle | undefined, own: XStyle): XStyle {
  if (!col) return own
  const border: XBorder = { ...(col.border ?? {}), ...(own.border ?? {}) }
  return {
    ...col,
    ...own,
    fill: own.fill ?? col.fill,
    border: Object.keys(border).length > 0 ? border : undefined,
  }
}

export function buildGanttSheet(inp: GanttExcelInput): XSheet {
  const { days, hm, tzShort, weekdayName, ymd } = inp.env.time
  const uiText = inp.env.text
  const P = paletteOf(inp.hue)
  const { t0, t1, cut } = exportRange(inp.env, inp.rows, inp.now, inp.baseTz)
  const dayList = days(inp.baseTz, t0, t1 - 1).filter((d) => d.t >= t0 && d.t < t1)
  const col0 = LEFT
  const todayIdx = dayList.findIndex((d) => inp.now >= d.t && inp.now < d.tn)

  // ── 列（左の6列と、1列＝1日。土日祝と今日は列の既定の書式で持つ）──
  const cols: XCol[] = [
    { width: 13 },
    { width: 42 },
    { width: 12 },
    { width: 20 },
    { width: 20 },
    { width: 22 },
  ]
  const dayStyle: (XStyle | undefined)[] = dayList.map((d, i) => {
    const k = inp.dayKind(d)
    let st: XStyle | undefined
    if (k === 'hol') st = { fill: { pattern: 'lightUp', fg: P.holLine, bg: P.sun } }
    else if (k === 'sat') st = { fill: { pattern: 'solid', fg: inp.calStyle === 'east' ? P.sat : P.wkend } }
    else if (k === 'sun') st = { fill: { pattern: 'solid', fg: inp.calStyle === 'east' ? P.sun : P.wkend } }
    if (i === todayIdx) st = { ...(st ?? {}), border: { left: { style: 'medium', color: P.bar } } }
    return st
  })
  dayList.forEach((_, i) => cols.push({ width: 3.4, style: dayStyle[i] }))

  const rows: XRow[] = []
  const merges: XSheet['merges'] = []
  const row = (r: number): XRow => {
    while (rows.length <= r) rows.push({ cells: new Map() })
    return rows[r]!
  }
  const put = (r: number, c: number, cell: XCell) => row(r).cells.set(c, cell)

  const muted: XStyle = { font: { color: P.muted } }

  // ── 表題（1行目）──
  const notes: string[] = []
  if (inp.truncated) notes.push(uiText('先頭の {value0}件だけを出力しています（全{value1}件）', { value0: inp.truncated.shown, value1: inp.truncated.total }))
  if (cut) notes.push(uiText('期間が {value0}日を超えるため、先頭の {value0}日だけを出力しています', { value0: MAX_DAYS }))
  put(0, 0, {
    v:
      `${inp.projectKey} ${uiText('ガント')}　` +
      `${uiText('出力')} ${ymd(inp.viewTz, inp.now)} ${hm(inp.viewTz, inp.now)} ${tzShort(inp.viewTz, inp.now)}` +
      (notes.length ? `　${notes.join('　')}` : ''),
    s: { font: { bold: true, color: P.text } },
  })
  row(0).height = 20

  // ── 日付の見出し（2〜4行目）──
  put(1, 0, { v: `${uiText('基準')} ${inp.baseTz}`, s: muted })
  let m0 = 0
  dayList.forEach((d, i) => {
    const next = dayList[i + 1]
    if (next && next.y === d.y && next.m === d.m) return
    const c0 = col0 + m0
    const c1 = col0 + i
    put(1, c0, { v: `${d.y}-${String(d.m).padStart(2, '0')}`, s: over(dayStyle[m0], { font: { bold: true, color: P.text } }) })
    if (c1 > c0) merges.push({ r0: 1, c0, r1: 1, c1 })
    m0 = i + 1
  })
  const center = { h: 'center' as const, v: 'center' as const }
  dayList.forEach((d, i) => {
    const k = inp.dayKind(d)
    put(2, col0 + i, { v: d.d, s: over(dayStyle[i], { font: { color: P.text }, align: center }) })
    put(3, col0 + i, {
      v: k === 'hol' ? uiText('祝') : weekdayName(d.wd),
      s: over(dayStyle[i], { font: { color: k === 'hol' || k === 'sun' ? P.danger : P.muted }, align: center }),
    })
  })

  // ── 5行目：左は列の見出し、右はスプリントの帯 ──
  const headCell: XStyle = { font: { bold: true, color: P.text }, fill: { pattern: 'solid', fg: P.head }, border: { bottom: { style: 'thin', color: P.grid } } }
  ;[uiText('ID'), uiText('タイトル'), uiText('状態'), uiText('開始'), uiText('終了'), uiText('先行')].forEach((v, c) => put(4, c, { v, s: headCell }))
  const taken = new Set<number>()
  for (const sp of inp.sprints) {
    const idx = dayList.map((d, i) => (d.t < sp.e && d.tn > sp.s ? i : -1)).filter((i) => i >= 0)
    if (idx.length === 0 || idx.some((i) => taken.has(i))) continue
    idx.forEach((i) => taken.add(i))
    const color = sp.status === 'active' ? P.neonAct : P.neon
    const side = { style: sp.status === 'active' ? ('medium' as const) : sp.status === 'planned' ? ('dashed' as const) : ('thin' as const), color }
    const c0 = col0 + idx[0]!
    const c1 = col0 + idx[idx.length - 1]!
    for (let c = c0; c <= c1; c++) {
      put(4, c, {
        v: c === c0 ? sp.name : undefined,
        s: over(dayStyle[c - col0], {
          font: { bold: sp.status === 'active', color: sp.status === 'active' ? P.neonAct : P.muted },
          border: { top: side, bottom: side, ...(c === c0 ? { left: side } : {}), ...(c === c1 ? { right: side } : {}) },
          align: { v: 'center' },
        }),
      })
    }
    if (c1 > c0) merges.push({ r0: 4, c0, r1: 4, c1 })
  }

  // ── 行（6行目〜）──
  const preds = new Map<number, string[]>()
  for (const l of inp.links) {
    const lag = l.lag_days ? ` ${l.lag_days > 0 ? '+' : ''}${l.lag_days}d` : ''
    const list = preds.get(l.target_seq) ?? []
    list.push(`${l.link_type} ${inp.idOf(l.source_seq)}${lag}`)
    preds.set(l.target_seq, list)
  }
  const hit = (s: number, e: number) => dayList.map((d, i) => (d.t < e && d.tn > s ? i : -1)).filter((i) => i >= 0)

  inp.rows.forEach((gr, n) => {
    const r = HEAD + n
    if (gr.kind === 'group') {
      const st: XStyle = { font: { bold: true, color: P.text }, fill: { pattern: 'solid', fg: P.head } }
      put(r, 0, { v: `${gr.label}（${gr.count}）`, s: st })
      for (let c = 1; c < LEFT; c++) put(r, c, { s: st })
      merges.push({ r0: r, c0: 0, r1: r, c1: LEFT - 1 })
      return
    }
    const it = gr.item
    const xr = row(r)
    if (gr.depth > 0) xr.outline = gr.depth
    const tone = it.done ? P.muted : P.text
    const txt: XStyle = { font: { color: tone } }
    put(r, 0, { v: inp.idOf(it.seq), s: { font: { bold: !it.done, color: tone } } })
    put(r, 1, { v: it.title, s: { font: { color: tone }, align: gr.depth > 0 ? { indent: gr.depth } : undefined } })
    put(r, 2, { v: `${inp.statusText(it)}${it.overdue ? ' ⚠' : ''}`, s: it.overdue ? { font: { color: P.danger } } : txt })
    if (it.s !== null || it.e !== null) {
      put(r, 3, { v: it.s !== null ? dateText(inp.env, it.s, it.allDay, false, inp.baseTz, inp.viewTz) : '', s: txt })
      put(r, 4, { v: it.e !== null ? dateText(inp.env, it.e, it.allDay, true, inp.baseTz, inp.viewTz) : '', s: txt })
    } else if (it.roll) {
      put(r, 3, { v: `—（${uiText('配下')} ${ymd(inp.baseTz, it.roll.s)}）`, s: muted })
      put(r, 4, { v: `—（${uiText('配下')} ${ymd(inp.baseTz, it.roll.e - 1)}）`, s: muted })
    }
    const pr = preds.get(it.seq)
    if (pr) put(r, 5, { v: pr.join(', '), s: txt })

    const cat = it.done ? 'done' : it.cat
    const line = cat === 'done' ? P.doneLine : P.bar
    if (it.s !== null && it.e !== null) {
      const idx = hit(it.s, Math.max(it.e, it.s + 1))
      const edge = { style: cat === 'review' ? ('dashed' as const) : ('thin' as const), color: line }
      const fill: XStyle['fill'] =
        cat === 'done'
          ? { pattern: 'solid', fg: P.doneFill }
          : cat === 'todo'
            ? { pattern: 'lightUp', fg: overWhite(P.bar, 0.38), bg: WHITE }
            : { pattern: 'solid', fg: P.barFill }
      idx.forEach((i, k) => {
        put(r, col0 + i, {
          s: over(dayStyle[i], {
            fill,
            border: {
              top: edge,
              bottom: edge,
              ...(k === 0 ? { left: { style: 'thin', color: line } } : {}),
              ...(k === idx.length - 1 ? { right: { style: 'thin', color: line } } : {}),
            },
          }),
        })
      })
    } else if (it.s !== null || it.e !== null) {
      // マイルストーン：開始だけは `[`、終了だけは `]`
      const at = it.s !== null ? it.s : it.e! - 1
      const i = dayList.findIndex((d) => at >= d.t && at < d.tn)
      if (i >= 0) put(r, col0 + i, { v: it.s !== null ? '[' : ']', s: over(dayStyle[i], { font: { bold: true, color: line }, align: center }) })
    } else if (it.roll) {
      // 配下の期間：寸法線の代わりに下罫
      for (const i of hit(it.roll.s, it.roll.e)) {
        put(r, col0 + i, { s: over(dayStyle[i], { border: { bottom: { style: 'thin', color: P.roll } } }) })
      }
    }
  })

  return {
    name: `${inp.projectKey} ${uiText('ガント')}`,
    cols,
    rows,
    merges,
    freeze: { cols: LEFT, rows: HEAD },
    font: 'Yu Gothic',
  }
}

/** ガントを xlsx にする。ファイル名は `<キー>-gantt-<YYYYMMDD>.xlsx`（見る人の今日） */
export async function buildGanttXlsx(inp: GanttExcelInput): Promise<{ blob: Blob; name: string }> {
  const blob = await buildXlsx(buildGanttSheet(inp))
  return { blob, name: `${inp.projectKey}-gantt-${inp.env.time.ymd(inp.viewTz, inp.now).replace(/-/g, '')}.xlsx` }
}

