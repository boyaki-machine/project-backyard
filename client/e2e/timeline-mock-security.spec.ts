import { expect, test } from '@playwright/test'
import { readFileSync } from 'node:fs'

const html = readFileSync(new URL('../../docs/design/mock/timeline-final.html', import.meta.url), 'utf8')

// This desktop mock explicitly sets .page { min-width: 1180px }.
for (const width of [1200, 1440]) {
  test(`timeline failure reasons remain text in body and tooltip at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.route('http://timeline.test/**', route => route.fulfill({ contentType: 'text/html', body: html }))
    await page.goto('http://timeline.test/?play=0&poll=0')
    await expect(page.locator('#rows .bar').first()).toBeVisible()

    for (const reason of ['prev', 'anchor', 'endpin']) {
      for (const title of ['<b>&"</b>', '<s<scr>ipt>', '<svg/onload=1>']) {
        const result = await page.evaluate(({ reason, title }) => {
          const tl = (window as any).__tl
          const endBlocker = tl.byId('a4')
          const originalAnchor = endBlocker.ps
          if (reason === 'prev') {
            const t = tl.byId('a3')
            tl.setTrial({ op: 'start', id: t.id, ns: tl.byId('a2').s })
          } else {
            endBlocker.ps = reason === 'anchor'
            tl.setTrial({ op: 'end', id: 'a3', ne: endBlocker.e + 600 })
          }
          const trial = tl.S.trial
          const blocker = tl.byId(trial.R.blocker)
          const originalTitle = blocker.title
          blocker.title = title
          tl.renderAll()
          blocker.title = originalTitle
          endBlocker.ps = originalAnchor
          return { ok: trial.R.ok, reason: trial.R.reason }
        }, { reason, title })
        expect(result).toEqual({ ok: false, reason })
        const message = page.locator('#notice.fail .msg')
        await expect(message).toBeVisible()
        await expect(message).toContainText(title)
        const tooltip = await message.getAttribute('title')
        expect(tooltip).toContain(title)
        // Both destinations must show the same literal reason, without double escaping.
        expect(await message.textContent()).toContain(tooltip)
        // The trial's heading contains one intentional <b>; the reason adds no elements.
        await expect(message.locator('b')).toHaveCount(1)
        await expect(message.locator('*:not(b)')).toHaveCount(0)
      }
    }

    // Capture the ordinary failed-trial layout too, with the original demo titles.
    await page.reload()
    await page.evaluate(() => {
      const tl = (window as any).__tl
      tl.setTrial({ op: 'end', id: 'a3', ne: tl.byId('a4').e + 600 })
    })
    const notice = page.locator('#notice.fail')
    await expect(notice).toBeVisible()
    const bounds = await notice.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    await page.screenshot({ path: info.outputPath(`timeline-failure-${width}.png`), fullPage: true })
  })
}
