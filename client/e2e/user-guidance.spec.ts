import { expect, test, type Page } from '@playwright/test'
import { parse } from '@vue/compiler-sfc'
import { uiMessages } from '../src/locales/en/ui'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const browserErrors = new WeakMap<Page, string[]>()
test.beforeEach(async ({ page }) => {
  const errors: string[] = []
  browserErrors.set(page, errors)
  page.on('pageerror', error => errors.push(error.message))
})
test.afterEach(async ({ page }) => {
  expect(browserErrors.get(page) ?? []).toEqual([])
})

const developmentText = /(?:GuiDesign|ApiDesign|DbDesign|Development|Requirements)\.md|docs\/design\/|Phase\s*[1-9]|設計文書|is_agent_reachable|proposal テーブル|スキーマの版/u

test('利用者向けテンプレートとプレースホルダーに開発文書への誘導を含まない', () => {
  const problems: string[] = []
  function scan(dir: string) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name)
      if (entry.isDirectory()) scan(path)
      else if (path.endsWith('.vue')) {
        const content = parse(readFileSync(path, 'utf8')).descriptor.template?.content ?? ''
        if (developmentText.test(content.replace(/<!--[\s\S]*?-->/g, ''))) problems.push(path)
      }
    }
  }
  scan('src')
  const routes = readFileSync('src/router/routes.ts', 'utf8').replace(/\/\*[\s\S]*?\*\/|\/\/[^\n]*/g, '')
  expect(developmentText.test(routes)).toBe(false)
  expect(problems).toEqual([])
})

for (const width of [1366, 768]) {
  for (const locale of ['ja', 'en']) {
    test(`未実装案内を維持しTLSの対処を画面内で示す ${width} ${locale}`, async ({ page }, info) => {
      await page.setViewportSize({ width, height: 900 })
      await page.route('**/api/v1/**', async route => {
        const path = new URL(route.request().url()).pathname
        let body: unknown = { items: [] }
        if (path === '/api/v1/me') body = { actor: { id: 'admin', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale, timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false }, permissions: ['system.settings', 'project.view'], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view'] }], expires_at: 4070908800000 }
        if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', members: [], workflow: { statuses: [] }, timezone: 'Asia/Tokyo' }
        if (path === '/api/v1/admin/settings/pending') body = { pending_confirmation: null }
        if (path === '/api/v1/admin/settings') body = { items: [{ key: 'tls_enabled', value: 'false', source: 'default', editable: true, default: 'false' }] }
        if (path === '/api/v1/admin/tls/certificates') body = { items: [{ id: 'cert', common_name: 'localhost', dns_names: ['localhost'], ip_addresses: ['127.0.0.1'], fingerprint: '00', is_self_signed: true, status: 'active', not_before: 1788080592000, not_after: 4070908800000, uploaded_at: 1788080592000, uploaded_by: null }], tls_enabled: false, listen_url: 'http://127.0.0.1:8080', listen_host: '127.0.0.1', listen_host_match: 'covered', secret_key_present: true, secret_key_origin: 'env' }
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
      })
      await page.goto('/p/demo/board')
      await expect(page.getByText(locale === 'ja' ? 'このページは未実装です' : uiMessages['このページは未実装です'])).toBeVisible()
      expect(await page.locator('main').innerText()).not.toMatch(developmentText)
      await page.screenshot({ path: info.outputPath(`placeholder-${width}-${locale}.png`), fullPage: true })
      await page.goto('/admin/settings')
      await page.getByRole('tab', { name: locale === 'ja' ? 'TLS 証明書' : 'TLS Certificate', exact: true }).click()
      await page.getByText(locale === 'ja' ? 'クライアントに信頼させる' : uiMessages['クライアントに信頼させる'], { exact: true }).click()
      const advice = page.getByText(locale === 'ja' ? '接続できない場合は、保存した証明書の場所と環境変数の設定を確認し、クライアントを再起動してください。' : 'If you cannot connect, check the saved certificate path and environment variable settings, then restart the client.', { exact: true })
      await expect(advice).toBeVisible()
      const bounds = await advice.boundingBox()
      expect(bounds!.width).toBeGreaterThan(200)
      expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
      expect(await page.locator('main').innerText()).not.toMatch(developmentText)
      await page.screenshot({ path: info.outputPath(`tls-${width}-${locale}.png`), fullPage: true })
      const issuer = page.getByText(locale === 'ja' ? '証明書の申込方法は発行元の案内を確認してください。登録できない場合は、証明書と秘密鍵が対応していること、有効期間、証明書の貼り付け順を確認してください。' : uiMessages['証明書の申込方法は発行元の案内を確認してください。登録できない場合は、証明書と秘密鍵が対応していること、有効期間、証明書の貼り付け順を確認してください。'], { exact: true })
      await issuer.scrollIntoViewIfNeeded()
      await expect(issuer).toBeVisible()
      await page.screenshot({ path: info.outputPath(`tls-help-${width}-${locale}.png`), fullPage: true })
    })
  }
}
