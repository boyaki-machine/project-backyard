import { expect, test, type Page } from '@playwright/test'

async function login(page: Page) {
  const email = process.env.PB_E2E_EMAIL
  const password = process.env.PB_E2E_PASSWORD
  if (!email || !password) {
    throw new Error('PB_E2E_EMAIL と PB_E2E_PASSWORD を設定してください')
  }
  await page.goto('/login')
  await page.locator('input[name="email"]').fill(email)
  await page.locator('input[name="password"]').fill(password)
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/projects$/)
}

async function box(page: Page, selector: string) {
  const value = await page.locator(selector).boundingBox()
  expect(value).not.toBeNull()
  return value!
}

test('バックログの検索条件はエピック・タグ・担当を優先し、狭い幅でも検索の上に保つ', async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1366, height: 900 })
  await login(page)
  await page.goto('/p/demo/backlog')
  await expect(page.getByRole('heading', { level: 1, name: 'バックログ' })).toBeVisible()

  const epic = await box(page, '.backlog-filter-epic')
  const tag = await box(page, '.backlog-filter-tag')
  const assignee = await box(page, '.backlog-filter-assignee')
  const search = await box(page, '.backlog-filter-primary')
  expect(epic.x).toBeLessThan(tag.x)
  expect(tag.x).toBeLessThan(assignee.x)
  expect(Math.abs(epic.y - tag.y)).toBeLessThanOrEqual(1)
  expect(Math.abs(tag.y - assignee.y)).toBeLessThanOrEqual(1)
  expect(search.y).toBeGreaterThan(epic.y + epic.height - 1)
  await expect(page.getByText('スプリント', { exact: true }).first()).not.toBeVisible()

  const wideShot = testInfo.outputPath('backlog-filters-1366.png')
  await page.screenshot({ path: wideShot, fullPage: true })
  await testInfo.attach('backlog-filters-1366', { path: wideShot, contentType: 'image/png' })

  await page.setViewportSize({ width: 1024, height: 900 })
  const narrowEpic = await box(page, '.backlog-filter-epic')
  const narrowTag = await box(page, '.backlog-filter-tag')
  const narrowAssignee = await box(page, '.backlog-filter-assignee')
  const narrowSearch = await box(page, '.backlog-filter-primary')
  expect(narrowEpic.x).toBeLessThan(narrowTag.x)
  expect(narrowTag.x).toBeLessThan(narrowAssignee.x)
  expect(Math.abs(narrowEpic.y - narrowTag.y)).toBeLessThanOrEqual(1)
  expect(Math.abs(narrowTag.y - narrowAssignee.y)).toBeLessThanOrEqual(1)
  expect(narrowSearch.y).toBeGreaterThan(narrowEpic.y + narrowEpic.height - 1)
  await expect(page.getByRole('button', { name: /絞り込み/ })).toBeVisible()

  const narrowShot = testInfo.outputPath('backlog-filters-1024.png')
  await page.screenshot({ path: narrowShot, fullPage: true })
  await testInfo.attach('backlog-filters-1024', { path: narrowShot, contentType: 'image/png' })
})

test('予定期間は両端をURLへ保持し、逆転した範囲は適用できない', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 })
  await login(page)
  await page.goto('/p/demo/backlog')

  await page.getByRole('button', { name: '予定期間：指定なし' }).click()
  await page.getByLabel('予定期間の開始日').fill('2026-09-01')
  await page.getByLabel('予定期間の終了日').fill('2026-09-30')
  await page.getByRole('button', { name: '適用' }).click()
  await expect(page).toHaveURL(/planned_from=2026-09-01/)
  await expect(page).toHaveURL(/planned_to=2026-09-30/)

  await page.getByRole('button', { name: /予定期間：2026-09-01〜2026-09-30/ }).click()
  await page.getByLabel('予定期間の開始日').fill('2026-10-01')
  await expect(page.getByText('終了日は開始日以降にしてください')).toBeVisible()
  await expect(page.getByRole('button', { name: '適用' })).toBeDisabled()
})
