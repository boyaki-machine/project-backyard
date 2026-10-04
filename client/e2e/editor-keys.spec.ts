import { expect, test } from '@playwright/test'
import { mockEditingTicket } from './fixtures/ticket-editing'

for (const width of [600, 900, 1440]) {
  test(`Home/End and selection use logical lines at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    await mockEditingTicket(page, 'ja')
    await page.goto('/p/demo/tickets/1')
    await page.getByTitle('クリックして説明を編集').click()
    const editor = page.locator('.cm-content')
    const longLine = '    ' + '長い説明の折り返しを確認する文章'.repeat(60)
    await editor.fill('先頭行\n' + longLine + '\n末尾行')
    await editor.press('ControlOrMeta+Home')
    for (let i = 0; i < 84; i++) await page.keyboard.press('ArrowRight')
    await editor.press('Home')
    await editor.press('X')
    await expect(editor).toHaveText('先頭行X' + longLine + '末尾行')
    await editor.press('End')
    await editor.press('Y')
    await expect(editor).toHaveText('先頭行X' + longLine + 'Y末尾行')
    // Shift extends to the same logical endpoints, including indentation/wrapping.
    await editor.press('Shift+Home')
    await expect.poll(() => page.evaluate(() => window.getSelection()?.toString())).toBe('X' + longLine + 'Y')
    await editor.press('Home')
    await editor.press('Shift+End')
    await expect.poll(() => page.evaluate(() => window.getSelection()?.toString())).toBe('X' + longLine + 'Y')
    await editor.press('ControlOrMeta+End')
    await editor.press('Z')
    await expect(editor).toHaveText('先頭行X' + longLine + 'Y末尾行Z')
    // Page navigation and arrows continue to edit; editor focus must stay inside.
    await editor.press('PageUp')
    await expect(editor).toBeFocused()
    await editor.press('PageDown')
    await expect(editor).toBeFocused()
    const bounds = await editor.evaluate((el) => ({ width: el.clientWidth, scroll: el.scrollWidth }))
    expect(bounds.scroll).toBeLessThanOrEqual(bounds.width + 1)
    await page.screenshot({ path: testInfo.outputPath(`editor-keys-${width}.png`), fullPage: true })
  })
}
