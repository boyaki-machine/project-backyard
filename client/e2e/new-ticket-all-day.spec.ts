import { expect, test, type Page } from '@playwright/test'

/**
 * 新規チケットの「終日」の切り替えと、詳細ペインの「終日」の置き場（`GuiDesign.md` 5.4.3・5.5。pb-327）。
 * API はすべてモックする。スクショは `PB_SHOT_DIR` があればそこへ置く。
 *
 * バックログから時刻付きのチケットを作れないと、タイムライン（5.15）に載るチケットを作れない。
 */

const JST = 9 * 3600_000
const T = (d: number, h: number, m = 0) => Date.UTC(2026, 9, d, h, m) - JST

const todo = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }

function ticket(seq: number, o: Partial<Record<string, unknown>> = {}) {
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type: 'task', title: `チケット${seq}`, status: todo,
    priority: null, assignee: null, reporter: null, working_agent: null, parent_seq: null, has_children: false,
    sort_key: `0|${seq}`, staged_at: null, tags: [], sprint: null, estimate_point: null, estimate_hours: null,
    actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: T(24, 10), due_at: T(24, 11), all_day: false, no_overlap: false, start_pinned: false, due_pinned: false,
    closed_at: null, version: 1, created_at: T(20, 8), updated_at: T(20, 8),
    body_md: '', links: [], references: [], children: [], dod: [], scope: {}, execution_mode: 'agent_draft',
    readiness: null, readiness_note: null, epic: null, parent: null, comment_count: 0, delegation: null, ...o,
  }
}

async function mockApi(page: Page) {
  const created: Record<string, unknown>[] = []
  const perms = ['project.view', 'ticket.view', 'ticket.create', 'ticket.edit']
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    const reply = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (path === '/api/v1/me') {
      return reply({
        actor: { id: '01K00000000000000000000000', kind: 'user', display_name: '検証 太郎', email: 'e2e@example.com',
          system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
        permissions: [],
        projects: [{ key: 'fes', name: '文化祭', role: 'project_admin', permissions: perms }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/fes') {
      return reply({ key: 'fes', name: '文化祭', description: null, status: 'active', timezone: 'Asia/Tokyo', version: 3, settings: {}, members: [],
        my_permissions: perms, workflow: { key: 'simple', name: 'シンプル', statuses: [todo] } })
    }
    if (path === '/api/v1/projects/fes/tickets' && req.method() === 'POST') {
      const body = req.postDataJSON() as Record<string, unknown>
      created.push(body)
      return reply(ticket(500, body), 201)
    }
    if (path === '/api/v1/projects/fes/tickets/410') return reply(ticket(410))
    return reply({ items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
  return created
}

const shotDir = process.env.PB_SHOT_DIR

test('新規チケット：既定は終日で、日付を終日として送る', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  const created = await mockApi(page)
  await page.goto('/p/fes/backlog')
  await page.locator('header').getByRole('button', { name: '+ 新規チケット' }).click()
  const dlg = page.getByRole('dialog', { name: '新規チケット' })
  await expect(dlg.getByRole('checkbox', { name: '終日' })).toBeChecked()
  await dlg.locator('input[type="text"]').first().fill('終日のチケット')
  await dlg.getByLabel('開始日').fill('2026-10-24')
  await dlg.getByLabel('期限').fill('2026-10-24')
  await dlg.getByRole('button', { name: '作成' }).click()
  await expect.poll(() => created.length).toBe(1)
  // 終日は基準タイムゾーンの0時、期限は締切日の翌日の0時
  expect(created[0]).toMatchObject({ all_day: true, start_at: T(24, 0), due_at: T(25, 0) })
})

test('新規チケット：終日を外すと日時で入れ、時刻付きで送る。切り替えても入力は保つ', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  const created = await mockApi(page)
  await page.goto('/p/fes/backlog')
  await page.locator('header').getByRole('button', { name: '+ 新規チケット' }).click()
  const dlg = page.getByRole('dialog', { name: '新規チケット' })
  await dlg.locator('input[type="text"]').first().fill('時刻付きのチケット')
  await dlg.getByLabel('開始日').fill('2026-10-24')
  await dlg.getByLabel('期限').fill('2026-10-24')

  // 終日 → 時刻付き：日の境界の時刻へ（期限は翌日の0時）
  await dlg.getByRole('checkbox', { name: '終日' }).uncheck()
  await expect(dlg.getByLabel('開始', { exact: true })).toHaveAttribute('type', 'datetime-local')
  await expect(dlg.getByLabel('開始', { exact: true })).toHaveValue('2026-10-24T00:00')
  await expect(dlg.getByLabel('終了', { exact: true })).toHaveValue('2026-10-25T00:00')
  // 時刻付き → 終日 → 時刻付き：往復しても同じ日に戻る
  await dlg.getByRole('checkbox', { name: '終日' }).check()
  await expect(dlg.getByLabel('開始日')).toHaveValue('2026-10-24')
  await expect(dlg.getByLabel('期限')).toHaveValue('2026-10-24')
  await dlg.getByRole('checkbox', { name: '終日' }).uncheck()

  // 終了が開始以前なら作らない
  await dlg.getByLabel('開始', { exact: true }).fill('2026-10-24T10:00')
  await dlg.getByLabel('終了', { exact: true }).fill('2026-10-24T10:00')
  await expect(dlg.getByText('終了は開始より後の日時を指定してください')).toBeVisible()
  await expect(dlg.getByRole('button', { name: '作成' })).toBeDisabled()

  await dlg.getByLabel('終了', { exact: true }).fill('2026-10-24T10:45')
  if (shotDir) await page.screenshot({ path: `${shotDir}/new-ticket-timed.png` })
  await dlg.getByRole('button', { name: '作成' }).click()
  await expect.poll(() => created.length).toBe(1)
  expect(created[0]).toMatchObject({ all_day: false, start_at: T(24, 10), due_at: T(24, 10, 45) })
})

test('新規チケット：予定が無くても、終日を外したことは送る', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  const created = await mockApi(page)
  await page.goto('/p/fes/backlog')
  await page.locator('header').getByRole('button', { name: '+ 新規チケット' }).click()
  const dlg = page.getByRole('dialog', { name: '新規チケット' })
  await dlg.locator('input[type="text"]').first().fill('予定なし')
  await dlg.getByRole('checkbox', { name: '終日' }).uncheck()
  await dlg.getByRole('button', { name: '作成' }).click()
  await expect.poll(() => created.length).toBe(1)
  expect(created[0]).toMatchObject({ all_day: false })
  expect(created[0]).not.toHaveProperty('start_at')
})

for (const width of [390, 1280]) {
  test(`詳細：「終日」は開始・終了のすぐ下にある（${width}px）`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockApi(page)
    await page.goto('/p/fes/tickets/410')
    const allDay = page.locator('dl .all-day-row').getByRole('checkbox', { name: '終日' })
    await expect(allDay).toBeVisible()
    await expect(allDay).not.toBeChecked()
    // 「終了」の欄の直後（間に別の欄を挟まない）
    const end = page.locator('.meta-item').filter({ has: page.locator('dt', { hasText: /^終了$/ }) })
    const endBox = (await end.boundingBox())!
    const rowBox = (await page.locator('dl .all-day-row').boundingBox())!
    expect(rowBox.y).toBeGreaterThanOrEqual(endBox.y + endBox.height - 1)
    expect(rowBox.y - (endBox.y + endBox.height)).toBeLessThan(24)
    if (shotDir) await page.screenshot({ path: `${shotDir}/detail-all-day-${width}.png` })
  })
}
