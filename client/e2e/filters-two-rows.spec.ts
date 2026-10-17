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

/** 見えている部品の上端を行として数える（4px 以内は同じ行） */
async function rowTops(page: Page, selector: string): Promise<number[]> {
  const tops = await page.locator(selector).evaluateAll((els) =>
    els
      .map((el) => el.getBoundingClientRect())
      .filter((r) => r.width > 0 && r.height > 0)
      .map((r) => r.top),
  )
  const rows: number[] = []
  for (const t of tops.sort((a, b) => a - b)) {
    if (rows.length === 0 || t - rows[rows.length - 1] > 4) rows.push(t)
  }
  return rows
}

async function leftOf(page: Page, selector: string): Promise<{ x: number; y: number }> {
  const b = await page.locator(selector).first().boundingBox()
  expect(b).not.toBeNull()
  return { x: b!.x, y: b!.y }
}

// pb-286：バックログ・チケット検索のフィルタを2行に収め、1行目の左端をエピック・タグで始める
for (const width of [1440, 1366]) {
  test(`バックログのフィルタは ${width}px で2行に収まる`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await login(page)
    await page.goto('/p/demo/backlog')
    await expect(page.getByRole('heading', { level: 1, name: 'バックログ' })).toBeVisible()

    const rows = await rowTops(page, '.backlog-filters .filter, .backlog-filter-clear')
    expect(rows).toHaveLength(2)

    const epic = await leftOf(page, '.backlog-filter-epic')
    const tag = await leftOf(page, '.backlog-filter-tag')
    expect(Math.abs(epic.y - rows[0])).toBeLessThanOrEqual(4)
    expect(Math.abs(tag.y - rows[0])).toBeLessThanOrEqual(4)
    expect(epic.x).toBeLessThan(tag.x)
    // 対象（#178 で足した条件）も2行目に入る
    const scope = await leftOf(page, '.backlog-filter-scope')
    expect(Math.abs(scope.y - rows[1])).toBeLessThanOrEqual(4)
  })
}

for (const width of [1440, 1280, 1100]) {
  test(`チケット検索の条件は ${width}px で2行に収まる`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await login(page)
    await page.goto('/p/demo/search')
    await expect(page.getByRole('heading', { level: 1, name: 'チケット検索' })).toBeVisible()

    const rows = await rowTops(page, '.search-conditions .search-filter, .search-clear')
    expect(rows).toHaveLength(2)

    const filters = page.locator('.search-conditions-select .search-filter-label')
    await expect(filters.nth(0)).toHaveText('エピック')
    await expect(filters.nth(1)).toHaveText('タグ')
    const epic = await leftOf(page, '.search-conditions-select .search-filter:nth-child(1)')
    const tag = await leftOf(page, '.search-conditions-select .search-filter:nth-child(2)')
    expect(epic.x).toBeLessThan(tag.x)
    // 入力欄が潰れていない（キーワードが読める幅を持つ）
    const keyword = await page.locator('.search-keyword input').boundingBox()
    expect(keyword!.width).toBeGreaterThanOrEqual(160)
  })
}

test('チケット検索のタグと着手日で絞り込める', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page)

  const tags = (await (await page.request.get('/api/v1/projects/demo/tags')).json()) as {
    items: { id: string; name: string }[]
  }
  test.skip(tags.items.length === 0, 'タグが要る')
  const tag = tags.items[0]

  await page.goto('/p/demo/search')
  await page.getByRole('button', { name: 'タグ：すべて' }).click()
  await page.getByRole('checkbox', { name: tag.name, exact: true }).check()
  await expect(page).toHaveURL(new RegExp(`tag=${tag.id}`))
  await page.keyboard.press('Escape')

  const rows = page.locator('table tbody tr')
  await expect(rows.first()).toBeVisible()
  const count = await rows.count()
  for (let i = 0; i < count; i++) {
    await expect(rows.nth(i).locator('.search-tag', { hasText: tag.name })).toHaveCount(1)
  }

  // 着手日は予定期間と同じ部品。両端が1回で URL に載る
  await page.getByRole('button', { name: '着手日：指定なし' }).click()
  await page.getByLabel('着手日の開始日').fill('2026-01-01')
  await page.getByLabel('着手日の終了日').fill('2026-12-31')
  await page.getByRole('button', { name: '適用' }).click()
  await expect(page).toHaveURL(/started_since=\d+/)
  await expect(page).toHaveURL(/started_before=\d+/)
  await expect(page.getByRole('button', { name: '着手日：2026-01-01〜2026-12-31' })).toBeVisible()
})
