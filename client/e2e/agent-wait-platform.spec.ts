import { expect, test, type Page, type Route } from '@playwright/test'

/**
 * 接続設定の OS・CPU（GuiDesign.md 5.8.2、ApiDesign.md 4.5.8.1c。pb-348）。
 *
 * Claude Code・Codex は、直接接続でも端末の OS と CPU を選べ、選ぶと zip に待ち受けの道具 pb-wait が入る。
 * 選ばなければ従来どおり（入らない）。pb-wait を同梱しない種別（Copilot など）には選択を出さない。
 * API はモックする。zip の中身はサーバの試験（me_agent_setup_test.go）で確かめる。
 */

const agentID = '01K00000000000000000000001'
const actor = {
  id: '01K00000000000000000000000', kind: 'user', display_name: 'Test User',
  email: 'test@example.com', system_role: 'administrator', locale: 'ja',
  timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
}

async function reply(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockApi(page: Page, kind: string, label: string, setupRequests: string[]) {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path === '/api/v1/me') return reply(route, {
      actor, permissions: [], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view'] }],
      expires_at: 4070908800000,
    })
    if (path === '/api/v1/me/agents') return reply(route, { items: [{
      id: agentID, display_name: `My ${label}`, client_kind: kind, model_name: null,
      model_version: null, project: { key: 'demo', name: 'Demo' }, token_env_suffix: 'TEST',
      token_env_name: 'PB_TOKEN_TEST', trust_level: 1, is_active: true,
      created_at: 1788220800000, token: null, open_works: [], delegations: [],
    }] })
    if (path === '/api/v1/agent-client-kinds') return reply(route, { items: [{ key: kind, display_name: label, has_setup_template: true }] })
    if (path === '/api/v1/agent-scopes') return reply(route, { defaults: [], optional: [] })
    if (path === `/api/v1/me/agents/${agentID}/setup`) {
      setupRequests.push(url.search)
      const windows = url.searchParams.get('os') === 'windows'
      return reply(route, {
        agent: { id: agentID, display_name: `My ${label}`, client_kind: kind, client_display_name: label, token_env_name: 'PB_TOKEN_TEST' },
        project: { key: 'demo', name: 'Demo' }, base_url: 'http://localhost', mcp_url: 'http://localhost/mcp/demo',
        transport: 'direct', export_line: windows ? "$env:PB_TOKEN_TEST='token'" : "export PB_TOKEN_TEST='token'",
        files: [{ path: '.mcp.json', client_kind: kind, mode: 'merge', language: 'json', content: '{}' }],
        ca_trust: { verification: 'partial', body_md: 'CA guide' },
      })
    }
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })
}

for (const width of [1280, 600]) {
  test(`Claude Code は直接接続でも OS・CPU を選べ、選ぶと zip の URL に乗る ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const setupRequests: string[] = []
    await mockApi(page, 'claude_code', 'Claude Code', setupRequests)
    await page.goto('/me/agents')
    await page.getByRole('button', { name: '接続の手順を開く ▾' }).click()

    // 選ばなくても接続設定と zip は出る（pb-wait は入らない）
    const zip = page.locator('.panel .file-head a.download')
    await expect(zip).toHaveAttribute('href', /setup\.zip\?transport=direct$/)
    await expect(page.locator('[data-role="wait-hint"]')).toContainText('待ち受けの道具（pb-wait）')
    await expect(page.getByLabel('端末のOS')).toBeVisible()

    await page.getByLabel('端末のOS').selectOption('windows')
    await page.getByLabel('CPUアーキテクチャ').selectOption('amd64')
    await expect(zip).toHaveAttribute('href', /setup\.zip\?transport=direct&os=windows&arch=amd64$/)
    expect(setupRequests.at(-1)).toContain('os=windows')
    // Windows では PowerShell の案内を、種別の名前で出す
    await expect(page.locator('.panel')).toContainText('同じ PowerShell から Claude Code を起動します')
    await page.screenshot({ path: info.outputPath(`wait-platform-${width}.png`), fullPage: true })
  })
}

test('pb-wait を同梱しない種別（Copilot）には OS・CPU を出さない', async ({ page }) => {
  const setupRequests: string[] = []
  await mockApi(page, 'copilot', 'GitHub Copilot', setupRequests)
  await page.goto('/me/agents')
  await page.getByRole('button', { name: '接続の手順を開く ▾' }).click()
  await expect(page.locator('.panel .file-head a.download')).toHaveAttribute('href', /setup\.zip\?transport=direct$/)
  await expect(page.getByLabel('端末のOS')).toHaveCount(0)
  await expect(page.locator('[data-role="wait-hint"]')).toHaveCount(0)
})
