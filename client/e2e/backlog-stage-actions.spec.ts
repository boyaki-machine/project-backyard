import { expect, test, type Page } from '@playwright/test'
import { mockEditingTicket, editingTicket } from './fixtures/ticket-editing'

function rows(page: Page, seq: number) {
  return page.locator('.row').filter({ has: page.locator('code.seq').filter({ hasText: new RegExp(`^demo-${seq}$`) }) })
}

async function select(page: Page, seq: number) {
  await rows(page, seq).first().locator('.title').click({ modifiers: ['Control'] })
}

async function setup(page: Page, options: { failSeq?: number; readOnly?: boolean; prepare?: (items: typeof editingTicket[]) => void } = {}) {
  const state = await mockEditingTicket(page, 'ja')
  // Parent, ordinary descendant, and a parent hidden by a filter.
  state.items.push({ ...editingTicket, seq: 3, title: '親と一緒の子', parent_seq: 1 })
  state.items.push({ ...editingTicket, seq: 4, title: '結果に親がいない子', parent_seq: 99 })
  state.items.push({ ...editingTicket, seq: 5, title: 'エピック直下', parent_seq: 9, sort_key: '0|5:' })
  options.prepare?.(state.items)
  const epic = { ...editingTicket, seq: 9, type: 'epic', title: 'テストエピック' }
  if (options.failSeq) state.items.push({ ...editingTicket, seq: 6, title: '失敗後は送らない', sort_key: '0|6:' })
  const moves: { seq: number; body: unknown }[] = []
  await page.route('**/api/v1/projects/demo/tickets*', async route => {
    const url = new URL(route.request().url())
    if (url.pathname !== '/api/v1/projects/demo/tickets') return route.fallback()
    const items = url.searchParams.get('type') === 'epic' ? [epic] : state.items
    await route.fulfill({ json: { items, total: items.length, page: 1, per_page: 200, total_pages: 1 } })
  })
  await page.route('**/api/v1/projects/demo/tickets/*/move', async route => {
    const seq = Number(new URL(route.request().url()).pathname.split('/').at(-2))
    const body = route.request().postDataJSON()
    moves.push({ seq, body })
    if (seq === options.failSeq) return route.fulfill({ status: 422, json: { error: { code: 'validation_failed', message: '移動に失敗しました', details: [] } } })
    const item = state.items.find(t => t.seq === seq)!
    item.staged_at = body.staged ? Date.now() : null
    item.version++
    item.sort_key = '0|z' + moves.length + ':'
    await route.fulfill({ json: { seq, staged_at: item.staged_at, version: item.version, sort_key: item.sort_key, rebalanced: moves.length === 1 } })
  })
  if (options.readOnly) await page.route('**/api/v1/me', async route => {
    await route.fulfill({ json: { actor: { ...editingTicket.reporter, locale: 'ja' }, permissions: [], projects: [{ key: 'demo', name: 'Demo', role: 'viewer', permissions: ['project.view', 'ticket.view'] }], expires_at: 4070908800000 } })
  })
  await page.goto('/p/demo/backlog')
  await expect(page.getByRole('link', { name: '配置確認', exact: true })).toBeVisible()
  return { state, moves }
}

for (const width of [600, 900, 1440]) {
  test(`single and multiple staging at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    const { state, moves } = await setup(page)
    await expect(page.getByRole('checkbox', { name: /demo-.*を選択/ })).toHaveCount(0)
    for (const seq of [3, 4]) {
      await select(page, seq)
      await expect(rows(page, seq)).not.toHaveClass(/stage-selected/)
    }
    await expect(page).toHaveURL(/backlog$/)
    await page.getByRole('button', { name: 'demo-1の移動操作', exact: true }).click()
    await expect(page.getByRole('menuitem', { name: 'オンステージへ', exact: true })).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath(`stage-menu-${width}.png`), fullPage: true })
    await page.getByRole('menuitem', { name: 'オンステージへ', exact: true }).click()
    await expect.poll(() => state.items[0].staged_at).not.toBeNull()
    await select(page, 1)
    await expect(rows(page, 1)).not.toHaveClass(/stage-selected/)
    // Both the parent and its child move; the child is never sent to move.
    const staged = page.locator('.group-section').filter({ has: page.getByText('オンステージ', { exact: true }) })
    await expect(staged.getByRole('link', { name: '親と一緒の子', exact: true })).toBeVisible()
    await select(page, 2)
    await select(page, 5)
    await expect(page).toHaveURL(/backlog$/)
    const bar = page.getByRole('group', { name: '選択したチケットの操作' })
    await expect(bar).toBeVisible()
    const bounds = await bar.boundingBox()
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    await page.screenshot({ path: testInfo.outputPath(`stage-selection-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: '選択したチケットをオンステージへ', exact: true }).click()
    await expect.poll(() => moves.length).toBe(3)
    await expect(bar).toHaveCount(0)
    expect(moves.map(m => m.seq)).toEqual([1, 2, 5])
    expect(moves.every(m => JSON.stringify(m.body) === JSON.stringify({ staged: true, position: 'last' }))).toBe(true)
  })
}

for (const source of ['toolbar', 'row menu']) {
  test(`partial failure from ${source} retains remaining selection and reports both counts`, async ({ page }) => {
    const { moves } = await setup(page, { failSeq: 5 })
    await select(page, 2)
    await select(page, 5)
    await select(page, 6)
    if (source === 'toolbar') {
      await page.getByRole('button', { name: '選択したチケットをオンステージへ', exact: true }).click()
    } else {
      await page.getByRole('button', { name: 'demo-2の移動操作', exact: true }).click()
      await page.getByRole('menuitem', { name: 'オンステージへ', exact: true }).click()
    }
    await expect(page.getByText(/1件をオンステージへ移動しました。残り2件は未処理です/)).toBeVisible()
    expect(moves.map(m => m.seq)).toEqual([2, 5])
    await expect(rows(page, 5)).toHaveClass(/stage-selected/)
    await expect(rows(page, 6)).toHaveClass(/stage-selected/)
    await expect(rows(page, 2)).not.toHaveClass(/stage-selected/)
  })
}

test('readonly users have no stage controls', async ({ page }) => {
  await setup(page, { readOnly: true })
  await expect(page.getByRole('checkbox', { name: /demo-.*を選択/ })).toHaveCount(0)
  await expect(page.getByRole('button', { name: /demo-.*の移動操作/ })).toHaveCount(0)
})

test('filter changes clear selections, and keyboard menu works with alternate sorting', async ({ page }) => {
  const { moves } = await setup(page)
  await select(page, 1)
  await page.getByRole('button', { name: /タイトル/ }).first().click()
  await expect(page.getByRole('group', { name: '選択したチケットの操作' })).toHaveCount(0)
  const menu = page.getByRole('button', { name: 'demo-1の移動操作', exact: true })
  await menu.focus()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('menuitem', { name: 'オンステージへ', exact: true })).toBeFocused()
  await page.keyboard.press('Enter')
  await expect.poll(() => moves.length).toBe(1)
})


test('tag groups share selection by ticket ID and send only one move', async ({ page }) => {
  const { state, moves } = await setup(page)
  state.items[0].tags = [{ id: 'tag-a', name: 'タグA' }, { id: 'tag-b', name: 'タグB' }]
  await page.goto('/p/demo/backlog?group=tag')
  const duplicates = rows(page, 1)
  await expect(duplicates).toHaveCount(2)
  await select(page, 1)
  await expect(duplicates.nth(1)).toHaveClass(/stage-selected/)
  await page.getByRole('button', { name: '選択したチケットをオンステージへ', exact: true }).click()
  await expect.poll(() => moves.length).toBe(1)
  expect(moves[0].seq).toBe(1)
})


test('modifier selection toggles without navigation; ordinary clicks open detail', async ({ page, context }) => {
  await setup(page)
  const count = context.pages().length
  // Ctrl uses contextmenu on macOS; click with ctrlKey covers the other event path.
  await rows(page, 1).locator('.title').dispatchEvent('click', { ctrlKey: true, button: 0 })
  await expect(rows(page, 1)).toHaveClass(/stage-selected/)
  await expect(rows(page, 1)).toHaveAttribute('aria-selected', 'true')
  await select(page, 1)
  await expect(rows(page, 1)).not.toHaveClass(/stage-selected/)
  await rows(page, 2).locator('code.seq').click({ modifiers: ['Meta'] })
  await expect(rows(page, 2)).toHaveClass(/stage-selected/)
  await page.getByRole('button', { name: 'demo-2の移動操作', exact: true }).click({ modifiers: ['Meta'] })
  await expect(page.getByRole('menuitem', { name: 'オンステージへ', exact: true })).toBeVisible()
  await expect(rows(page, 2)).toHaveClass(/stage-selected/)
  await page.keyboard.press('Escape')
  await rows(page, 1).focus()
  await page.keyboard.press('Control+Space')
  await expect(rows(page, 1)).toHaveClass(/stage-selected/)
  await page.getByRole('button', { name: '選択を解除', exact: true }).click()
  await expect(page.locator('.row.stage-selected')).toHaveCount(0)
  await expect(page).toHaveURL(/backlog$/)
  expect(context.pages()).toHaveLength(count)
  await rows(page, 1).locator('.title').click()
  await expect(page).toHaveURL(/tickets\/1$/)
})


test('selected row menu moves every selected ticket', async ({ page }) => {
  const { moves } = await setup(page)
  for (const seq of [1, 2, 5]) await select(page, seq)
  await page.getByRole('button', { name: 'demo-2の移動操作', exact: true }).click()
  await page.getByRole('menuitem', { name: 'オンステージへ', exact: true }).click()
  await expect.poll(() => moves.map(m => m.seq)).toEqual([1, 2, 5])
  expect(moves.every(m => JSON.stringify(m.body) === JSON.stringify({ staged: true, position: 'last' }))).toBe(true)
  await expect(page.locator('.row.stage-selected')).toHaveCount(0)
})

test('unselected row menu moves only that row and retains the selection', async ({ page }) => {
  const { moves } = await setup(page)
  for (const seq of [1, 2]) await select(page, seq)
  await page.getByRole('button', { name: 'demo-5の移動操作', exact: true }).click()
  await page.getByRole('menuitem', { name: 'オンステージへ', exact: true }).click()
  await expect.poll(() => moves.map(m => m.seq)).toEqual([5])
  await expect(rows(page, 1)).toHaveClass(/stage-selected/)
  await expect(rows(page, 2)).toHaveClass(/stage-selected/)
})

test('onstage todo menu returns to backlog; other categories never offer return', async ({ page }) => {
  const { moves, state } = await setup(page, { prepare(items) {
    items[0].staged_at = Date.now()
    // A custom status key still counts as todo by category.
    items[0].status = { ...editingTicket.status, key: 'ready', name: '準備済み' }
    for (const [seq, category] of [[7, 'in_progress'], [8, 'review'], [10, 'done']] as const) {
      items.push({ ...editingTicket, seq, title: category, staged_at: Date.now(), status: { ...editingTicket.status, key: category, category, name: category } })
    }
  } })
  for (const seq of [7, 8, 10]) {
    await expect(rows(page, seq)).toBeVisible()
    const button = rows(page, seq).getByRole('button', { name: `demo-${seq}の移動操作`, exact: true })
    if (await button.count()) {
      await button.click()
      await expect(page.getByRole('menuitem', { name: 'バックログへ', exact: true })).toHaveCount(0)
      await expect(page.getByRole('menuitem', { name: 'オンステージへ', exact: true })).toHaveCount(0)
      await page.keyboard.press('Escape')
    }
  }
  await page.getByRole('button', { name: 'demo-3の移動操作', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: 'バックログへ', exact: true })).toBeDisabled()
  await expect(page.getByText('配下のチケットは親と一緒にバックログへ移動します', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: 'demo-1の移動操作', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: 'オンステージへ', exact: true })).toHaveCount(0)
  await page.getByRole('menuitem', { name: 'バックログへ', exact: true }).click()
  await expect.poll(() => moves.map(m => m.seq)).toEqual([1])
  expect(moves[0].body).toEqual({ staged: false, position: 'last' })
  await expect.poll(() => state.items[0].staged_at).toBeNull()
  const backlog = page.locator('.group-section').filter({ has: page.getByText('バックログ', { exact: true }) })
  await expect(backlog.getByRole('link', { name: '配置確認', exact: true })).toBeVisible()
  await expect(backlog.getByRole('link', { name: '親と一緒の子', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'demo-1の移動操作', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: 'オンステージへ', exact: true })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'バックログへ', exact: true })).toHaveCount(0)
})

for (const width of [600, 900, 1440]) {
  test(`long titles shrink before right aligned tags and menu at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page, { prepare(items) {
      items[0].tags = [{ id: 'a', name: 'タグA' }, { id: 'b', name: 'タグB' }, { id: 'c', name: 'タグC' }]
      items[1].title = '非常に長いチケットタイトルがタグの表示領域を奪わないことを確認する'.repeat(8)
      items[1].tags = [...items[0].tags]
    } })
    const short = rows(page, 1).locator('.title-line')
    const long = rows(page, 2).locator('.title-line')
    for (const line of [short, long]) {
      const geometry = await line.evaluate(el => {
        const title = el.querySelector<HTMLElement>('.title')!
        const tags = el.querySelector<HTMLElement>('.tag-trail')!
        const menu = el.querySelector<HTMLElement>('.stage-controls')!
        const t = title.getBoundingClientRect(), g = tags.getBoundingClientRect(), m = menu.getBoundingClientRect()
        return { lineLeft: el.getBoundingClientRect().left, titleLeft: t.left, titleRight: t.right, tagLeft: g.left, tagRight: g.right, menuLeft: m.left, menuRight: m.right, lineRight: el.getBoundingClientRect().right, gap: m.left - g.right, expectedGap: parseFloat(getComputedStyle(el).columnGap),
          tagsFullyVisible: [...tags.querySelectorAll<HTMLElement>('.tag')].every(tag => tag.scrollWidth <= tag.clientWidth),
          tagWidth: g.width, titleClipped: title.scrollWidth > title.clientWidth, ellipsis: getComputedStyle(title).textOverflow }
      })
      expect(geometry.titleLeft).toBeCloseTo(geometry.lineLeft, 0)
      expect(geometry.titleRight).toBeLessThanOrEqual(geometry.tagLeft)
      expect(geometry.tagRight).toBeLessThanOrEqual(geometry.menuLeft)
      expect(geometry.menuRight).toBeCloseTo(geometry.lineRight, 0)
      // The right hand group has only its normal inter-item gap, never an auto margin.
      expect(geometry.gap).toBeCloseTo(geometry.expectedGap, 1)
      expect(geometry.tagsFullyVisible).toBe(true)
      expect(geometry.tagWidth).toBeGreaterThan(0)
      expect(geometry.ellipsis).toBe('ellipsis')
      if (line === long) expect(geometry.titleClipped).toBe(true)
    }
    await expect(long.locator('.tag-more')).toHaveText('+1')
    await page.screenshot({ path: testInfo.outputPath(`title-tags-menu-${width}.png`), fullPage: true })
  })
}


test('failed backlog return keeps the ticket onstage and offers retry', async ({ page }) => {
  const { moves, state } = await setup(page, { failSeq: 1, prepare(items) { items[0].staged_at = Date.now() } })
  await page.getByRole('button', { name: 'demo-1の移動操作', exact: true }).click()
  await page.getByRole('menuitem', { name: 'バックログへ', exact: true }).click()
  await expect(page.getByText(/0件をバックログへ移動しました。残り1件は未処理です/)).toBeVisible()
  expect(moves).toEqual([{ seq: 1, body: { staged: false, position: 'last' } }])
  expect(state.items[0].staged_at).not.toBeNull()
  const onstage = page.locator('.group-section').filter({ has: page.getByText('オンステージ', { exact: true }) })
  await expect(onstage.getByRole('link', { name: '配置確認', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'demo-1の移動操作', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: 'バックログへ', exact: true })).toBeEnabled()
})
