import { expect, test, type Page, type Route, type TestInfo } from '@playwright/test'

/**
 * ガント（GuiDesign.md 5.14。pb-220）。
 *
 * **API はすべてモックする。** 描き分け・依存線・暦を決まった値で確かめるためで、
 * dev DB のデモデータに依らない。「今」は見本（docs/design/mock/gantt.html）と同じ
 * 2026-09-29 22:10 JST に固定する。**題名は多バイト文字で書く**（規約）。
 *
 * 走らせ方：`make build` した client を配っているサーバに向ける（PB_E2E_BASE_URL）。
 * API をモックするのでログインは要らない。
 */

const JST = 9 * 3600_000
const D = (m: number, d: number, h = 0, mi = 0) => Date.UTC(2026, m - 1, d, h, mi) - JST
const NOW = D(9, 29, 22, 10)

interface Opts {
  timezone?: string
  theme?: 'light' | 'dark'
  now?: number
  tickets?: Record<string, unknown>[]
  links?: Record<string, unknown>[]
  onTickets?: (url: URL) => void
}

const statuses = [
  { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 },
  { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 2 },
  { key: 'review', name: 'レビュー中', category: 'review', sort_order: 3 },
  { key: 'done', name: '完了', category: 'done', sort_order: 4 },
]
const st = (key: string) => statuses.find((s) => s.key === key)!

function tk(seq: number, title: string, o: { s?: number; e?: number; parent?: number; status?: string; allDay?: boolean; tags?: { id: string; name: string }[] } = {}) {
  const status = st(o.status ?? 'todo')
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type: 'task', title, status,
    priority: null, assignee: null, reporter: null, working_agent: null,
    parent_seq: o.parent ?? null, has_children: false, sort_key: `0|${String(seq).padStart(6, '0')}:`,
    staged_at: null, tags: o.tags ?? [], sprint: null, estimate_point: null, estimate_hours: null,
    actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: o.s ?? null, due_at: o.e ?? null, all_day: o.allDay ?? true,
    closed_at: status.category === 'done' ? D(9, 28) : null,
    version: 1, created_at: D(9, 1), updated_at: D(9, 1),
  }
}

const tagA = { id: '01K0000000000000000000TAGA', name: '設計' }

/** 見本と同じ顔ぶれ（5.14 の描き分けを1通りずつ含む） */
const sampleTickets = [
  tk(88, 'ガントチャート画面を提供する'),
  tk(216, '基準タイムゾーンと祝日カレンダー', { parent: 88, status: 'done', s: D(9, 24), e: D(9, 28), tags: [tagA] }),
  tk(224, 'REST API の日時をエポックミリ秒に統一', { parent: 88, status: 'done', s: D(9, 27), e: D(9, 29) }),
  tk(217, '予定日時をタイムスタンプで持つ', { parent: 88, status: 'done', s: D(9, 27), e: D(9, 30) }),
  tk(218, 'デザイン案を作り承認を得る', { parent: 88, status: 'in_progress', s: D(9, 29), e: D(10, 3), tags: [tagA] }),
  tk(219, 'チケットと依存を全件返す API', { parent: 88, s: D(10, 5), e: D(10, 9) }),
  tk(220, 'ガント画面（閲覧）を提供する', { parent: 88, s: D(10, 8), e: D(10, 21) }),
  tk(221, 'ドラッグで予定と依存を編集する', { parent: 88, s: D(10, 21), e: D(10, 31) }),
  tk(222, '集中モード（終了日だけ）', { parent: 88, e: D(10, 17) }),
  tk(225, '操作説明を書く', { parent: 88, s: D(10, 27), e: D(11, 4) }),
  tk(223, 'Excel に出力する（日付なし）', { parent: 88 }),
  tk(240, 'stg を v3.1 へ上げる'),
  tk(241, '移行手順をリハーサルする', { parent: 240, status: 'review', s: D(10, 6), e: D(10, 8) }),
  tk(242, 'stg の DB を移行する（時刻付き）', { parent: 240, s: D(10, 10, 21), e: D(10, 10, 23, 30), allDay: false }),
  tk(243, '受け入れを確認する（開始日だけ）', { parent: 240, s: D(10, 13) }),
  tk(215, '祝日の取得に失敗したときの表示を直す', { status: 'in_progress', s: D(9, 25), e: D(9, 29) }),
]

let linkId = 0
const ln = (s: number, t: number, type: string, o: { lag?: number; ai?: boolean } = {}) => ({
  id: `01K00000000000000000LNK${String(++linkId).padStart(3, '0')}`, source_seq: s, target_seq: t,
  link_type: type, lag_days: o.lag ?? 0, origin: o.ai ? 'ai_suggested' : 'human',
})

const sampleLinks = [
  ln(216, 217, 'FS'), ln(224, 217, 'SS'), ln(218, 220, 'FS'), ln(219, 220, 'FS'), ln(220, 221, 'FS'),
  ln(220, 222, 'SF'), ln(221, 225, 'FF', { lag: 2 }), ln(220, 223, 'FS'), ln(241, 242, 'blocks'),
  ln(242, 243, 'FS', { ai: true }),
]

const sprints = [
  { id: '01K000000000000000000SPR14', name: 'スプリント 14', goal: null, start_at: D(9, 14), end_at: D(9, 28), all_day: true, status: 'completed', ticket_count: 0, closed_count: 0 },
  { id: '01K000000000000000000SPR15', name: 'スプリント 15', goal: null, start_at: D(9, 28), end_at: D(10, 12), all_day: true, status: 'active', ticket_count: 0, closed_count: 0 },
  { id: '01K000000000000000000SPR16', name: 'スプリント 16', goal: null, start_at: D(10, 12), end_at: D(10, 26), all_day: true, status: 'planned', ticket_count: 0, closed_count: 0 },
]

async function reply(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
}

/** 暦：土日と、9/21〜23・10/12 の祝日（プロジェクトの暦の答えの形。ApiDesign.md 5.8.5） */
function calendarDays(from: string, to: string) {
  const days = []
  const hol: Record<string, string> = { '2026-09-21': '敬老の日', '2026-09-22': '国民の休日', '2026-09-23': '秋分の日', '2026-10-12': 'スポーツの日' }
  for (let t = Date.parse(`${from}T00:00:00Z`); t < Date.parse(`${to}T00:00:00Z`); t += 86_400_000) {
    const d = new Date(t)
    const day = d.toISOString().slice(0, 10)
    const wd = d.getUTCDay()
    if (hol[day]) days.push({ day, is_holiday: true, reason: 'holiday', events: [{ kind: 'holiday', name: hol[day] }], override: null })
    else if (wd === 0 || wd === 6) days.push({ day, is_holiday: true, reason: 'weekend', events: [], override: null })
  }
  return { days }
}

async function mockApi(page: Page, o: Opts = {}) {
  const tickets = o.tickets ?? sampleTickets
  const links = o.links ?? sampleLinks
  await page.clock.install({ time: o.now ?? NOW })
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path === '/api/v1/me') {
      return reply(route, {
        actor: {
          id: '01K00000000000000000000000', kind: 'user', display_name: '検証 太郎', email: 'e2e@example.com',
          system_role: 'administrator', locale: 'ja', timezone: o.timezone ?? 'Asia/Tokyo', theme: o.theme ?? 'light',
          hue: 'blue', must_change_password: false,
        },
        permissions: [],
        projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit'] }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/demo') {
      return reply(route, { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses } })
    }
    if (path === '/api/v1/projects/demo/tickets') {
      if (url.searchParams.get('type') === 'epic') return reply(route, { items: [], total: 0, page: 1, per_page: 200, total_pages: 0 })
      o.onTickets?.(url)
      const staged = url.searchParams.get('staged') === 'true'
      const items = staged ? tickets.filter((t) => t.seq === 240 || t.parent_seq === 240) : tickets
      const seqs = new Set(items.map((t) => t.seq))
      return reply(route, {
        items, links: links.filter((l) => seqs.has(l.source_seq as number) && seqs.has(l.target_seq as number)),
        total: items.length, page: 1, per_page: 5000, total_pages: 1,
      })
    }
    const one = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)$/.exec(path)
    if (one) {
      const t = tickets.find((x) => x.seq === Number(one[1]))
      if (!t) return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
      return reply(route, {
        ...t, body_md: '', parent: null, epic: null, children: [], dod: [], links: [], references: [],
        comment_count: 0, execution_mode: 'agent_draft', readiness: null, readiness_note: null, scope: {},
      })
    }
    if (path.endsWith('/sprints')) return reply(route, { items: sprints })
    if (path.endsWith('/calendar/days')) return reply(route, calendarDays(url.searchParams.get('from')!, url.searchParams.get('to')!))
    if (path.endsWith('/tags') || path.endsWith('/comments') || path.endsWith('/activity')) {
      return reply(route, { items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
    }
    await route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })
}

async function openGantt(page: Page, query = '', ready = '.gantt-svg rect.b') {
  await page.goto(`/p/demo/gantt${query}`)
  await expect(page.getByRole('heading', { level: 1, name: 'ガント' })).toBeVisible()
  await expect(page.locator(ready).first()).toBeAttached()
}

async function shot(page: Page, testInfo: TestInfo, name: string) {
  const path = testInfo.outputPath(`${name}.png`)
  await page.screenshot({ path })
  await testInfo.attach(name, { path, contentType: 'image/png' })
}

/** SVG の中の要素の数 */
const count = (page: Page, selector: string) => page.locator(`.gantt-svg ${selector}`).count()

test('日付の有無で描き分け、依存線を種別の手がかりで描く', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApi(page)
  await openGantt(page)

  // 帯：両日ありはバー、完了は沈める
  expect(await count(page, 'rect.b.done')).toBeGreaterThanOrEqual(2)
  expect(await count(page, 'rect.b.in_progress')).toBeGreaterThanOrEqual(1)
  expect(await count(page, 'rect.b.review')).toBe(1)
  // 片方だけはマイルストーン記号、日付の無い親は寸法線
  expect(await count(page, 'path.ms')).toBeGreaterThanOrEqual(1)
  expect(await count(page, 'path.roll')).toBeGreaterThanOrEqual(2)
  await expect(page.locator('.gantt-svg text.lbl', { hasText: '配下' }).first()).toBeAttached()

  // 依存線：FS 以外は札、blocks は止め、AI は紫の破線、ずらしは +2d
  const tags = await page.locator('.gantt-svg .d-tagt').allTextContents()
  expect(tags).toEqual(expect.arrayContaining(['SS', 'SF']))
  await expect(page.locator('.gantt-svg .dep.blk .d-stop')).toHaveCount(1)
  // 日付の無い相手（pb-223）への線は描かない
  const deps = await count(page, 'g.dep')
  expect(deps).toBeLessThan(sampleLinks.length)

  // 背景：土日祝・スプリント・「今」の線
  expect(await count(page, 'rect.c-sat')).toBeGreaterThanOrEqual(1)
  expect(await count(page, 'rect.c-sun')).toBeGreaterThanOrEqual(1)
  await expect(page.locator('.gantt-svg rect.spr.act')).toHaveCount(1)
  await expect(page.locator('.gantt-svg rect.spr.plan')).toHaveCount(1)
  // 「今」の線は本体とヘッダ（札から小目盛りまで）の2本（5.14「時間軸のヘッダ」）
  await expect(page.locator('.gantt-svg line.g-now')).toHaveCount(2)
  await expect(page.locator('.gantt-svg .h-nowt')).toHaveText('22:10')
  // 基準と同じタイムゾーンなら見る人の段を出さない
  await expect(page.locator('.gantt-svg .h-v')).toHaveCount(0)
  await expect(page.locator('.gantt-thdr')).not.toContainText('あなた')
  await shot(page, testInfo, 'gantt-1440-light')
})

test('見る人のタイムゾーンが違うと2段目を出し、FF のずらしを札に出す', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApi(page, { timezone: 'America/Los_Angeles' })
  await openGantt(page)
  await expect(page.locator('.gantt-thdr')).toContainText('America/Los_Angeles')
  expect(await count(page, '.h-v')).toBeGreaterThan(3)
  // 「今」の時刻は見る人の時刻（JST 22:10 は PDT 06:10）
  await expect(page.locator('.gantt-svg .h-nowt')).toHaveText('06:10')
  // 日本・韓国以外は土日を灰で塗る
  await expect(page.locator('.gantt')).toHaveAttribute('data-cal', 'other')
  // FF +2d は後ろの帯なので、送ってから見る
  await page.locator('.gantt-seg button', { hasText: '週' }).click()
  await expect(page.locator('.gantt-svg .d-tagt', { hasText: '+2d' })).toBeAttached()
  await shot(page, testInfo, 'gantt-la-week')
})

test('ツリーを畳んでも配下の期間は残り、開閉はバックログと共有する', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApi(page)
  await openGantt(page)
  const rowsBefore = await page.locator('.gantt-tr').count()
  await page.locator('.gantt-tr[data-seq="88"] .caret').click()
  await expect(page.locator('.gantt-tr[data-seq="220"]')).toHaveCount(0)
  expect(await page.locator('.gantt-tr').count()).toBeLessThan(rowsBefore)
  await expect(page.locator('.gantt-svg text.lbl', { hasText: '配下' }).first()).toBeAttached()
  const stored = await page.evaluate(() => localStorage.getItem('pb.backlog_tree_collapsed'))
  expect(JSON.parse(stored!)).toEqual({ demo: [88] })
})

test('絞り込みは URL に載り、API に送る', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const seen: string[] = []
  await mockApi(page, { onTickets: (url) => seen.push(url.search) })
  await openGantt(page)
  expect(seen[0]).toContain('view=gantt')
  expect(seen[0]).toContain('type=story%2Ctask')
  await page.getByRole('button', { name: /絞り込み/ }).click()
  await page.getByLabel('オンステージ').check()
  await page.getByLabel('スプリント中').check()
  await expect(page).toHaveURL(/staged=true/)
  await expect(page).toHaveURL(/sprint=active/)
  await expect.poll(() => seen.at(-1)).toContain('staged=true')
  await expect.poll(() => seen.at(-1)).toContain('sprint=active')
  await expect(page.getByRole('button', { name: /絞り込み（2）/ })).toBeVisible()
  await expect(page.locator('.gantt-tr[data-seq="220"]')).toHaveCount(0)
  await expect(page.locator('.gantt-tr[data-seq="241"]')).toHaveCount(1)
})

test('帯を押すと詳細を浮かせて開き、閉じるとガントの URL へ戻る', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApi(page, { theme: 'dark' })
  await openGantt(page, '?group=tag')
  await page.locator('.gantt-tr[data-seq="218"]').first().click()
  await expect(page).toHaveURL(/\/p\/demo\/tickets\/218\?.*from=gantt/)
  await expect(page).toHaveURL(/group=tag/)
  const pane = page.locator('.gantt-detail')
  await expect(pane).toBeVisible()
  const box = (await pane.boundingBox())!
  expect(Math.round(box.width)).toBe(380)
  await expect(page.locator('.gantt-svg rect.rowsel')).toHaveCount(1)
  await shot(page, testInfo, 'gantt-detail-dark')
  await pane.getByRole('button', { name: /閉じる/ }).first().click()
  await expect(page).toHaveURL(/\/p\/demo\/gantt\?group=tag$/)
})

test('拡大縮小：段階のボタン・+ / -・Ctrl＋ホイール', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApi(page)
  await openGantt(page)
  const pressed = () => page.locator('.gantt-seg button[aria-pressed="true"]').textContent()
  expect(await pressed()).toContain('日')
  await page.locator('.gantt-seg button', { hasText: '月' }).click()
  await expect.poll(pressed).toContain('月')
  await page.locator('.gantt-chart').click({ position: { x: 5, y: 700 } })
  await page.keyboard.press('+')
  await expect.poll(pressed).toContain('週')
  await page.keyboard.press('-')
  await expect.poll(pressed).toContain('月')
  // Ctrl＋ホイールは連続量（段階から外れると、どのボタンも押された形にしない）
  const chart = page.locator('.gantt-chart')
  const b = (await chart.boundingBox())!
  await page.mouse.move(b.x + 300, b.y + 400)
  await page.keyboard.down('Control')
  await page.mouse.wheel(0, -120)
  await page.keyboard.up('Control')
  await expect(page.locator('.gantt-seg button[aria-pressed="true"]')).toHaveCount(0)
  // 分の段階では時刻の目盛りを出す
  await page.locator('.gantt-seg button', { hasText: '分' }).click()
  await expect.poll(async () => (await page.locator('.gantt-svg .h-min').allTextContents()).some((s) => /^\d\d:00$/.test(s))).toBe(true)
})

test('選んだグループ化の軸を覚え、group の無いバックログで補う', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApi(page)
  await openGantt(page)
  await page.locator('.gantt-group select').selectOption('tag')
  await expect(page).toHaveURL(/group=tag/)
  await expect(page.locator('.gantt-tgroup', { hasText: '設計' })).toBeVisible()
  expect(JSON.parse((await page.evaluate(() => localStorage.getItem('pb.group_axis')))!)).toEqual({ demo: 'tag' })
  await page.goto('/p/demo/backlog')
  await expect(page).toHaveURL(/\/p\/demo\/backlog\?group=tag$/)
})

test('複数の画面幅とライト／ダークで崩れない', async ({ page }, testInfo) => {
  for (const theme of ['light', 'dark'] as const) {
    await mockApi(page, { theme, timezone: 'America/Los_Angeles' })
    for (const [w, h] of [[1440, 900], [1024, 768], [700, 800]] as const) {
      await page.setViewportSize({ width: w, height: h })
      await openGantt(page)
      // 見出しの「ガント」が道具に押し潰されない
      const title = (await page.getByRole('heading', { level: 1, name: 'ガント' }).boundingBox())!
      expect(title.width).toBeGreaterThan(40)
      // ツリーの見出しと本体の最初の行の上端が揃う
      const thdr = (await page.locator('.gantt-thdr').boundingBox())!
      const firstRow = (await page.locator('.gantt-tr').first().boundingBox())!
      expect(Math.abs(thdr.y + thdr.height - firstRow.y)).toBeLessThanOrEqual(1)
      // 横にページがはみ出さない
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      await shot(page, testInfo, `gantt-${w}-${theme}`)
    }
    await page.unrouteAll({ behavior: 'ignoreErrors' })
  }
})

test('2,000行・4,000本の依存でスクロールが滑らか', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const tickets: Record<string, unknown>[] = []
  const links: Record<string, unknown>[] = []
  let seq = 1
  let seed = 220
  const rnd = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648)
  const dated: number[] = []
  for (let g = 0; g < 200; g++) {
    const parent = seq++
    tickets.push(tk(parent, `性能検証の親チケット ${g}（多バイト）`))
    const base = Math.floor(rnd() * 360) - 120
    for (let c = 0; c < 9; c++) {
      const s = D(9, 29) + (base + Math.floor(rnd() * 40)) * 86_400_000
      const me = seq++
      tickets.push(tk(me, `子チケット ${g}-${c} 検索・集計・表示の調整`, { parent, s, e: s + (1 + Math.floor(rnd() * 14)) * 86_400_000, status: ['todo', 'in_progress', 'review', 'done'][c % 4] }))
      dated.push(me)
    }
  }
  const types = ['FS', 'FS', 'SS', 'FF', 'SF', 'blocks']
  while (links.length < 4000) {
    const a = dated[Math.floor(rnd() * dated.length)]!
    const b = dated[Math.floor(rnd() * dated.length)]!
    if (a !== b) links.push(ln(a, b, types[links.length % types.length]!))
  }
  expect(tickets.length).toBe(2000)
  await mockApi(page, { tickets, links })
  // 乱数の予定なので、最初の画面にバーが入るとは限らない。行が出るのを待つ
  await openGantt(page, '', '.gantt-tr')
  await expect(page.locator('.gantt-tr').first()).toBeVisible()
  // DOM の行は見えている分だけ（仮想スクロール）
  expect(await page.locator('.gantt-tr').count()).toBeLessThan(60)

  // 縦に 120 フレーム、横に 60 フレーム送り、1フレームの時間を測る（縦と横を分けて出す）
  const frames = await page.evaluate(async () => {
    const el = document.querySelector('.gantt-chart') as HTMLElement
    const frame = () => new Promise<number>((r) => requestAnimationFrame(r))
    const run = async (n: number, step: () => void) => {
      const times: number[] = []
      let last = await frame()
      for (let i = 0; i < n; i++) {
        step()
        const t = await frame()
        times.push(t - last)
        last = t
      }
      times.sort((a, b) => a - b)
      return { median: times[Math.floor(times.length / 2)]!, p95: times[Math.floor(times.length * 0.95)]! }
    }
    const vertical = await run(120, () => { el.scrollTop += 180 })
    const horizontal = await run(60, () => { el.scrollLeft += 240 })
    return { vertical, horizontal }
  })
  testInfo.annotations.push({ type: 'frames', description: JSON.stringify(frames) })
  console.log('gantt frames(ms)', JSON.stringify(frames))
  // 1フレーム 16.7ms（60fps）。ヘッドレスの揺れを見込み、中央値は 2フレーム以内とする
  expect(frames.vertical.median).toBeLessThan(34)
  expect(frames.horizontal.median).toBeLessThan(34)
  await shot(page, testInfo, 'gantt-2000')
})


// pb-235：月末の夜と月初、右端でも札と月名を両方読める。
test('月境界の時刻札は大目盛りを隠さない', async ({ page }, testInfo) => {
  for (const theme of ['light', 'dark'] as const) {
    for (const width of [1440, 1024]) {
      for (const now of [D(9, 30, 20, 54), D(10, 1, 0, 5)]) {
        await page.setViewportSize({ width, height: 900 })
        await mockApi(page, { theme, now })
        await openGantt(page)
        const labels = page.locator('.gantt-svg .h-maj')
        await expect(labels.filter({ hasText: '2026-10' })).toBeAttached()
        const check = async () => {
          const bounds = await page.locator('.gantt-svg').evaluate((svg) => {
            const box = svg.querySelector('.now-box')!.getBoundingClientRect()
            const rect = svg.getBoundingClientRect()
            return {
              inside: box.left >= rect.left && box.right <= rect.right,
              clear: [...svg.querySelectorAll('.h-maj')].every((el) => {
                const b = el.getBoundingClientRect()
                return b.right <= box.left || b.left >= box.right
              }),
            }
          })
          expect(bounds).toEqual({ inside: true, clear: true })
        }
        await check()
        await shot(page, testInfo, `boundary-${theme}-${width}-${now}`)
        // 時刻札が右端へ寄ったときは、見出しを左へ逃がす。
        await page.locator('.gantt-chart').evaluate((el) => {
          const line = document.querySelector('.gantt-svg line.g-now') as SVGLineElement
          const svg = document.querySelector('.gantt-svg')!.getBoundingClientRect()
          el.scrollLeft += Number(line.getAttribute('x1')) - svg.width + 20
        })
        await expect.poll(() => page.locator('.gantt-svg').evaluate((svg) => {
          const box = svg.querySelector('.now-box')!.getBoundingClientRect()
          return svg.getBoundingClientRect().right - box.right
        })).toBeLessThan(5)
        await check()
        await page.unrouteAll({ behavior: 'ignoreErrors' })
      }
    }
  }
})
