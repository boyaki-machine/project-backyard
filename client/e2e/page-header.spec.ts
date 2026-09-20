import { expect, test, type Page } from '@playwright/test'

const MOBILE_WIDTH = 390
const DESKTOP_BREAKPOINT = 768

async function login(page: Page) {
  const email = process.env.PB_E2E_EMAIL
  const password = process.env.PB_E2E_PASSWORD
  if (!email || !password) {
    throw new Error('PB_E2E_EMAIL と PB_E2E_PASSWORD を設定してください')
  }

  await page.goto('/login')
  await page.locator('input[name="email"]').fill(email)
  await page.locator('input[name="password"]').fill(password)
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/projects$/)
}

test('ページ見出しは狭い幅で浮遊メニューボタンと重ならず、768pxでは既存位置を保つ', async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: MOBILE_WIDTH, height: 844 })
  await login(page)
  await page.goto('/admin/settings')

  const header = page.locator('.page-header')
  const title = page.getByRole('heading', { level: 1, name: 'アプリケーション設定' })
  const floatingToggle = page.locator('.floating .toggle')

  await expect(title).toBeVisible()
  await expect(floatingToggle).toBeVisible()

  const mobileTitleBox = await title.boundingBox()
  const mobileToggleBox = await floatingToggle.boundingBox()
  expect(mobileTitleBox).not.toBeNull()
  expect(mobileToggleBox).not.toBeNull()
  expect(mobileTitleBox!.x).toBeGreaterThanOrEqual(
    mobileToggleBox!.x + mobileToggleBox!.width + 23,
  )
  await expect(header).toHaveCSS('padding-left', '80px')

  const mobileScreenshot = testInfo.outputPath('app-settings-390.png')
  await page.screenshot({ path: mobileScreenshot, fullPage: true })
  await testInfo.attach('app-settings-390', { path: mobileScreenshot, contentType: 'image/png' })

  await page.setViewportSize({ width: DESKTOP_BREAKPOINT, height: 900 })
  await expect(floatingToggle).toHaveCount(0)

  const desktopHeaderBox = await header.boundingBox()
  const desktopTitleBox = await title.boundingBox()
  expect(desktopHeaderBox).not.toBeNull()
  expect(desktopTitleBox).not.toBeNull()
  expect(desktopTitleBox!.x - desktopHeaderBox!.x).toBeCloseTo(24, 0)
  await expect(header).toHaveCSS('padding-left', '24px')

  const desktopScreenshot = testInfo.outputPath('app-settings-768.png')
  await page.screenshot({ path: desktopScreenshot, fullPage: true })
  await testInfo.attach('app-settings-768', { path: desktopScreenshot, contentType: 'image/png' })
})
