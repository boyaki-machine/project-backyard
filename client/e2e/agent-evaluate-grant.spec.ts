import { execFileSync } from 'node:child_process'
import { expect, test } from '@playwright/test'
import { login } from './fixtures/session'

// 評価エージェントの権限（delegation.evaluate）を、発行モーダルから付けられる（pb-288）。
// サーバは許可リストに持っていたが、画面に選択肢が無く、評価役を用意できなかった。
test('token issue dialog grants delegation.evaluate only when checked', async ({ page }) => {
  // 「自律実行の評価を許す」は自律実行が有効なときだけ出る（Delegation.md 1.3。pb-353）。
  // サーバの設定を変えずに、GET /me の features だけを有効にして見る
  await page.route('**/api/v1/me', async (route) => {
    const response = await route.fetch()
    const json = await response.json()
    await route.fulfill({ response, json: { ...json, features: { delegation: true } } })
  })
  await login(page)
  const csrf = (await page.context().cookies()).find(c => c.name === 'pb_csrf')!.value
  const headers = { 'X-PB-CSRF': csrf }
  const key = `eval-${Date.now()}`
  const created = await page.request.post('/api/v1/projects', { headers, data: { key, name: 'Agent evaluate grant', workflow_template: 'simple' } })
  expect(created.status()).toBe(201)
  let agentID = ''
  try {
    const agentResponse = await page.request.post('/api/v1/me/agents', { headers, data: { project_key: key, display_name: key, client_kind: 'other' } })
    expect(agentResponse.status()).toBe(201)
    agentID = (await agentResponse.json()).id
    await page.goto('/me/agents')
    const card = page.locator('article.card').filter({ hasText: key })
    const dialog = page.getByRole('dialog', { name: 'エージェント用トークンを発行', exact: true })
    const issue = async (grantEvaluate: boolean, reissue: boolean) => {
      if (reissue) {
        await card.getByRole('button', { name: `${key} の操作メニュー`, exact: true }).click()
        await page.getByRole('menuitem', { name: 'トークンを再発行' }).click()
      } else {
        await card.getByRole('button', { name: 'トークンを発行', exact: true }).click()
      }
      const grant = dialog.getByLabel('自律実行の評価を許す')
      // 毎回外れた状態から始まる（前の選択を引き継がない）
      await expect(grant).not.toBeChecked()
      if (grantEvaluate) {
        await grant.check()
        await expect(dialog.getByText('評価エージェントに指定するエージェントにだけ追加します。')).toBeVisible()
      }
      const issuing = page.waitForResponse(r => r.url().endsWith(`/me/agents/${agentID}/tokens`) && r.request().method() === 'POST')
      await dialog.getByRole('button', { name: '発行', exact: true }).click()
      const response = await issuing
      expect(response.status()).toBe(201)
      await page.getByRole('dialog', { name: 'エージェント用トークンを発行しました' }).getByRole('button', { name: '閉じる', exact: true }).last().click()
      return (await response.json()).scopes as string[]
    }
    expect(await issue(true, false)).toContain('delegation.evaluate')
    expect(await issue(false, true)).not.toContain('delegation.evaluate')
  } finally {
    execFileSync('make', ['psql'], { cwd: process.env.PB_DEV_ROOT ?? '..', input: `DELETE FROM project WHERE key = '${key}' AND name = 'Agent evaluate grant';\n${agentID ? `DELETE FROM audit_log WHERE actor_id = '${agentID}'; DELETE FROM actor WHERE id = '${agentID}' AND display_name = '${key}';` : ''}\n`, stdio: ['pipe', 'pipe', 'pipe'] })
  }
})
