import { expect, test, type Page } from '@playwright/test'
import { mockEditingTicket, editingTicket } from './fixtures/ticket-editing'

async function setup(page: Page, options: { failSeq?: number; readOnly?: boolean;  } = {}) {
  const state = await mockEditingTicket(page, 'ja')
  // Parent, ordinary descendant, and a parent hidden by a filter.
  state.items.push({ ...editingTicket, seq: 3, title: '親と一緒の子', parent_seq: 1 })
  state.items.push({ ...editingTicket, seq: 4, title: '結果に親がいない子', parent_seq: 99 })
  state.items.push({ ...editingTicket, seq: 5, title: 'エピック直下', parent_seq: 9, sort_key: '0|5:' })
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
    item.staged_at = Date.now()
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
    const child = page.getByRole('checkbox', { name: 'demo-3を選択', exact: true })
    await expect(child).toBeDisabled()
    await expect(page.getByRole('checkbox', { name: 'demo-4を選択', exact: true })).toBeDisabled()
    await page.getByRole('button', { name: 'demo-1のオンステージ操作', exact: true }).click()
    await expect(page.getByRole('menuitem', { name: 'オンステージへ', exact: true })).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath(`stage-menu-${width}.png`), fullPage: true })
    await page.getByRole('menuitem', { name: 'オンステージへ', exact: true }).click()
    await expect.poll(() => state.items[0].staged_at).not.toBeNull()
    await expect(page.getByRole('checkbox', { name: 'demo-1を選択', exact: true })).toBeDisabled()
    // Both the parent and its child move; the child is never sent to move.
    const staged = page.locator('.group-section').filter({ has: page.getByText('オンステージ', { exact: true }) })
    await expect(staged.getByRole('link', { name: '親と一緒の子', exact: true })).toBeVisible()
    await page.getByRole('checkbox', { name: 'demo-2を選択', exact: true }).check()
    await page.getByRole('checkbox', { name: 'demo-5を選択', exact: true }).check()
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

test('partial failure retains remaining selection and reports both counts', async ({ page }) => {
  const { moves } = await setup(page, { failSeq: 5 })
  await page.getByRole('checkbox', { name: 'demo-2を選択', exact: true }).check()
  await page.getByRole('checkbox', { name: 'demo-5を選択', exact: true }).check()
  await page.getByRole('checkbox', { name: 'demo-6を選択', exact: true }).check()
  await page.getByRole('button', { name: '選択したチケットをオンステージへ', exact: true }).click()
  await expect(page.getByText(/1件をオンステージへ移動しました。残り2件は未処理です/)).toBeVisible()
  expect(moves.map(m => m.seq)).toEqual([2, 5])
  await expect(page.getByRole('checkbox', { name: 'demo-5を選択', exact: true })).toBeChecked()
  await expect(page.getByRole('checkbox', { name: 'demo-6を選択', exact: true })).toBeChecked()
  await expect(page.getByRole('checkbox', { name: 'demo-2を選択', exact: true })).not.toBeChecked()
})

test('readonly users have no stage controls', async ({ page }) => {
  await setup(page, { readOnly: true })
  await expect(page.getByRole('checkbox', { name: /demo-.*を選択/ })).toHaveCount(0)
  await expect(page.getByRole('button', { name: /demo-.*のオンステージ操作/ })).toHaveCount(0)
})

test('filter changes clear selections, and keyboard menu works with alternate sorting', async ({ page }) => {
  const { moves } = await setup(page)
  await page.getByRole('checkbox', { name: 'demo-1を選択', exact: true }).check()
  await page.getByRole('button', { name: /タイトル/ }).first().click()
  await expect(page.getByRole('group', { name: '選択したチケットの操作' })).toHaveCount(0)
  const menu = page.getByRole('button', { name: 'demo-1のオンステージ操作', exact: true })
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
  const checks = page.getByRole('checkbox', { name: 'demo-1を選択', exact: true })
  await expect(checks).toHaveCount(2)
  await checks.first().check()
  await expect(checks.nth(1)).toBeChecked()
  await page.getByRole('button', { name: '選択したチケットをオンステージへ', exact: true }).click()
  await expect.poll(() => moves.length).toBe(1)
  expect(moves[0].seq).toBe(1)
})
