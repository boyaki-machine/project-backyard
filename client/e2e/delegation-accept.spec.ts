import { expect, test, type Page } from '@playwright/test'

/**
 * 成果の受け入れ（GuiDesign.md 5.5「自律実行」、Delegation.md 4.3「受け入れ」。pb-340）。
 *
 * 成果の評価に合格したら、ユーザ確認待ちの先頭に「成果の受け入れ」を出し、確かめる → マージを承認する →
 * 子をまとめて完了にしてから根を完了にする、を1枚に並べる。「やめる」のモーダルには受け入れとの違いを添える。
 * API はすべてモックする。
 */

const inProgress = { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 1 }
const done = { key: 'done', name: '完了', category: 'done', sort_order: 2 }

type State = { children: Record<number, string>; posted: string[] }

function root(state: State) {
  return {
    seq: 10, id: '10', title: '文化祭モード', type: 'task', parent_seq: null, parent: null, epic: null, status: inProgress, tags: [],
    priority: null, assignee: null, reporter: null, working_agent: null, sprint: null, staged_at: null, sort_key: '10',
    due_at: null, start_at: null, closed_at: null, all_day: true, no_overlap: false, start_pinned: false, due_pinned: false,
    body_md: '', has_children: true, dod: [], links: [], references: [], comment_count: 0, version: 1,
    execution_mode: 'delegated', readiness: null, readiness_note: null, scope: {}, delegation: null,
    estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
    created_at: 1, updated_at: 1,
    children: [11, 12].map((seq) => ({
      seq, title: seq === 11 ? '受付画面' : '演劇の台本', type: 'task', assignee: null,
      status: state.children[seq] === 'done' ? done : inProgress,
    })),
  }
}

const me = {
  actor: { id: 'user', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
  permissions: [],
  projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit', 'ticket.transition', 'ticket.close', 'delegation.manage'] }],
  expires_at: 4070908800000,
}

function delegationStatus(passed: boolean) {
  return {
    in_delegation: true, role: 'readonly', root_seq: 10, status: 'approved', scope: {},
    main: { id: 'm', kind: 'agent', display_name: 'main' }, evaluator: { id: 'e', kind: 'agent', display_name: 'evaluator' },
    main_next_move: 'PM の回答を待つ', main_stopped: false, deliverable_passed: passed,
    open_confirmations: [{
      id: 'c1', ticket_seq: 10, reason: 'irreversible_external', question_md: 'develop へマージしてよいですか',
      options: [], created_at: 1, raised_by: { id: 'm', kind: 'agent', display_name: 'main' },
    }],
  }
}

async function mockApi(page: Page, state: State, passed: boolean) {
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    let body: unknown = { items: [] }
    if (path === '/api/v1/me') body = me
    if (path === '/api/v1/projects/demo') body = { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses: [inProgress, done] } }
    if (path === '/api/v1/projects/demo/tickets/10') body = root(state)
    if (path === '/api/v1/projects/demo/tickets/10/delegation-status') body = delegationStatus(passed)
    const tr = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)\/transitions$/.exec(path)
    if (tr) {
      const seq = Number(tr[1])
      const openChildren = Object.values(state.children).some((s) => s !== 'done')
      const allowed = seq === 11 || (seq === 10 && !openChildren)
      const reason = seq === 12 ? 'レビューを通してから完了にしてください' : seq === 10 && openChildren ? '未完了の子チケットがあります' : undefined
      body = { current: inProgress, items: [{ key: 'done', name: '完了', category: 'done', allowed, ...(reason ? { reason } : {}) }] }
    }
    const post = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)\/transition$/.exec(path)
    if (post && req.method() === 'POST') {
      const seq = Number(post[1])
      state.posted.push(`${seq}→${(req.postDataJSON() as { to: string }).to}`)
      if (seq in state.children) state.children[seq] = 'done'
      body = root(state)
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
}

for (const width of [1366, 768]) {
  test(`合格後は「成果の受け入れ」を出し、子をまとめて完了にしてから受け入れる ${width}`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    const state: State = { children: { 11: 'in_progress', 12: 'in_progress' }, posted: [] }
    await mockApi(page, state, true)
    await page.goto('/p/demo/tickets/10')

    const card = page.locator('#confirmations .card.accept')
    await expect(card).toBeVisible()
    await expect(page.locator('#confirmations .card').first()).toHaveClass(/accept/) // 先頭に出す
    await expect(card.locator('[data-merge="open"]')).toHaveText('下の「後戻りできない操作」に回答してください')
    await expect(card.locator('[data-role="accept-reason"]')).toHaveText('未完了の子チケットがあります')
    await expect(card.locator('[data-action="accept"]')).toBeDisabled()
    await card.scrollIntoViewIfNeeded()
    await page.screenshot({ path: info.outputPath(`accept-before-${width}.png`) })

    // まとめて完了：直接進められる子だけ進め、進められない子は理由つきで残す
    state.children[12] = 'in_progress'
    await card.locator('[data-action="close-children"]').click()
    await expect(card.locator('[data-role="closed"]')).toHaveText('子 1 件を完了にしました')
    await expect(card.locator('[data-role="skipped"]')).toContainText('#12 演劇の台本')
    await expect(card.locator('[data-role="skipped"]')).toContainText('レビューを通してから完了にしてください')
    expect(state.posted).toEqual(['11→done'])

    // 残りの子を（画面の外で）完了にしたら、受け入れられる
    state.children[12] = 'done'
    await page.reload()
    await expect(card.locator('[data-action="accept"]')).toBeEnabled()
    await expect(card.locator('[data-action="close-children"]')).toHaveCount(0)
    await card.locator('[data-action="accept"]').click()
    await expect.poll(() => state.posted).toEqual(['11→done', '10→done'])
  })
}

test('合格後の「自律実行をやめる…」には、受け入れとの違いを添える', async ({ page }) => {
  const state: State = { children: { 11: 'in_progress', 12: 'in_progress' }, posted: [] }
  await mockApi(page, state, true)
  await page.goto('/p/demo/tickets/10')
  await page.locator('.delegation-item').getByRole('button', { name: '自律実行をやめる…' }).click()
  await expect(page.getByRole('dialog').locator('[data-role="accept-hint"]')).toContainText('やめると取り下げとして記録されます')
})

test('合格していなければ「成果の受け入れ」も添え書きも出さない', async ({ page }) => {
  const state: State = { children: { 11: 'in_progress', 12: 'in_progress' }, posted: [] }
  await mockApi(page, state, false)
  await page.goto('/p/demo/tickets/10')
  await expect(page.locator('#confirmations')).toContainText('develop へマージしてよいですか')
  await expect(page.locator('#confirmations .card.accept')).toHaveCount(0)
  await page.locator('.delegation-item').getByRole('button', { name: '自律実行をやめる…' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.getByRole('dialog').locator('[data-role="accept-hint"]')).toHaveCount(0)
})
