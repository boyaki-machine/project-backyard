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
    ctx.fillStyle = '#23a744'
    ctx.fillRect(0, 240, 320, 240)
    ctx.fillStyle = '#d9b23b'
    ctx.fillRect(320, 240, 320, 240)
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
  await expect(modal.getByRole('button', { name: '登録' })).toBeEnabled()
  const pixelAt = () => preview.evaluate((canvas: HTMLCanvasElement) =>
    [...canvas.getContext('2d')!.getImageData(100, 64, 1, 1).data])
  const before = await pixelAt()
  expect(before[0]).toBeGreaterThan(before[2]!)
  const bounds = (await preview.boundingBox())!
  await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)
  await page.mouse.down()
  await page.mouse.move(bounds.x + bounds.width / 2 - 100, bounds.y + bounds.height / 2, { steps: 6 })
  await page.mouse.up()
  await expect.poll(async () => {
    const pixel = await pixelAt()
    return pixel[2]! > pixel[0]!
  }).toBe(true)
  const afterDrag = await preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())
  await preview.press('ArrowRight')
  await expect.poll(() => preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())).not.toBe(afterDrag)
  const afterArrow = await preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())
  await modal.locator('input[type=range]').evaluate((input: HTMLInputElement) => {
    input.value = '2'
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await expect.poll(() => preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())).not.toBe(afterArrow)
  const afterZoom = await preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())
  await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)
  await page.mouse.down()
  await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2 - 80, { steps: 6 })
  await page.mouse.up()
  await expect.poll(() => preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())).not.toBe(afterZoom)
  const afterMouse = await preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 })
  const touchX = bounds.x + bounds.width / 2
  const touchY = bounds.y + bounds.height / 2
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: touchX, y: touchY }] })
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: touchX, y: touchY + 80 }] })
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await expect.poll(() => preview.evaluate((canvas: HTMLCanvasElement) => canvas.toDataURL())).not.toBe(afterMouse)
  await page.screenshot({ path: testInfo.outputPath('avatar-crop.png') })
  await page.setViewportSize({ width: 390, height: 900 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(391)
  await page.screenshot({ path: testInfo.outputPath('avatar-crop-390.png') })
  const croppedPixel = await preview.evaluate((canvas: HTMLCanvasElement) =>
    [...canvas.getContext('2d')!.getImageData(128, 128, 1, 1).data])
  await modal.getByRole('button', { name: '登録' }).click()
  await expect(icon.getByRole('status')).toHaveText('アイコンを登録しました')
  await expect(icon.locator('img')).toBeVisible()
  const savedPixel = await icon.locator('img').evaluate(async (img: HTMLImageElement) => {
    if (!img.complete) await new Promise<void>((resolve) => img.addEventListener('load', () => resolve(), { once: true }))
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 256
    const ctx = canvas.getContext('2d')!
    ctx.drawImage(img, 0, 0, 256, 256)
    return [...ctx.getImageData(128, 128, 1, 1).data]
  })
  expect(savedPixel).toEqual(croppedPixel)

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
  await expect(ownRow.locator('td.kind img')).toBeVisible()
  await expect(ownRow.locator('td.name-col .pb-avatar')).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath('avatar-users.png') })
  await ownRow.locator('td.name-col a').click()
  await expect(page.locator('.page-header .header-avatar img')).toBeVisible()
  await expect(page.locator('.page-header h1')).not.toContainText('👤')
  await page.screenshot({ path: testInfo.outputPath('avatar-user-detail.png') })
  await page.goto('/me')

  await icon.getByRole('button', { name: '削除' }).click()
  await expect(icon.getByRole('status')).toHaveText('アイコンを削除しました')
  await expect(icon.locator('img')).toHaveCount(0)
})

test('registered member icon appears in icon columns and actor choices', async ({ page }, testInfo) => {
  const email = process.env.PB_E2E_MEMBER_EMAIL
  test.skip(!email || !process.env.PB_E2E_PASSWORD, 'Development member account is required')
  await page.goto('/login')
  await page.locator('input[type=email]').fill(email!)
  await page.locator('input[type=password]').fill(process.env.PB_E2E_PASSWORD!)
  await page.getByRole('button', { name: 'ログイン', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login$/)

  const png = await page.evaluate(() => {
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 256
    const ctx = canvas.getContext('2d')!
    ctx.fillStyle = '#3176c4'
    ctx.fillRect(0, 0, 256, 256)
    return canvas.toDataURL('image/png').split(',')[1]!
  })
  await page.goto('/me')
  await page.locator('.avatar-file').setInputFiles({ name: 'member.png', mimeType: 'image/png', buffer: Buffer.from(png, 'base64') })
  await page.getByRole('dialog', { name: 'アイコンを切り抜く' }).getByRole('button', { name: '登録' }).click()
  await expect(page.locator('.avatar-field img')).toBeVisible()

  try {
    await page.goto('/p/demo/settings')
    await page.getByRole('tab', { name: 'メンバー' }).click()
    const memberRow = page.getByRole('row').filter({ hasText: email! })
    await expect(memberRow.locator('td.icon-col img')).toBeVisible()
    await expect(memberRow.locator('td').nth(1).locator('.pb-avatar')).toHaveCount(0)
    await page.screenshot({ path: testInfo.outputPath('avatar-project-members.png') })

    await page.goto('/p/demo/backlog')
    const filter = page.locator('.backlog-filter-assignee .actor-select-trigger')
    await filter.click()
    const choice = page.locator('.actor-select-panel [role=option]').filter({ hasText: '開発PM' })
    await expect(choice.locator('img')).toBeVisible()
    await expect(choice.locator('.pb-avatar > span:not(.sr-only)')).toHaveCount(0)
    await choice.click()
    await expect(filter.locator('img')).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath('avatar-backlog-filter.png') })

    await page.getByRole('button', { name: '+ 新規チケット' }).click()
    const newTicket = page.getByRole('dialog', { name: '新規チケット' })
    await newTicket.locator('.actor-select-trigger').click()
    const newTicketChoice = page.locator('.actor-select-panel [role=option]').filter({ hasText: '開発PM' })
    await expect(newTicketChoice.locator('img')).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath('avatar-new-ticket.png') })
    await newTicketChoice.click()
    await expect(newTicket.locator('.actor-select-trigger img')).toBeVisible()
    await newTicket.locator('.actor-select-trigger').click()
    await page.keyboard.press('Escape')
    await expect(newTicket).toBeVisible()
    await newTicket.getByRole('button', { name: 'キャンセル' }).click()

    const tickets = await page.request.get('/api/v1/projects/demo/tickets')
    expect(tickets.ok()).toBe(true)
    const ticketItems = (await tickets.json()).items as { seq: number }[]
    expect(ticketItems.length).toBeGreaterThan(0)
    await page.goto(`/p/demo/tickets/${ticketItems[0]!.seq}`)
    const detailAssignee = page.locator('.meta-item').filter({ has: page.locator('dt', { hasText: '担当' }) }).first()
    await detailAssignee.locator('.actor-select-trigger').click()
    await expect(page.locator('.actor-select-panel [role=option]').filter({ hasText: '開発PM' }).locator('img')).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath('avatar-ticket-assignee.png') })

    await page.goto('/p/demo/search')
    await page.locator('.search-filter').filter({ hasText: '担当' }).locator('.multi-select-trigger').click()
    await expect(page.locator('.multi-select-choice').filter({ hasText: '開発PM' }).locator('img')).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath('avatar-search-filter.png') })

    for (const width of [390, 900, 1440]) {
      await page.setViewportSize({ width, height: 900 })
      await page.goto('/p/demo/backlog')
      await page.locator('.backlog-filter-assignee .actor-select-trigger').click()
      await expect(page.locator('.actor-select-panel [role=option]').filter({ hasText: '開発PM' }).locator('img')).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width + 1)
      await page.screenshot({ path: testInfo.outputPath(`avatar-choice-${width}.png`) })
    }
  } finally {
    await page.request.delete('/api/v1/me/avatar')
  }
})
