import { expect, test, type Page } from '@playwright/test'

/**
 * main が止まったとみなす時間（GuiDesign.md 5.5、Delegation.md 4.2・10.4。pb-310）。
 *
 * - 始めるフォームの「main の停止」の値が、委任を作る要求の scope.main_idle_minutes に乗る
 * - 委任状況が main_stopped のとき、エージェントステイタスに黄の行が出る
 *
 * API はすべてモックする。
 */

const status = { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 1 }

function ticket(executionMode: string) {
  return {
    seq: 10, id: '10', title: '文化祭モード', type: 'task', parent_seq: null, parent: null, epic: null, status, tags: [],
    priority: null, assignee: null, reporter: null, working_agent: null, sprint: null, staged_at: null, sort_key: '10',
    due_at: null, start_at: null, closed_at: null, all_day: true, no_overlap: false, start_pinned: false, due_pinned: false,
    body_md: '', children: [], has_children: false, dod: [], links: [], references: [], comment_count: 0, version: 1,
    execution_mode: executionMode, readiness: null, readiness_note: null, scope: {}, delegation: null,
    estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
    created_at: 1, updated_at: 1,
  }
}

const me = {
  actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
  permissions: [],
  projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit', 'delegation.manage'] }],
  features: { delegation: true }, expires_at: 4070908800000,
}

async function mockApi(page: Page, executionMode: string, delegationStatus: unknown, created: unknown[]) {
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    let body: unknown = { items: [] }
    let code = 200
    if (path === '/api/v1/me') body = me
    if (path === '/api/v1/me/agents') body = { items: [
      { id: 'a1', display_name: 'main', client_kind: 'claude_code', is_active: true },
      { id: 'a2', display_name: 'evaluator', client_kind: 'codex', is_active: true },
    ] }
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [status] } }
    if (path === '/api/v1/projects/demo/tickets/10') body = ticket(executionMode)
    if (path === '/api/v1/projects/demo/tickets/10/delegation-status') body = delegationStatus
    if (path === '/api/v1/projects/demo/tickets/10/delegation' && req.method() === 'POST') {
      created.push(req.postDataJSON())
      code = 201
      body = {}
    }
    await route.fulfill({ status: code, contentType: 'application/json', body: JSON.stringify(body) })
  })
}

test('始めるフォームの「main の停止」が scope.main_idle_minutes に乗る', async ({ page }, info) => {
  await page.setViewportSize({ width: 1366, height: 1000 })
  const created: { scope?: { main_idle_minutes?: number } }[] = []
  await mockApi(page, 'agent_only', { in_delegation: false }, created)
  await page.goto('/p/demo/tickets/10')
  await page.getByLabel('実行モード').selectOption('delegated')
  const form = page.locator('.delegation-item')
  await form.locator('select').nth(0).selectOption('a1')
  await form.locator('select').nth(1).selectOption('a2')
  const idle = form.locator('.idle input')
  await expect(idle).toHaveAttribute('placeholder', '30')
  await idle.fill('15')
  await page.screenshot({ path: info.outputPath('main-idle-form.png'), fullPage: true })
  await form.getByRole('button', { name: '自律実行を始める' }).click()
  await expect.poll(() => created.length).toBe(1)
  expect(created[0]!.scope?.main_idle_minutes).toBe(15)
})

for (const width of [1366, 1024, 768]) {
  test(`main が止まっているとき、エージェントステイタスに黄の行を出す ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await mockApi(page, 'delegated', {
      in_delegation: true, role: 'readonly', root_seq: 10, status: 'approved', scope: {},
      main: { id: 'm', kind: 'agent', display_name: 'main' }, evaluator: { id: 'e', kind: 'agent', display_name: 'evaluator' },
      main_next_move: '子チケットを進める',
      main_stopped: true, main_last_seen_at: Date.now() - 45 * 60 * 1000,
    }, [])
    await page.goto('/p/demo/tickets/10')
    const line = page.locator('.delegation-item .main-stopped')
    await expect(line).toHaveText('main が 45 分動いていません（利用枠の上限などで止まっている可能性）')
    const color = await line.evaluate((el) => getComputedStyle(el).color)
    const plain = await page.locator('.delegation-item .items dd').first().evaluate((el) => getComputedStyle(el).color)
    expect(color).not.toBe(plain)
    await page.screenshot({ path: info.outputPath(`main-stopped-${width}.png`), fullPage: true })
  })
}

test('main が動いているときは黄の行を出さない', async ({ page }) => {
  await mockApi(page, 'delegated', {
    in_delegation: true, role: 'readonly', root_seq: 10, status: 'approved', scope: {},
    main: { id: 'm', kind: 'agent', display_name: 'main' }, evaluator: { id: 'e', kind: 'agent', display_name: 'evaluator' },
    main_next_move: '子チケットを進める', main_stopped: false, main_last_seen_at: Date.now(),
  }, [])
  await page.goto('/p/demo/tickets/10')
  await expect(page.locator('.delegation-item')).toContainText('子チケットを進める')
  await expect(page.locator('.delegation-item .main-stopped')).toHaveCount(0)
})
