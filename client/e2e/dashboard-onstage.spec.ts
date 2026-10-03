import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { expect, test, type Page } from '@playwright/test'
import type { ProjectStats } from '../src/api/dashboard'

async function login(page: Page) {
  if (!process.env.PB_E2E_EMAIL || !process.env.PB_E2E_PASSWORD) throw new Error('Development E2E credentials are required')
  await page.goto('/login')
  await page.locator('input[name="email"]').fill(process.env.PB_E2E_EMAIL)
  await page.locator('input[name="password"]').fill(process.env.PB_E2E_PASSWORD)
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/projects$/)
}

async function showFilters(page: Page) {
  if (!await page.locator('.backlog-filter-scope select').isVisible()) {
    await page.locator('.backlog-filter-toggle').click()
  }
}

test('real onstage cards and backlog agree across descendants, retirement, scope and widths', async ({ page }, info) => {
  test.setTimeout(120_000)
  await login(page)
  const csrf = (await page.context().cookies()).find(c => c.name === 'pb_csrf')!.value
  const headers = { 'X-PB-CSRF': csrf }
  const key = `os178-${Date.now().toString(36)}`
  const base = `/api/v1/projects/${key}`
  const project = await page.request.post('/api/v1/projects', {
    headers, data: { key, name: 'Onstage regression', workflow_template: 'with_review' },
  })
  expect(project.status(), await project.text()).toBe(201)
  try {
    const stats = async () => (await (await page.request.get(base + '/stats')).json()) as ProjectStats
    expect((await stats()).onstage).toEqual({ by_category: { todo: 0, in_progress: 0, review: 0, done: 0 }, total: 0, unknown: 0 })
    const create = async (title: string, type = 'task', parent_seq?: number) => {
      const r = await page.request.post(base + '/tickets', { headers, data: { title, type, parent_seq } })
      expect(r.status(), await r.text()).toBe(201)
      return await r.json()
    }
    const stage = async (seq: number, staged = true) => {
      const r = await page.request.post(`${base}/tickets/${seq}/move`, { headers, data: { staged, position: 'last' } })
      expect(r.status(), await r.text()).toBe(200)
    }
    const transition = async (seq: number, to: string) => {
      const current = await page.request.get(`${base}/tickets/${seq}`)
      const r = await page.request.post(`${base}/tickets/${seq}/transition`, {
        headers: { ...headers, 'If-Match': current.headers()['etag']! }, data: { to },
      })
      expect(r.status(), await r.text()).toBe(200)
    }
    const root = await create('Onstage parent', 'story')
    const child = await create('Onstage child', 'task', root.seq)
    const shelf = await create('Retired root')
    await stage(root.seq)
    await stage(shelf.seq)
    const started = await page.request.post(base + '/sprints/start', { headers, data: { name: 'Regression sprint' } })
    expect(started.status(), await started.text()).toBe(201)
    const sprint = await started.json()
    expect((await page.request.post(`${base}/sprints/${sprint.id}/finish`, { headers })).status()).toBe(200)
    await transition(child.seq, 'in_progress')
    await transition(root.seq, 'review')
    await page.goto(`/p/${key}`)
    await expect(page.locator('.stat-card-value').nth(1)).toHaveText('1')
    await page.locator('.stat-card').nth(1).focus()
    await page.locator('.stat-card').nth(1).press('Enter')
    await expect(page).toHaveURL(/staged=true/)
    await expect(page.locator('.total')).toHaveText('1件')
    await expect(page.locator('.group-section').filter({ hasText: 'Onstage child' }).locator('.section-name')).toHaveText('オンステージ')
    await transition(child.seq, 'review')
    await transition(child.seq, 'done')
    await transition(shelf.seq, 'in_progress')
    await transition(shelf.seq, 'review')
    await transition(shelf.seq, 'done')
    const standalone = await create('Unassigned sprint completion')
    await transition(standalone.seq, 'in_progress')
    await transition(standalone.seq, 'review')
    await transition(standalone.seq, 'done')
    const back = await create('Offstage completion')
    await transition(back.seq, 'in_progress')
    await transition(back.seq, 'review')
    await transition(back.seq, 'done')
    await stage(back.seq, false)
    const todo = await create('Planned onstage work')
    await stage(todo.seq)
    await create('Grouping only', 'epic')
    const snapshot = await stats()
    expect(snapshot.onstage.by_category).toEqual({ todo: 1, in_progress: 0, review: 1, done: 2 })
    expect(snapshot.by_category.done).toBe(4)

    for (const width of [360, 768, 1440]) {
      await page.setViewportSize({ width, height: 1000 })
      await page.goto(`/p/${key}`)
      await expect(page.locator('.dash-onstage-title')).toHaveText('オンステージの状態別件数')
      for (const [i, value] of [1, 0, 1, 2].entries()) await expect(page.locator('.stat-card-value').nth(i)).toHaveText(String(value))
      const boxes = await page.locator('.stat-card').evaluateAll(elements => elements.map(e => {
        const b = e.getBoundingClientRect(); return { x: b.x, y: b.y, right: b.right, width: b.width, height: b.height }
      }))
      for (const b of boxes) { expect(b.x).toBeGreaterThanOrEqual(0); expect(b.right).toBeLessThanOrEqual(width + 1); expect(b.height).toBeGreaterThan(40) }
      expect(boxes[1]!.x).toBeGreaterThan(boxes[0]!.x)
      expect(Math.abs(boxes[1]!.y - boxes[0]!.y)).toBeLessThan(2)
      const dashShot = info.outputPath(`onstage-dashboard-${width}.png`)
      await page.screenshot({ path: dashShot, fullPage: true })
      await info.attach(`dashboard-${width}`, { path: dashShot, contentType: 'image/png' })
      const listResponse = page.waitForResponse(r => r.url().includes(`${base}/tickets?`) && new URL(r.url()).searchParams.get('status_category') === 'done')
      await page.locator('.stat-card').nth(3).click()
      const response = await listResponse
      const query = new URL(response.url()).searchParams
      expect(query.get('staged')).toBe('true')
      expect(query.has('retired')).toBe(false)
      expect((await response.json()).total).toBe(snapshot.onstage.by_category.done)
      await expect(page.locator('.total')).toHaveText('2件')
      await expect(page.locator('.backlog-scope-note')).toBeVisible()
      await expect(page.locator('.group-section').filter({ hasText: 'Onstage child' }).locator('.section-name')).toHaveText('オンステージ')
      await showFilters(page)
      await expect(page.locator('.backlog-filter-scope select')).toHaveValue('true')
      const scopeBox = await page.locator('.backlog-filter-scope').boundingBox()
      expect(scopeBox!.x).toBeGreaterThanOrEqual(0)
      expect(scopeBox!.x + scopeBox!.width).toBeLessThanOrEqual(width + 1)
      const filterBoxes = await page.locator('.backlog-filter-options > .filter').evaluateAll(elements => elements.map(e => {
        const b = e.getBoundingClientRect(); return { x: b.x, y: b.y, right: b.right, bottom: b.bottom, width: b.width }
      }))
      for (const [i, box] of filterBoxes.entries()) {
        expect(box.width).toBeGreaterThan(80)
        for (const other of filterBoxes.slice(i + 1)) {
          expect(box.right <= other.x + 1 || other.right <= box.x + 1 || box.bottom <= other.y + 1 || other.bottom <= box.y + 1).toBe(true)
        }
      }
      const shot = info.outputPath(`onstage-backlog-${width}.png`)
      await page.screenshot({ path: shot, fullPage: true })
      await info.attach(`backlog-${width}`, { path: shot, contentType: 'image/png' })
      // Changing only scope preserves the chosen status and restores whole-project retirement behavior.
      await page.locator('.backlog-filter-scope select').selectOption('')
      await expect(page).toHaveURL(/status_category=done/)
      await expect(page.locator('.total')).toHaveText('4件')
      await page.locator('.backlog-filter-scope select').selectOption('true')
      await expect(page.locator('.total')).toHaveText('2件')
      await page.locator('.backlog-filter-group select').selectOption('assignee')
      await expect(page.locator('.total')).toHaveText('2件')
      await expect(page.locator('.backlog-scope-note')).toBeVisible()
      await page.locator('.backlog-filter-group select').selectOption('')
    }
    await page.goto(`/p/${key}/backlog?staged=true&status_category=done&retired=true`)
    await expect(page.locator('.total')).toHaveText('2件')
  } finally {
    // Project deletion has no REST route. Remove only this test-created development fixture.
    execFileSync('make', ['psql'], {
      cwd: fileURLToPath(new URL('../../', import.meta.url)),
      input: `DELETE FROM project WHERE key = '${key}' AND name = 'Onstage regression';\n`,
      stdio: ['pipe', 'pipe', 'pipe'],
    })
  }
})

test('onstage block handles failure, retry, unknown statuses, zero and English', async ({ page }) => {
  await login(page)
  const response = await page.request.get('/api/v1/projects/demo/stats')
  const sample = await response.json() as ProjectStats
  let fail = true
  await page.route('**/api/v1/projects/demo/stats', async route => {
    if (fail) return route.fulfill({ status: 500, json: { error: { code: 'internal_error', message: 'Regression error' } } })
    return route.fulfill({ json: { ...sample, onstage: { by_category: { todo: 0, in_progress: 0, review: 0, done: 0 }, total: 1, unknown: 1 } } })
  })
  await page.goto('/p/demo')
  await expect(page.locator('.dash-cards [role="alert"]')).toContainText('サーバでエラーが発生しました')
  fail = false
  await page.locator('.dash-cards .dash-retry').click()
  await expect(page.locator('.dash-cards')).toContainText('状態を分類できない1件')
  await expect(page.locator('.stat-card-value')).toHaveText(['0', '0', '0', '0'])
  await page.route('**/api/v1/me', async route => {
    const r = await route.fetch()
    const data = await r.json()
    data.actor.locale = 'en'
    data.permissions = data.permissions.filter((p: string) => p !== 'ticket.view')
    data.projects = data.projects.map((p: { permissions: string[] }) => ({ ...p, permissions: p.permissions.filter(value => value !== 'ticket.view') }))
    await route.fulfill({ response: r, json: data })
  })
  await page.reload()
  await expect(page.locator('.dash-onstage-title')).toHaveText('Onstage ticket counts by status')
  await expect(page.locator('.dash-cards')).toContainText('1 tickets have unclassified statuses')
  await expect(page.locator('.dash-cards a')).toHaveCount(0)
  await expect(page.locator('.stat-card')).toHaveCount(4)
})
