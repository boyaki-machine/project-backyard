import { expect, test, type Page, type Route } from '@playwright/test'

const session = {
  actor: { id: '01K00000000000000000000000', kind: 'user', display_name: 'Test User',
    email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo',
    theme: 'light', hue: 'blue', must_change_password: false },
  permissions: ['project.view', 'system.settings'], projects: [], expires_at: 4070908800000,
}

async function mockPolling(page: Page, holdFresh = false) {
  const fresh = new Map<string, Route>()
  const held = new Map<string, Route>()
  const calls = new Map<string, number>()
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/v1/auth/logout') return route.fulfill({ status: 204 })
    if (path === '/api/v1/me' || path === '/api/v1/auth/login') return route.fulfill({ json: session })
    if (path === '/api/v1/projects') return route.fulfill({ json: { items: [], page: 1, per_page: 25, total: 0, total_pages: 0 } })
    if (path === '/api/v1/me/notifications/unread-count' || path === '/api/v1/admin/settings/pending') {
      const n = (calls.get(path) ?? 0) + 1
      calls.set(path, n)
      if (n === 1) { held.set(path, route); return }
      if (n === 2 && holdFresh) { fresh.set(path, route); return }
      return route.fulfill({ json: path.endsWith('unread-count') ? { count: 2 } : { pending_confirmation: null } })
    }
    return route.fulfill({ status: 404, json: {} })
  })
  return { held, fresh }
}

async function releaseOldPolling(page: Page, held: Map<string, Route>) {
  for (const [path, route] of held) {
    const response = page.waitForResponse(r => new URL(r.url()).pathname === path)
    await route.fulfill({ json: path.endsWith('unread-count') ? { count: 9 } : {
      pending_confirmation: { keys: ['cookie_secure'], expires_at: Date.now() + 300_000,
        changed_by: { id: '01K00000000000000000000009', kind: 'user', display_name: 'Previous User' } },
    } })
    await (await response).finished()
  }
  // Fetchの継続処理とVueの描画を済ませてから、古い応答が採用されなかったかを見る。
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

for (const width of [600, 1440]) {
  test(`late polling responses cannot overwrite newer data at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.clock.install()
    const { held } = await mockPolling(page)
    await page.goto('/projects')
    await expect.poll(() => held.size).toBe(2)
    await page.clock.fastForward(30_000)
    const count = page.locator('.unread-count').first()
    await expect(count).toHaveText('2')
    await releaseOldPolling(page, held)
    await expect(count).toHaveText('2')
    await expect(page.locator('.bar[role="alert"]')).toHaveCount(0)
    const box = await count.boundingBox()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(width)
    await page.screenshot({ path: info.outputPath(`polling-${width}.png`) })
  })
}

test('polling responses from a previous login are discarded', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const { held, fresh } = await mockPolling(page, true)
  await page.goto('/projects')
  await expect.poll(() => held.size).toBe(2)
  await page.locator('.user > button.row').click()
  await page.getByRole('button', { name: /ログアウト/ }).click()
  await expect(page).toHaveURL(/\/login$/)
  // stopの後、次のstartより前に届く結果も、前の利用者の状態へ戻してはならない。
  await releaseOldPolling(page, held)
  await page.getByLabel('メールアドレス', { exact: true }).fill('test@example.com')
  await page.locator('input[name="password"]').fill('example-test-password')
  await page.getByRole('button', { name: 'ログイン', exact: true }).click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect.poll(() => fresh.size).toBe(2)
  await expect(page.locator('.unread-count')).toHaveCount(0)
  await expect(page.locator('.bar[role="alert"]')).toHaveCount(0)
  for (const [path, route] of fresh) {
    await route.fulfill({ json: path.endsWith('unread-count') ? { count: 2 } : { pending_confirmation: null } })
  }
  await expect(page.locator('.unread-count').first()).toHaveText('2')
})

test('a poll started before confirmation cannot restore the pending banner', async ({ page }) => {
  await page.clock.install()
  const { held, fresh } = await mockPolling(page, true)
  await page.route('**/api/v1/admin/settings/confirm', route => route.fulfill({ status: 204 }))
  await page.goto('/projects')
  await expect.poll(() => held.size).toBe(2)
  await releaseOldPolling(page, held)
  const confirm = page.getByRole('button', { name: 'アクセスできました', exact: true })
  await expect(confirm).toBeVisible()
  await page.clock.fastForward(30_000)
  await expect.poll(() => fresh.size).toBe(2)
  await confirm.click()
  await expect(page.locator('.bar[role="alert"]')).toHaveCount(0)
  await releaseOldPolling(page, fresh)
  await expect(page.locator('.bar[role="alert"]')).toHaveCount(0)
})

test('confirmation from a previous login cannot clear the new pending banner', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const { held, fresh } = await mockPolling(page, true)
  let confirmation: Route | undefined
  await page.route('**/api/v1/admin/settings/confirm', route => { confirmation = route })
  await page.goto('/projects')
  await expect.poll(() => held.size).toBe(2)
  await releaseOldPolling(page, held)
  await page.getByRole('button', { name: 'アクセスできました', exact: true }).click()
  await expect.poll(() => confirmation !== undefined).toBe(true)
  // 未確認の帯は下端のメニューに重なるので、実キーで開く。
  await page.locator('.user > button.row').focus()
  await page.keyboard.press('Enter')
  await page.getByRole('button', { name: /ログアウト/ }).focus()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('メールアドレス', { exact: true }).fill('test@example.com')
  await page.locator('input[name="password"]').fill('example-test-password')
  await page.getByRole('button', { name: 'ログイン', exact: true }).click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect.poll(() => fresh.size).toBe(2)
  await releaseOldPolling(page, fresh)
  const confirm = page.getByRole('button', { name: 'アクセスできました', exact: true })
  await expect(confirm).toBeEnabled()
  const response = page.waitForResponse(r => r.url().endsWith('/admin/settings/confirm'))
  await confirmation!.fulfill({ status: 204 })
  await response // 204は本文の完了イベントを待たず、fetchの継続と描画を待つ。
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await expect(confirm).toBeVisible()
  await expect(confirm).toBeEnabled()
})
