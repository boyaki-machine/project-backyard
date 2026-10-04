import { type Page, type Route } from '@playwright/test'

const actor = {
  id: '01K00000000000000000000000', kind: 'user', display_name: 'Test User',
  email: 'test@example.com', system_role: 'administrator', locale: 'ja',
  timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
}
const status = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }
export const editingTicket = {
  id: '01K00000000000000000000001', seq: 1, type: 'task', title: '配置確認',
  status, priority: null, assignee: null, reporter: actor, working_agent: null,
  parent_seq: null, has_children: false, sort_key: '0|1:', staged_at: null,
  tags: [], sprint: null, estimate_point: 5, actual_point: 5,
  actual_point_version: 'actual-v0', estimate_hours: 8, actual_hours: 3.5,
  start_at: 1786201200000, due_at: 1786719600000, all_day: true, closed_at: null,
  version: 1, created_at: 1788220800000, updated_at: 1788220800000,
  body_md: '', parent: null, epic: null, children: [], dod: [], links: [], references: [],
  comment_count: 0, execution_mode: 'agent_draft', readiness: null, readiness_note: null, scope: {},
}

async function reply(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
}

export async function mockEditingTicket(page: Page, locale: 'ja' | 'en') {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path === '/api/v1/me') return reply(route, {
      actor: { ...actor, locale }, permissions: [],
      projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit', 'ticket.actual_point.edit'] }],
      expires_at: 4070908800000,
    })
    if (path === '/api/v1/projects/demo') return reply(route, {
      key: 'demo', name: 'Demo', members: [], workflow: { statuses: [status] },
    })
    if (path === '/api/v1/projects/demo/tickets/1') return reply(route, editingTicket)
    if (path === '/api/v1/projects/demo/tickets') return reply(route, {
      items: url.searchParams.get('type') === 'epic' ? [] : [editingTicket],
      total: 1, page: 1, per_page: 200, total_pages: 1,
    })
    if (path.endsWith('/tags') || path.endsWith('/sprints') || path.endsWith('/comments')) return reply(route, { items: [] })
    await route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })
}

