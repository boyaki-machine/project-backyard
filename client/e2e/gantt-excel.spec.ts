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

interface Book {
  cells: Map<string, { v: string | null; s: number }>
  /** 列の既定の書式（列の番号 1始まり → s） */
  colStyle: Map<number, number>
  xf: { fillId: number; fontId: number; borderId: number }[]
  fills: { pattern: string; fg: string | null }[]
  fonts: { bold: boolean; color: string | null }[]
  borders: string[]
  merges: string[]
  outline: Map<number, number>
  sheet: string
  files: Map<string, string>
}

function readBook(path: string): Book {
  const files = unzip(readFileSync(path))
  const sheet = files.get('xl/worksheets/sheet1.xml')!
  const styles = files.get('xl/styles.xml')!
  const cells = new Map<string, { v: string | null; s: number }>()
  for (const m of sheet.matchAll(/<c r="([A-Z]+\d+)"(?: s="(\d+)")?(?: t="inlineStr")?(?:\/>|>(?:<is><t[^>]*>([\s\S]*?)<\/t><\/is>|<v>([^<]*)<\/v>)<\/c>)/g)) {
    const raw = m[3] ?? m[4] ?? null
    cells.set(m[1]!, { v: raw === null ? null : raw.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&amp;/g, '&'), s: Number(m[2] ?? 0) })
  }
  const colStyle = new Map<number, number>()
  for (const m of sheet.matchAll(/<col min="(\d+)" max="\d+"[^>]*?(?: style="(\d+)")?\/>/g)) if (m[2]) colStyle.set(Number(m[1]), Number(m[2]))
  const section = (tag: string) => styles.match(new RegExp(`<${tag} count="\\d+">([\\s\\S]*?)</${tag}>`))![1]!
  const xf = [...section('cellXfs').matchAll(/<xf [^>]*?fontId="(\d+)" fillId="(\d+)" borderId="(\d+)"/g)].map((m) => ({ fontId: Number(m[1]), fillId: Number(m[2]), borderId: Number(m[3]) }))
  const fills = [...section('fills').matchAll(/<fill>([\s\S]*?)<\/fill>/g)].map((m) => ({
    pattern: m[1]!.match(/patternType="(\w+)"/)![1]!,
    fg: m[1]!.match(/<fgColor rgb="FF([0-9A-F]{6})"/)?.[1]?.toLowerCase() ?? null,
  }))
  const fonts = [...section('fonts').matchAll(/<font>([\s\S]*?)<\/font>/g)].map((m) => ({
    bold: m[1]!.includes('<b/>'),
    color: m[1]!.match(/<color rgb="FF([0-9A-F]{6})"/)?.[1]?.toLowerCase() ?? null,
  }))
  const borders = [...section('borders').matchAll(/<border>([\s\S]*?)<\/border>/g)].map((m) => m[1]!)
  const merges = [...sheet.matchAll(/<mergeCell ref="([^"]+)"/g)].map((m) => m[1]!)
  const outline = new Map<number, number>()
  for (const m of sheet.matchAll(/<row r="(\d+)"[^>]*? outlineLevel="(\d+)"/g)) outline.set(Number(m[1]), Number(m[2]))
  return { cells, colStyle, xf, fills, fonts, borders, merges, outline, sheet, files }
}

const hex = (c: string) => c.replace('#', '').toLowerCase()

/** セルに効いている書式（セルに無ければ列の既定） */
function styleOf(b: Book, ref: string) {
  const col = ref.replace(/\d+/g, '')
  const colNo = [...col].reduce((n, ch) => n * 26 + ch.charCodeAt(0) - 64, 0)
  const s = b.cells.get(ref)?.s ?? b.colStyle.get(colNo) ?? 0
  const x = b.xf[s]!
  return { fill: b.fills[x.fillId]!, font: b.fonts[x.fontId]!, border: b.borders[x.borderId]! }
}

/** ID の列（A）でチケットの行を探す */
function rowOf(b: Book, id: string): number {
  for (const [ref, c] of b.cells) if (ref.startsWith('A') && /^A\d+$/.test(ref) && c.v === id) return Number(ref.slice(1))
  throw new Error(`${id} の行が無い`)
}

/** 日（3行目）の数字と月（2行目の結合）から、その日の列の名前を探す */
function colOfDay(b: Book, m: number, d: number): string {
  const monthStart = [...b.cells].find(([ref, c]) => /^[A-Z]+2$/.test(ref) && c.v === `2026-${String(m).padStart(2, '0')}`)![0].replace('2', '')
  const toNo = (col: string) => [...col].reduce((n, ch) => n * 26 + ch.charCodeAt(0) - 64, 0)
  const toName = (n: number) => { let s = ''; while (n > 0) { const r = (n - 1) % 26; s = String.fromCharCode(65 + r) + s; n = Math.floor((n - 1) / 26) } return s }
  for (let n = toNo(monthStart); n < toNo(monthStart) + 31; n++) if (b.cells.get(`${toName(n)}3`)?.v === String(d)) return toName(n)
  throw new Error(`${m}/${d} の列が無い`)
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

test('押したときだけ Excel のコードを読み込み、画面と対応する中身の xlsx を落とす', async ({ page }, testInfo) => {
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

  // 表題と見出し。日本語が崩れない
  expect(b.cells.get('A1')!.v).toContain('demo ガント')
  expect(b.files.get('xl/workbook.xml')).toContain('name="demo ガント"')
  expect(['A5', 'B5', 'C5', 'D5', 'E5', 'F5'].map((r) => b.cells.get(r)!.v)).toEqual(['ID', 'タイトル', '状態', '開始', '終了', '先行'])
  // 固定と行の階層（親の下の子は 1）
  expect(b.sheet).toContain('<pane xSplit="6" ySplit="5" topLeftCell="G6" activePane="bottomRight" state="frozen"/>')
  expect(b.outline.get(rowOf(b, 'demo-501'))).toBe(1)
  expect(b.outline.has(rowOf(b, 'demo-500'))).toBe(false)

  // 行の中身
  const r501 = rowOf(b, 'demo-501')
  expect(b.cells.get(`B${r501}`)!.v).toBe('書き出しを作る')
  expect(b.cells.get(`C${r501}`)!.v).toBe('進行中')
  expect(b.cells.get(`D${r501}`)!.v).toBe('2026-09-28')
  expect(b.cells.get(`E${r501}`)!.v).toBe('2026-10-01') // 終わりは1日戻す
  const r502 = rowOf(b, 'demo-502')
  expect(b.cells.get(`F${r502}`)!.v).toBe('FS demo-501, SS demo-503 +2d')
  const r505 = rowOf(b, 'demo-505')
  expect(b.cells.get(`D${r505}`)!.v).toBe('2026-10-05 21:00 GMT+9')
  // 親は配下の期間
  expect(b.cells.get(`D${rowOf(b, 'demo-500')}`)!.v).toBe('—（配下 2026-09-24）')

  // バー：進行中は基調色 26%、未着手は斜線、完了は --pb-3、レビュー中は破線の罫
  expect(styleOf(b, `${colOfDay(b, 9, 30)}${r501}`).fill).toEqual({ pattern: 'solid', fg: hex(P.barFill) })
  expect(styleOf(b, `${colOfDay(b, 10, 2)}${r502}`).fill.pattern).toBe('lightUp')
  const r503 = rowOf(b, 'demo-503')
  expect(styleOf(b, `${colOfDay(b, 9, 25)}${r503}`).fill).toEqual({ pattern: 'solid', fg: hex(P.doneFill) })
  expect(styleOf(b, `${colOfDay(b, 10, 5)}${r505}`).border).toContain('style="dashed"')
  // バーの外は塗らない
  expect(styleOf(b, `${colOfDay(b, 10, 2)}${r501}`).fill.pattern).toBe('none')
  // 完了の行は文字を沈める
  expect(styleOf(b, `B${r503}`).font.color).toBe(hex(P.muted))
  // マイルストーン（終了だけ）は ]
  expect(b.cells.get(`${colOfDay(b, 10, 8)}${rowOf(b, 'demo-504')}`)!.v).toBe(']')

  // 土日祝：土曜は青み・日曜は赤み・祝日は斜線（列の既定の書式）
  expect(styleOf(b, `${colOfDay(b, 10, 3)}${r501}`).fill).toEqual({ pattern: 'solid', fg: hex(P.sat) })
  expect(styleOf(b, `${colOfDay(b, 10, 4)}${r501}`).fill).toEqual({ pattern: 'solid', fg: hex(P.sun) })
  expect(styleOf(b, `${colOfDay(b, 10, 12)}${r501}`).fill.pattern).toBe('lightUp')
  expect(b.cells.get(`${colOfDay(b, 10, 12)}4`)!.v).toBe('祝')
  // 土曜に掛かるバーは、バーの塗りが勝つ
  expect(styleOf(b, `${colOfDay(b, 10, 3)}${r502}`).fill.pattern).toBe('lightUp')

  // 今日の列の左に基調色の太い罫、スプリントの帯（進行中はオレンジの太線）
  expect(styleOf(b, `${colOfDay(b, 9, 29)}${r501}`).border).toContain(`<left style="medium"><color rgb="FF${hex(P.bar).toUpperCase()}"/>`)
  const spr = colOfDay(b, 9, 28)
  expect(b.cells.get(`${spr}5`)!.v).toBe('スプリント 15')
  expect(styleOf(b, `${spr}5`).border).toContain(`<top style="medium"><color rgb="FF${hex(P.neonAct).toUpperCase()}"/>`)
  expect(b.merges.some((m) => m.startsWith(`${spr}5:`))).toBe(true)
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
