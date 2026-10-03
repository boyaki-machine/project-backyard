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

function sample(period: TicketTrends['period'], zero = false): TicketTrends {
  const days = { '7d': 7, '4w': 28, '3m': 92, '6m': 183 }[period]
  const end = new Date('2026-10-03T00:00:00Z')
  const daily = Array.from({ length: days }, (_, index) => {
    const date = new Date(end)
    date.setUTCDate(date.getUTCDate() - days + index + 1)
    return {
      date: date.toISOString().slice(0, 10),
      done: zero ? 0 : index % 4,
      in_progress: zero ? 0 : (index + 1) % 3,
      todo: zero ? 0 : 5 + index % 2,
      new_todo: zero ? 0 : index % 3,
      unknown: zero ? 0 : (index === 0 ? 2 : 0),
      is_partial: index === days - 1,
    }
  })
  const latest = daily.at(-1)!
  return {
    period, days, timezone: 'Asia/Tokyo', start_date: daily[0]!.date, end_date: latest.date,
    as_of: Date.parse('2026-10-03T03:00:00Z'), basis: 'reconstructed_status_for_existing_tickets',
    latest, daily,
  }
}

test('real API counts current statuses and new todo, handles reopening and excludes deletions', async ({ page }) => {
  await login(page)
  const csrf = (await page.context().cookies()).find(c => c.name === 'pb_csrf')!.value
  const headers = { 'X-PB-CSRF': csrf }
  const base = '/api/v1/projects/demo'
  const before = await (await page.request.get(base + '/ticket-trends?period=7d')).json() as TicketTrends
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
    const added = await (await page.request.get(base + '/ticket-trends?period=7d')).json() as TicketTrends
    expect(added.latest.todo - before.latest.todo).toBe(2)
    expect(added.latest.new_todo - before.latest.new_todo).toBe(2)
    for (const [index, to] of ['in_progress', 'done', 'in_progress', 'done'].entries()) {
      const current = await page.request.get(base + '/tickets/' + child.seq)
      const response = await page.request.post(base + '/tickets/' + child.seq + '/transition', {
        headers: { ...headers, 'If-Match': current.headers()['etag']! }, data: { to },
      })
      expect(response.status(), await response.text()).toBe(200)
      const snapshot = await (await page.request.get(base + '/ticket-trends?period=7d')).json() as TicketTrends
      expect(snapshot.latest.done - before.latest.done).toBe(index % 2)
    }
    const after = await (await page.request.get(base + '/ticket-trends?period=7d')).json() as TicketTrends
    // Starting a child also starts its todo parent: both are distinct tickets.
    expect(after.latest.done - before.latest.done).toBe(1)
    expect(after.latest.in_progress - before.latest.in_progress).toBe(1)
    expect(after.latest.todo).toBe(before.latest.todo)
    expect(after.latest.new_todo).toBe(before.latest.new_todo)
    for (const period of ['7d', '4w', '3m', '6m'] as const) {
      const response = await page.request.get(base + '/ticket-trends?period=' + period)
      expect(response.status()).toBe(200)
      const data = await response.json() as TicketTrends
      expect(data.period).toBe(period)
      expect(data.daily.length).toBe(data.days)
      expect(data.latest.done).toBe(after.latest.done)
      expect(data.latest.in_progress).toBe(after.latest.in_progress)
      expect(data.latest.todo).toBe(after.latest.todo)
      expect(data.daily.at(-1)!.is_partial).toBe(true)
    }
    await page.goto('/p/demo')
    const block = page.locator('.dash-trends')
    await expect(block.locator('.dash-trends-period')).toContainText(after.start_date)
    for (const [i, key] of ['done', 'in_progress', 'todo', 'new_todo'].entries()) {
      await expect(block.locator('dd').nth(i)).toHaveText(after.latest[key as keyof typeof after.latest] + '件')
    }
    await block.locator('summary').click()
    await expect(block.locator('tbody tr')).toHaveCount(7)
    for (const day of after.daily) {
      const row = block.locator('tbody tr').filter({ hasText: day.date })
      await expect(row.locator('td').nth(0)).toHaveText(String(day.done))
      await expect(row.locator('td').nth(1)).toHaveText(String(day.in_progress))
      await expect(row.locator('td').nth(2)).toHaveText(String(day.todo))
    }
    const current = await page.request.get(base + '/tickets/' + child.seq)
    const response = await page.request.delete(base + '/tickets/' + child.seq, {
      headers: { ...headers, 'If-Match': current.headers()['etag']! },
    })
    expect(response.status()).toBe(204)
    created.splice(created.indexOf(child.seq), 1)
    const deleted = await (await page.request.get(base + '/ticket-trends?period=7d')).json() as TicketTrends
    expect(deleted.latest.done).toBe(before.latest.done)
    expect(deleted.latest.in_progress - before.latest.in_progress).toBe(1)

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

test('stacked chart fits four periods at three widths; keyboard, error and stale responses work', async ({ page }, testInfo) => {
  await login(page)
  let fail = false
  let hold28 = false
  let waiting28 = false
  let release28: () => void = () => {}
  let done28: () => void = () => {}
  await page.route('**/api/v1/projects/demo/ticket-trends?*', async route => {
    const period = new URL(route.request().url()).searchParams.get('period') as TicketTrends['period']
    if (period === '4w' && hold28) await new Promise<void>(resolve => { waiting28 = true; release28 = resolve })
    await route.fulfill({ status: fail ? 500 : 200, json: fail ? { error: { message: 'fixture error', code: 'internal_error' } } : sample(period) })
    if (period === '4w' && hold28) done28()
  })
  await page.goto('/p/demo')
  const block = page.locator('.dash-trends')
  const period = block.getByLabel('集計期間')
  await expect(block.locator('.dash-trends-period')).toContainText('2026-09-27')
  await block.locator('summary').click()
  for (const width of [360, 768, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    for (const value of ['7d', '4w', '3m', '6m'] as const) {
      const days = sample(value).days
      await period.selectOption(value)
      await expect(block.locator('tbody tr')).toHaveCount(days)
      if (!(await block.locator('details').evaluate(d => (d as HTMLDetailsElement).open))) await block.locator('summary').click()
      await expect(block.locator('tbody tr').first()).toBeVisible()
      await expect(block.locator('.dash-trends-point')).toHaveCount(days)
      await expect(block.locator('polyline')).toHaveCount(5)
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
      expect(overflow).toBe(false)
      const box = await block.boundingBox()
      expect(box).not.toBeNull()
      expect(box!.x).toBeGreaterThanOrEqual(0)
      expect(box!.x + box!.width).toBeLessThanOrEqual(width)
      const invalid = await block.locator('polyline').evaluateAll(lines =>
        lines.some(line => line.getAttribute('points')!.split(' ').some(p => {
          const [x, y] = p.split(',').map(Number)
          return x! < 48 || x! > line.ownerSVGElement!.viewBox.baseVal.width - 30 || y! < 30 || y! > 180
        })),
      )
      expect(invalid).toBe(false)
      const labelBoxes = await block.locator('svg > text').evaluateAll(labels => labels.map(l => {
        const b = l.getBoundingClientRect(); return { left: b.left, right: b.right }
      }))
      labelBoxes.slice(1).forEach((b, i) => expect(b.left - labelBoxes[i]!.right).toBeGreaterThanOrEqual(4))

      // Each layer's upper line must equal the cumulative stocks, excluding duplicate new_todo.
      const points = await block.locator('polyline').evaluateAll(lines => lines.map(l => l.getAttribute('points')!.split(' ').map(p => Number(p.split(',')[1]))))
      const fixture = sample(value)
      const max = Math.max(1, ...fixture.daily.map(d => d.done + d.in_progress + d.todo + d.unknown))
      fixture.daily.forEach((day, i) => {
        const stocks = [day.done, day.in_progress, day.todo - day.new_todo, day.new_todo, day.unknown]
        let sum = 0
        stocks.forEach((stock, layer) => { sum += stock; expect(points[layer]![i]).toBeCloseTo(180 - sum / max * 150) })
      })
      await expect(block).toContainText('履歴から状態を判定できない')
      await expect(block).toContainText('2026-10-03 12:00')
      await expect(block.locator('svg text').last()).toContainText('10-03*')
      await block.scrollIntoViewIfNeeded()
      const screenshot = testInfo.outputPath(`trends-${width}-${value}.png`)
      await block.locator('summary').click()
      await block.screenshot({ path: screenshot })
      await block.locator('summary').click()
      await testInfo.attach(`trends-${width}-${value}`, { path: screenshot, contentType: 'image/png' })
    }
  }
  const point = block.locator('.dash-trends-point').first()
  await point.focus()
  await page.keyboard.press('ArrowRight')
  await expect(block.locator('.dash-trends-point').nth(1)).toBeFocused()
  await expect(block.locator('.dash-trends-selected')).toContainText(sample('6m').daily[1]!.date)
  await page.keyboard.press('End')
  await expect(block.locator('.dash-trends-point').last()).toBeFocused()
  await block.locator('summary').focus()
  await page.keyboard.press('Enter')
  await expect(block.locator('details')).not.toHaveAttribute('open', '')
  await page.keyboard.press('Enter')
  await expect(block.locator('details')).toHaveAttribute('open', '')
  fail = true
  await period.selectOption('7d')
  await expect(block.getByRole('alert')).toBeVisible()
  await expect(page.locator('.dash-cards')).toBeVisible()
  fail = false
  await block.getByRole('button', { name: '再試行' }).click()
  await expect(block.locator('tbody tr')).toHaveCount(7)
  hold28 = true
  const completed28 = new Promise<void>(resolve => { done28 = resolve })
  await period.selectOption('4w')
  await expect.poll(() => waiting28).toBe(true)
  await period.selectOption('7d')
  await expect(block.locator('tbody tr')).toHaveCount(7)
  release28()
  await completed28
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await expect(block.locator('tbody tr')).toHaveCount(7)
  await expect(period).toHaveValue('7d')
})

test('English zero-ticket state retains chart and numeric values', async ({ page }) => {
  await login(page)
  await page.route('**/api/v1/me', async route => {
    const response = await route.fetch()
    const body = await response.json()
    body.actor.locale = 'en'
    await route.fulfill({ response, json: body })
  })
  await page.route('**/api/v1/projects/demo/ticket-trends?*', route => route.fulfill({ json: sample('7d', true) }))
  await page.goto('/p/demo')
  const block = page.locator('.dash-trends')
  await expect(block.getByRole('heading', { name: 'Ticket status trends' })).toBeVisible()
  await expect(block.locator('dd')).toHaveText(['0 tickets', '0 tickets', '0 tickets', '0 tickets'])
  await block.locator('summary').click()
  await expect(block.locator('tbody tr')).toHaveCount(7)
  await expect(block.locator('polyline')).toHaveCount(4)
  expect(await block.innerText()).not.toMatch(/[ぁ-んァ-ヶ一-龠]/u)
})
