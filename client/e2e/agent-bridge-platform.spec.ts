import { expect, test, type Page, type Route } from '@playwright/test'

const agentID = '01K00000000000000000000001'
const actor = {
  id: '01K00000000000000000000000', kind: 'user', display_name: 'Test User',
  email: 'test@example.com', system_role: 'administrator', locale: 'ja',
  timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
}

async function reply(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockApi(page: Page, setupRequests: string[]) {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path === '/api/v1/me') return reply(route, {
      actor, permissions: [], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view'] }],
      expires_at: '2099-01-01T00:00:00Z',
    })
    if (path === '/api/v1/me/agents') return reply(route, { items: [{
      id: agentID, display_name: 'My Codex', client_kind: 'codex', model_name: null,
      model_version: null, project: { key: 'demo', name: 'Demo' }, token_env_suffix: 'TEST',
      token_env_name: 'PB_TOKEN_TEST', trust_level: 1, is_active: true,
      created_at: '2026-09-01T00:00:00Z', token: null,
    }] })
    if (path === '/api/v1/agent-client-kinds') return reply(route, { items: [{ key: 'codex', display_name: 'OpenAI Codex', has_setup_template: true }] })
    if (path === '/api/v1/agent-scopes') return reply(route, { defaults: [], optional: [] })
    if (path === `/api/v1/me/agents/${agentID}/setup`) {
      setupRequests.push(url.search)
      const windows = url.searchParams.get('os') === 'windows'
      return reply(route, {
        agent: { id: agentID, display_name: 'My Codex', client_kind: 'codex', client_display_name: 'OpenAI Codex', token_env_name: 'PB_TOKEN_TEST' },
        project: { key: 'demo', name: 'Demo' }, base_url: 'http://localhost', mcp_url: 'http://localhost/mcp/demo',
        transport: url.searchParams.get('transport'), export_line: windows ? "$env:PB_TOKEN_TEST='token'" : "export PB_TOKEN_TEST='token'",
        files: [{ path: '.codex/config.toml', client_kind: 'codex', mode: 'merge', language: 'toml', content: `command = "pb-mcp-bridge${windows ? '.exe' : ''}"` }],
        ca_trust: { verification: 'partial', body_md: 'CA guide' },
      })
    }
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })
}

test('Codex bridge ZIP uses the selected OS and CPU', async ({ page }, testInfo) => {
  const setupRequests: string[] = []
  await mockApi(page, setupRequests)
  await page.goto('/me/agents')
  await page.getByRole('button', { name: '接続の手順を開く ▾' }).click()
  await page.getByRole('radio', { name: /ローカル stdio ブリッジ/ }).check()
  await expect(page.getByLabel('端末のOS')).toBeVisible()
  await expect(page.getByLabel('CPUアーキテクチャ')).toBeVisible()
  expect(setupRequests).toHaveLength(1)
  await expect(page.locator('.panel .download')).toHaveCount(0)

  await page.getByLabel('端末のOS').selectOption('windows')
  expect(setupRequests).toHaveLength(1)
  await page.getByLabel('CPUアーキテクチャ').selectOption('amd64')
  await expect.poll(() => setupRequests.length).toBe(2)
  expect(setupRequests[1]).toContain('transport=bridge')
  expect(setupRequests[1]).toContain('os=windows')
  expect(setupRequests[1]).toContain('arch=amd64')
  const zip = page.locator('.panel .file-head a.download')
  await expect(zip).toHaveAttribute('href', new RegExp(`setup\\.zip\\?transport=bridge&os=windows&arch=amd64$`))
  await expect(page.locator('.panel .content')).toContainText('pb-mcp-bridge.exe')
  await expect(page.locator('.panel .export')).toContainText('$env:PB_TOKEN_TEST')

  for (const width of [1280, 600]) {
    await page.setViewportSize({ width, height: 900 })
    const shot = testInfo.outputPath(`bridge-platform-${width}.png`)
    await page.screenshot({ path: shot, fullPage: true })
    await testInfo.attach(`bridge-platform-${width}`, { path: shot, contentType: 'image/png' })
  }
})
