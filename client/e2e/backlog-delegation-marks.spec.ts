import { expect, test, type Page } from '@playwright/test'

/**
 * バックログの自律実行の印（GuiDesign.md 5.4「自律実行の印」。pb-276・pb-307）。
 *
 * 完了レポートを出したのに完了していない子（`reported`）は、灰の文字のまま「· 報告済み」を足す。
 * 黄の面は人の対応が急ぎで要るとき（確認待ち・一時停止）だけに残す。API はすべてモックする。
 */

const status = { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 1 }

type Mark = { root_seq: number; is_root: boolean; status: string; open_confirmations: number; reported: boolean }

function ticket(seq: number, parent_seq: number | null, delegation: Mark | null) {
  return { seq, id: String(seq), title: `検証チケット${seq}`, type: 'task', parent_seq, status, tags: [],
    priority: null, assignee: null, working_agent: null, sprint: null, staged_at: null,
    sort_key: String(seq), due_at: null, start_at: null, closed_at: null, all_day: true, delegation }
}

const mark = (over: Partial<Mark>): Mark =>
  ({ root_seq: 10, is_root: false, status: 'approved', open_confirmations: 0, reported: false, ...over })

const rows = [
  ticket(10, null, mark({ is_root: true })),
  ticket(11, 10, mark({ reported: true })), // 報告済み：灰のまま
  ticket(12, 10, mark({})), // 作業中の子
  ticket(20, null, mark({ root_seq: 20, is_root: true, status: 'paused', open_confirmations: 1 })),
  ticket(21, 20, mark({ root_seq: 20, status: 'paused', open_confirmations: 1, reported: true })),
  ticket(30, null, null), // 木の外
]

async function mockBacklog(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let body: unknown = { items: [] }
    if (path === '/api/v1/me') body = {
      actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
      permissions: [], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view'] }], expires_at: 4070908800000 }
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [status] } }
    if (path.endsWith('/tickets')) body = { items: rows, total: rows.length, page: 1, per_page: 200, total_pages: 1 }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
}

for (const width of [1366, 1024, 768]) {
  test(`完了レポートを出した子に「報告済み」を灰で出す ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 700 })
    await mockBacklog(page)
    await page.goto('/p/demo/backlog')
    await expect(page.locator('tr.row')).toHaveCount(rows.length)

    const markOf = (seq: number) => page.locator('tr.row', { hasText: `検証チケット${seq}` }).locator('.mark')
    await expect(markOf(10)).toHaveText('自律実行')
    await expect(markOf(11)).toHaveText('自律実行の中 · 報告済み')
    await expect(markOf(12)).toHaveText('自律実行の中')
    // 急ぎの対応が重なるときは「報告済み」を出さない（狭い幅で印を切らせない）
    await expect(markOf(21)).toHaveText('自律実行の中 · 一時停止 · 確認待ち 1')
    await expect(markOf(30)).toHaveCount(0)

    // 報告済みだけでは黄にしない。急ぎの対応（停止・確認待ち）があれば黄
    expect(await markOf(11).getAttribute('class')).not.toContain('attention')
    expect(await markOf(21).getAttribute('class')).toContain('attention')
    await expect(markOf(11)).toHaveAttribute('title', 'demo-10 が自律実行を回しています（自律実行の中 · 報告済み）')

    // すべての印がセルの外へはみ出さない。確認待ち・一時停止が重なる子の行は、狭い幅では
    // 「自律実行の中」から縮め、一時停止・確認待ちは言葉のまま残す（5.4「自律実行の印」）
    const all = page.locator('tr.row .mark')
    await expect(all).toHaveCount(5)
    const outside = await all.evaluateAll((els) => els.flatMap((el) => {
      const c = el.closest('td')!.getBoundingClientRect()
      const b = el.getBoundingClientRect()
      return b.right <= c.right + 1 && b.left >= c.left - 1 ? [] : [`${el.textContent}: ${Math.round(b.right)} > ${Math.round(c.right)}`]
    }))
    expect(outside).toEqual([])
    // 縮めても、急ぎの対応の言葉は切らない
    const rest = markOf(21).locator('.mark-rest')
    expect(await rest.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true)

    await page.screenshot({ path: info.outputPath(`delegation-marks-${width}.png`), fullPage: true })
  })
}
