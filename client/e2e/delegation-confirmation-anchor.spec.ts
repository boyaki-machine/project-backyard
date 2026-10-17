import { expect, test, type Page } from '@playwright/test'

/**
 * 確認待ちの通知の遷移先（GuiDesign.md 5.5「自律実行」、Delegation.md 8.2）。
 *
 * 通知は根のチケットを `#confirmations` 付きで開く。詳細はユーザ確認待ちのセクションまで寄せ、
 * 問いが画面の中に見える。付いていなければ寄せない。API はすべてモックする。
 */

const status = { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 1 }

const ticket = {
  seq: 10, id: '10', title: '文化祭モード', type: 'task', parent_seq: null, parent: null, epic: null, status, tags: [],
  priority: null, assignee: null, reporter: null, working_agent: null, sprint: null, staged_at: null, sort_key: '10',
  due_at: null, start_at: null, closed_at: null, all_day: true, no_overlap: false, start_pinned: false, due_pinned: false,
  body_md: '', children: [], has_children: false, dod: [], links: [], references: [], comment_count: 0, version: 1,
  execution_mode: 'delegated', readiness: null, readiness_note: null, scope: {}, delegation: null,
  estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
  created_at: 1, updated_at: 1,
}

const question = '子チケットを2件増やしてよいですか'

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let body: unknown = { items: [] }
    if (path === '/api/v1/me') body = {
      actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
      permissions: [],
      projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit', 'delegation.manage'] }],
      features: { delegation: true }, expires_at: 4070908800000 }
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [status] } }
    if (path === '/api/v1/projects/demo/tickets/10') body = ticket
    if (path === '/api/v1/projects/demo/tickets/10/delegation-status') body = {
      in_delegation: true, role: 'readonly', root_seq: 10, status: 'approved', scope: {},
      main: { id: 'm', kind: 'agent', display_name: 'main' }, evaluator: { id: 'e', kind: 'agent', display_name: 'evaluator' },
      main_next_move: 'PM の回答を待つ', main_stopped: false,
      open_confirmations: [{
        id: 'c1', ticket_seq: 10, reason: 'scope_change', question_md: question, options: [],
        created_at: 1, raised_by: { id: 'm', kind: 'agent', display_name: 'main' },
      }],
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
}

/** セクションを囲むスクロール枠のうち、寄せた量（scrollTop）の合計 */
async function scrolled(page: Page): Promise<number> {
  return page.locator('#confirmations').evaluate((el) => {
    let sum = 0
    for (let p = el.parentElement; p; p = p.parentElement) sum += p.scrollTop
    return sum
  })
}

for (const width of [1440, 768]) {
  test(`#confirmations で開くと、ユーザ確認待ちまで寄せる ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 600 })
    await mockApi(page)
    await page.goto('/p/demo/tickets/10#confirmations')
    const q = page.locator('#confirmations').getByText(question)
    await expect(q).toBeInViewport()
    expect(await scrolled(page)).toBeGreaterThan(0)
    await page.screenshot({ path: info.outputPath(`confirmation-anchor-${width}.png`) })
  })
}

test('#confirmations が無ければ寄せない', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 600 })
  await mockApi(page)
  await page.goto('/p/demo/tickets/10')
  await expect(page.locator('#confirmations')).toContainText(question)
  expect(await scrolled(page)).toBe(0)
})
