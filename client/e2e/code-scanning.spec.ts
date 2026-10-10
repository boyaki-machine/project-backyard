import { expect, test } from '@playwright/test'
import { parse } from '@vue/compiler-sfc'
import { readFileSync } from 'node:fs'
import { templateWithoutComments } from '../scripts/vue-source.mjs'

test('Vue source inspection preserves text and locations around nested comments', () => {
  const source = '<template>\n<p>visible<!-- コメント\n<div>設計文書</div> -->text</p>\n<p>日本語</p>\n</template>'
  const block = parse(source).descriptor.template!
  const masked = templateWithoutComments(block)
  expect(masked.length).toBe(block.content.length)
  expect(masked.split('\n').length).toBe(block.content.split('\n').length)
  expect(masked).not.toContain('設計文書')
  expect(masked).toContain('<p>日本語</p>')
  expect(masked.indexOf('日本語')).toBe(block.content.indexOf('日本語'))
  expect(masked).toContain('visible')
  expect(masked).toContain('text</p>')
})

const mockSource = readFileSync('../docs/design/mock/gantt.html', 'utf8')
// Test hooks expose only this local design mock's closed-over data and renderers.
const instrumentedMock = mockSource.replace('\n})();\n</script>', `
window.__mock = { S, BY, openDetail, renderTreeHeader,
  resetHeader() { thdrKey = ''; },
  setZoneLabel(fn) { tzShort = fn; }
};
})();
</script>`)

// Below its 1100px minimum layout width, the design mock scrolls horizontally.
for (const width of [1366, 768]) {
  test(`gantt design mock renders dynamic text safely at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('http://pb.test/gantt-code-scanning.html', route =>
      route.fulfill({ contentType: 'text/html', body: instrumentedMock }))
    await page.goto('http://pb.test/gantt-code-scanning.html')
    await expect(page.locator('#trows .tr').first()).toBeVisible()
    // Use the normal UI path before testing hostile values in the same renderer.
    await page.locator('#trows .tr[data-seq="88"]').dblclick()
    await expect(page.locator('#detail')).toBeVisible()
    await page.locator('.mock-scroll').evaluate(element => { element.scrollLeft = element.scrollWidth })
    const bounds = await page.locator('#detail').boundingBox()
    const parentBounds = await page.locator('.mock-scroll').boundingBox()
    expect(bounds!.width).toBeGreaterThanOrEqual(320)
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width + 1)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(parentBounds!.x + parentBounds!.width + 1)
    expect(await page.locator('#dinner').textContent()).not.toContain('<span')
    expect(await page.locator('#dinner dd .muted').count()).toBeGreaterThan(0)
    await page.screenshot({ path: info.outputPath(`gantt-mock-${width}.png`) })

    const payload = '<img src="x" onerror="window.mockXSS=1">'
    await page.evaluate(value => {
      const mock = (window as any).__mock
      mock.BY.get(88).pr = value
      mock.openDetail(88)
      const originalZone = mock.S.vtz
      mock.S.vtz = value
      mock.setZoneLabel(() => value)
      mock.resetHeader()
      mock.renderTreeHeader(true, 50, 16)
      mock.S.vtz = originalZone
    }, payload)
    expect(await page.locator('#thdr').textContent()).toContain(payload)
    expect(await page.locator('#dinner').textContent()).toContain(payload)
    expect(await page.locator('#thdr img, #dinner img').count()).toBe(0)
    expect(await page.evaluate(() => (window as any).mockXSS)).toBeUndefined()
    expect(errors).toEqual([])
  })
}
