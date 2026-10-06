import { execFileSync } from 'node:child_process'
import { expect, test } from '@playwright/test'

test('usage keeps missing values, resubmissions and pages visible at desktop and phone widths', async ({ page }, info) => {
  test.setTimeout(90_000)
  await page.goto('/login')
  await page.locator('input[name="email"]').fill(process.env.PB_E2E_EMAIL!)
  await page.locator('input[name="password"]').fill(process.env.PB_E2E_PASSWORD!)
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/projects$/)
  const csrf = (await page.context().cookies()).find(c => c.name === 'pb_csrf')!.value
  const headers = { 'X-PB-CSRF': csrf }
  const key = `usage-${Date.now()}`
  const base = `/api/v1/projects/${key}`
  const created = await page.request.post('/api/v1/projects', { headers, data: { key, name: 'Agent usage regression', workflow_template: 'simple' } })
  expect(created.status(), await created.text()).toBe(201)
  try {
    const ticketResponse = await page.request.post(`${base}/tickets`, { headers, data: { type: 'task', title: 'Usage fixture' } })
    expect(ticketResponse.status()).toBe(201)
    const ticket = await ticketResponse.json()
    for (let i = 0; i < 51; i++) {
      const response = await page.request.post(`${base}/tickets/${ticket.seq}/reports`, { headers, data: { status: i ? 'completed' : 'partial', ...(i ? {} : { cost: { tokens: 0, turns: 0, wall_clock_min: 0 } }) } })
      expect(response.status(), await response.text()).toBe(201)
    }
    const response = await page.request.get(`${base}/agent-usage`)
    expect(response.status(), await response.text()).toBe(200)
    const usage = await response.json()
    expect(usage.totals.runs_count).toBe(51)
    expect(usage.totals.tickets_count).toBe(1)
    expect(usage.totals.run_tokens_missing).toBe(50)
    expect(usage.totals.wall_clock_min_missing).toBe(50)
    await page.goto(`/p/${key}/insights`)
    await expect(page.locator('.totals')).toBeVisible()
    await expect(page.locator('.totals')).toContainText('未計測 50件')
    await expect(page.locator('h2')).toHaveText('エージェント実績')
    await expect(page.locator('.pager')).toContainText('1 / 2')
    await page.getByRole('button', { name: '次へ', exact: true }).click()
    await expect(page.locator('.pager')).toContainText('2 / 2')
    await expect(page.getByLabel('実行明細', { exact: true }).first().locator('tbody tr')).toHaveCount(1)
    await expect(page.getByLabel('実行明細', { exact: true }).first()).toContainText('一部完了の報告')
    const zeroRow = page.getByLabel('実行明細', { exact: true }).first().locator('tbody tr')
    await expect(zeroRow.locator('td').nth(4)).toHaveText('0')
    for (const width of [1440, 768, 360]) {
      await page.setViewportSize({ width, height: 900 })
      const bounds = await page.evaluate(() => ({ viewport: innerWidth, document: document.documentElement.scrollWidth, tables: [...document.querySelectorAll('.table-scroll')].map(e => ({ width: e.getBoundingClientRect().width, right: e.getBoundingClientRect().right })) }))
      expect(bounds.document).toBeLessThanOrEqual(bounds.viewport + 1)
      expect(bounds.tables.every(t => t.width > 0 && t.right <= width + 1)).toBeTruthy()
      await page.screenshot({ path: info.outputPath(`usage-${width}.png`), fullPage: true })
      await page.locator('.pager').scrollIntoViewIfNeeded()
      await page.screenshot({ path: info.outputPath(`usage-details-${width}.png`), fullPage: true })
      await page.locator('h2').scrollIntoViewIfNeeded()
    }
    // All records can be missing while their known sum is zero.
    const missingRoute = `**${base}/agent-usage?*`
    await page.route(missingRoute, async route => {
      const real = await route.fetch()
      const data = await real.json()
      data.totals.run_tokens_missing = data.totals.runs_count
      data.daily.forEach((d: { run_tokens_missing: number; runs_count: number }) => { d.run_tokens_missing = d.runs_count })
      await route.fulfill({ json: data })
    })
    await page.getByRole('button', { name: '更新', exact: true }).click()
    await expect(page.locator('.totals div').filter({ has: page.locator('dt').filter({ hasText: /^実行トークン$/ }) }).locator('dd')).toHaveText('未計測')
    await page.unroute(missingRoute)
    const date = page.locator('input[type="date"]').first()
    await date.fill('2000-01-01')
    await page.locator('input[type="date"]').nth(1).fill('2000-01-02')
    await page.getByRole('button', { name: '更新', exact: true }).click()
    await expect(page.locator('.pager')).toContainText('1 / 0')
    await expect(page.getByText('この期間の記録はありません')).toBeVisible()
    await page.route(`**${base}/agent-usage*`, route => route.fulfill({ status: 500, json: { error: { code: 'internal_error', message: 'Usage request failed' } } }))
    await page.getByRole('button', { name: '更新', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.locator('.totals')).toHaveCount(0)
  } finally {
    execFileSync('make', ['psql'], { cwd: process.env.PB_DEV_ROOT ?? '..', input: `DELETE FROM project WHERE key = '${key}' AND name = 'Agent usage regression';\n`, stdio: ['pipe', 'pipe', 'pipe'] })
  }
})
