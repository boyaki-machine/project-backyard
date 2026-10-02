import { expect, test, type Page } from '@playwright/test'

/**
 * バックログのエピックの袖章とタグ（GuiDesign.md 5.4・5.4.4。pb-238）。
 *
 * 全エピック（完了を含む）を 34件返し、順位 0・8・16・24・32 のエピックに属する行を置く。
 * 4通りの線の組と、33件目からの灰を1行ずつ確かめる。API はすべてモックする。
 */

const status = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }
const done = { key: 'done', name: '完了', category: 'done', sort_order: 2 }
/** 全エピック。番号 1000〜1033。**1000 番台の偶数は完了**（順位に数えることを確かめる） */
const allEpics = Array.from({ length: 34 }, (_, i) => ({
  seq: 1000 + i, title: `エピック${i + 1}：多バイトの名前`, type: 'epic', parent_seq: null,
  status: i % 2 === 0 ? done : status,
}))
/** 絞り込みの候補（棚に戻ったものを含まない） */
const vocabEpics = allEpics.filter((e) => e.status === status)

function ticket(seq: number, parent_seq: number | null, tags = [{ id: 'tag1', name: '改善' }]) {
  return { seq, id: String(seq), title: `検証チケット${seq}`, type: 'task', parent_seq, status, tags,
    priority: null, assignee: null, working_agent: null, sprint: null, staged_at: null,
    sort_key: String(seq), due_at: null, start_at: null, closed_at: null, all_day: true }
}

const longTags = [{ id: 't1', name: '改善' }, { id: 't2', name: '長いタグ名'.repeat(8) }, { id: 't3', name: 'GUI' }]
const rows = [
  ticket(11, 1000, longTags), // 順位 0：太1本・黄土
  ticket(12, 11), // 孫：祖先をたどって順位 0
  ticket(13, 1008), // 順位 8：細2本・黄土
  ticket(14, 1016), // 順位 16：太＋細
  ticket(15, 1024), // 順位 24：細＋太
  ticket(16, 1032), // 順位 32：灰の細線1本
  ticket(17, 99), // 親が一覧に無い → 詳細 API で順位 1 を補う
  ticket(18, null, []), // 所属なし
]
const parent99 = { ...ticket(99, 1001), epic: { seq: 1001, title: allEpics[1]!.title } }

/** 背景の線分（`linear-gradient` の色の区間）を [色, 始まり, 終わり] で取り出す */
function segments(style: string): [string, number, number][] {
  return [...style.matchAll(/(var\([^)]+\)) (\d+)px (\d+)px/g)].map((m) => [m[1]!, Number(m[2]), Number(m[3])])
}

async function mockBacklog(page: Page, theme: 'light' | 'dark', hue: 'blue' | 'green'): Promise<string[]> {
  const fetched: string[] = []
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    let body: unknown = { items: [] }
    if (path === '/api/v1/me') body = {
      actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme, hue, must_change_password: false },
      permissions: [], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view'] }], expires_at: 4070908800000 }
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [status, done] } }
    if (path.endsWith('/tickets')) {
      const epicQuery = url.searchParams.get('type') === 'epic'
      const items = !epicQuery ? rows : url.searchParams.get('retired') === 'true' ? allEpics : vocabEpics
      body = { items, total: items.length, page: 1, per_page: 200, total_pages: 1 }
    }
    if (path.endsWith('/tickets/99')) { fetched.push(path); body = parent99 }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
  return fetched
}

for (const width of [1366, 1024, 768]) {
  test(`エピックを袖章で、タグを末尾で示し、行の高さを揃える ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const fetched = await mockBacklog(page, 'light', 'blue')
    await page.goto('/p/demo/backlog')
    await expect(page.locator('tr.row')).toHaveCount(rows.length)
    // 袖章の割り当てには、完了も含む全エピックを番号順で取る（5.4.4）
    await expect(page.locator('tr.row td.sleeved')).toHaveCount(7)

    // 2段のバッジは無い
    await expect(page.locator('.ticket-badges, .epic-badge')).toHaveCount(0)

    const cell = (seq: number) => page.locator('tr.row', { hasText: `検証チケット${seq}` }).locator('td').first()
    const seg = async (seq: number) => segments((await cell(seq).getAttribute('style')) ?? '')
    expect(await seg(11)).toEqual([['var(--pb-epic-0)', 0, 8]])
    expect(await seg(12)).toEqual([['var(--pb-epic-0)', 0, 8]])
    expect(await seg(13)).toEqual([['var(--pb-epic-0)', 0, 2], ['var(--pb-epic-0)', 6, 8]])
    expect(await seg(14)).toEqual([['var(--pb-epic-0)', 0, 4], ['var(--pb-epic-0)', 6, 8]])
    expect(await seg(15)).toEqual([['var(--pb-epic-0)', 0, 2], ['var(--pb-epic-0)', 4, 8]])
    expect(await seg(16)).toEqual([['var(--pb-text-muted)', 0, 2]])
    expect(await seg(17)).toEqual([['var(--pb-epic-1)', 0, 8]])
    expect(await seg(18)).toEqual([])
    expect(fetched).toContain('/api/v1/projects/demo/tickets/99')
    await expect(cell(13)).toHaveAttribute('title', `エピック: ${allEpics[8]!.title}`)

    // 線の色が実際に塗られている（トークンが定義されている）
    const painted = await cell(11).evaluate((el) => getComputedStyle(el).backgroundImage)
    expect(painted).toMatch(/rgb/)

    // タグは2つまで、3つ目からは +N。行の高さは全行で同じ
    const first = page.locator('tr.row').first()
    await expect(first.locator('.tag')).toHaveCount(2)
    await expect(first.locator('.tag-more')).toHaveText('+1')
    const heights = await page.locator('tr.row').evaluateAll((trs) => trs.map((tr) => Math.round(tr.getBoundingClientRect().height)))
    expect(new Set(heights).size).toBe(1)
    expect(heights[0]).toBeLessThanOrEqual(41)

    // タグはセルの外へはみ出さず、タイトルが読める幅を保つ
    const inside = await page.locator('.tag-trail').evaluateAll((els) => els.every((el) => {
      const c = el.closest('td')!.getBoundingClientRect()
      const b = el.getBoundingClientRect()
      return b.right <= c.right + 1 && b.left >= c.left - 1
    }))
    expect(inside).toBe(true)
    const title = await page.locator('tr.row .title').first().boundingBox()
    expect(title!.width).toBeGreaterThan(70)

    await page.screenshot({ path: info.outputPath(`sleeves-${width}.png`), fullPage: true })

    // 絞り込みの候補にも同じ袖章を添える（凡例の代わり）
    await page.locator('.backlog-filter-epic .trigger').click()
    await expect(page.locator('.epic-panel .epic-sleeve')).toHaveCount(vocabEpics.length)
    await page.screenshot({ path: info.outputPath(`sleeves-filter-${width}.png`) })
  })
}

// テーマと色相の4通りで、袖章の色が塗られることを確かめて撮る（8.11 の切り替えを1回ずつ）
for (const theme of ['light', 'dark'] as const) {
  for (const hue of ['blue', 'green'] as const) {
    test(`袖章を ${theme}・${hue} で描く`, async ({ page }, info) => {
      await page.setViewportSize({ width: 1366, height: 700 })
      await mockBacklog(page, theme, hue)
      await page.goto('/p/demo/backlog')
      await expect(page.locator('tr.row td.sleeved')).toHaveCount(7)
      const bg = await page.locator('tr.row td.sleeved').first().evaluate((el) => getComputedStyle(el).backgroundImage)
      expect(bg).toMatch(/rgb/)
      await page.screenshot({ path: info.outputPath(`sleeves-${theme}-${hue}.png`) })
    })
  }
}
