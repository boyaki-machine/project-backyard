import { expect, test } from '@playwright/test'
import { login } from './fixtures/session'

// pb-284：アーカイブ済みのプロジェクトを一覧に出し、開いて、設定から解除できる。
// **アーカイブ済みのプロジェクトをこの試験で作り、最後に消す**（administrator で走らせる）。
// 既存のものを借りると、他の試験の消し残しに頼ることになる。
test('チェックボックスでアーカイブ済みを一覧に出し、開いて解除できる', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page)

  const csrf = (await page.context().cookies()).find((c) => c.name === 'pb_csrf')!.value
  const headers = { 'X-PB-CSRF': csrf }
  const target = { key: `arch-e2e-${Date.now().toString(36)}` }
  const created = await page.request.post('/api/v1/projects', {
    headers, data: { key: target.key, name: 'Archived listing regression', workflow_template: 'simple' },
  })
  expect(created.status(), await created.text()).toBe(201)

  try {
    expect((await page.request.post(`/api/v1/projects/${target.key}/archive`, { headers })).ok()).toBe(true)
    await page.reload()
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
    // 削除はアーカイブ済みにしか効かない。解除のあとなので、アーカイブし直してから消す
    await page.request.post(`/api/v1/projects/${target.key}/archive`, { headers })
    const removed = await page.request.delete(`/api/v1/projects/${target.key}`, { headers, data: { confirm_key: target.key } })
    expect(removed.status()).toBe(204)
  }
})
