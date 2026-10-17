import { expect, test, type Page } from '@playwright/test'

/**
 * 作業中・兼務中のエージェントを main・評価に選ぶと注意を出す（GuiDesign.md 5.5「自律実行」、
 * ApiDesign.md 4.5.1 の open_works・delegations。pb-343）。止めはしない。API はすべてモックする。
 */

const status = { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 1 }

const ticket = {
  seq: 10, id: '10', title: '文化祭モード', type: 'task', parent_seq: null, parent: null, epic: null, status, tags: [],
  priority: null, assignee: null, reporter: null, working_agent: null, sprint: null, staged_at: null, sort_key: '10',
  due_at: null, start_at: null, closed_at: null, all_day: true, no_overlap: false, start_pinned: false, due_pinned: false,
  body_md: '', children: [], has_children: false, dod: [], links: [], references: [], comment_count: 0, version: 1,
  execution_mode: 'agent_only', readiness: null, readiness_note: null, scope: {}, delegation: null,
  estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
  created_at: 1, updated_at: 1,
}

const me = {
  actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
  permissions: [],
  projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit', 'delegation.manage'] }],
  features: { delegation: true }, expires_at: 4070908800000,
}

const agents = [
  { id: 'busy', display_name: '作業中の Claude Code', client_kind: 'claude_code', is_active: true,
    open_works: [{ ticket_seq: 52, ticket_title: '受付画面' }], delegations: [] },
  { id: 'eval', display_name: '評価を兼ねる Codex', client_kind: 'codex', is_active: true,
    open_works: [], delegations: [{ root_seq: 226, role: 'evaluator', status: 'approved' }] },
  { id: 'free', display_name: '空いている Claude Code', client_kind: 'claude_code', is_active: true,
    open_works: [], delegations: [] },
]

async function mockApi(page: Page, created: unknown[]) {
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    let body: unknown = { items: [] }
    let code = 200
    if (path === '/api/v1/me') body = me
    if (path === '/api/v1/me/agents') body = { items: agents }
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [status] } }
    if (path === '/api/v1/projects/demo/tickets/10') body = ticket
    if (path === '/api/v1/projects/demo/tickets/10/delegation-status') body = { in_delegation: false }
    if (path === '/api/v1/projects/demo/tickets/10/delegation' && req.method() === 'POST') {
      created.push(req.postDataJSON())
      code = 201
      body = {}
    }
    await route.fulfill({ status: code, contentType: 'application/json', body: JSON.stringify(body) })
  })
}

for (const width of [1366, 768]) {
  test(`作業中・兼務中のエージェントを選ぶと注意を出し、始めるのは止めない ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    const created: unknown[] = []
    await mockApi(page, created)
    await page.goto('/p/demo/tickets/10')
    await page.getByLabel('実行モード').selectOption('delegated')
    const form = page.locator('.delegation-item')
    const notes = form.locator('.busy')

    // 空いているエージェントでは注意を出さない
    await form.locator('select').nth(0).selectOption('free')
    await form.locator('select').nth(1).selectOption('eval')
    await expect(form.locator('.busy[data-role="main"]')).toHaveCount(0)
    await expect(form.locator('.busy[data-role="evaluator"]')).toHaveText('⚠ 評価エージェント：#226 の自律実行で評価を務めています')

    await form.locator('select').nth(0).selectOption('busy')
    await expect(form.locator('.busy[data-role="main"]')).toHaveText('⚠ mainエージェント：#52 で作業中です（作業記録が開いています）')
    await expect(notes).toHaveCount(2)
    await page.screenshot({ path: info.outputPath(`busy-agent-${width}.png`), fullPage: true })

    // 注意は出しても、始められる
    await form.getByRole('button', { name: '自律実行を始める' }).click()
    await expect.poll(() => created.length).toBe(1)
  })
}
