import { expect, test } from '@playwright/test'

test('crop, register, display and remove a user icon', async ({ page }, testInfo) => {
  test.skip(!process.env.PB_E2E_EMAIL || !process.env.PB_E2E_PASSWORD, 'Development account is required')
  await page.goto('/login')
  await page.locator('input[type=email]').fill(process.env.PB_E2E_EMAIL!)
  await page.locator('input[type=password]').fill(process.env.PB_E2E_PASSWORD!)
  await page.getByRole('button', { name: 'ログイン', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login$/)
  await page.goto('/me')
  const icon = page.locator('.avatar-field')
  await expect(icon).toBeVisible()

  const base64 = await page.evaluate(() => {
    const canvas = document.createElement('canvas')
    canvas.width = 640
    canvas.height = 480
    const ctx = canvas.getContext('2d')!
    ctx.fillStyle = '#dc4435'
    ctx.fillRect(0, 0, 320, 480)
    ctx.fillStyle = '#2967be'
    ctx.fillRect(320, 0, 320, 480)
    return canvas.toDataURL('image/png').split(',')[1]!
  })
  const firstChooser = page.waitForEvent('filechooser')
  await icon.getByRole('button', { name: /登録|変更/ }).click()
  await (await firstChooser).setFiles({
    name: 'icon.png',
    mimeType: 'image/png',
    buffer: Buffer.from(base64, 'base64'),
  })
  const modal = page.getByRole('dialog', { name: 'アイコンを切り抜く' })
  await expect(modal).toBeVisible()
  const preview = modal.locator('canvas')
  const before = await preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())
  for (const [index, value] of [[2, '2'], [0, '0']] as const) {
    await modal.locator('input[type=range]').nth(index).evaluate((input: HTMLInputElement, next) => {
      input.value = next
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }, value)
  }
  await expect.poll(() => preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())).not.toBe(before)
  await page.screenshot({ path: testInfo.outputPath('avatar-crop.png') })
  await page.setViewportSize({ width: 390, height: 900 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(391)
  await page.screenshot({ path: testInfo.outputPath('avatar-crop-390.png') })
  await modal.getByRole('button', { name: '登録' }).click()
  await expect(icon.getByRole('status')).toHaveText('アイコンを登録しました')
  await expect(icon.locator('img')).toBeVisible()

  for (const width of [390, 900, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    const bounds = await icon.locator('.pb-avatar').boundingBox()
    expect(bounds?.width).toBe(48)
    expect(bounds?.height).toBe(48)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width + 1)
    await page.screenshot({ path: testInfo.outputPath(`avatar-${width}.png`), fullPage: true })
    if (width === 900) await expect(page.locator('.user .row img')).toBeVisible()
  }

  const oldURL = await icon.locator('img').getAttribute('src')
  const replacement = await page.evaluate(() => {
    const canvas = document.createElement('canvas')
    canvas.width = 600
    canvas.height = 400
    const ctx = canvas.getContext('2d')!
    ctx.fillStyle = '#23a744'
    ctx.fillRect(0, 0, 600, 400)
    return canvas.toDataURL('image/png').split(',')[1]!
  })
  const secondChooser = page.waitForEvent('filechooser')
  await icon.getByRole('button', { name: '変更' }).click()
  await (await secondChooser).setFiles({ name: 'replacement.png', mimeType: 'image/png', buffer: Buffer.from(replacement, 'base64') })
  await page.getByRole('dialog', { name: 'アイコンを切り抜く' }).getByRole('button', { name: '登録' }).click()
  await expect(icon.locator('img')).not.toHaveAttribute('src', oldURL!)
  const pixel = await icon.locator('img').evaluate(async (img: HTMLImageElement) => {
    if (!img.complete) await new Promise<void>((resolve) => img.addEventListener('load', () => resolve(), { once: true }))
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 1
    const ctx = canvas.getContext('2d')!
    ctx.drawImage(img, 0, 0, 1, 1)
    return [...ctx.getImageData(0, 0, 1, 1).data]
  })
  expect(pixel[1]).toBeGreaterThan(pixel[0]!)

  await page.goto('/admin/users')
  const ownRow = page.getByRole('row').filter({ hasText: process.env.PB_E2E_EMAIL! })
  await expect(ownRow.locator('img')).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('avatar-users.png') })
  await page.goto('/me')

  await icon.getByRole('button', { name: '削除' }).click()
  await expect(icon.getByRole('status')).toHaveText('アイコンを削除しました')
  await expect(icon.locator('img')).toHaveCount(0)
})
