import { expect, test } from '@playwright/test'

const status = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }
const epic = { seq: 10, title: '所属エピック：長い日本語の名前'.repeat(5), type: 'epic', parent_seq: null, status }
function ticket(seq: number, parent_seq: number | null) {
  return { seq, id: String(seq), title: `検証チケット${seq}`, type: 'task', parent_seq, status,
    tags: [{ id: 'tag1', name: '改善' }, { id: 'tag2', name: '長いタグ名'.repeat(8) }],
    priority: null, assignee: null, working_agent: null, sprint: null, staged_at: null,
    sort_key: String(seq), due_at: null, start_at: null, closed_at: null, all_day: true }
}

for (const width of [1366, 1024, 768]) {
  test(`エピック・タグを識別し、親が検索結果に無くても所属を表示する ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const requests: number[] = []
    await page.route('**/api/v1/**', async route => {
      const url = new URL(route.request().url())
      const path = url.pathname
      let body: unknown = { items: [] }
      if (path === '/api/v1/me') body = {
        actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
        permissions: [], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view'] }], expires_at: 4070908800000 }
      if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [status] } }
      if (path.endsWith('/tickets')) {
        const items = url.searchParams.get('type') === 'epic' ? [epic] : [ticket(11, 10), ticket(12, 11), ticket(13, 99), { ...ticket(14, null), tags: [] }]
        body = { items, total: items.length, page: 1, per_page: 200, total_pages: 1 }
      }
      if (path.endsWith('/tickets/99')) { requests.push(99); body = { ...ticket(99, 10), epic } }
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
    })
    await page.goto('/p/demo/backlog')
    await expect(page.locator('.epic-badge')).toHaveCount(3)
    await expect(page.locator('.ticket-badges .tag')).toHaveCount(6)
    await expect(page.locator('.epic-badge').first()).toContainText(epic.title)
    expect(requests.length).toBeGreaterThan(0)
    const geometry = await page.locator('.ticket-badges').evaluateAll(elements => elements.map(el => {
      const cell = el.closest('td')!.getBoundingClientRect()
      return [...el.children].every(child => {
        const b = child.getBoundingClientRect()
        return b.width > 0 && b.right <= cell.right + 1 && b.left >= cell.left - 1
      })
    }))
    expect(geometry.every(Boolean)).toBe(true)
    const title = await page.locator('.row .title').first().boundingBox()
    expect(title!.width).toBeGreaterThan(90)
    await page.screenshot({ path: info.outputPath(`badges-${width}.png`), fullPage: true })
  })
}
