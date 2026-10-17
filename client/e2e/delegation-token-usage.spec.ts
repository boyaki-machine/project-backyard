import { expect, test, type Page } from '@playwright/test'

/**
 * 自律実行の「参考」の行のトークン（GuiDesign.md 5.5、Delegation.md 12.1。pb-311）。
 *
 * 対話型のクライアントは自分の消費を返さないので、全件が欠測になりうる。そのとき合計の「0」を
 * 出すと「使っていない」と読み違えられるので、「申告なし」と出す。API はすべてモックする。
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

async function mockTicket(page: Page, budget: { tokens_total: number; tokens_missing: number }) {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let body: unknown = { items: [] }
    if (path === '/api/v1/me') body = {
      actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
      permissions: [], projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view'] }], features: { delegation: true }, expires_at: 4070908800000 }
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [status] } }
    if (path === '/api/v1/projects/demo/tickets/10') body = ticket
    if (path === '/api/v1/projects/demo/tickets/10/delegation-status') body = {
      in_delegation: true, role: 'readonly', root_seq: 10, status: 'approved', scope: {},
      main: { id: 'm', kind: 'agent', display_name: 'main' }, evaluator: { id: 'e', kind: 'agent', display_name: 'evaluator' },
      budget: { tickets: 6, max_depth: 1, limits: {}, today: '2026-10-13', tokens_this_month: 0, ...budget },
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
}

for (const c of [
  { name: '全件が欠測', budget: { tokens_total: 0, tokens_missing: 17 }, want: 'トークン 申告なし（17件。対話型のクライアントは消費を返さない）', notWant: '申告されたトークン' },
  { name: '一部が欠測', budget: { tokens_total: 420000, tokens_missing: 3 }, want: '申告されたトークン 420,000（申告なし 3件）', notWant: '対話型のクライアント' },
  { name: '全件を申告', budget: { tokens_total: 420000, tokens_missing: 0 }, want: '申告されたトークン 420,000', notWant: '申告なし' },
]) {
  test(`参考のトークン：${c.name}`, async ({ page }, info) => {
    await page.setViewportSize({ width: 1366, height: 900 })
    await mockTicket(page, c.budget)
    await page.goto('/p/demo/tickets/10')
    const usage = page.locator('.delegation-item dd.muted', { hasText: '子チケット 6件' })
    await expect(usage).toContainText(c.want)
    await expect(usage).not.toContainText(c.notWant)
    await page.screenshot({ path: info.outputPath(`token-usage-${c.name}.png`) })
  })
}
