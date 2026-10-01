import { expect, test, type Page } from '@playwright/test'

const browserErrors = new WeakMap<Page, string[]>()
test.beforeEach(async ({ page }) => {
  const errors: string[] = []
  browserErrors.set(page, errors)
  page.on('pageerror', error => errors.push(error.message))
})
test.afterEach(async ({ page }) => {
  expect(browserErrors.get(page) ?? []).toEqual([])
})

async function setup(page: Page, manage = true, fail = false) {
  const requests: { method: string; path: string; body?: any }[] = []
  let members = [{ actor_id: 'existing', kind: 'user', display_name: '既存メンバー', email: 'existing@example.com', role: 'project_member', joined_at: 1788080592000 }]
  const roles = ['project_admin', 'project_member', 'project_viewer'].map((key, i) => ({ key, scope: 'project', display_name: ['プロジェクト管理者', 'メンバー', '閲覧者'][i], description: '', sort_order: i }))
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url()), path = url.pathname, method = route.request().method()
    let body: unknown = { items: [] }, status = 200
    if (path === '/api/v1/me') body = { actor: { id: 'admin', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: manage ? 'administrator' : 'operator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false }, permissions: manage ? ['user.manage'] : [], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'project.edit'] }], expires_at: 4070908800000 }
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', status: 'active', timezone: 'Asia/Tokyo', members, workflow: { id: 'workflow', name: '検証', statuses: [] }, settings: {}, my_permissions: ['project.edit'], version: 1 }
    if (path === '/api/v1/roles') body = { items: roles }
    if (path === '/api/v1/admin/users') { requests.push({ method, path }); body = { items: [{ id: 'existing', kind: 'user', display_name: '既存メンバー', email: 'existing@example.com', is_active: true }, { id: 'new', kind: 'user', display_name: '追加メンバー', email: 'new@example.com', is_active: true }], total: 2, per_page: 200 } }
    const membership = /\/admin\/users\/([^/]+)\/memberships\/demo$/.exec(path)
    if (membership) {
      const request = method === 'PUT' ? route.request().postDataJSON() : undefined
      requests.push({ method, path, body: request })
      if (fail) { status = 403; body = { error: { code: 'forbidden', message: '変更できません', details: [] } } }
      else if (method === 'DELETE') { members = members.filter(m => m.actor_id !== membership[1]); status = 204; body = undefined }
      else { const found = members.find(m => m.actor_id === membership[1]); if (found) found.role = request.role; else members.push({ actor_id: 'new', kind: 'user', display_name: '追加メンバー', email: 'new@example.com', role: request.role, joined_at: 1788080592000 }); body = {} }
    }
    await route.fulfill({ status, contentType: 'application/json', body: body === undefined ? '' : JSON.stringify(body) })
  })
  await page.goto('/p/demo/settings')
  await page.getByRole('tab', { name: 'メンバー', exact: true }).click()
  return requests
}

for (const width of [1366, 768]) {
  test(`追加・ロール変更・削除をメンバータブで行う ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const requests = await setup(page)
    await page.getByRole('button', { name: 'メンバーを追加', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('option', { name: /既存メンバー/ })).toHaveCount(0)
    await dialog.getByLabel('ユーザー', { exact: true }).selectOption('new')
    await expect(dialog.getByLabel('ロール', { exact: true })).toHaveValue('project_member')
    const fields = await dialog.locator('input, select').evaluateAll(els => els.map(el => { const b = el.getBoundingClientRect(); return b.width > 0 && b.left >= 0 && b.right <= innerWidth }))
    expect(fields.every(Boolean)).toBe(true)
    await page.screenshot({ path: info.outputPath(`add-${width}.png`), fullPage: true })
    await dialog.getByRole('button', { name: '追加', exact: true }).click()
    await expect(page.getByRole('cell', { name: '追加メンバー', exact: true })).toBeVisible()
    await page.getByLabel('追加メンバー のロール', { exact: true }).selectOption('project_viewer')
    await expect(page.getByLabel('追加メンバー のロール', { exact: true })).toHaveValue('project_viewer')
    await page.screenshot({ path: info.outputPath(`members-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: '追加メンバー をメンバーから削除', exact: true }).click()
    await expect(page.getByRole('dialog')).toContainText('ユーザーアカウントは残ります')
    await page.getByRole('button', { name: '削除する', exact: true }).click()
    await expect(page.getByRole('cell', { name: '追加メンバー', exact: true })).toHaveCount(0)
    expect(requests.filter(r => r.method === 'PUT').map(r => r.body.role)).toEqual(['project_member', 'project_viewer'])
    expect(requests.filter(r => r.method === 'DELETE')).toHaveLength(1)
  })
}

test('管理権限なしでは参照のみで候補APIを呼ばない', async ({ page }) => {
  const requests = await setup(page, false)
  await expect(page.getByRole('button', { name: 'メンバーを追加' })).toHaveCount(0)
  await expect(page.getByRole('tabpanel').getByRole('combobox')).toHaveCount(0)
  await expect(page.getByText('メンバーの管理はアドミニストレータに依頼してください。')).toBeVisible()
  expect(requests).toHaveLength(0)
})

test('更新拒否時は元のロールと一覧を保ちエラーを表示する', async ({ page }) => {
  await setup(page, true, true)
  await page.getByLabel('既存メンバー のロール', { exact: true }).selectOption('project_viewer')
  await expect(page.getByRole('alert')).toContainText('この操作を行う権限がありません')
  await expect(page.getByLabel('既存メンバー のロール', { exact: true })).toHaveValue('project_member')
  await expect(page.getByText('メンバー情報を更新しました')).toHaveCount(0)
  await page.getByRole('button', { name: '既存メンバー をメンバーから削除', exact: true }).click()
  await page.getByRole('button', { name: '削除する', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('cell', { name: '既存メンバー', exact: true })).toBeVisible()
  await expect(page.getByRole('alert')).toContainText('この操作を行う権限がありません')
})
