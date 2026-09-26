import { expect, test, type Page, type Route } from '@playwright/test'

const actor = {
  id: '01K00000000000000000000000', kind: 'user', display_name: 'Test User',
  email: 'test@example.com', system_role: 'administrator', locale: 'ja',
  timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
}
const status = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }
const ticket = (seq: number, title: string, parent_seq: number | null, staged_at: string | null) => ({
  id: `01K000000000000000000000${seq}`, seq, type: 'task', title,
  status, priority: null, assignee: null, reporter: actor, working_agent: null,
  parent_seq, has_children: seq === 2, sort_key: `0|${seq}:`, staged_at,
  tags: [], sprint: null, estimate_point: null, estimate_hours: null, actual_hours: null,
  start_date: null, due_date: null, closed_at: null, version: 1,
  created_at: '2026-09-01T00:00:00Z', updated_at: '2026-09-01T00:00:00Z',
})
const rows = [
  ticket(1, 'オンステージの根', null, '2026-09-01T00:00:00Z'),
  ticket(2, 'バックログの根', null, null),
  ticket(3, '移動する子', 2, null),
]

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockBacklog(page: Page, moves: unknown[], items = rows) {
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    if (path === '/api/v1/me') {
      await json(route, { actor, permissions: [], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit'] }], expires_at: '2099-01-01T00:00:00Z' })
    } else if (path === '/api/v1/projects/demo') {
      await json(route, { key: 'demo', name: 'Demo', members: [], workflow: { statuses: [status] } })
    } else if (path === '/api/v1/projects/demo/tickets' && request.method() === 'GET') {
      await json(route, { items: url.searchParams.get('type') === 'epic' ? [] : items, total: 3, page: 1, per_page: 200, total_pages: 1 })
    } else if (path === '/api/v1/projects/demo/tickets/3/move') {
      moves.push(request.postDataJSON())
      await json(route, { seq: 3, sort_key: '0|1.5:', staged_at: '2026-09-01T00:00:00Z', version: 2, rebalanced: false })
    } else if (path.endsWith('/tags') || path.endsWith('/sprints')) {
      await json(route, { items: [] })
    } else {
      await route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
    }
  })
}

test('列幅は全セクションへ反映され、子をオンステージの根へ移せる', async ({ page }, testInfo) => {
  const moves: unknown[] = []
  await page.setViewportSize({ width: 1366, height: 900 })
  await mockBacklog(page, moves)
  await page.goto('/p/demo/backlog')
  const tables = page.locator('.group-section .table')
  await expect(tables).toHaveCount(2)
  const firstTitle = tables.first().locator('th[data-column="title"]')
  const secondTitle = tables.last().locator('th[data-column="title"]')
  const before = await firstTitle.boundingBox()
  expect(before).not.toBeNull()
  const handle = firstTitle.locator('.column-resize')
  const handleBox = await handle.boundingBox()
  expect(handleBox).not.toBeNull()
  await page.mouse.move(handleBox!.x + handleBox!.width / 2, handleBox!.y + handleBox!.height / 2)
  await page.mouse.down()
  await page.mouse.move(handleBox!.x + handleBox!.width / 2 + 100, handleBox!.y + handleBox!.height / 2)
  await page.mouse.up()
  expect((await firstTitle.boundingBox())!.width).toBeGreaterThan(before!.width + 80)
  expect(Math.abs((await firstTitle.boundingBox())!.width - (await secondTitle.boundingBox())!.width)).toBeLessThan(2)
  const scrollArea = page.locator('.table-scroll').first()
  expect(await scrollArea.evaluate((el) => el.scrollWidth)).toBeGreaterThan(await scrollArea.evaluate((el) => el.clientWidth))
  await page.screenshot({ path: testInfo.outputPath('backlog-1366.png'), fullPage: true })

  await page.getByText('移動する子', { exact: true }).dragTo(page.locator('.group-section').first().locator('.section-head'))
  await expect.poll(() => moves.length).toBe(1)
  expect(moves[0]).toMatchObject({ parent_seq: null, staged: true, position: 'first' })

  await page.setViewportSize({ width: 1024, height: 900 })
  await page.screenshot({ path: testInfo.outputPath('backlog-1024.png'), fullPage: true })
})

test('オンステージ配下の子をバックログの根へ移せる', async ({ page }) => {
  const moves: unknown[] = []
  const items = [
    ticket(1, 'バックログの根', null, null),
    ticket(2, 'オンステージの根', null, '2026-09-01T00:00:00Z'),
    ticket(3, '移動する子', 2, null),
  ]
  await mockBacklog(page, moves, items)
  await page.goto('/p/demo/backlog')
  await expect(page.locator('.group-section').first().locator('.row')).toHaveCount(2)
  await page.getByText('移動する子', { exact: true }).dragTo(page.locator('.group-section').last().locator('.section-head'))
  await expect.poll(() => moves.length).toBe(1)
  expect(moves[0]).toMatchObject({ parent_seq: null, staged: false, position: 'first' })
})
