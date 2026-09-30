import { expect, test, type Page, type Route } from '@playwright/test'

/**
 * チケット検索から開いた詳細の中のリンク（GuiDesign.md 3.2・5.13。pb-229）。
 *
 * **親・エピック・子・関連チケットのどれを押しても、`from=search` と検索の条件が残り、
 * 後ろに検索の一覧が残る**——落とすと、押した先の詳細の後ろがバックログに変わる。
 *
 * API はすべてモックする。題名は多バイト文字で書く。
 */

const JST = 9 * 3600_000
const D = (m: number, d: number) => Date.UTC(2026, m - 1, d) - JST
const NOW = Date.UTC(2026, 8, 29, 13, 10)

const statuses = [
  { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 },
  { key: 'done', name: '完了', category: 'done', sort_order: 2 },
]

function tk(seq: number, type: string, title: string, parent: number | null = null) {
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type, title, status: statuses[0],
    priority: null, assignee: null, reporter: null, working_agent: null, parent_seq: parent, has_children: false,
    sort_key: `0|${String(seq).padStart(6, '0')}:`, staged_at: null, tags: [], sprint: null,
    estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: null, due_at: null, all_day: true, closed_at: null, version: 1, created_at: D(9, 1), updated_at: D(9, 1),
  }
}

// 700 エピック ─ 710 親 ─ 711 開く詳細 ─ 712 子。711 は 713 と関連する
const tickets = [
  tk(700, 'epic', '計画の柱'),
  tk(710, 'task', '計画の親', 700),
  tk(711, 'task', '計画を立てる', 710),
  tk(712, 'task', '計画の子', 711),
  tk(713, 'task', '計画の相手'),
]
const brief = (seq: number) => {
  const t = tickets.find((x) => x.seq === seq)!
  return { seq, title: t.title, type: t.type, status: t.status, assignee: null }
}

async function reply(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockApi(page: Page) {
  await page.clock.install({ time: NOW })
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path === '/api/v1/me') {
      return reply(route, {
        actor: { id: '01K00000000000000000000000', kind: 'user', display_name: '検証 四郎', email: 'e2e@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
        permissions: [],
        projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit'] }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/demo') return reply(route, { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses }, settings: {}, version: 1 })
    if (path === '/api/v1/projects/demo/tickets') {
      const items = url.searchParams.get('type') === 'epic' ? tickets.filter((t) => t.type === 'epic') : tickets
      return reply(route, { items, links: [], total: items.length, page: 1, per_page: 50, total_pages: 1 })
    }
    const one = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)$/.exec(path)
    if (one) {
      const t = tickets.find((x) => x.seq === Number(one[1]))
      if (!t) return reply(route, {}, 404)
      const parent = t.parent_seq === null ? null : brief(t.parent_seq)
      const epic = t.seq === 711 || t.seq === 710 ? brief(700) : null
      const children = tickets.filter((x) => x.parent_seq === t.seq).map((x) => brief(x.seq))
      const links = t.seq === 711 ? [{ id: '01K00000000000000000LNKREL', direction: 'outgoing', link_type: 'relates', ticket: brief(713), lag_days: 0, origin: 'human', created_at: D(9, 1) }] : []
      return reply(route, { ...t, body_md: '', parent, epic, children, dod: [], links, references: [], comment_count: 0, execution_mode: 'agent_draft', readiness: null, readiness_note: null, scope: {} })
    }
    if (path.endsWith('/calendar/days')) return reply(route, { days: [] })
    return reply(route, { items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
}

const START = '/p/demo/tickets/711?q=%E8%A8%88%E7%94%BB&type=task&from=search'
const detail = (page: Page) => page.locator('.split-secondary')

const cases: { name: string; to: number; link: (page: Page) => ReturnType<Page['locator']> }[] = [
  { name: '親', to: 710, link: (page) => detail(page).getByRole('link', { name: '親チケット demo-710 を開く' }) },
  { name: 'エピック', to: 700, link: (page) => detail(page).getByRole('link', { name: 'エピック demo-700 を開く' }) },
  { name: '子', to: 712, link: (page) => detail(page).locator('a.child', { hasText: 'demo-712' }) },
  { name: '関連チケット', to: 713, link: (page) => detail(page).locator('a.rel-main', { hasText: 'demo-713' }) },
]

for (const c of cases) {
  test(`検索から開いた詳細の${c.name}のリンクは、検索の条件と from=search を残す`, async ({ page }) => {
    await mockApi(page)
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(START)
    await expect(page.getByRole('heading', { level: 1, name: 'チケット検索' })).toBeVisible()
    await expect(detail(page).getByText('計画を立てる').first()).toBeVisible()

    await c.link(page).click()
    await expect(page).toHaveURL(new RegExp(`/p/demo/tickets/${c.to}\\?`))
    const q = new URL(page.url()).searchParams
    expect(q.get('from')).toBe('search')
    expect(q.get('q')).toBe('計画')
    expect(q.get('type')).toBe('task')
    // 後ろは検索の一覧のまま（バックログに変わらない）
    await expect(page.getByRole('heading', { level: 1, name: 'チケット検索' })).toBeVisible()
    await expect(page.getByRole('heading', { level: 1, name: 'バックログ' })).toHaveCount(0)
  })
}
