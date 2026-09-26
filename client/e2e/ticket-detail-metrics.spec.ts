import { expect, test, type Page, type Route } from '@playwright/test'

const actor = {
  id: '01K00000000000000000000000', kind: 'user', display_name: 'Test User',
  email: 'test@example.com', system_role: 'administrator', locale: 'ja',
  timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
}
const status = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }
const ticket = {
  id: '01K00000000000000000000001', seq: 1, type: 'task', title: '配置確認',
  status, priority: null, assignee: null, reporter: actor, working_agent: null,
  parent_seq: null, has_children: false, sort_key: '0|1:', staged_at: null,
  tags: [], sprint: null, estimate_point: 5, actual_point: 5,
  actual_point_version: 'actual-v0', estimate_hours: 8, actual_hours: 3.5,
  start_date: '2026-08-09', due_date: '2026-08-14', closed_at: null,
  version: 1, created_at: '2026-09-01T00:00:00Z', updated_at: '2026-09-01T00:00:00Z',
  body_md: '', parent: null, epic: null, children: [], dod: [], links: [], references: [],
  comment_count: 0, execution_mode: 'agent_draft', readiness: null, readiness_note: null, scope: {},
}

async function reply(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockTicket(page: Page, locale: 'ja' | 'en') {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path === '/api/v1/me') return reply(route, {
      actor: { ...actor, locale }, permissions: [],
      projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit', 'ticket.actual_point.edit'] }],
      expires_at: '2099-01-01T00:00:00Z',
    })
    if (path === '/api/v1/projects/demo') return reply(route, {
      key: 'demo', name: 'Demo', members: [], workflow: { statuses: [status] },
    })
    if (path === '/api/v1/projects/demo/tickets/1') return reply(route, ticket)
    if (path === '/api/v1/projects/demo/tickets') return reply(route, {
      items: url.searchParams.get('type') === 'epic' ? [] : [ticket],
      total: 1, page: 1, per_page: 200, total_pages: 1,
    })
    if (path.endsWith('/tags') || path.endsWith('/sprints') || path.endsWith('/comments')) return reply(route, { items: [] })
    await route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })
}

test('見積・実績・日付は3行2列で、見出しは折り返さない', async ({ page }, testInfo) => {
  for (const locale of ['ja', 'en'] as const) {
    for (const width of [1440, 900, 600]) {
      await page.setViewportSize({ width, height: 900 })
      await mockTicket(page, locale)
      await page.goto('/p/demo/tickets/1')
      const labels = page.locator('.meta-item.measure dt')
      const expected = locale === 'ja'
        ? ['見積(point)', '実績(point)', '見積(時間)', '実績(時間)', '開始', '終了']
        : ['Estimate (points)', 'Actual (points)', 'Estimate (hours)', 'Actual Time (hours)', 'Start', 'End']
      await expect(labels).toHaveText(expected)
      const boxes = await labels.evaluateAll((nodes) => nodes.map((node) => {
        const box = node.getBoundingClientRect()
        return { x: box.x, y: box.y, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, scrollHeight: node.scrollHeight, clientHeight: node.clientHeight }
      }))
      for (let row = 0; row < 3; row++) {
        expect(boxes[row * 2].x).toBeLessThan(boxes[row * 2 + 1].x)
        expect(Math.abs(boxes[row * 2].y - boxes[row * 2 + 1].y)).toBeLessThan(2)
        for (const box of boxes.slice(row * 2, row * 2 + 2)) {
          expect(box.scrollHeight).toBeLessThanOrEqual(box.clientHeight)
          expect(box.scrollWidth).toBeLessThanOrEqual(box.clientWidth)
        }
        if (row > 0) expect(boxes[row * 2].y).toBeGreaterThan(boxes[(row - 1) * 2].y)
      }
      const shot = testInfo.outputPath(`ticket-detail-${locale}-${width}.png`)
      await page.screenshot({ path: shot, fullPage: true })
      await testInfo.attach(`ticket-detail-${locale}-${width}`, { path: shot, contentType: 'image/png' })
    }
  }
})
