import { expect, test } from '@playwright/test'
import { mockEditingTicket } from './fixtures/ticket-editing'

for (const width of [600, 1440]) {
  test(`untrusted Markdown stays inert and ordinary links still work at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.addInitScript(() => { (window as unknown as { executed: boolean }).executed = false })
    const state = await mockEditingTicket(page, 'ja')
    const body = '# 概要\n安全な本文\n\n[参考](https://example.com/docs)\n\n'
      + '[危険](javascript:window.executed=true)\n\n'
      + '<img src=x onerror="window.executed=true">\n'
      + '<svg onload="window.executed=true"><script>window.executed=true</script></svg>'
    state.items[0]!.body_md = body
    await page.goto('/p/demo/tickets/1')
    const rendered = page.getByTitle('クリックして説明を編集').locator('.markdown-body')
    await expect(rendered.getByRole('heading', { name: '概要', level: 4 })).toBeVisible()
    await expect(rendered.locator('img, svg, script, [onerror], [onload], a[href^="javascript:"]')).toHaveCount(0)
    await expect(rendered).toContainText('<img src=x onerror=')
    const link = rendered.getByRole('link', { name: '参考', exact: true })
    await expect(link).toHaveAttribute('href', 'https://example.com/docs')
    await expect(link).toHaveAttribute('target', '_blank')
    await expect(link).toHaveAttribute('rel', /noopener/)
    expect(await page.evaluate(() => (window as unknown as { executed: boolean }).executed)).toBe(false)
    await page.getByTitle('クリックして説明を編集').click()
    const preview = page.locator('.preview')
    await expect(preview.getByRole('heading', { name: '概要', level: 4 })).toBeVisible()
    await expect(preview.locator('img, svg, script, [onerror], [onload], a[href^="javascript:"]')).toHaveCount(0)
    await preview.scrollIntoViewIfNeeded()
    const bounds = await preview.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    await page.screenshot({ path: info.outputPath(`markdown-security-${width}.png`), fullPage: true })
  })
}
