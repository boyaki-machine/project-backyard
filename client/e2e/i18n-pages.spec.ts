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
  expires_at: '2099-01-01T00:00:00Z',
}

const settingItems = [
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
  updated_at: null, updated_by: null,
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
    return reply(route, { error: { code: 'not_found', message: '対象が見つかりません', details: [] } }, 404)
  })
}

async function expectEnglish(page: Page, path: string) {
  await page.goto(path)
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await expect(page.locator('body')).toBeVisible()
  const text = await page.locator('body').innerText()
  expect(text, `${path} contains Japanese UI text`).not.toMatch(japanese)
}

test('English locale covers every implemented page and application-owned setting descriptions', async ({ page }, testInfo) => {
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
  const desktop = testInfo.outputPath('i18n-pages-settings-1440.png')
  await page.screenshot({ path: desktop, fullPage: true })
  await testInfo.attach('i18n-pages-settings-1440', { path: desktop, contentType: 'image/png' })

  await page.setViewportSize({ width: 390, height: 844 })
  for (const path of ['/me', '/p/demo/backlog', '/admin/settings']) await expectEnglish(page, path)
  const mobile = testInfo.outputPath('i18n-pages-settings-390.png')
  await page.screenshot({ path: mobile, fullPage: true })
  await testInfo.attach('i18n-pages-settings-390', { path: mobile, contentType: 'image/png' })
})
