import { expect, test, type Page } from '@playwright/test'

/**
 * 自律実行を機能として切り替える（Delegation.md 1.3、GuiDesign.md 5.12「機能の有効・無効は、
 * 区切りを分けてスイッチで出す」・5.5「自律実行」。pb-353）。API はすべてモックする。
 */

const status = { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 1 }

const ticket = {
  seq: 10, id: '10', title: '文化祭モード', type: 'task', parent_seq: null, parent: null, epic: null, status, tags: [],
  priority: null, assignee: null, reporter: null, working_agent: null, sprint: null, staged_at: null, sort_key: '10',
  due_at: null, start_at: null, closed_at: null, all_day: true, no_overlap: false, start_pinned: false, due_pinned: false,
  body_md: '', children: [], has_children: false, dod: [], links: [], references: [], comment_count: 0, version: 1,
  execution_mode: 'agent_only', readiness: null, readiness_note: null, scope: {},
  delegation: { root_seq: 10, is_root: true, status: 'approved', open_confirmations: 0, reported: false },
  estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
  created_at: 1, updated_at: 1,
}

function me(delegation: boolean) {
  return {
    actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
    permissions: ['system.settings'],
    projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit', 'delegation.manage'] }],
    features: { delegation }, expires_at: 4070908800000,
  }
}

function setting(value: 'true' | 'false', source: 'default' | 'database') {
  return {
    key: 'delegation_enabled', value, default_value: 'false', source, layer: 2, value_type: 'bool', allowed: null,
    editable: true, restart_required: false, secret: false, env_key: 'PB_DELEGATION_ENABLED',
    config_file_key: 'delegation_enabled', display_name: '自律実行',
    description: 'エージェントに計画から完成までを任せる機能。成熟途上のため既定は無効です',
    updated_at: null, updated_by: null,
  }
}

type Calls = { me: number; delegationStatus: number; put: unknown[] }

/** state.delegation がサーバの実効値。PUT で書き換わる（conflict なら 409 を返して変えない） */
async function mockApi(page: Page, state: { delegation: boolean; conflict?: boolean }, calls: Calls) {
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    let body: unknown = { items: [] }
    let code = 200
    if (path === '/api/v1/me') {
      calls.me++
      body = me(state.delegation)
    }
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [status] } }
    if (path === '/api/v1/projects/demo/tickets') body = { items: [ticket], total: 1, page: 1, per_page: 200, total_pages: 1 }
    if (path === '/api/v1/projects/demo/tickets/10') body = ticket
    if (path === '/api/v1/projects/demo/tickets/10/delegation-status') {
      calls.delegationStatus++
      body = { in_delegation: false }
    }
    if (path === '/api/v1/admin/settings/pending') body = { pending_confirmation: null }
    if (path === '/api/v1/me/notifications/unread-count') body = { count: 0 }
    if (path === '/api/v1/admin/settings') {
      if (req.method() === 'PUT') {
        calls.put.push(req.postDataJSON())
        if (state.conflict) {
          code = 409
          body = { error: { code: 'conflict', message: '終了していない自律実行が 2 件あります。取り下げるか完了させてから無効にしてください' } }
        } else {
          state.delegation = !state.delegation
        }
      }
      if (code === 200) {
        body = { items: [setting(state.delegation ? 'true' : 'false', state.delegation ? 'database' : 'default')], config_file_path: null, pending_confirmation: null }
      }
    }
    await route.fulfill({ status: code, contentType: 'application/json', body: JSON.stringify(body) })
  })
}

for (const width of [1366, 768]) {
  test(`無効なら自律実行の入口を出さず、有効なら出す ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const state = { delegation: false }
    const calls: Calls = { me: 0, delegationStatus: 0, put: [] }
    await mockApi(page, state, calls)

    // 無効：実行モードの選択肢に「自律実行」が無く、委任の状態も読まない
    await page.goto('/p/demo/tickets/10')
    const mode = page.getByLabel('実行モード')
    await expect(mode).toBeVisible()
    await expect(mode.locator('option[value="delegated"]')).toHaveCount(0)
    await expect(mode.locator('option')).not.toHaveCount(0)
    expect(calls.delegationStatus).toBe(0)
    await page.screenshot({ path: info.outputPath(`ticket-disabled-${width}.png`), fullPage: true })

    // 無効：バックログに自律実行の印を出さない（チケットは delegation を持っている）
    await page.goto('/p/demo/backlog')
    await expect(page.locator('tr.row')).toHaveCount(1)
    await expect(page.locator('tr.row .mark')).toHaveCount(0)

    // 有効：同じチケットで選択肢と印が出る（対照）
    state.delegation = true
    await page.goto('/p/demo/tickets/10')
    await expect(page.getByLabel('実行モード').locator('option[value="delegated"]')).toHaveCount(1)
    await page.goto('/p/demo/backlog')
    await expect(page.locator('tr.row .mark')).toHaveText('自律実行')
  })

  test(`一般タブの「機能」でスイッチを切り替え、保存すると入口が出入りする ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const state = { delegation: false }
    const calls: Calls = { me: 0, delegationStatus: 0, put: [] }
    await mockApi(page, state, calls)

    await page.goto('/admin/settings')
    const section = page.locator('section.group', { has: page.getByRole('heading', { name: '機能' }) })
    await expect(section).toBeVisible()
    // 「機能」は「実行時の設定」より前に置く
    await expect(page.locator('section.group h2').first()).toHaveText('機能')
    const sw = section.getByRole('switch', { name: '自律実行' })
    await expect(sw).not.toBeChecked()
    // 運用の設定と混ぜない
    await expect(page.locator('section.group', { has: page.getByRole('heading', { name: '実行時の設定' }) }).getByRole('switch')).toHaveCount(0)
    await page.screenshot({ path: info.outputPath(`settings-off-${width}.png`), fullPage: true })

    // 押しただけでは保存しない
    await sw.click()
    await expect(sw).toBeChecked()
    await expect(section.getByText('変更あり')).toBeVisible()
    expect(calls.put).toHaveLength(0)

    const meBefore = calls.me
    await page.getByRole('button', { name: '変更を保存' }).click()
    await expect.poll(() => calls.put.length).toBe(1)
    expect(calls.put[0]).toEqual({ items: [{ key: 'delegation_enabled', value: 'true' }] })
    // 保存が通ったら GET /me を引き直す
    await expect.poll(() => calls.me).toBeGreaterThan(meBefore)
    await expect(sw).toBeChecked()
    await page.screenshot({ path: info.outputPath(`settings-on-${width}.png`), fullPage: true })
  })

  test(`終了していない委任があると無効化は 409 で返り、その文面を出す ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const state = { delegation: true, conflict: true }
    const calls: Calls = { me: 0, delegationStatus: 0, put: [] }
    await mockApi(page, state, calls)

    await page.goto('/admin/settings')
    const sw = page.getByRole('switch', { name: '自律実行' })
    await expect(sw).toBeChecked()
    await sw.click()
    await page.getByRole('button', { name: '変更を保存' }).click()
    // 共通のエラー処理は conflict の文面を決まり文句へ差し替えるので、画面が次の一手を書く
    await expect(page.getByRole('alert')).toHaveText('終了していない自律実行があるため、無効にできません。取り下げるか完了させてから無効にしてください')
    expect(state.delegation).toBe(true)
    await page.screenshot({ path: info.outputPath(`settings-conflict-${width}.png`), fullPage: true })
  })
}
