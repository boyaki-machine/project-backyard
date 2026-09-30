import { expect, test, type Page } from '@playwright/test'

const session = {
  actor: {
    id: '01K00000000000000000000000', kind: 'user', display_name: '監査担当',
    email: 'audit@example.com', system_role: 'administrator', locale: 'ja',
    timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
  },
  permissions: ['auditlog.view', 'user.manage'], projects: [], expires_at: 4070908800000,
}

const rows = [
  { id: '01K00000000000000000000001', occurred_at: Date.UTC(2026, 8, 30, 3, 0, 0, 123), actor_id: session.actor.id, actor_kind: 'user', actor_name: '監査担当', token_id: null, ip: '127.0.0.1', user_agent: 'Test Browser', action: 'login.success', target_type: 'access_token', target_id: '01K000000000000000000000000000000000000000000001', result: 'success', detail: { method: 'password' }, request_id: '01K00000000000000000000002' },
  { id: '01K00000000000000000000003', occurred_at: Date.UTC(2026, 8, 29, 3), actor_id: session.actor.id, actor_kind: 'user', actor_name: '監査担当', token_id: null, ip: null, user_agent: null, action: 'login.failure', target_type: null, target_id: null, result: 'failure', detail: { reason: 'invalid' }, request_id: null },
]

async function mockAudit(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === '/api/v1/me') return route.fulfill({ json: session })
    if (url.pathname === '/api/v1/admin/settings/pending') return route.fulfill({ json: { pending_confirmation: null } })
    if (url.pathname === '/api/v1/admin/audit.csv') return route.fulfill({
      contentType: 'text/csv', body: 'id,action\n01K00000000000000000000001,login.success\n',
    })
    if (url.pathname === '/api/v1/admin/audit') {
      const filtered = rows.filter((row) =>
        (!url.searchParams.get('action') || row.action.includes(url.searchParams.get('action')!)) &&
        (!url.searchParams.get('result') || row.result === url.searchParams.get('result')),
      )
      return route.fulfill({ json: { items: filtered, page: 1, per_page: Number(url.searchParams.get('per_page')), total: filtered.length, total_pages: filtered.length ? 1 : 0 } })
    }
    return route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '対象が見つかりません' } } })
  })
}

test('監査ログの絞り込み・詳細・CSVと複数画面幅', async ({ page }, testInfo) => {
  await mockAudit(page)
  for (const width of [1440, 768, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/admin/audit')
    await expect(page.getByRole('heading', { name: '監査ログ' })).toBeVisible()
    await expect(page.locator('tbody tr.record')).toHaveCount(2)
    const filters = page.locator('form.filters')
    const box = await filters.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x + box!.width).toBeLessThanOrEqual(width)
    if (width === 1440) expect(box!.height).toBeLessThan(130)
    const table = page.locator('.table-wrap')
    const tableBox = await table.boundingBox()
    expect(tableBox).not.toBeNull()
    expect(tableBox!.x + tableBox!.width).toBeLessThanOrEqual(width)
    await page.screenshot({ path: testInfo.outputPath(`audit-${width}.png`), fullPage: true })
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(page.locator('tbody tr.record').first().locator('time')).toContainText('12:00:00.123')
  await expect(page.locator('tbody tr.record').first().getByRole('link', { name: '監査担当' })).toHaveAttribute('href', `/admin/users/${session.actor.id}`)
  await expect(page.locator('tbody tr.record').first()).not.toContainText('audit@example.com')
  const target = page.locator('tbody tr.record').first().locator('.target span')
  await expect(target).toHaveCSS('text-overflow', 'ellipsis')
  await expect(target).toHaveCSS('white-space', 'nowrap')
  expect(await target.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true)
  const before = await page.locator('th').nth(3).boundingBox()
  const handle = await page.locator('th').nth(3).locator('.resize-handle').boundingBox()
  await page.mouse.move(handle!.x + 4, handle!.y + 10)
  await page.mouse.down()
  await page.mouse.move(handle!.x + 64, handle!.y + 10)
  await page.mouse.up()
  const after = await page.locator('th').nth(3).boundingBox()
  expect(after!.width).toBeGreaterThan(before!.width + 40)
  const sizeRequest = page.waitForRequest((request) => new URL(request.url()).pathname === '/api/v1/admin/audit' && new URL(request.url()).searchParams.get('per_page') === '400')
  await page.locator('.page-size select').selectOption('400')
  await sizeRequest
  await page.locator('tbody tr.record').first().click()
  await expect(page.getByText('Test Browser')).toBeVisible()
  await page.getByLabel('開始日時').fill('2026-09-29T00:00')
  await page.getByLabel('終了日時').fill('2026-10-01T00:00')
  await page.getByPlaceholder('例: login').fill('login.failure')
  await page.getByPlaceholder('名前で検索').fill('監査')
  await page.locator('.filters select').selectOption('failure')
  await page.getByPlaceholder('実行者・操作・対象を検索').fill('login')
  const listRequest = page.waitForRequest((request) => new URL(request.url()).pathname === '/api/v1/admin/audit' && new URL(request.url()).searchParams.has('from_at'))
  await page.getByRole('button', { name: '絞り込む' }).click()
  const params = new URL((await listRequest).url()).searchParams
  expect(params.get('from_at')).toBe(String(Date.UTC(2026, 8, 28, 15)))
  expect(params.get('to_at')).toBe(String(Date.UTC(2026, 9, 0, 15)))
  expect(params.get('actor')).toBe('監査')
  expect(params.get('result')).toBe('failure')
  expect(params.get('q')).toBe('login')
  expect(params.get('per_page')).toBe('400')
  await expect(page.locator('tbody tr.record')).toHaveCount(1)
  await expect(page.locator('tbody tr.record')).toContainText('login.failure')
  const csvRequest = page.waitForRequest((request) => new URL(request.url()).pathname === '/api/v1/admin/audit.csv')
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'CSVエクスポート' }).click()
  expect(new URL((await csvRequest).url()).searchParams.get('result')).toBe('failure')
  expect((await download).suggestedFilename()).toBe('pb-audit.csv')
})
