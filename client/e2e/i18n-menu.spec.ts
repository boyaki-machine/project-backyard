import { expect, test, type Page, type Route } from '@playwright/test'

const session = {
  actor: {
    id: '01K00000000000000000000000',
    kind: 'user',
    display_name: 'Test User',
    email: 'test@example.com',
    system_role: 'administrator',
    locale: 'ja',
    timezone: 'Asia/Tokyo',
    theme: 'light',
    hue: 'blue',
    must_change_password: false,
  },
  permissions: [
    'project.view',
    'project.create',
    'user.manage',
    'auditlog.view',
    'system.settings',
  ],
  projects: [
    {
      id: '01K00000000000000000000001',
      key: 'demo',
      name: 'Demo project',
      role: 'project_admin',
      permissions: ['project.view', 'project.edit', 'ticket.view', 'doc.view'],
    },
  ],
  expires_at: 4070908800000,
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname

    if (path === '/api/v1/me' && request.method() === 'PATCH') {
      const update = request.postDataJSON() as { locale?: 'ja' | 'en' }
      if (update.locale) session.actor.locale = update.locale
      await json(route, session)
      return
    }
    if (path === '/api/v1/me') {
      await json(route, session)
      return
    }
    if (path === '/api/v1/me/mfa') {
      await json(route, { totp: [], recovery_codes: null })
      return
    }
    if (path === '/api/v1/me/passkeys') {
      await json(route, { items: [] })
      return
    }
    if (path === '/api/v1/admin/settings/pending') {
      await json(route, { pending_confirmation: null })
      return
    }
    await route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })
}

test('言語設定を英語で保存すると各メニューが即時に切り替わり、狭い幅でも収まる', async ({
  page,
}, testInfo) => {
  session.actor.locale = 'ja'
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockApi(page)
  await page.goto('/me')

  await expect(page.getByRole('navigation', { name: 'メインメニュー' })).toBeVisible()
  await expect(page.locator('select[name="locale"]')).toHaveAccessibleName('言語(Language)')
  const localeField = page.locator('select[name="locale"]')
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 })
    const box = await localeField.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(width)
    await page.screenshot({ path: testInfo.outputPath(`me-ja-${width}.png`), fullPage: true })
  }
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.locator('select[name="locale"]').selectOption('en')
  await page.locator('form').first().getByRole('button', { name: '保存' }).click()

  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await expect(page.getByRole('heading', { level: 1, name: 'My settings' })).toBeVisible()
  expect(await page.locator('body').innerText()).not.toMatch(/[ぁ-んァ-ヶ一-龠]/u)
  const mainMenu = page.getByRole('navigation', { name: 'Main menu' })
  await expect(mainMenu.getByText('Projects', { exact: true })).toBeVisible()
  await expect(mainMenu.getByText('Application settings', { exact: true })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Settings sections' })).toContainText(
    'Access tokens',
  )

  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await expect(page.getByRole('navigation', { name: 'Main menu' })).toBeVisible()

  await page.goto('/p/demo')
  const projectMenu = page.getByRole('navigation', { name: 'Main menu' })
  for (const label of ['Dashboard', 'Backlog', 'Ticket search', 'Docs', 'Project settings']) {
    await expect(projectMenu.getByText(label, { exact: true })).toBeVisible()
  }
  await page.goto('/me')

  await page.getByRole('button', { name: 'Test User menu' }).click()
  await expect(page.getByRole('link', { name: 'My settings' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible()

  const desktopScreenshot = testInfo.outputPath('i18n-menu-en-1280.png')
  await page.screenshot({ path: desktopScreenshot, fullPage: true })
  await testInfo.attach('i18n-menu-en-1280', {
    path: desktopScreenshot,
    contentType: 'image/png',
  })

  await page.getByRole('button', { name: 'Test User menu' }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  const mobileToggle = page.getByRole('button', { name: 'Toggle main menu' })
  await mobileToggle.click()
  await expect(mobileToggle).toHaveAttribute('aria-expanded', 'true')
  await expect(mainMenu).toBeVisible()
  await expect(mainMenu.getByText('Projects', { exact: true })).toBeVisible()
  const menuBox = await mainMenu.boundingBox()
  expect(menuBox).not.toBeNull()
  expect(menuBox!.x).toBeGreaterThanOrEqual(0)
  expect(menuBox!.x + menuBox!.width).toBeLessThanOrEqual(390)

  const mobileScreenshot = testInfo.outputPath('i18n-menu-en-390.png')
  await page.screenshot({ path: mobileScreenshot, fullPage: true })
  await testInfo.attach('i18n-menu-en-390', {
    path: mobileScreenshot,
    contentType: 'image/png',
  })
})
