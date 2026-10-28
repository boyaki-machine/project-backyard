import { expect, test, type Page } from '@playwright/test'
import { hasPassword, login } from './fixtures/session'

async function csrf(page: Page): Promise<Record<string, string>> {
  const token = (await page.context().cookies()).find((c) => c.name === 'pb_csrf')!.value
  return { 'X-PB-CSRF': token }
}

// pb-285：プロジェクト設定から、アーカイブ済みのプロジェクトを、キーを打ち込んで消す。
// **administrator で走らせる。** 消すプロジェクトはこの試験で作る。
test('アーカイブ済みのプロジェクトだけを、キーを打ち込んで削除できる', async ({ page }) => {
  test.skip(!hasPassword(), 'PB_E2E_PASSWORD が要る')
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page)

  const key = `del-e2e-${Date.now().toString(36)}`
  const headers = await csrf(page)
  const created = await page.request.post('/api/v1/projects', {
    headers,
    data: { key, name: '削除の確認' },
  })
  expect(created.ok()).toBe(true)

  try {
    // 有効なうちは押せない
    await page.goto(`/p/${key}/settings`)
    const block = page.locator('.delete-block')
    const remove = block.getByRole('button', { name: 'プロジェクトを削除' })
    await expect(remove).toBeDisabled()
    await expect(block).toContainText('アーカイブしてから削除できます。')

    // アーカイブすると押せる
    await page.getByRole('button', { name: 'アーカイブする' }).click()
    await page.locator('[role=dialog]').getByRole('button', { name: 'アーカイブする' }).click()
    await expect(remove).toBeEnabled()
    await remove.click()

    const dialog = page.locator('[role=dialog]')
    const confirm = dialog.getByRole('button', { name: '削除する' })
    const input = dialog.locator('input[name="confirm_project_key"]')
    await expect(confirm).toBeDisabled()
    await input.fill(key + 'x')
    await expect(confirm).toBeDisabled()
    await input.fill(key)
    await expect(confirm).toBeEnabled()
    await confirm.click()

    await expect(page).toHaveURL(/\/projects$/)
    const gone = await page.request.get(`/api/v1/projects/${key}`)
    expect(gone.status()).toBe(404)
  } finally {
    // 途中で落ちたときだけ残る。消えていれば 404 で何もしない
    const h = await csrf(page)
    await page.request.post(`/api/v1/projects/${key}/archive`, { headers: h })
    await page.request.delete(`/api/v1/projects/${key}`, { headers: h, data: { confirm_key: key } })
  }
})

test('project_admin にはプロジェクトの削除が出ない', async ({ page }) => {
  test.skip(!hasPassword(), 'PB_E2E_PASSWORD が要る')
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, 'pm')
  await page.goto('/p/demo/settings')
  await expect(page.getByRole('heading', { name: 'プロジェクトの状態' })).toBeVisible()
  await expect(page.locator('.delete-block')).toHaveCount(0)
})
