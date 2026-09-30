import { inflateRawSync } from 'node:zlib'
import { readFileSync } from 'node:fs'

import { expect, test, type Page, type Route } from '@playwright/test'

import { CSS_OF, paletteOf } from '../src/lib/gantt/excelPalette'
import type { ExcelPalette } from '../src/lib/gantt/excelPalette'

/**
 * ガントの Excel 出力（GuiDesign.md 5.14「Excel 出力」。pb-223）。
 *
 * **落としたファイルを試験の中で解いて中身を見る**——セルの値・塗りの色・日本語・休日の列・
 * 完了の沈み。zip は node の `inflateRawSync` で解き、XML は必要な所だけを読む。
 * **色の表（excelPalette.ts）が画面の色とずれていないこと**も、ライトで canvas に塗って確かめる。
 *
 * API はすべてモックする。「今」は 2026-09-29 22:10 JST、基準は Asia/Tokyo。題名は多バイト文字。
 */

const JST = 9 * 3600_000
const D = (m: number, d: number, h = 0, mi = 0) => Date.UTC(2026, m - 1, d, h, mi) - JST
const NOW = D(9, 29, 22, 10)

const statuses = [
  { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 },
  { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 2 },
  { key: 'review', name: 'レビュー中', category: 'review', sort_order: 3 },
  { key: 'done', name: '完了', category: 'done', sort_order: 4 },
]

function tk(seq: number, title: string, o: { s?: number; e?: number; parent?: number; status?: string; allDay?: boolean } = {}) {
  const status = statuses.find((x) => x.key === (o.status ?? 'todo'))!
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type: 'task', title, status,
    priority: null, assignee: null, reporter: null, working_agent: null, parent_seq: o.parent ?? null, has_children: false,
    sort_key: `0|${String(seq).padStart(6, '0')}:`, staged_at: null, tags: [], sprint: null,
    estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: o.s ?? null, due_at: o.e ?? null, all_day: o.allDay ?? true,
    closed_at: status.category === 'done' ? D(9, 20) : null, version: 1, created_at: D(9, 1), updated_at: D(9, 1),
  }
}

const tickets = [
  tk(500, 'ガントを Excel に出す（親）'),
  tk(501, '書き出しを作る', { parent: 500, status: 'in_progress', s: D(9, 28), e: D(10, 2) }),
  tk(502, '色の表を用意する', { parent: 500, s: D(10, 1), e: D(10, 6) }),
  tk(503, '設計を書く', { parent: 500, status: 'done', s: D(9, 24), e: D(9, 26) }),
  tk(504, '受け入れ（終了日だけ）', { parent: 500, e: D(10, 9) }),
  tk(505, 'リハーサル（時刻付き）', { status: 'review', s: D(10, 5, 21), e: D(10, 5, 23, 30), allDay: false }),
]
const links = [
  { id: '01K00000000000000000LNK001', source_seq: 501, target_seq: 502, link_type: 'FS', lag_days: 0, origin: 'human' },
  { id: '01K00000000000000000LNK002', source_seq: 503, target_seq: 502, link_type: 'SS', lag_days: 2, origin: 'human' },
  { id: '01K00000000000000000LNK003', source_seq: 503, target_seq: 505, link_type: 'blocks', lag_days: 0, origin: 'human' },
  // 配下を持つ親を止める blocks（枠は親から最後の子の行まで）
  { id: '01K00000000000000000LNK004', source_seq: 505, target_seq: 500, link_type: 'blocks', lag_days: 0, origin: 'human' },
]
const sprints = [
  { id: '01K000000000000000000SPR15', name: 'スプリント 15', goal: null, start_at: D(9, 28), end_at: D(10, 12), all_day: true, status: 'active', ticket_count: 0, closed_count: 0 },
]

async function reply(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockApi(page: Page, hue: 'blue' | 'green' = 'blue') {
  await page.clock.install({ time: NOW })
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path === '/api/v1/me') {
      return reply(route, {
        actor: {
          id: '01K00000000000000000000000', kind: 'user', display_name: '検証 次郎', email: 'e2e@example.com',
          system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue, must_change_password: false,
        },
        permissions: [],
        projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view'] }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/demo') {
      return reply(route, { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses }, settings: {}, version: 1 })
    }
    if (path === '/api/v1/projects/demo/tickets') {
      if (url.searchParams.get('type') === 'epic') return reply(route, { items: [], total: 0, page: 1, per_page: 200, total_pages: 0 })
      return reply(route, { items: tickets, links, total: tickets.length, page: 1, per_page: 5000, total_pages: 1 })
    }
    if (path.endsWith('/sprints')) return reply(route, { items: sprints })
    if (path.endsWith('/calendar/days')) {
      // 10/3・4 は土日、10/12 は祝日（スポーツの日）
      return reply(route, {
        days: [
          { day: '2026-10-03', is_holiday: true, reason: 'weekend', events: [], override: null },
          { day: '2026-10-04', is_holiday: true, reason: 'weekend', events: [], override: null },
          { day: '2026-10-12', is_holiday: true, reason: 'holiday', events: [{ kind: 'holiday', name: 'スポーツの日' }], override: null },
        ],
      })
    }
    return reply(route, { items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
}

// ── xlsx を読む（試験用の最小限）────────────────────────────

function unzip(buf: Buffer): Map<string, string> {
  const out = new Map<string, string>()
  let eocd = buf.length - 22
  while (eocd >= 0 && buf.readUInt32LE(eocd) !== 0x06054b50) eocd--
  const count = buf.readUInt16LE(eocd + 10)
  let p = buf.readUInt32LE(eocd + 16)
  for (let i = 0; i < count; i++) {
    const method = buf.readUInt16LE(p + 10)
    const csize = buf.readUInt32LE(p + 20)
    const nlen = buf.readUInt16LE(p + 28)
    const elen = buf.readUInt16LE(p + 30)
    const clen = buf.readUInt16LE(p + 32)
    const local = buf.readUInt32LE(p + 42)
    const name = buf.toString('utf8', p + 46, p + 46 + nlen)
    const lnlen = buf.readUInt16LE(local + 26)
    const lelen = buf.readUInt16LE(local + 28)
    const data = buf.subarray(local + 30 + lnlen + lelen, local + 30 + lnlen + lelen + csize)
    out.set(name, (method === 8 ? inflateRawSync(data) : data).toString('utf8'))
    p += 46 + nlen + elen + clen
  }
  return out
}

const unxml = (s: string) => s.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&amp;/g, '&')

interface Cell { v: string | null; f: string | null; s: number }

interface Sheet {
  xml: string
  cells: Map<string, Cell>
  hiddenCols: Set<number>
  merges: string[]
  outline: Map<number, number>
  links: Map<string, string>
  /** 条件付き書式の規則（優先順）。formula と dxf の番号 */
  rules: { formula: string; dxf: number; priority: number; sqref: string }[]
}

interface Book {
  files: Map<string, string>
  sheets: Sheet[]
  names: Map<string, string>
  sheetNames: string[]
  xf: { numFmtId: number; fillId: number; fontId: number; borderId: number }[]
  numFmts: Map<number, string>
  fills: { pattern: string; fg: string | null }[]
  fonts: { bold: boolean; color: string | null }[]
  dxfs: string[]
}

function readSheet(xml: string): Sheet {
  const cells = new Map<string, Cell>()
  for (const m of xml.matchAll(/<c r="([A-Z]+\d+)"(?: s="(\d+)")?(?: t="(?:inlineStr|str)")?(?:\/>|>([\s\S]*?)<\/c>)/g)) {
    const body = m[3] ?? ''
    const f = body.match(/<f>([\s\S]*?)<\/f>/)?.[1] ?? null
    const v = body.match(/<t[^>]*>([\s\S]*?)<\/t>/)?.[1] ?? body.match(/<v>([^<]*)<\/v>/)?.[1] ?? null
    cells.set(m[1]!, { v: v === null ? null : unxml(v), f: f === null ? null : unxml(f), s: Number(m[2] ?? 0) })
  }
  const hiddenCols = new Set<number>()
  for (const m of xml.matchAll(/<col min="(\d+)"[^>]*? hidden="1"\/>/g)) hiddenCols.add(Number(m[1]))
  const merges = [...xml.matchAll(/<mergeCell ref="([^"]+)"/g)].map((m) => m[1]!)
  const outline = new Map<number, number>()
  for (const m of xml.matchAll(/<row r="(\d+)"[^>]*? outlineLevel="(\d+)"/g)) outline.set(Number(m[1]), Number(m[2]))
  const links = new Map<string, string>()
  for (const m of xml.matchAll(/<hyperlink ref="([A-Z]+\d+)" location="([^"]+)"/g)) links.set(m[1]!, unxml(m[2]!))
  const rules: Sheet['rules'] = []
  for (const block of xml.matchAll(/<conditionalFormatting sqref="([^"]+)">([\s\S]*?)<\/conditionalFormatting>/g)) {
    for (const m of block[2]!.matchAll(/<cfRule type="expression" dxfId="(\d+)" priority="(\d+)"><formula>([\s\S]*?)<\/formula>/g)) {
      rules.push({ dxf: Number(m[1]), priority: Number(m[2]), formula: unxml(m[3]!), sqref: block[1]! })
    }
  }
  return { xml, cells, hiddenCols, merges, outline, links, rules }
}

function readBook(path: string): Book {
  const files = unzip(readFileSync(path))
  const wb = files.get('xl/workbook.xml')!
  const sheetNames = [...wb.matchAll(/<sheet name="([^"]+)"/g)].map((m) => unxml(m[1]!))
  const names = new Map<string, string>()
  for (const m of wb.matchAll(/<definedName name="([^"]+)">([^<]+)<\/definedName>/g)) names.set(m[1]!, unxml(m[2]!))
  const sheets = sheetNames.map((_, i) => readSheet(files.get(`xl/worksheets/sheet${i + 1}.xml`)!))
  const styles = files.get('xl/styles.xml')!
  const section = (tag: string) => styles.match(new RegExp(`<${tag} count="\\d+">([\\s\\S]*?)</${tag}>`))?.[1] ?? ''
  const xf = [...section('cellXfs').matchAll(/<xf numFmtId="(\d+)" fontId="(\d+)" fillId="(\d+)" borderId="(\d+)"/g)].map((m) => ({
    numFmtId: Number(m[1]), fontId: Number(m[2]), fillId: Number(m[3]), borderId: Number(m[4]),
  }))
  const numFmts = new Map<number, string>()
  for (const m of section('numFmts').matchAll(/<numFmt numFmtId="(\d+)" formatCode="([^"]+)"/g)) numFmts.set(Number(m[1]), unxml(m[2]!))
  const fills = [...section('fills').matchAll(/<fill>([\s\S]*?)<\/fill>/g)].map((m) => ({
    pattern: m[1]!.match(/patternType="(\w+)"/)![1]!,
    fg: m[1]!.match(/<fgColor rgb="FF([0-9A-F]{6})"/)?.[1]?.toLowerCase() ?? null,
  }))
  const fonts = [...section('fonts').matchAll(/<font>([\s\S]*?)<\/font>/g)].map((m) => ({
    bold: m[1]!.includes('<b/>'),
    color: m[1]!.match(/<color rgb="FF([0-9A-F]{6})"/)?.[1]?.toLowerCase() ?? null,
  }))
  const dxfs = [...section('dxfs').matchAll(/<dxf>([\s\S]*?)<\/dxf>/g)].map((m) => m[1]!)
  return { files, sheets, names, sheetNames, xf, numFmts, fills, fonts, dxfs }
}

const hex = (c: string) => c.replace('#', '').toUpperCase()
/** 壁時計の年月日時分を Excel のシリアル値に */
const serialOf = (y: number, m: number, d: number, h = 0, mi = 0) => Date.UTC(y, m - 1, d, h, mi) / 86_400_000 + 25569

/** ID の列（A）でチケットの行を探す */
function rowOf(sh: Sheet, id: string): number {
  for (const [ref, c] of sh.cells) if (/^A\d+$/.test(ref) && c.v === id) return Number(ref.slice(1))
  throw new Error(`${id} の行が無い`)
}

async function download(page: Page, dir: string): Promise<Book> {
  const [dl] = await Promise.all([page.waitForEvent('download'), page.locator('.gantt-excel').click()])
  expect(dl.suggestedFilename()).toBe('demo-gantt-20260929.xlsx')
  const path = `${dir}/${dl.suggestedFilename()}`
  await dl.saveAs(path)
  return readBook(path)
}

async function openGantt(page: Page) {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/p/demo/gantt')
  await expect(page.locator('.gantt-svg rect.b').first()).toBeAttached()
}

test('押したときだけ Excel のコードを読み込み、関数で動くガントの xlsx を落とす', async ({ page }, testInfo) => {
  const scripts: string[] = []
  page.on('request', (r) => {
    if (r.resourceType() === 'script') scripts.push(new URL(r.url()).pathname)
  })
  await mockApi(page)
  await openGantt(page)
  // 開いただけでは読み込まない
  expect(scripts.some((s) => /\/excel-[\w-]+\.js$/.test(s))).toBe(false)
  const b = await download(page, testInfo.outputDir)
  expect(scripts.some((s) => /\/excel-[\w-]+\.js$/.test(s))).toBe(true)
  const P = paletteOf('blue')

  // タブは3つ。休日の一覧は名前付き範囲
  expect(b.sheetNames).toEqual(['demo ガント', '休日', '関係'])
  expect(b.names.get('HolidayList')).toBe("'休日'!$A$2:$A$1000")
  const [main, hol, rel] = b.sheets as [Sheet, Sheet, Sheet]

  // 見出し・固定・隠し列（区分・終日）
  expect(main.cells.get('A1')!.v).toContain('demo ガント')
  expect(['A5', 'B5', 'C5', 'D5', 'E5', 'F5', 'G5', 'H5'].map((r) => main.cells.get(r)!.v)).toEqual(['ID', 'タイトル', '状態', '開始', '終了', '先行', '区分', '終日'])
  expect(main.xml).toContain('<pane xSplit="8" ySplit="5" topLeftCell="I6" activePane="bottomRight" state="frozen"/>')
  expect([...main.hiddenCols].sort()).toEqual([7, 8])

  // 日付は日時の値（終日は最後の日を含む。時刻付きは基準タイムゾーンの壁時計）
  const r501 = rowOf(main, 'demo-501')
  expect(Number(main.cells.get(`D${r501}`)!.v)).toBe(serialOf(2026, 9, 28))
  expect(Number(main.cells.get(`E${r501}`)!.v)).toBe(serialOf(2026, 10, 1))
  const fmt = (ref: string) => b.numFmts.get(b.xf[main.cells.get(ref)!.s]!.numFmtId)
  expect(fmt(`D${r501}`)).toBe('yyyy-mm-dd')
  expect(main.cells.get(`G${r501}`)!.v).toBe('in_progress')
  expect(main.cells.get(`H${r501}`)!.v).toBe('1')
  const r505 = rowOf(main, 'demo-505')
  expect(Number(main.cells.get(`D${r505}`)!.v)).toBeCloseTo(serialOf(2026, 10, 5, 21), 6)
  expect(fmt(`D${r505}`)).toBe('yyyy-mm-dd hh:mm')
  expect(main.cells.get(`H${r505}`)!.v).toBe('0')

  // 親の期間は配下の MIN / MAX の式
  const r500 = rowOf(main, 'demo-500')
  const range = `D${r500 + 1}:E${r500 + 4}`
  expect(main.cells.get(`D${r500}`)!.f).toBe(`IF(COUNT(${range})>0,MIN(${range}),"")`)
  expect(main.cells.get(`E${r500}`)!.f).toBe(`IF(COUNT(${range})>0,MAX(${range}),"")`)
  expect(main.cells.get(`G${r500}`)!.v).toBe('roll')
  expect(main.outline.get(r501)).toBe(1)

  // 見出しの日は日付の値、曜日は休日タブを見る式
  expect(main.cells.get('I3')!.v).toBe(String(serialOf(2026, 9, 17)))
  expect(main.cells.get('I4')!.f).toBe('IF(COUNTIF(HolidayList,I3)>0,"祝",CHOOSE(WEEKDAY(I3),"日","月","火","水","木","金","土"))')

  // 条件付き書式：バー（区分ごと）が土日祝より先
  // 本表の範囲（I6 から）の規則だけを見る（見出しの行にも土日祝の規則がある）
  const body = main.rules.filter((r) => r.sqref.startsWith('I6:'))
  const idx = (needle: string) => body.findIndex((r) => r.formula.includes(needle))
  const inProgress = 'AND($G6="in_progress",$D6<>"",$E6<>"",I$3+1>$D6,I$3<$E6+$H6)'
  expect(idx(inProgress)).toBeGreaterThanOrEqual(0)
  expect(idx(inProgress)).toBeLessThan(idx('COUNTIF(HolidayList,I$3)>0'))
  expect(idx(inProgress)).toBeLessThan(idx('WEEKDAY(I$3,2)=6'))
  expect(b.dxfs[body[idx(inProgress)]!.dxf]).toContain(`<bgColor rgb="FF${hex(P.barFill)}"/>`)
  expect(b.dxfs[body[idx('AND($G6="todo"')]!.dxf]).toContain('patternType="lightUp"')
  expect(b.dxfs[body[idx('WEEKDAY(I$3,2)=6')]!.dxf]).toContain(`<bgColor rgb="FF${hex(P.sat)}"/>`)
  // バーの左右の罫：始まりの日は左、終わりの日は右、1日だけは両方。途中の規則より先
  const firstDay = `${inProgress.slice(0, -1)},INT($D6)=I$3)`
  const lastDay = `${inProgress.slice(0, -1)},INT($E6+$H6-1/86400)=I$3)`
  const single = `${inProgress.slice(0, -1)},INT($D6)=I$3,INT($E6+$H6-1/86400)=I$3)`
  const ruleOf = (f: string) => body.find((r) => r.formula === f)!
  expect(b.dxfs[ruleOf(firstDay).dxf]).toMatch(/<left style="thin">/)
  expect(b.dxfs[ruleOf(firstDay).dxf]).not.toMatch(/<right /)
  expect(b.dxfs[ruleOf(lastDay).dxf]).toMatch(/<right style="thin">/)
  expect(b.dxfs[ruleOf(single).dxf]).toMatch(/<left style="thin">[\s\S]*<right style="thin">/)
  expect(body.indexOf(ruleOf(single))).toBeLessThan(body.indexOf(ruleOf(firstDay)))
  expect(body.indexOf(ruleOf(firstDay))).toBeLessThan(idx(inProgress))
  // 今日は罫ではなく図形の線（下で確かめる）
  expect(main.rules.some((r) => r.formula.includes('TODAY()'))).toBe(false)

  // 休日タブ
  expect(Number(hol.cells.get('A2')!.v)).toBe(serialOf(2026, 10, 12))
  expect(hol.cells.get('B2')!.v).toBe('スポーツの日')

  // 関係タブ：ID から本表の行へ飛べ、判定は式（FS 501→502 は反している）
  expect(rel.cells.get('A2')!.v).toBe('demo-501')
  expect(rel.cells.get('C2')!.v).toBe('FS')
  expect(rel.cells.get('E2')!.v).toBe('demo-502')
  expect(rel.links.get('A2')).toBe(`'demo ガント'!A${r501}`)
  expect(rel.cells.get('G2')!.f).toContain('IF(AND(COUNT(')
  expect(rel.cells.get('G2')!.v).toBe('⚠ 反している')
  expect(rel.cells.get('C3')!.v).toBe('SS')
  expect(rel.cells.get('G3')!.v).toBe('')
  expect(b.files.get('xl/workbook.xml')).toContain('fullCalcOnLoad="1"')

  // 図形：依存線（FS は実線と矢印、反する配置は赤）・blocks は角丸の囲みと丸止めの線
  const drawing = b.files.get('xl/drawings/drawing1.xml')!
  expect(main.xml).toContain('<drawing r:id="rId1"/>')
  expect(b.files.get('xl/worksheets/_rels/sheet1.xml.rels')).toContain('../drawings/drawing1.xml')
  expect(b.files.get('[Content_Types].xml')).toContain('/xl/drawings/drawing1.xml')
  const cxn = [...drawing.matchAll(/<xdr:cxnSp[\s\S]*?<\/xdr:cxnSp>/g)].map((m) => m[0])
  expect(cxn).toHaveLength(5) // FS・SS・blocks の線 2本・今日の線
  // FS は反している（赤）、SS は基調色の破線。どちらも 1.25pt
  expect(cxn.some((x) => x.includes('<a:ln w="15875"><a:solidFill><a:srgbClr val="' + hex(P.danger) + '"/></a:solidFill><a:prstDash val="solid"/><a:tailEnd type="triangle"'))).toBe(true)
  expect(cxn.some((x) => x.includes('<a:srgbClr val="' + hex(P.bar) + '"/></a:solidFill><a:prstDash val="dash"/>'))).toBe(true)
  // blocks：シアンの太い枠（2.25pt）と、同じ色の丸止めの線
  const rect = drawing.match(/<xdr:twoCellAnchor>(?:(?!<\/xdr:twoCellAnchor>)[\s\S])*?prst="roundRect"[\s\S]*?<\/xdr:twoCellAnchor>/)![0]
  expect(rect).toContain('<a:ln w="28575"><a:solidFill><a:srgbClr val="' + hex(P.neon) + '"/>')
  expect(rect).toContain(`<xdr:row>${r505 - 1}</xdr:row>`)
  expect(cxn.some((x) => x.includes('<a:tailEnd type="oval"') && x.includes(hex(P.neon)))).toBe(true)
  // 親（demo-500）を止める blocks の枠は、親の行から最後の子（demo-504）の行までを囲む
  const frames = [...drawing.matchAll(/<xdr:twoCellAnchor><xdr:from>[\s\S]*?<\/xdr:twoCellAnchor>/g)].map((m) => m[0]).filter((x) => x.includes('prst="roundRect"'))
  expect(frames).toHaveLength(2)
  const r504 = rowOf(main, 'demo-504')
  expect(frames.some((x) => x.includes(`<xdr:from><xdr:col>`) && new RegExp(`<xdr:from>.*?<xdr:row>${r500 - 1}</xdr:row>.*?</xdr:from><xdr:to>.*?<xdr:row>${r504 - 1}</xdr:row>`).test(x))).toBe(true)
  // 今日の線：まっすぐな図形の線（今日の列の左端）
  expect(cxn.some((x) => x.includes('prst="line"') && x.includes('<a:ln w="19050">'))).toBe(true)
})

test('色の表（excelPalette.ts）が、ライトの画面の色と一致する', async ({ page }) => {
  for (const hue of ['blue', 'green'] as const) {
    await mockApi(page, hue)
    await openGantt(page)
    await expect(page.locator('html')).toHaveAttribute('data-hue', hue)
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    // CSS の式を白い canvas に塗って読み戻す（透明度のある色は白の上に重なる）
    const got = await page.evaluate((css: Record<string, string>) => {
      const probe = document.createElement('div')
      document.body.appendChild(probe)
      const canvas = document.createElement('canvas')
      canvas.width = canvas.height = 1
      const ctx = canvas.getContext('2d', { willReadFrequently: true })!
      const out: Record<string, number[]> = {}
      for (const [k, expr] of Object.entries(css)) {
        probe.style.color = expr
        const c = getComputedStyle(probe).color
        ctx.fillStyle = '#ffffff'
        ctx.fillRect(0, 0, 1, 1)
        ctx.fillStyle = c
        ctx.fillRect(0, 0, 1, 1)
        out[k] = [...ctx.getImageData(0, 0, 1, 1).data.slice(0, 3)]
      }
      probe.remove()
      return out
    }, CSS_OF)
    const P = paletteOf(hue)
    for (const k of Object.keys(CSS_OF) as (keyof ExcelPalette)[]) {
      const want = [1, 3, 5].map((i) => parseInt(P[k].slice(i, i + 2), 16))
      const diff = Math.max(...want.map((v, i) => Math.abs(v - got[k]![i]!)))
      expect(diff, `${hue} ${k}: 表 ${P[k]} / 画面 rgb(${got[k]!.join(',')})`).toBeLessThanOrEqual(2)
    }
    await page.unrouteAll({ behavior: 'ignoreErrors' })
  }
})
