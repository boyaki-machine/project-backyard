import { expect, test, type Page, type Route } from '@playwright/test'

const japanese = /[ぁ-んァ-ヶ一-龠]/u

const session = {
  actor: {
    id: '01K00000000000000000000000', kind: 'user', display_name: 'Test User',
    email: 'test@example.com', system_role: 'administrator', locale: 'en',
    timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
  },
  permissions: ['project.view', 'project.create', 'user.manage', 'auditlog.view', 'system.settings', 'authprovider.manage'],
  projects: [{
    id: '01K00000000000000000000001', key: 'demo', name: 'Demo Project', role: 'project_admin',
    permissions: ['project.view', 'project.edit', 'ticket.view', 'ticket.create', 'doc.view', 'agent.register'],
  }],
  expires_at: 4070908800000,
}

const settingItems = [
  ['database_url', 'DB接続文字列', 'pb_app での接続文字列。起動時に接続プールを張るため、変更には再起動が要ります', '', 'string'],
  ['bind', '待受アドレス', 'HTTP を待ち受けるアドレスとポート。切り替えは即時で、期限内に確認しないと元へ戻ります。コンテナで動かしている場合は、公開側の設定（compose の ports や Service の targetPort）も合わせて変えてください', '127.0.0.1:8080', 'string'],
  ['log_format', 'ログ形式', '標準出力へ書くログの形式。text は開発時に人が読むためのものです', 'json', 'enum'],
  ['log_level', 'ログレベル', '記録するログの最低レベル。debug では /healthcheck のアクセスログも出ます', 'info', 'enum'],
  ['health_show_version', 'ヘルスチェックにバージョンを含める', 'GET /healthcheck の応答にバージョンを入れます。未認証の呼び出し元への情報開示になるため既定は無効です', 'false', 'bool'],
  ['cookie_secure', 'Cookie に Secure を付ける', 'HTTPS で公開する環境では有効にします', 'false', 'bool'],
].map(([key, display_name, description, value, value_type]) => ({
  key, display_name, description, value, value_type, source: 'default', layer: 2,
  env_key: `PB_${key.toUpperCase()}`, config_file_key: key, default_value: value,
  editable: true, secret: false, restart_required: false, needs_confirmation: key === 'bind' || key === 'cookie_secure',
  allowed: value_type === 'enum' ? ['json', 'text', 'debug', 'info', 'warn', 'error'] : null,
  updated_at: key === 'bind' ? 1790391845000 : null, updated_by: null,
}))

async function reply(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/v1/me') return reply(route, session)
    if (path === '/api/v1/me/mfa') return reply(route, { totp: [], recovery_codes: null })
    if (path === '/api/v1/me/passkeys' || path === '/api/v1/me/tokens' || path === '/api/v1/me/agents') return reply(route, { items: [] })
    if (path === '/api/v1/admin/settings/pending') return reply(route, { pending_confirmation: null })
    if (path === '/api/v1/admin/settings') return reply(route, { items: settingItems, config_file_path: null })
    if (path === '/api/v1/admin/database') return reply(route, {
      fetched_at: 1790391845000, connection: { host: 'localhost', port: 5432, database: 'pb', user: 'pb_app', tls: false },
      server: { version: '17', started_at: 1790305445000, max_connections: 100 },
      migration_version: 1, sessions: { database: 2, pb: 1 }, pool: { total: 1, acquired: 0, idle: 1, max: 10 }, size_bytes: 1024, tables: [],
    })
    return reply(route, { error: { code: 'not_found', message: '対象が見つかりません', details: [] } }, 404)
  })
}

async function mockProjectCatalog(page: Page) {
  await page.route('**/api/v1/roles?**', (route) => reply(route, { items: [
    { key: 'project_admin', scope: 'project', display_name: 'プロジェクト管理者', description: '当該プロジェクトの全操作と承認ができます', is_builtin: true, sort_order: 30 },
  ] }))
  await page.route('**/api/v1/projects/demo/sprints', (route) => reply(route, { items: [
    { id: '01K00000000000000000000003', name: 'Sprint One', goal: null, start_date: '2026-09-26', end_date: '2026-10-02', status: 'planned', ticket_count: 1, closed_count: 0 },
  ] }))
  await page.route('**/api/v1/projects/demo', (route) => reply(route, {
      id: session.projects[0].id, key: 'demo', name: 'Demo Project', description: null,
      status: 'active', workflow: { id: '01K0000000000000000000000002', name: 'Simple', statuses: [
        { key: 'todo', name: '未着手', category: 'todo', sort_order: 1, requires_human_approval: false, is_agent_reachable: true },
        { key: 'done', name: '完了', category: 'done', sort_order: 2, requires_human_approval: true, is_agent_reachable: false },
      ] },
      members: [{ actor_id: session.actor.id, kind: 'user', display_name: 'Test User', email: session.actor.email, role: 'project_admin', joined_at: 1790391845000 }],
      my_role: 'project_admin', my_permissions: ['project.view', 'project.edit', 'ticket.view'],
      settings: {}, version: 1, created_at: 1790391845000, updated_at: 1790391845000,
    }))
}

async function expectEnglish(page: Page, path: string) {
  await page.goto(path)
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await expect(page.locator('body')).toBeVisible()
  const text = await page.locator('body').innerText()
  expect(text, `${path} contains Japanese UI text`).not.toMatch(japanese)
}

test('English locale covers every implemented page and application-owned setting descriptions', async ({ page }, testInfo) => {
  test.setTimeout(60_000)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await mockApi(page)

  const routes = [
    '/projects', '/me', '/me/tokens', '/me/agents', '/admin/users', '/admin/settings',
    '/p/demo', '/p/demo/backlog', '/p/demo/search', '/p/demo/docs', '/p/demo/settings',
    '/p/demo/settings/agents', '/admin/audit', '/p/demo/board', '/p/demo/gantt',
    '/p/demo/approvals', '/p/demo/knowledge', '/p/demo/insights', '/p/demo/history', '/admin/system',
  ]
  for (const path of routes) await expectEnglish(page, path)

  await expectEnglish(page, '/admin/settings')
  await expect(page.getByText('Listening Address', { exact: true })).toBeVisible()
  await expect(page.getByText(/Address and port to listen on for HTTP/)).toBeVisible()
  await expect(page.getByText('Database Connection String', { exact: true })).toBeVisible()
  await expect(page.getByText(/Connection string for pb_app/)).toBeVisible()
  await expect(page.getByText('09/26/2026 12:04:05 PM', { exact: true })).toBeVisible()
  const desktop = testInfo.outputPath('i18n-pages-settings-1440.png')
  await page.screenshot({ path: desktop, fullPage: true })
  await testInfo.attach('i18n-pages-settings-1440', { path: desktop, contentType: 'image/png' })

  await page.setViewportSize({ width: 390, height: 844 })
  for (const path of ['/me', '/p/demo/backlog', '/admin/settings']) await expectEnglish(page, path)
  const mobile = testInfo.outputPath('i18n-pages-settings-390.png')
  await page.screenshot({ path: mobile, fullPage: true })
  await testInfo.attach('i18n-pages-settings-390', { path: mobile, contentType: 'image/png' })

  await mockProjectCatalog(page)
  await page.goto('/p/demo/settings')
  await expect(page.getByText('Unstarted', { exact: true })).toBeVisible()
  await expect(page.locator('.statuses')).toContainText('Completed')
  await page.getByRole('tab', { name: 'Members' }).click()
  await expect(page.getByText('Project Administrator', { exact: true })).toBeVisible()
  await expect(page.getByText('09/26/2026', { exact: true })).toBeVisible()
  const membersScroll = page.locator('.members-table-scroll')
  const membersBox = await membersScroll.boundingBox()
  expect(membersBox).not.toBeNull()
  expect(membersBox!.x + membersBox!.width).toBeLessThanOrEqual(390)
  expect(await membersScroll.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true)
  await membersScroll.evaluate((element) => { element.scrollLeft = element.scrollWidth })
  const roles = testInfo.outputPath('i18n-pages-roles-390.png')
  await page.screenshot({ path: roles, fullPage: true })
  await testInfo.attach('i18n-pages-roles-390', { path: roles, contentType: 'image/png' })
  await page.getByRole('tab', { name: 'Sprint' }).click()
  await expect(page.getByText('09/26/2026 — 10/02/2026', { exact: true })).toBeVisible()

  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/admin/settings')
  await page.getByRole('tab', { name: 'DB', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Choose File' })).toBeVisible()
  await expect(page.getByText('No file selected')).toBeVisible()
  const desktopPicker = page.getByRole('button', { name: 'Choose File' })
  await desktopPicker.scrollIntoViewIfNeeded()
  const desktopPickerBox = await desktopPicker.boundingBox()
  expect(desktopPickerBox).not.toBeNull()
  expect(desktopPickerBox!.x + desktopPickerBox!.width).toBeLessThanOrEqual(1440)
  const databaseDesktop = testInfo.outputPath('i18n-pages-database-1440.png')
  await page.screenshot({ path: databaseDesktop, fullPage: true })
  await testInfo.attach('i18n-pages-database-1440', { path: databaseDesktop, contentType: 'image/png' })
  await page.setViewportSize({ width: 390, height: 844 })
  const picker = page.getByRole('button', { name: 'Choose File' })
  const pickerBox = await picker.boundingBox()
  expect(pickerBox).not.toBeNull()
  expect(pickerBox!.x + pickerBox!.width).toBeLessThanOrEqual(390)
  await picker.scrollIntoViewIfNeeded()
  const database = testInfo.outputPath('i18n-pages-database-390.png')
  await page.screenshot({ path: database, fullPage: true })
  await testInfo.attach('i18n-pages-database-390', { path: database, contentType: 'image/png' })
  const [fileChooser] = await Promise.all([page.waitForEvent('filechooser'), picker.click()])
  await fileChooser.setFiles({ name: 'backup.gz', mimeType: 'application/gzip', buffer: Buffer.from('invalid archive') })
  await expect(page.getByText('backup.gz', { exact: true })).toBeVisible()

  await page.route('**/api/v1/projects?**', (route) => reply(route, { items: [{
    id: session.projects[0].id, key: 'demo', name: 'Demo Project', description: null,
    status: 'active', ticket_count: 1, closed_count: 0, progress: 0, my_role: 'project_admin',
    updated_at: 1790391845000,
  }], page: 1, per_page: 25, total: 1, total_pages: 1 }))
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/projects')
  await expect(page.locator('td.updated')).toHaveText('09/26/2026 12:04:05 PM')
  expect(await page.locator('td.updated').evaluate((cell) => cell.scrollWidth <= cell.clientWidth)).toBe(true)

})
