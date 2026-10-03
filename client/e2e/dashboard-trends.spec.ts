import { expect, test, type Page } from '@playwright/test'
import type { TicketTrends } from '../src/api/dashboard'

async function login(page: Page) {
  if (!process.env.PB_E2E_EMAIL || !process.env.PB_E2E_PASSWORD) {
    throw new Error('PB_E2E_EMAIL and PB_E2E_PASSWORD are required')
  }
  await page.goto('/login')
  await page.locator('input[name="email"]').fill(process.env.PB_E2E_EMAIL)
  await page.locator('input[name="password"]').fill(process.env.PB_E2E_PASSWORD)
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/projects$/)
}

function sample(days: 7 | 28, zero = false): TicketTrends {
  const end = new Date('2026-10-03T00:00:00Z')
  const daily = Array.from({ length: days }, (_, index) => {
    const date = new Date(end)
    date.setUTCDate(date.getUTCDate() - days + index + 1)
    return {
      date: date.toISOString().slice(0, 10),
      created: zero ? 0 : index % 4,
      completed: zero ? 0 : (index + 1) % 3,
      reopened: zero ? 0 : index % 2,
      unclassified_transitions: zero ? 0 : (index === 0 ? 2 : 0),
      is_partial: index === days - 1,
    }
  })
  const totals = daily.reduce((sum, row) => ({
    created: sum.created + row.created, completed: sum.completed + row.completed,
    reopened: sum.reopened + row.reopened,
    unclassified_transitions: sum.unclassified_transitions + row.unclassified_transitions,
  }), { created: 0, completed: 0, reopened: 0, unclassified_transitions: 0 })
  return {
    days, timezone: 'Asia/Tokyo', start_date: daily[0]!.date, end_date: daily[days - 1]!.date,
    as_of: Date.parse('2026-10-03T03:00:00Z'), basis: 'recorded_activity_for_existing_tickets',
    totals, daily,
  }
}

test('real API records completion/reopening, renders matching daily values and excludes deletions', async ({ page }) => {
  await login(page)
  const csrf = (await page.context().cookies()).find(c => c.name === 'pb_csrf')!.value
  const headers = { 'X-PB-CSRF': csrf }
  const base = '/api/v1/projects/demo'
  const before = await (await page.request.get(base + '/ticket-trends?days=7')).json() as TicketTrends
  const created: number[] = []
  try {
    const create = async (type: string, parent_seq?: number) => {
      const response = await page.request.post(base + '/tickets', {
        headers, data: { type, title: 'Dashboard trends regression fixture', parent_seq },
      })
      expect(response.status()).toBe(201)
      const ticket = await response.json()
      created.push(ticket.seq)
      return ticket
    }
    const root = await create('story')
    const child = await create('task', root.seq)
    await create('epic')
    for (const to of ['in_progress', 'done', 'in_progress', 'done']) {
      const current = await page.request.get(base + '/tickets/' + child.seq)
      const response = await page.request.post(base + '/tickets/' + child.seq + '/transition', {
        headers: { ...headers, 'If-Match': current.headers()['etag']! }, data: { to },
      })
      expect(response.status(), await response.text()).toBe(200)
    }
    const after = await (await page.request.get(base + '/ticket-trends?days=7')).json() as TicketTrends
    expect(after.totals.created - before.totals.created).toBe(2)
    expect(after.totals.completed - before.totals.completed).toBe(2)
    expect(after.totals.reopened - before.totals.reopened).toBe(1)
    await page.goto('/p/demo')
    const block = page.locator('.dash-trends')
    await expect(block.locator('.dash-trends-period')).toContainText(after.start_date)
    for (const [i, key] of ['created', 'completed', 'reopened'].entries()) {
      await expect(block.locator('dd').nth(i)).toHaveText(after.totals[key as keyof typeof after.totals] + '回')
    }
    await block.locator('summary').click()
    await expect(block.locator('tbody tr')).toHaveCount(7)
    for (const day of after.daily) {
      const row = block.locator('tbody tr').filter({ hasText: day.date })
      await expect(row.locator('td').nth(0)).toHaveText(String(day.created))
      await expect(row.locator('td').nth(1)).toHaveText(String(day.completed))
      await expect(row.locator('td').nth(2)).toHaveText(String(day.reopened))
    }
    const current = await page.request.get(base + '/tickets/' + child.seq)
    const response = await page.request.delete(base + '/tickets/' + child.seq, {
      headers: { ...headers, 'If-Match': current.headers()['etag']! },
    })
    expect(response.status()).toBe(204)
    created.splice(created.indexOf(child.seq), 1)
    const deleted = await (await page.request.get(base + '/ticket-trends?days=7')).json() as TicketTrends
    expect(deleted.totals.created - before.totals.created).toBe(1)
    expect(deleted.totals.completed).toBe(before.totals.completed)
    expect(deleted.totals.reopened).toBe(before.totals.reopened)
  } finally {
    for (const seq of created.reverse()) {
      const current = await page.request.get(base + '/tickets/' + seq)
      if (current.status() === 404) continue
      const response = await page.request.delete(base + '/tickets/' + seq, {
        headers: { ...headers, 'If-Match': current.headers()['etag']! },
      })
      expect(response.status()).toBe(204)
    }
  }
})

test('daily chart and table fit three screen widths; error/retry and stale responses are handled', async ({ page }, testInfo) => {
  await login(page)
  let fail = false
  let hold28 = false
  let waiting28 = false
  let release28: () => void = () => {}
  let done28: () => void = () => {}
  await page.route('**/api/v1/projects/demo/ticket-trends?*', async route => {
    const period = new URL(route.request().url()).searchParams.get('days') === '28' ? 28 : 7
    if (period === 28 && hold28) await new Promise<void>(resolve => { waiting28 = true; release28 = resolve })
    await route.fulfill({ status: fail ? 500 : 200, json: fail ? { error: { message: 'fixture error', code: 'internal_error' } } : sample(period) })
    if (period === 28 && hold28) done28()
  })
  await page.goto('/p/demo')
  const block = page.locator('.dash-trends')
  const period = block.getByLabel('集計期間')
  await expect(block.locator('.dash-trends-period')).toContainText('2026-09-27')
  await block.locator('summary').click()
  for (const width of [360, 768, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    for (const days of [7, 28]) {
      await period.selectOption(String(days))
      await expect(block.locator('tbody tr')).toHaveCount(days)
      if (!(await block.locator('details').evaluate(d => (d as HTMLDetailsElement).open))) await block.locator('summary').click()
      await expect(block.locator('tbody tr').first()).toBeVisible()
      await expect(block.locator('rect.dash-trends-bar')).toHaveCount(days * 3)
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
      expect(overflow).toBe(false)
      const box = await block.boundingBox()
      expect(box).not.toBeNull()
      expect(box!.x).toBeGreaterThanOrEqual(0)
      expect(box!.x + box!.width).toBeLessThanOrEqual(width)
      const invalid = await block.locator('rect.dash-trends-bar').evaluateAll(bars =>
        bars.some(bar => Number(bar.getAttribute('height')) < 0 || Number(bar.getAttribute('y')) < 0),
      )
      expect(invalid).toBe(false)
      await expect(block).toContainText('2回の状態変更を分類できない')
      await expect(block).toContainText('2026-10-03 12:00')
      await block.locator('.dash-trends-scroll').first().evaluate(el => { el.scrollLeft = el.scrollWidth })
      await expect(block.locator('svg text').last()).toContainText('10-03*')
      await block.locator('.dash-trends-scroll').first().evaluate(el => { el.scrollLeft = 0 })
      await block.scrollIntoViewIfNeeded()
      const screenshot = testInfo.outputPath(`trends-${width}-${days}.png`)
      await page.screenshot({ path: screenshot, fullPage: true })
      await testInfo.attach(`trends-${width}-${days}`, { path: screenshot, contentType: 'image/png' })
    }
  }
  await block.locator('summary').focus()
  await page.keyboard.press('Enter')
  await expect(block.locator('details')).not.toHaveAttribute('open', '')
  await page.keyboard.press('Enter')
  await expect(block.locator('details')).toHaveAttribute('open', '')
  fail = true
  await period.selectOption('7')
  await expect(block.getByRole('alert')).toBeVisible()
  await expect(page.locator('.dash-cards')).toBeVisible()
  fail = false
  await block.getByRole('button', { name: '再試行' }).click()
  await expect(block.locator('tbody tr')).toHaveCount(7)
  hold28 = true
  const completed28 = new Promise<void>(resolve => { done28 = resolve })
  await period.selectOption('28')
  await expect.poll(() => waiting28).toBe(true)
  await period.selectOption('7')
  await expect(block.locator('tbody tr')).toHaveCount(7)
  release28()
  await completed28
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await expect(block.locator('tbody tr')).toHaveCount(7)
  await expect(period).toHaveValue('7')
})

test('English zero-record state retains chart and numeric values', async ({ page }) => {
  await login(page)
  await page.route('**/api/v1/me', async route => {
    const response = await route.fetch()
    const body = await response.json()
    body.actor.locale = 'en'
    await route.fulfill({ response, json: body })
  })
  await page.route('**/api/v1/projects/demo/ticket-trends?*', route => route.fulfill({ json: sample(7, true) }))
  await page.goto('/p/demo')
  const block = page.locator('.dash-trends')
  await expect(block.getByRole('heading', { name: 'Ticket intake and completion' })).toBeVisible()
  await expect(block.locator('dd')).toHaveText(['0 operations', '0 operations', '0 operations'])
  await block.locator('summary').click()
  await expect(block.locator('tbody tr')).toHaveCount(7)
  await expect(block.locator('rect.dash-trends-bar')).toHaveCount(21)
  expect(await block.innerText()).not.toMatch(/[ぁ-んァ-ヶ一-龠]/u)
})
