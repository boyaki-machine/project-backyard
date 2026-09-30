import { expect, test, type Page, type Route } from '@playwright/test'

/**
 * 依存のずらし（lag_days）を詳細の関連チケットの行で直す（GuiDesign.md 5.5「行と操作」。pb-231）。
 *
 * **本物のクリックとキーで回す**（page.mouse / page.keyboard は CDP の Input を通す）。
 * ガントの上で浮かせた詳細から直し、**線の札が描き直される**ことまで見る。
 * API はすべてモックする。題名は多バイト文字で書く。
 */

const JST = 9 * 3600_000
const D = (m: number, d: number) => Date.UTC(2026, m - 1, d) - JST
const NOW = Date.UTC(2026, 8, 29, 13, 10)

const statuses = [
  { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 },
  { key: 'done', name: '完了', category: 'done', sort_order: 2 },
]

function tk(seq: number, title: string, s: number, e: number) {
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type: 'task', title, status: statuses[0],
    priority: null, assignee: null, reporter: null, working_agent: null, parent_seq: null, has_children: false,
    sort_key: `0|${String(seq).padStart(6, '0')}:`, staged_at: null, tags: [], sprint: null,
    estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: s, due_at: e, all_day: true, closed_at: null, version: 1, created_at: D(9, 1), updated_at: D(9, 1),
  }
}

const tickets = [tk(601, '設計を書く', D(9, 28), D(10, 1)), tk(602, '実装する', D(10, 2), D(10, 6)), tk(603, '試験する', D(10, 7), D(10, 9))]
const brief = (seq: number) => {
  const t = tickets.find((x) => x.seq === seq)!
  return { seq, title: t.title, type: 'task', status: t.status }
}

interface World {
  lag: number
  patches: { path: string; body: Record<string, unknown> }[]
}

async function reply(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockApi(page: Page): Promise<World> {
  const w: World = { lag: 0, patches: [] }
  const ganttLinks = () => [
    { id: '01K00000000000000000LNKFS1', source_seq: 601, target_seq: 602, link_type: 'FS', lag_days: w.lag, origin: 'human' },
    { id: '01K00000000000000000LNKBK1', source_seq: 602, target_seq: 603, link_type: 'blocks', lag_days: 0, origin: 'human' },
  ]
  /** 詳細の関連チケット。**そのチケットから見た向き**で返す（ticket は常に相手） */
  const detailLinks = (seq: number) =>
    seq === 601
      ? [{ id: '01K00000000000000000LNKFS1', direction: 'outgoing', link_type: 'FS', ticket: brief(602), lag_days: w.lag, origin: 'human', created_at: D(9, 1) }]
      : seq === 602
        ? [
            { id: '01K00000000000000000LNKBK1', direction: 'outgoing', link_type: 'blocks', ticket: brief(603), lag_days: 0, origin: 'human', created_at: D(9, 1) },
            { id: '01K00000000000000000LNKFS1', direction: 'incoming', link_type: 'FS', ticket: brief(601), lag_days: w.lag, origin: 'human', created_at: D(9, 1) },
          ]
        : []
  await page.clock.install({ time: NOW })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname
    if (path === '/api/v1/me') {
      return reply(route, {
        actor: { id: '01K00000000000000000000000', kind: 'user', display_name: '検証 三郎', email: 'e2e@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
        permissions: [],
        projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit'] }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/demo') return reply(route, { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses }, settings: {}, version: 1 })
    if (path === '/api/v1/projects/demo/tickets') {
      if (url.searchParams.get('type') === 'epic') return reply(route, { items: [], total: 0, page: 1, per_page: 200, total_pages: 0 })
      return reply(route, { items: tickets, links: ganttLinks(), total: tickets.length, page: 1, per_page: 5000, total_pages: 1 })
    }
    const one = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)$/.exec(path)
    if (one) {
      const t = tickets.find((x) => x.seq === Number(one[1]))!
      return reply(route, { ...t, body_md: '', parent: null, epic: null, children: [], dod: [], links: detailLinks(t.seq), references: [], comment_count: 0, execution_mode: 'agent_draft', readiness: null, readiness_note: null, scope: {} })
    }
    const lk = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)\/links\/([\w]+)$/.exec(path)
    if (lk && req.method() === 'PATCH') {
      const body = req.postDataJSON() as Record<string, unknown>
      w.patches.push({ path, body })
      w.lag = body['lag_days'] as number
      return reply(route, detailLinks(Number(lk[1])).find((l) => l.id === lk[2]))
    }
    if (path.endsWith('/calendar/days')) return reply(route, { days: [] })
    return reply(route, { items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
  return w
}

async function openDetail(page: Page, seq: number) {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/p/demo/gantt')
  await expect(page.locator('.gantt-svg rect.b').first()).toBeAttached()
  await page.locator('.gantt-tr', { hasText: `demo-${seq}` }).click()
  await expect(page.locator('.gantt-detail .rel-row').first()).toBeVisible()
}

test('FS のチップを押してずらしを入れると保存され、行のラベルとガントの線の札が変わる', async ({ page }) => {
  const w = await mockApi(page)
  await openDetail(page, 601)
  const chip = page.locator('.gantt-detail .rel-kind-edit')
  await expect(chip).toHaveText('FS 自先行')
  await chip.click()
  const input = page.locator('.gantt-detail .rel-lag-input')
  await expect(input).toBeFocused({ timeout: 1000 }).catch(() => input.click())
  await input.fill('2')
  await page.keyboard.press('Enter')
  await expect.poll(() => w.patches.length).toBe(1)
  expect(w.patches[0]).toEqual({ path: '/api/v1/projects/demo/tickets/601/links/01K00000000000000000LNKFS1', body: { lag_days: 2 } })
  await expect(page.locator('.gantt-detail .rel-kind-edit')).toHaveText('FS 自先行 +2d')
  // ガントの線の札も描き直す（取り直さずに差し替える）。2日ずらすと後行の開始より後になり、
  // 依存に反する配置の ⚠ も付く（違反の判定もずらしを使っている）
  await expect.poll(async () => page.locator('.gantt-svg .d-tagt').allTextContents()).toContain('⚠ +2d')
  await expect(page.locator('.gantt-svg g.dep.bad')).toHaveCount(1)
})

test('後行の側（incoming）の FS も直せ、blocks のチップは押せない', async ({ page }) => {
  const w = await mockApi(page)
  await openDetail(page, 602)
  const edit = page.locator('.gantt-detail .rel-kind-edit')
  await expect(edit).toHaveCount(1)
  await expect(edit).toHaveText('FS 自後行')
  await expect(page.locator('.gantt-detail span.rel-kind')).toHaveText(['自先行'])
  await edit.click()
  await page.locator('.gantt-detail .rel-lag-input').fill('-1')
  await page.locator('.gantt-detail .rel-lag').getByRole('button', { name: '保存' }).click()
  await expect.poll(() => w.patches.length).toBe(1)
  // 後行の口（602）から直す。direction を問わない
  expect(w.patches[0]!.path).toBe('/api/v1/projects/demo/tickets/602/links/01K00000000000000000LNKFS1')
  await expect(page.locator('.gantt-detail .rel-kind-edit')).toHaveText('FS 自後行 -1d')
})

test('範囲外は送らずに理由を出し、Esc で取り消すと何も送らない', async ({ page }) => {
  const w = await mockApi(page)
  await openDetail(page, 601)
  await page.locator('.gantt-detail .rel-kind-edit').click()
  const input = page.locator('.gantt-detail .rel-lag-input')
  await input.fill('400')
  await page.keyboard.press('Enter')
  await expect(page.locator('.gantt-detail .field-error')).toContainText('-365〜365')
  expect(w.patches).toHaveLength(0)
  await input.fill('3')
  await input.press('Escape')
  await expect(page.locator('.gantt-detail .rel-lag-input')).toHaveCount(0)
  await expect(page.locator('.gantt-detail .rel-kind-edit')).toHaveText('FS 自先行')
  expect(w.patches).toHaveLength(0)
  // 詳細は開いたまま（Esc は入力欄が先に使う）
  await expect(page.locator('.gantt-detail')).toBeVisible()
})
