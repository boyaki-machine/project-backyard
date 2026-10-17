import { expect, test, type Page } from '@playwright/test'

/**
 * 一時停止の伝わり方を画面で添える（GuiDesign.md 5.5、Delegation.md 8.4。pb-309）。
 *
 * PC を止めるときに、各セッションへチャットで伝えて回らなくて済むようにする。API はすべてモックする。
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

async function mockApi(page: Page, delegationState: string) {
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
      in_delegation: true, role: 'readonly', root_seq: 10, status: delegationState, scope: {},
      main: { id: 'm', kind: 'agent', display_name: 'main' }, evaluator: { id: 'e', kind: 'agent', display_name: 'evaluator' },
      main_next_move: '子チケットを進める', main_stopped: false,
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
}

test('一時停止のモーダルに、伝わり方とチャット不要を添える', async ({ page }, info) => {
  await mockApi(page, 'approved')
  await page.goto('/p/demo/tickets/10')
  await page.locator('.delegation-item').getByRole('button', { name: '一時停止' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('待ち受け中のエージェントにはすぐ伝わり、作業中のものは次に PB に触れたときに止まります')
  await expect(dialog).toContainText('チャットで伝える必要はありません')
  await page.screenshot({ path: info.outputPath('pause-modal.png') })
})

test('一時停止中は、セッションを閉じてよいと添える', async ({ page }) => {
  await mockApi(page, 'paused')
  await page.goto('/p/demo/tickets/10')
  await expect(page.locator('.delegation-item')).toContainText(
    '一時停止中です。再開を待っています。エージェントのセッションは閉じて構いません（再開後、同じ一言で続きから始まります）')
})
