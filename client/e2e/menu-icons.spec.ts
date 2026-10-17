import { expect, test, type Page } from '@playwright/test'
import ja from '../src/locales/ja/menu'
import en from '../src/locales/en/menu'

const permissions = ['project.view', 'project.edit', 'ticket.view', 'doc.view']

async function setup(page: Page, locale: 'ja' | 'en', theme: 'light' | 'dark', timeline = true) {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let body: unknown = { items: [], total: 0, page: 1, per_page: 50, total_pages: 0 }
    if (path === '/api/v1/me') body = {
      actor: { id: 'test-admin', kind: 'user', display_name: 'Test User', email: 'test@example.com',
        system_role: 'administrator', locale, timezone: 'Asia/Tokyo', theme, hue: 'blue', must_change_password: false },
      permissions: ['project.view', 'user.manage', 'auditlog.view', 'system.settings'],
      projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions }], expires_at: 4070908800000,
    }
    if (path === '/api/v1/projects/demo') body = {
      key: 'demo', name: 'Demo', description: null, status: 'active', timezone: 'Asia/Tokyo', version: 1,
      settings: { views: { timeline } }, members: [], my_permissions: permissions,
      workflow: { key: 'simple', name: 'Simple', statuses: [{ key: 'todo', name: 'Todo', category: 'todo', sort_order: 1 }] },
    }
    if (path === '/api/v1/me/notifications/unread-count') body = { count: 12 }
    if (path === '/api/v1/admin/settings/pending') body = { pending_confirmation: null }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/p/demo/settings')
}

for (const scenario of [
  { width: 1440, collapsed: true, locale: 'ja', theme: 'light' },
  { width: 1024, collapsed: true, locale: 'en', theme: 'dark' },
  { width: 1440, collapsed: false, locale: 'en', theme: 'light' },
  { width: 390, collapsed: false, locale: 'ja', theme: 'light' },
] as const) {
  test(`メニューのSVGと寸法・項目名 ${JSON.stringify(scenario)}`, async ({ page }, testInfo) => {
    const errors: string[] = []
    page.on('pageerror', (e) => errors.push(e.message))
    await page.setViewportSize({ width: scenario.width, height: 900 })
    await page.addInitScript((collapsed) => localStorage.setItem('pb.menu_collapsed', String(collapsed)), scenario.collapsed)
    await setup(page, scenario.locale, scenario.theme)
    const text = scenario.locale === 'ja' ? ja.main : en.main
    if (scenario.width < 768) await page.getByRole('button', { name: text.toggle }).click()
    const menu = page.getByRole('navigation', { name: text.label })
    await expect(menu).toBeVisible()
    const entries = [
      ['projects', text.projects, '/projects'], ['dashboard', text.dashboard, '/p/demo'],
      ['backlog', text.backlog, '/p/demo/backlog'], ['gantt', text.gantt, '/p/demo/gantt'],
      ['clock', scenario.locale === 'ja' ? 'タイムライン' : 'Timeline', '/p/demo/timeline'],
      ['search', text.search, '/p/demo/search'], ['insights', text.insights, '/p/demo/insights'],
      ['docs', text.docs, '/p/demo/docs'], ['project-settings', text.projectSettings, '/p/demo/settings'],
      ['accounts', text.accounts, '/admin/users'], ['audit', text.auditLog, '/admin/audit'],
      ['app-settings', text.appSettings, '/admin/settings'],
    ]
    await expect(menu.locator('[data-menu-icon]')).toHaveCount(entries.length)
    const signatures: string[] = []
    for (const [iconName, label, href] of entries) {
      const link = menu.getByRole('link', { name: label, exact: true })
      await expect(link).toHaveAttribute('href', href)
      await expect(link).toHaveAttribute('title', label)
      const svg = link.locator(`svg[data-menu-icon="${iconName}"]`)
      await expect(svg).toBeVisible()
      await expect(svg).toHaveAttribute('aria-hidden', 'true')
      await expect(svg).toHaveAttribute('viewBox', '0 0 16 16')
      const box = await svg.boundingBox()
      expect(box?.width).toBe(16)
      expect(box?.height).toBe(16)
      const row = await link.boundingBox()
      expect(row?.height).toBe(40)
      expect(box!.x - row!.x).toBeCloseTo(20)
      expect(box!.y - row!.y).toBeCloseTo(12)
      // Different destinations must not regress to the same glyph (notably the two settings).
      signatures.push(await svg.locator('g').innerHTML())
      await expect(link.locator('.label')).toHaveCount(scenario.collapsed ? 0 : 1)
    }
    expect(new Set(signatures).size).toBe(entries.length)
    const notification = menu.getByRole('link', {
      name: text.notificationsUnread.replace('{count}', '12'), exact: true,
    })
    await expect(notification.locator('svg')).toBeVisible()
    await expect(notification.locator('.unread-count')).toHaveText('12')
    const badge = (await notification.locator('.unread-count').boundingBox())!
    const notificationRow = (await notification.boundingBox())!
    expect(badge.x).toBeGreaterThanOrEqual(notificationRow.x)
    expect(badge.x + badge.width).toBeLessThanOrEqual(notificationRow.x + notificationRow.width)
    expect(badge.y + badge.height).toBeLessThanOrEqual(notificationRow.y + notificationRow.height)
    const gear = menu.locator('[data-menu-icon="app-settings"]')
    const geometry = await gear.evaluate((svg) => {
      const outline = svg.querySelector('path')!.getBBox()
      const circle = svg.querySelector('circle')!
      return { x: outline.x, y: outline.y, width: outline.width, height: outline.height,
        cx: Number(circle.getAttribute('cx')), cy: Number(circle.getAttribute('cy')) }
    })
    expect(geometry.width).toBeCloseTo(geometry.height, 4)
    expect(geometry.x + geometry.width / 2).toBeCloseTo(geometry.cx, 4)
    expect(geometry.y + geometry.height / 2).toBeCloseTo(geometry.cy, 4)
    await expect(menu.locator('[data-menu-icon="clock"] circle')).toHaveAttribute('r', '6')
    await expect.poll(async () => (await menu.boundingBox())?.width).toBe(scenario.collapsed ? 56 : 240)
    const navBox = (await menu.boundingBox())!
    expect(navBox.x).toBeGreaterThanOrEqual(0)
    expect(navBox.x + navBox.width).toBeLessThanOrEqual(scenario.width)
    await page.screenshot({ path: testInfo.outputPath('menu.png'), fullPage: true })
    expect(errors).toEqual([])
  })
}

test('タイムライン非表示設定では時計も表示しない', async ({ page }) => {
  await setup(page, 'ja', 'light', false)
  await expect(page.getByRole('navigation', { name: ja.main.label })).toBeVisible()
  await expect(page.locator('[data-menu-icon="clock"]')).toHaveCount(0)
  await expect(page.locator('[data-menu-icon="gantt"]')).toBeVisible()
})
