import { expect, test } from '@playwright/test'
import { editingTicket, mockEditingTicket } from './fixtures/ticket-editing'

for (const width of [1366, 1024, 768, 390]) {
  test(`title minimum follows short text and preserves tags and controls at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const state = await mockEditingTicket(page, 'ja')
    const tags = [{ id: 'a', name: '改善' }, { id: 'b', name: '長いタグ名'.repeat(8) }, { id: 'c', name: 'GUI' }]
    state.items.splice(0, state.items.length, ...[
      { seq: 1, title: '画面表示の不具合を修正する長いタイトル', tags },
      { seq: 2, title: '修正', tags },
      { seq: 3, title: '子チケットの長いタイトルも読みやすくする', tags, parent_seq: 1 },
      { seq: 4, title: 'オンステージの長いタイトルとタグを確認する', tags, staged_at: Date.now() },
      { seq: 5, title: 'タグなし', tags: [] },
      { seq: 6, title: '通常のタイトルと短いタグ', tags: [tags[0]] },
    ].map(item => ({ ...editingTicket, ...item })))
    await page.goto('/p/demo/backlog')
    await expect(page.locator('tr.row')).toHaveCount(state.items.length)
    const row = (seq: number) => page.locator('tr.row').filter({ has: page.locator('code.seq', { hasText: new RegExp(`^demo-${seq}$`) }) })
    for (const seq of [1, 2, 3, 4]) {
      // The accessible name remains the title, with no duplicate sizing text.
      await expect(row(seq).getByRole('link', { name: state.items.find(t => t.seq === seq)!.title, exact: true })).toBeVisible()
      await expect(row(seq).locator('.tag-more')).toHaveText('+1')
      const bounds = await row(seq).locator('.title-line').evaluate(el => {
        const title = el.querySelector<HTMLElement>('.title')!
        const range = document.createRange()
        range.selectNodeContents(title.querySelector('.title-text')!)
        const natural = range.getBoundingClientRect().width
        const t = title.getBoundingClientRect(), line = el.getBoundingClientRect()
        const six = parseFloat(getComputedStyle(title).fontSize) * 6
        const controls = [...el.querySelectorAll('.tag-trail, .tag-more, .stage-controls')].map(node => node.getBoundingClientRect())
        return { width: t.width, natural, six, inside: controls.every(b => b.left >= t.right - 1 && b.right <= line.right + 1), rowHeight: el.closest('tr')!.getBoundingClientRect().height }
      })
      expect(bounds.width).toBeGreaterThanOrEqual(Math.min(bounds.natural, bounds.six) - 1)
      expect(bounds.inside).toBe(true)
      expect(bounds.rowHeight).toBeCloseTo(40, 0)
      if (seq === 2 && width <= 1024) expect(bounds.width).toBeLessThan(bounds.six)
    }
    await expect(row(4).locator('.stage-controls')).toHaveCount(0)
    await expect(row(1).getByRole('button', { name: 'demo-1の移動操作', exact: true })).toBeVisible()
    await expect(row(5).locator('.tag-trail')).toHaveCount(0)
    await page.screenshot({ path: info.outputPath(`title-width-${width}.png`), fullPage: true })
    // Pan only for the screenshot when the existing 900px table exceeds the viewport.
    await row(1).locator('.title-line').scrollIntoViewIfNeeded()
    await page.screenshot({ path: info.outputPath(`title-rows-${width}.png`), fullPage: true })
    // A changed title must immediately acquire its new natural minimum, including Latin text.
    state.items.find(t => t.seq === 2)!.title = 'Fix'
    await page.reload()
    await expect(row(2).getByRole('link', { name: 'Fix', exact: true })).toBeVisible()
    if (width <= 1024) {
      const title = await row(2).locator('.title').boundingBox()
      expect(title!.width).toBeLessThan(40)
    }
  })
}
