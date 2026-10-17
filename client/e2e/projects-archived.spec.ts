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

// pb-284：アーカイブ済みのプロジェクトを一覧に出し、開いて、設定から解除できる。
// **既存のアーカイブ済みを1件借り、最後にアーカイブへ戻す**（project.archive を持つ利用者で走らせる）。
test('チェックボックスでアーカイブ済みを一覧に出し、開いて解除できる', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page)

  const res = await page.request.get('/api/v1/projects?status=archived&per_page=100')
  expect(res.ok()).toBe(true)
  const archived = ((await res.json()) as { items: { key: string; name: string; my_role: string | null }[] })
    .items.filter((p) => p.my_role !== null)
  test.skip(archived.length === 0, 'メンバーであるアーカイブ済みのプロジェクトが要る')
  const target = archived[0]

  try {
    const checkbox = page.getByRole('checkbox', { name: 'アーカイブプロジェクトも表示' })
    const row = page.locator('tbody tr', { hasText: target.key })
    await expect(checkbox).not.toBeChecked()
    await expect(row).toHaveCount(0)

    await checkbox.check()
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('アーカイブ済み')

    // 開ける（閲覧できる）
    await row.locator('a').first().click()
    await expect(page).toHaveURL(new RegExp(`/p/${target.key}$`))

    // 設定から解除する。解除は確認を挟まない（GuiDesign.md 5.9.3）
    await page.goto(`/p/${target.key}/settings`)
    await page.getByRole('button', { name: 'アーカイブを解除' }).click()
    await expect(page.getByText('● 有効')).toBeVisible()
    await expect(page.getByRole('button', { name: 'アーカイブする' })).toBeVisible()
  } finally {
    const csrf = (await page.context().cookies()).find((c) => c.name === 'pb_csrf')!.value
    const back = await page.request.post(`/api/v1/projects/${target.key}/archive`, {
      headers: { 'X-PB-CSRF': csrf },
    })
    expect(back.ok()).toBe(true)
  }
})
