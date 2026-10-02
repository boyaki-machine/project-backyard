import { expect, test, type Page } from '@playwright/test'

/**
 * ガントのエピックの袖章（GuiDesign.md 5.4.4・5.14。pb-238）。
 *
 * ツリーの行の左端と、帯の頭に同じ袖章を描く。帯の頭は**区切りの線を引かず、
 * 帯の枠の上まで描いて左端を揃える**。API はすべてモックする。
 */

const JST = 9 * 3600_000
const D = (m: number, d: number) => Date.UTC(2026, m - 1, d) - JST
const NOW = Date.UTC(2026, 8, 29, 13, 10)

const todo = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }
const doing = { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 2 }

const allEpics = Array.from({ length: 10 }, (_, i) => ({
  seq: 500 + i, type: 'epic', title: `エピック${i + 1}`, parent_seq: null, status: todo,
}))

function tk(seq: number, parent: number | null, s: number | null, e: number | null, status = todo) {
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type: 'task', title: `ガントの検証${seq}`, status,
    priority: null, assignee: null, reporter: null, working_agent: null,
    parent_seq: parent, has_children: false, sort_key: `0|${String(seq).padStart(6, '0')}:`,
    staged_at: null, tags: [], sprint: null, estimate_point: null, estimate_hours: null,
    actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: s, due_at: e, all_day: true, closed_at: null,
    version: 1, created_at: D(9, 1), updated_at: D(9, 1),
  }
}

const tickets = [
  tk(11, 500, D(9, 29), D(10, 3), doing), // 順位 0：太1本
  tk(12, 508, D(10, 1), D(10, 5)), // 順位 8：細2本
  tk(13, null, D(10, 2), D(10, 6)), // 所属なし
]

async function mockApi(page: Page, theme: 'light' | 'dark') {
  await page.clock.install({ time: NOW })
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    const reply = (body: unknown) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
    if (path === '/api/v1/me') {
      return reply({
        actor: { id: '01K00000000000000000000000', kind: 'user', display_name: '検証 太郎', email: 'e2e@example.com',
          system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme, hue: 'blue', must_change_password: false },
        permissions: [],
        projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view'] }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/demo') return reply({ key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [todo, doing] } })
    if (path === '/api/v1/projects/demo/tickets') {
      if (url.searchParams.get('type') === 'epic') return reply({ items: allEpics, total: allEpics.length, page: 1, per_page: 200, total_pages: 1 })
      return reply({ items: tickets, links: [], total: tickets.length, page: 1, per_page: 5000, total_pages: 1 })
    }
    if (path.endsWith('/calendar/days')) return reply({ days: [] })
    return reply({ items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
}

for (const theme of ['light', 'dark'] as const) {
  test(`ツリーの行と帯の頭に袖章を描く（${theme}）`, async ({ page }, info) => {
    await page.setViewportSize({ width: 1366, height: 700 })
    await mockApi(page, theme)
    await page.goto('/p/demo/gantt')
    await expect(page.locator('.gantt-svg rect.b').first()).toBeAttached()

    // ツリー：所属のある2行だけに袖章
    await expect(page.locator('.gantt-tr.sleeved')).toHaveCount(2)
    const row11 = page.locator('.gantt-tr[data-seq="11"]')
    await expect(row11).toHaveAttribute('title', 'エピック: エピック1')
    expect(await row11.getAttribute('style')).toContain('var(--pb-epic-0) 0px 8px')
    expect(await page.locator('.gantt-tr[data-seq="12"]').getAttribute('style')).toContain('var(--pb-epic-0) 6px 8px')

    // 帯の頭：太1本は1本、細2本は2本、所属なしは0本。左端は帯の枠の外側（x0 - 0.5）に揃う
    const bars = await page.locator('.gantt-svg g.bar').evaluateAll((gs) => gs.map((g) => {
      const b = g.querySelector('rect.b')!
      const sv = [...g.querySelectorAll('rect.sleeve')]
      return {
        x: Number(b.getAttribute('x')),
        sleeves: sv.map((r) => ({ x: Number(r.getAttribute('x')), w: Number(r.getAttribute('width')), fill: getComputedStyle(r).fill })),
      }
    }))
    const counts = bars.map((b) => b.sleeves.length).sort()
    expect(counts).toEqual([0, 1, 2])
    for (const b of bars.filter((v) => v.sleeves.length > 0)) {
      expect(b.sleeves[0]!.x).toBeCloseTo(b.x - 0.5, 1)
      expect(b.sleeves.every((s) => /rgb/.test(s.fill))).toBe(true)
    }
    const thin = bars.find((b) => b.sleeves.length === 2)!
    expect(thin.sleeves.map((s) => s.w)).toEqual([2, 2])
    expect(thin.sleeves[1]!.x - thin.sleeves[0]!.x).toBe(6)

    const path = info.outputPath(`gantt-sleeves-${theme}.png`)
    await page.screenshot({ path })
    await info.attach(`gantt-sleeves-${theme}`, { path, contentType: 'image/png' })
  })
}
