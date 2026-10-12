import { expect, test, type Page } from '@playwright/test'

/**
 * タイムラインの画面（`GuiDesign.md` 5.15。pb-298）。API はすべてモックする。
 *
 * 行・レーン・2行固定のバー・状態の色・当初計画の破線の枠・試算の影 → 確定・
 * 他の人の変更の光り・メニューの出し分けを見る。スクショは `PB_SHOT_DIR` があればそこへ置く。
 */

const JST = 9 * 3600_000
const T = (h: number, m = 0) => Date.UTC(2026, 9, 24, h, m) - JST
const NOW = T(11, 42)

const todo = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }
const doing = { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 2 }
const done = { key: 'done', name: '完了', category: 'done', sort_order: 3 }

const epics = [
  { seq: 301, title: '体育館ステージ', status: doing, sort_key: '0|a', closed_at: null },
  { seq: 302, title: '中庭ステージ', status: doing, sort_key: '0|b', closed_at: null },
  { seq: 303, title: '本部', status: todo, sort_key: '0|c', closed_at: null },
]

const LONG = '演劇部「銀河鉄道の夜 ―― 二枚の切符とひとつの約束」とカーテンコール・花束贈呈・記念撮影'

let version = 1
function tk(seq: number, epic: number, title: string, s: number | null, e: number | null, status = todo, o: Partial<Record<string, unknown>> = {}) {
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type: 'task', title, status,
    priority: null, assignee: null, reporter: null, working_agent: null,
    parent_seq: epic, has_children: false, sort_key: `0|${seq}`,
    staged_at: null, tags: [], sprint: null, estimate_point: null, estimate_hours: null,
    actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: s, due_at: e, all_day: false, no_overlap: true, start_pinned: false, due_pinned: false,
    epic_seq: epic, started_at: null, last_changed_by: null, closed_at: status === done ? e : null,
    version: version++, created_at: T(8), updated_at: T(8), ...o,
  }
}

function baseItems() {
  return [
    tk(410, 301, '吹奏楽部 演奏', T(10), T(10, 40), done, { closed_at: T(10, 41) }),
    tk(411, 301, 'ダンス部「Re:Start」', T(10, 50), T(11, 30), doing),
    tk(412, 301, LONG, T(11, 30), T(12, 20)),
    tk(413, 301, '照明・音響チェック', T(12, 15), T(12, 45), todo, { no_overlap: false }),
    tk(414, 301, '軽音楽部 ライブ', T(12, 30), T(13, 20)),
    tk(415, 301, '閉会式・表彰', T(15, 30), T(16), todo, { start_pinned: true }),
    tk(416, 301, '椅子並べ', T(9, 30), T(10, 10), done, { no_overlap: false }),
    tk(417, 301, '客席の養生', T(9, 40), T(10, 20), done, { no_overlap: false }),
    tk(420, 302, 'もちつき大会', T(11), T(11, 45), done, { closed_at: T(11, 35) }),
    tk(421, 302, 'クイズ大会', T(12), T(12, 40)),
    tk(422, 302, 'テント撤収', T(15, 20), null, todo, { no_overlap: false }),
  ]
}

async function mockApi(page: Page, opts: { theme: 'light' | 'dark'; views?: Record<string, boolean>; epics?: number[]; role?: 'admin' | 'viewer'; outside?: boolean; deps?: boolean }) {
  let items = baseItems()
  let etagN = 1
  const settings = { views: opts.views ?? { timeline: true }, timeline_snap_seconds: 60, timeline_epics: opts.epics ?? [301, 302, 303] }
  const perms = opts.role === 'viewer' ? ['project.view', 'ticket.view'] : ['project.view', 'ticket.view', 'ticket.create', 'ticket.edit', 'project.edit']
  const state = { rescheduled: 0, changeNext: false, created: [] as Record<string, unknown>[], linked: [] as { seq: number; body: Record<string, unknown> }[], aligned: [] as Record<string, unknown>[] }
  // 依存（5.15「依存線」。pb-332）。行またぎの SS と、反している FF
  const extraLinks = opts.deps
    ? [
        { id: 'l2', source_seq: 412, target_seq: 421, link_type: 'SS', lag_days: 0, lag_seconds: 1800, origin: 'human' },
        { id: 'l3', source_seq: 414, target_seq: 421, link_type: 'FF', lag_days: 0, lag_seconds: 0, origin: 'human' },
      ]
    : []
  let links = [{ id: 'l1', source_seq: 411, target_seq: 412, link_type: 'FS', lag_days: 0, lag_seconds: 0, origin: 'human' }, ...extraLinks]
  await page.clock.install({ time: NOW })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname
    const reply = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
      route.fulfill({ status, contentType: 'application/json', headers, body: JSON.stringify(body) })
    if (path === '/api/v1/me') {
      return reply({
        actor: { id: '01K00000000000000000000000', kind: 'user', display_name: '検証 太郎', email: 'e2e@example.com',
          system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: opts.theme, hue: 'blue', must_change_password: false },
        permissions: [],
        projects: [{ key: 'fes', name: '文化祭', role: 'project_admin', permissions: perms }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/fes') {
      return reply({ key: 'fes', name: '文化祭', description: null, status: 'active', timezone: 'Asia/Tokyo', version: 3, settings, members: [],
        my_permissions: perms, workflow: { key: 'simple', name: 'シンプル', statuses: [todo, doing, done] } })
    }
    if (path === '/api/v1/projects/fes/tickets' && req.method() === 'POST') {
      // 作成（5.15「チケットを作る」）。サーバと同じく、作ったものは次の取得から出る
      const body = req.postDataJSON() as { title: string; parent_seq?: number; start_at?: number; due_at?: number; all_day?: boolean }
      state.created.push(body)
      const t = tk(600 + state.created.length, body.parent_seq ?? 0, body.title, body.start_at ?? null, body.due_at ?? null, todo,
        { all_day: body.all_day ?? true, no_overlap: false, parent_seq: body.parent_seq ?? null })
      items = [...items, t]
      etagN++
      return reply(t, 201)
    }
    if (path === '/api/v1/projects/fes/tickets') {
      if (url.searchParams.get('type') === 'epic') {
        const list = epics.map((e) => ({ ...tk(e.seq, 0, e.title, null, null, e.status), type: 'epic', parent_seq: null }))
        return reply({ items: list, total: list.length, page: 1, per_page: 200, total_pages: 1 })
      }
      if (url.searchParams.get('view') === 'timeline') {
        if (state.changeNext) {
          state.changeNext = false
          items = items.map((t) => (t.seq === 421 ? { ...t, due_at: T(12, 50), version: t.version + 1, last_changed_by: { id: 'x', kind: 'user', display_name: '鈴木' } } : t))
          etagN++
        }
        const tag = `W/"tl-${etagN}"`
        if (req.headers()['if-none-match'] === tag) return route.fulfill({ status: 304, headers: { ETag: tag } })
        const parent = url.searchParams.get('parent')
        // 見る人の絞り込み（parent）は、プロジェクトの対象（timeline_epics）の内側だけを絞る
        const rowSeqs = parent ? parent.split(',').map(Number).filter((n) => settings.timeline_epics.includes(n)) : settings.timeline_epics
        // 終日は返さない（ApiDesign.md 9.2.7）
        const shown = items.filter((t) => rowSeqs.includes(t.epic_seq) && !t.all_day)
        return reply({
          items: shown,
          epics: epics.filter((e) => rowSeqs.includes(e.seq)),
          links,
          baselines: { count: 1, latest_id: 'B1', latest_created_at: T(8, 30) },
          page: 1, per_page: 5000, total: shown.length, total_pages: 1,
        }, 200, { ETag: tag })
      }
    }
    if (path === '/api/v1/projects/fes/schedule-baselines') {
      return reply({ items: [{ id: 'B1', label: '当日朝', item_count: 3, created_by: { id: 'x', display_name: '鈴木' }, created_at: T(8, 30) }] })
    }
    if (path === '/api/v1/projects/fes/schedule-baselines/B1') {
      return reply({ id: 'B1', label: '当日朝', item_count: 3, created_by: { id: 'x', display_name: '鈴木' }, created_at: T(8, 30), items: [
        { ticket_seq: 411, exists: true, title: 'ダンス部', epic_seq: 301, epic_title: '体育館ステージ', start_at: T(10, 40), due_at: T(11, 20), all_day: false, no_overlap: true, start_pinned: false, due_pinned: false },
        { ticket_seq: 410, exists: true, title: '吹奏楽部', epic_seq: 301, epic_title: '体育館ステージ', start_at: T(10), due_at: T(10, 40), all_day: false, no_overlap: true, start_pinned: false, due_pinned: false },
        { ticket_seq: 499, exists: false, title: '校長先生の挨拶', epic_seq: 302, epic_title: '中庭ステージ', start_at: T(13), due_at: T(13, 15), all_day: false, no_overlap: true, start_pinned: false, due_pinned: false },
        ...(opts.outside
          ? [
              // 対象から外した中庭ステージ（302）の、いまも在るチケットと、消したエピック（309）の行
              { ticket_seq: 420, exists: true, title: 'もちつき大会', epic_seq: 302, epic_title: '中庭ステージ', start_at: T(11), due_at: T(11, 45), all_day: false, no_overlap: true, start_pinned: false, due_pinned: false },
              { ticket_seq: 431, exists: false, title: '旧ステージの前説', epic_seq: 309, epic_title: '旧ステージ', start_at: T(12), due_at: T(12, 30), all_day: false, no_overlap: true, start_pinned: false, due_pinned: false },
            ]
          : []),
      ] })
    }
    const linkPost = /^\/api\/v1\/projects\/fes\/tickets\/(\d+)\/links$/.exec(path)
    if (linkPost && req.method() === 'POST') {
      const body = req.postDataJSON() as { target_seq: number; link_type: string }
      state.linked.push({ seq: Number(linkPost[1]), body })
      links = [...links, { id: `n${state.linked.length}`, source_seq: Number(linkPost[1]), target_seq: body.target_seq, link_type: body.link_type, lag_days: 0, lag_seconds: 0, origin: 'human' }]
      etagN++
      return reply({ id: `n${state.linked.length}`, direction: 'outgoing', link_type: body.link_type, lag_days: 0, lag_seconds: 0, origin: 'human', created_at: T(8),
        ticket: { seq: body.target_seq, title: 'x', type: 'task', status: todo } }, 201)
    }
    if (path === '/api/v1/projects/fes/tickets/414/reschedule') {
      // 依存を作った直後のそろえ（きっかけを動かさない。9.17.2）。FS の後続 421 が 414 の終了に寄る
      const body = req.postDataJSON() as Record<string, unknown>
      state.aligned.push(body)
      const changes = [{ seq: 421, reason: 'ripple', before: { start_at: T(12), due_at: T(12, 40) }, after: { start_at: T(13, 20), due_at: T(14) }, version: 9 }]
      return reply({ applied: false, result: 'ok', changes, block: null, alternative: null })
    }
    const taskDetail = /^\/api\/v1\/projects\/fes\/tickets\/(\d+)$/.exec(path)
    const task = taskDetail && req.method() === 'GET' ? items.find((t) => t.seq === Number(taskDetail[1])) : undefined
    if (task) return reply({ ...task, body_md: '', links: [], references: [], children: [], dod: [], scope: {}, execution_mode: 'agent_draft', readiness: null, readiness_note: null,
      epic: null, parent: null, comment_count: 0, delegation: null })
    if (path === '/api/v1/projects/fes/tickets/412/reschedule') {
      const body = req.postDataJSON() as { dry_run: boolean; due_at: number }
      const changes = [
        { seq: 412, reason: 'target', before: { start_at: T(11, 30), due_at: T(12, 20) }, after: { start_at: T(11, 30), due_at: body.due_at }, version: 3 },
        { seq: 414, reason: 'ripple', before: { start_at: T(12, 30), due_at: T(13, 20) }, after: { start_at: body.due_at, due_at: body.due_at + 50 * 60_000 }, version: 5 },
      ]
      if (body.dry_run) return reply({ applied: false, result: 'ok', changes, block: null, alternative: null })
      state.rescheduled++
      items = items.map((t) => {
        const c = changes.find((x) => x.seq === t.seq)
        return c ? { ...t, start_at: c.after.start_at, due_at: c.after.due_at, version: t.version + 1 } : t
      })
      etagN++
      return reply({ applied: true, result: 'ok', changes, block: null, alternative: null, items: items.filter((t) => t.seq === 412 || t.seq === 414) })
    }
    const epicDetail = /^\/api\/v1\/projects\/fes\/tickets\/(\d+)$/.exec(path)
    const epic = epicDetail && req.method() === 'GET' ? epics.find((e) => e.seq === Number(epicDetail[1])) : undefined
    if (epic) return reply({ ...tk(epic.seq, 0, epic.title, null, null, epic.status), type: 'epic', parent_seq: null,
      body_md: '', links: [], references: [], children: [], dod: [], scope: {}, execution_mode: 'agent_draft', readiness: null, readiness_note: null,
      epic: null, parent: null, comment_count: 0, delegation: null, tags: [], sprint: null })
    if (path.startsWith('/api/v1/projects/fes/tags')) return reply({ items: [] })
    return reply({ items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
  return state
}

const shotDir = process.env.PB_SHOT_DIR

/** ヘッダ右端のメニュー `[⋯]` から項目を選ぶ（5.15「ヘッダとメニュー」） */
async function more(page: Page, item: string) {
  await page.getByRole('button', { name: 'タイムラインのメニュー' }).click()
  await page.getByRole('menuitem', { name: item, exact: true }).click()
}

for (const theme of ['light', 'dark'] as const) {
  for (const width of [1280, 1920]) {
    test(`行・レーン・バー・当初計画を描く（${theme} ${width}px）`, async ({ page }) => {
      const errors: string[] = []
      page.on('pageerror', (e) => errors.push(e.message))
      await page.setViewportSize({ width, height: 800 })
      await mockApi(page, { theme })
      await page.goto('/p/fes/timeline')
      await expect(page.locator('.tl-row')).toHaveCount(3)

      // 体育館：メイン + サブ2本（椅子並べと養生が重なる）
      const gym = page.locator('.tl-row').first()
      await expect(gym.locator('.lanetag')).toHaveText(['メイン', 'サブ×2'])
      // 2行固定：長い題名でもバーの高さは 36px、1行目は1行で省略
      const long = page.locator('.bar[data-seq="412"]')
      const box = await long.boundingBox()
      expect(box!.height).toBe(36)
      const l1 = long.locator('.l1')
      expect(await l1.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true)
      expect((await l1.boundingBox())!.height).toBe(15)
      // 状態の色
      await expect(page.locator('.bar[data-seq="411"]')).toHaveClass(/st-late/)
      await expect(page.locator('.bar[data-seq="410"]')).toHaveClass(/st-done/)
      await expect(page.locator('.bar[data-seq="412"]')).toHaveClass(/st-wait/)
      await expect(page.locator('.bar[data-seq="421"]')).toHaveClass(/st-todo/)
      // アンカー・FS・片方だけ
      await expect(page.locator('.bar[data-seq="415"] .pin.s')).toHaveCount(1)
      await expect(page.locator('.fs.j')).toHaveCount(1)
      await expect(page.locator('.fs.ln')).toHaveCount(0)
      await expect(page.locator('.bar[data-seq="422"]')).toHaveClass(/half-s/)
      // 当初計画：ずれたものにだけ破線の枠。消えたもの。計画外
      await expect(page.locator('.base:not(.gone)')).toHaveCount(1)
      await expect(page.locator('.gonelbl')).toHaveText('消えた：校長先生の挨拶')
      await expect(page.locator('.bar[data-seq="412"] .newtag')).toHaveText('計画外')
      await expect(page.locator('.bar[data-seq="411"] .dv')).toHaveText('終+10分')
      // メニューに出る
      await expect(page.locator('nav .item', { hasText: 'タイムライン' })).toHaveCount(1)
      expect(errors).toEqual([])
      if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-mock-${theme}-${width}.png` })
    })
  }
}

test('端のドラッグ → 試算の影 → 確定', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  const state = await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline?zoom=hour')
  const bar = page.locator('.bar[data-seq="412"]')
  await expect(bar).toBeVisible()
  const h = bar.locator('.hE')
  const b = (await h.boundingBox())!
  await page.mouse.move(b.x + 4, b.y + 10)
  await page.mouse.down()
  await page.mouse.move(b.x + 40, b.y + 10, { steps: 5 })
  await page.mouse.up()
  await expect(page.locator('.tl-notice')).toContainText('後ろの1件が動きます')
  await expect(page.locator('.ghost')).toHaveCount(2)
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-mock-trial.png` })
  await page.locator('.tl-notice button', { hasText: '確定' }).click()
  await expect(page.locator('.tl-notice')).toContainText('確定しました（2件を動かしました）')
  await expect(page.locator('.ghost')).toHaveCount(0)
  expect(state.rescheduled).toBe(1)
})

test('他の人の変更を光らせ、通知の行に出す', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  const state = await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline')
  await expect(page.locator('.bar[data-seq="421"]')).toBeVisible()
  state.changeNext = true
  await page.clock.runFor(5100)
  await expect(page.locator('.bar[data-seq="421"]')).toHaveClass(/flash/)
  await expect(page.locator('.tl-notice .other')).toContainText('鈴木')
  await expect(page.locator('.tl-notice .other')).toContainText('クイズ大会 の終了を 12:50 に')
})

test('タイムラインを出さない設定ではメニューに無い。URL は開ける', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 700 })
  await mockApi(page, { theme: 'light', views: { gantt: false } })
  await page.goto('/p/fes/timeline')
  await expect(page.locator('.tl-row')).toHaveCount(3)
  await expect(page.locator('nav .item', { hasText: 'タイムライン' })).toHaveCount(0)
  await expect(page.locator('nav .item', { hasText: 'ガント' })).toHaveCount(0)
  await expect(page.locator('nav .item', { hasText: 'バックログ' })).toHaveCount(1)
})

test('タイムライン対象エピックが無ければ選ぶ導線。見るだけの人は依頼の文', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 700 })
  await mockApi(page, { theme: 'light', epics: [], role: 'viewer' })
  await page.goto('/p/fes/timeline')
  await expect(page.getByText('タイムライン対象エピックを選んでください')).toBeVisible()
  await expect(page.getByText('プロジェクト管理者に依頼してください')).toBeVisible()
  // 見るだけの人のメニューには、作成・記録・タイムライン対象エピックが無い。空いた所から作れない（5.15「ヘッダとメニュー」）
  await page.getByRole('button', { name: 'タイムラインのメニュー' }).click()
  await expect(page.getByRole('menuitem', { name: 'タイムラインを比較する…' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '現在のタイムラインを記録' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: 'タイムライン対象エピック…' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: '新しいチケット' })).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(page.locator('.tl-track.can-create')).toHaveCount(0)
  await expect(page.locator('.hE')).toHaveCount(0)
})

test('試算中に定期取得の結果を保留しても、確定した予定が古い一覧で戻らない', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  const state = await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline?zoom=hour')
  const bar = page.locator('.bar[data-seq="412"]')
  await expect(bar).toBeVisible()
  const h = (await bar.locator('.hE').boundingBox())!
  await page.mouse.move(h.x + 4, h.y + 10)
  await page.mouse.down()
  await page.mouse.move(h.x + 40, h.y + 10, { steps: 5 })
  await page.mouse.up()
  await expect(page.locator('.tl-notice')).toContainText('後ろの1件が動きます')
  // 試算の確認中に、他の人の変更（クイズ大会）を含む 200 が届く（画面に当てずに保留する）
  state.changeNext = true
  await page.clock.runFor(5100)
  await expect.poll(() => state.changeNext).toBe(false)
  await expect(page.locator('.bar[data-seq="421"]')).not.toHaveClass(/flash/)
  await page.locator('.tl-notice button', { hasText: '確定' }).click()
  await expect(page.locator('.tl-notice')).toContainText('確定しました（2件を動かしました）')
  expect(state.rescheduled).toBe(1)
  // 確定した予定のまま（12:20 に戻らない）。自分の確定は光らせない
  await expect(page.locator('.bar[data-seq="412"]')).toHaveAttribute('title', /11:30–12:30/)
  await expect(page.locator('.bar[data-seq="414"]')).toHaveAttribute('title', /12:30–13:20/)
  await expect(page.locator('.bar[data-seq="412"]')).not.toHaveClass(/flash/)
  await expect(page.locator('.bar[data-seq="414"]')).not.toHaveClass(/flash/)
  // 保留中に来た他の人の変更は保つ
  await expect(page.locator('.bar[data-seq="421"]')).toHaveAttribute('title', /12:00–12:50/)
  await expect(page.locator('.bar[data-seq="421"]')).toHaveClass(/flash/)
  await expect(page.locator('.tl-notice .other')).toContainText('鈴木')
  await expect(page.locator('.tl-notice .other')).not.toContainText('演劇部')
  // 保留した一覧を当てた後も、確定した予定のまま
  await expect(page.locator('.bar[data-seq="412"]')).toHaveAttribute('title', /11:30–12:30/)
})

test('行の見出しは番号だけ。番号を押すとエピックの詳細をタイムラインの中で開く（pb-326）', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline?zoom=hour')
  const gym = page.locator('.tl-row').first()
  // 件数（重複チェック付き・なし）は出さない。レーンの札は残す
  await expect(gym.locator('.tl-rlabel .rm')).toHaveText('pb-301')
  await expect(gym.locator('.lanetag')).toHaveText(['メイン', 'サブ×2'])
  for (const rm of await page.locator('.tl-rlabel .rm').allTextContents()) expect(rm).not.toMatch(/メイン|サブ|本番|裏/)

  await gym.getByRole('button', { name: 'pb-301' }).click()
  await expect(page).toHaveURL(/\/p\/fes\/tickets\/301\?.*from=timeline/)
  await expect(page.locator('.tl-detail')).toBeVisible()
  await expect(page.locator('.tl-detail')).toContainText('体育館ステージ')
  await expect(gym.locator('button.tl-seq')).toHaveClass(/sel/)
  // タイムラインは開いたまま（詳細はペインで重ねる）
  await expect(page.locator('.tl-row')).toHaveCount(3)
})

test('対象から外したエピック・消したエピックも、版を重ねる・変更推移で見るときは版の行で読める', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  await mockApi(page, { theme: 'light', epics: [301, 303], outside: true })
  await page.goto('/p/fes/timeline?zoom=hour')
  await expect(page.locator('.tl-row')).toHaveCount(4)
  // 重ねるとき：版に保存した行の見出しで出し、いまのチケットは「消えた」の影
  const yard = page.locator('.tl-row', { hasText: '中庭ステージ' })
  await expect(yard.locator('.tl-outside')).toHaveCount(1)
  await expect(yard.locator('.gonelbl')).toHaveText(['消えた：校長先生の挨拶', '消えた：もちつき大会'])
  const old = page.locator('.tl-row', { hasText: '旧ステージ' })
  await expect(old.locator('.gonelbl')).toHaveText('消えた：旧ステージの前説')
  // 対象の行には印を付けない
  await expect(page.locator('.tl-row', { hasText: '体育館ステージ' }).locator('.tl-outside')).toHaveCount(0)
  // 見出しの番号：在るエピックは詳細を開ける。もう無いエピックは開く先が無いので文字だけ（pb-326）
  await expect(yard.locator('.tl-rlabel button.tl-seq')).toHaveText('pb-302')
  await expect(old.locator('.tl-rlabel .rm')).toContainText('pb-309')
  await expect(old.locator('.tl-rlabel button.tl-seq')).toHaveCount(0)
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-mock-outside-overlay.png` })

  // タイムライン変更推移を見るとき：版のバーとして描く
  await more(page, 'タイムライン変更推移')
  await expect(page.locator('.tl-notice')).toContainText('タイムライン変更推移 1/2')
  await expect(page.locator('.bar[data-seq="420"]')).toHaveCount(1)
  await expect(page.locator('.bar[data-seq="420"]')).toHaveClass(/st-plan/)
  await expect(page.locator('.bar[data-seq="431"]')).toHaveCount(1)
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-mock-outside-step.png` })
  await page.locator('.tl-notice button', { hasText: '終える' }).click()

  // 見る人の絞り込み（parent）で外した行は、版の表示でも出さない
  await page.goto('/p/fes/timeline?zoom=hour&parent=301')
  await expect(page.locator('.tl-row')).toHaveCount(1)
  await expect(page.locator('.tl-row', { hasText: '中庭ステージ' })).toHaveCount(0)
  await more(page, 'タイムライン変更推移')
  await expect(page.locator('.tl-notice')).toContainText('タイムライン変更推移 1/2')
  await expect(page.locator('.tl-row')).toHaveCount(1)
  await expect(page.locator('.bar[data-seq="420"]')).toHaveCount(0)
})

test('タイムライン対象エピックが空でも、保存した版と比較の影は読める。版も行も無ければ空状態', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  await mockApi(page, { theme: 'light', epics: [], outside: true })
  // 重ねるとき（既定は最後に保存した版）
  await page.goto('/p/fes/timeline?zoom=hour')
  await expect(page.locator('.tl-row')).toHaveCount(3)
  await expect(page.locator('.tl-row .gonelbl', { hasText: '旧ステージの前説' })).toBeVisible()
  // 何も選んでいないことは、行の上の帯で伝え続ける（全体を隠さない）
  await expect(page.locator('.tl-select-band')).toContainText('タイムライン対象エピックを選んでください')
  // タイムライン変更推移を見るとき
  await more(page, 'タイムライン変更推移')
  await expect(page.locator('.tl-notice')).toContainText('タイムライン変更推移 1/2')
  await expect(page.locator('.bar[data-seq="431"]')).toBeVisible()
  await expect(page.locator('.bar[data-seq="420"]')).toBeVisible()
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-mock-empty-target-step.png` })
  // 最後の「いま」でも、前の版との比較の影は読める
  await page.locator('.tl-notice button[aria-label="次の版"]').click()
  await expect(page.locator('.tl-notice')).toContainText('いま（当日の予定）')
  await expect(page.locator('.tl-row .gonelbl', { hasText: '旧ステージの前説' })).toBeVisible()
  await page.locator('.tl-notice button', { hasText: '終える' }).click()
  // 重ねない（版も行も無い）ときは、これまでどおりの空状態
  await page.goto('/p/fes/timeline?zoom=hour&baseline=none')
  await expect(page.getByText('タイムライン対象エピックを選んでください')).toBeVisible()
  await expect(page.locator('.tl-body')).toBeHidden()
})

test('設定に残った不在のエピック番号も、版の行の情報で読める。parent で外せば出さない', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  await mockApi(page, { theme: 'light', epics: [301, 303, 309], outside: true })
  await page.goto('/p/fes/timeline?zoom=hour')
  await more(page, 'タイムライン変更推移')
  await expect(page.locator('.tl-notice')).toContainText('タイムライン変更推移 1/2')
  await expect(page.locator('.bar[data-seq="431"]')).toHaveCount(1)
  await expect(page.locator('.tl-row', { hasText: '旧ステージ' }).locator('.tl-outside')).toHaveCount(1)
  await page.goto('/p/fes/timeline?zoom=hour&parent=301')
  await more(page, 'タイムライン変更推移')
  await expect(page.locator('.tl-notice')).toContainText('タイムライン変更推移 1/2')
  await expect(page.locator('.bar[data-seq="431"]')).toHaveCount(0)
  await expect(page.locator('.tl-row')).toHaveCount(1)
})

test('ヘッダ：見る範囲の操作と版の札だけを置き、比較・記録・タイムライン対象エピックはメニューにまとめる', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline')
  await expect(page.locator('.tl-row')).toHaveCount(3)
  const tools = page.locator('.tl-tools')
  // pb-325：「行」→「表示するエピック」
  await expect(tools.locator('.tl-chip', { hasText: '表示するエピック' })).toContainText('3/3')
  await expect(tools.getByText('行', { exact: true })).toHaveCount(0)
  // pb-322・pb-324：ヘッダからボタンが消える。重ねている版は押せない札で残す
  for (const name of ['タイムライン変更推移', '計画を保存', '現在のタイムラインを記録', 'タイムライン対象エピック']) {
    await expect(tools.getByRole('button', { name, exact: false })).toHaveCount(0)
  }
  const chip = tools.locator('.tl-chip-static')
  await expect(chip).toContainText('当初計画')
  await expect(chip).toContainText('10/24 08:30')
  expect(await chip.evaluate((el) => el.tagName)).toBe('SPAN')
  // ヘッダが1段に収まる（折り返さない）
  const box = (await page.locator('.tl-page > .page-header').boundingBox())!
  expect(box.height).toBeLessThanOrEqual(56)

  // メニューの並び（区切りはタイムライン対象エピックの上）
  await page.getByRole('button', { name: 'タイムラインのメニュー' }).click()
  await expect(page.getByRole('menuitem')).toHaveText(['新しいチケット', 'タイムラインを比較する…', 'タイムライン変更推移', '現在のタイムラインを記録', 'タイムライン対象エピック…'])
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-menu-1280.png` })

  // タイムラインを比較する → 版の一覧 →「重ねない」で札が消える
  await page.getByRole('menuitem', { name: 'タイムラインを比較する…' }).click()
  const versions = page.locator('.tl-versions')
  await expect(versions).toBeVisible()
  await expect(versions.locator('.tl-vrow')).toHaveCount(1)
  await versions.getByText('重ねない').click()
  await expect(page).toHaveURL(/baseline=none/)
  await expect(tools.locator('.tl-chip-static')).toHaveCount(0)
  await expect(page.locator('.base')).toHaveCount(0)

  // タイムライン変更推移 ⇄ 終える（メニューの項目が切り替わる）
  await more(page, 'タイムライン変更推移')
  await expect(page.locator('.tl-notice')).toContainText('タイムライン変更推移 1/2')
  await more(page, 'タイムライン変更推移を終える')
  await expect(page.locator('.tl-notice')).not.toContainText('タイムライン変更推移 1/2')
})

test('メニュー：現在のタイムラインを記録（pb-323）', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  let posted: unknown = null
  await mockApi(page, { theme: 'light' })
  await page.route('**/api/v1/projects/fes/schedule-baselines', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback()
    posted = route.request().postDataJSON()
    return route.fulfill({ status: 201, contentType: 'application/json',
      body: JSON.stringify({ id: 'B2', label: 'リハ後', item_count: 11, created_by: { id: 'x', display_name: '検証 太郎' }, created_at: T(11, 40) }) })
  })
  await page.goto('/p/fes/timeline')
  await expect(page.locator('.tl-row')).toHaveCount(3)
  await more(page, '現在のタイムラインを記録')
  const dlg = page.getByRole('dialog', { name: '現在のタイムラインを記録' })
  await expect(dlg).toBeVisible()
  await expect(page.getByText('計画を保存')).toHaveCount(0)
  await dlg.locator('input[type="text"]').fill('リハ後')
  await dlg.getByRole('button', { name: '記録', exact: true }).click()
  await expect(dlg).toHaveCount(0)
  expect(posted).toEqual({ label: 'リハ後' })
  await expect(page.locator('.tl-notice')).toContainText('の版を保存しました（11件）')
})

test('メニュー：タイムライン対象エピックをダイアログで選んで保存する（pb-324）', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  let patched: { settings?: Record<string, unknown> } | null = null
  await mockApi(page, { theme: 'light', epics: [301] })
  await page.route('**/api/v1/projects/fes', async (route) => {
    if (route.request().method() !== 'PATCH') return route.fallback()
    patched = route.request().postDataJSON()
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
      key: 'fes', name: '文化祭', description: null, status: 'active', timezone: 'Asia/Tokyo', version: 4, settings: patched!.settings, members: [],
      my_permissions: ['project.view', 'ticket.view', 'ticket.edit', 'project.edit'], workflow: { key: 'simple', name: 'シンプル', statuses: [] } }) })
  })
  // 版を重ねると版の行（中庭ステージ）も出るので、重ねずに開く
  await page.goto('/p/fes/timeline?baseline=none')
  await expect(page.locator('.tl-row')).toHaveCount(1)
  await more(page, 'タイムライン対象エピック…')
  const dlg = page.getByRole('dialog', { name: 'タイムライン対象エピック' })
  await expect(dlg).toBeVisible()
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-epic-dialog.png` })
  await dlg.getByText('中庭ステージ').click()
  await dlg.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dlg).toHaveCount(0)
  expect(patched!.settings).toMatchObject({ timeline_epics: [301, 302], views: { timeline: true }, timeline_snap_seconds: 60 })
})

test('メニューの「新しいチケット」：時刻付き・いまから30分・絞った行のエピックで作る（pb-321）', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const state = await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline?parent=302')
  await expect(page.locator('.tl-row')).toHaveCount(1)
  await more(page, '新しいチケット')
  const dlg = page.getByRole('dialog', { name: '新規チケット' })
  await expect(dlg.getByRole('checkbox', { name: '終日' })).not.toBeChecked()
  // 「いま」（時計は 11:42 から進んでいる）を吸着の単位（1分）に切り上げ、30分
  await expect(dlg.getByLabel('開始', { exact: true })).toHaveValue('2026-10-24T11:43')
  await expect(dlg.getByLabel('終了', { exact: true })).toHaveValue('2026-10-24T12:13')
  await expect(dlg.locator('select').filter({ hasText: '中庭ステージ' })).toHaveValue('302')
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-create-menu.png` })
  await dlg.locator('input[type="text"]').first().fill('落とし物の案内')
  await dlg.getByRole('button', { name: '作成' }).click()
  await expect(dlg).toHaveCount(0)
  expect(state.created).toHaveLength(1)
  expect(state.created[0]).toMatchObject({ title: '落とし物の案内', all_day: false, start_at: T(11, 43), due_at: T(12, 13), parent_seq: 302 })
  // 時間重複チェックは付けない
  expect(state.created[0]).not.toHaveProperty('no_overlap')
  await expect(page.locator('.tl-notice')).toContainText('fes-601 を作りました')
  const bar = page.locator('.bar[data-seq="601"]')
  await expect(bar).toBeVisible()
  // 自分で作ったものは光らせない。光は 2.4秒で消えるので、待ち直さずにその場で読む
  expect(await bar.getAttribute('class')).not.toMatch(/flash/)
})

test('行の空いた所を横にドラッグして、その時間帯で作る。押しただけでは作らない（pb-321）', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  const state = await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline?zoom=hour')
  const hq = page.locator('.tl-row', { hasText: '本部' }).locator('.tl-track')
  await expect(hq).toHaveClass(/can-create/)
  const box = (await hq.boundingBox())!
  const y = box.y + box.height / 2

  // 押しただけ（動かさない）では開かない
  await page.mouse.click(box.x + 300, y)
  await expect(page.getByRole('dialog', { name: '新規チケット' })).toHaveCount(0)

  // 時の段階は 1時間＝220px。110px で30分
  await page.mouse.move(box.x + 300, y)
  await page.mouse.down()
  await page.mouse.move(box.x + 360, y, { steps: 4 })
  await expect(page.locator('.tl-newrange')).toHaveCount(1)
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-create-drag.png` })
  await page.mouse.move(box.x + 410, y, { steps: 4 })
  await page.mouse.up()
  await expect(page.locator('.tl-newrange')).toHaveCount(0)
  const dlg = page.getByRole('dialog', { name: '新規チケット' })
  await expect(dlg).toBeVisible()
  await expect(dlg.getByRole('checkbox', { name: '終日' })).not.toBeChecked()
  await expect(dlg.locator('select').filter({ hasText: '本部' })).toHaveValue('303')
  await dlg.locator('input[type="text"]').first().fill('迷子の呼び出し')
  await dlg.getByRole('button', { name: '作成' }).click()
  await expect(dlg).toHaveCount(0)
  const body = state.created[0] as { start_at: number; due_at: number; all_day: boolean; parent_seq: number }
  expect(body).toMatchObject({ all_day: false, parent_seq: 303 })
  // 両端は吸着の単位（1分）に寄る
  expect(body.start_at % 60_000).toBe(0)
  expect(body.due_at % 60_000).toBe(0)
  expect(body.due_at - body.start_at).toBeGreaterThanOrEqual(29 * 60_000)
  expect(body.due_at - body.start_at).toBeLessThanOrEqual(31 * 60_000)
  await expect(page.locator('.tl-row', { hasText: '本部' }).locator('.bar[data-seq="601"]')).toBeVisible()

  // バーの上から始めたドラッグでは作らない（バーの操作に任せる）
  const bar = (await page.locator('.bar[data-seq="421"]').boundingBox())!
  await page.mouse.move(bar.x + bar.width / 2, bar.y + 10)
  await page.mouse.down()
  await page.mouse.move(bar.x + bar.width / 2 + 120, bar.y + 10, { steps: 4 })
  await page.mouse.up()
  await expect(page.locator('.tl-newrange')).toHaveCount(0)
  expect(state.created).toHaveLength(1)
})

test('作ったものが描かれないときは理由を出す。タイムライン変更推移を見ているあいだは空いた所から作れない（pb-321）', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const state = await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline')
  await expect(page.locator('.tl-row')).toHaveCount(3)
  await more(page, '新しいチケット')
  const dlg = page.getByRole('dialog', { name: '新規チケット' })
  await dlg.locator('input[type="text"]').first().fill('前日の搬入')
  await dlg.getByRole('checkbox', { name: '終日' }).check()
  await dlg.getByRole('button', { name: '作成' }).click()
  await expect(dlg).toHaveCount(0)
  expect(state.created[0]).toMatchObject({ all_day: true })
  await expect(page.locator('.tl-notice')).toContainText('fes-601 を作りました。終日の予定はタイムラインに描きません')

  await more(page, 'タイムライン変更推移')
  await expect(page.locator('.tl-notice')).toContainText('タイムライン変更推移 1/2')
  await expect(page.locator('.tl-track.can-create')).toHaveCount(0)
})

for (const [theme, width] of [['light', 1920], ['dark', 1280]] as const) {
test(`依存線：行をまたぐ線と種別・ずらしの札、反した配置は ⚠。接している FS は継ぎ目の ◆（${theme} ${width}px。pb-332）`, async ({ page }) => {
  await page.setViewportSize({ width, height: 900 })
  await mockApi(page, { theme, deps: true })
  await page.goto('/p/fes/timeline')
  await expect(page.locator('.tl-row')).toHaveCount(3)
  await expect(page.locator('.fs.j')).toHaveCount(1)
  const lines = page.locator('.tl-deps g.dep')
  await expect(lines).toHaveCount(2)
  await expect(page.locator('.tl-deps g.dep[data-link="l2"] .dtt')).toHaveText('SS +30m')
  // 414 の終了 13:20 に 421 の終了 12:40 がそろっていない → 反している
  await expect(page.locator('.tl-deps g.dep[data-link="l3"]')).toHaveClass(/bad/)
  await expect(page.locator('.tl-deps g.dep[data-link="l3"] .dtt')).toHaveText('⚠ FF')
  await expect(page.locator('.tl-deps g.dep[data-link="l2"]')).not.toHaveClass(/bad/)
  // 線は行をまたぐ（出る側と入る側の高さが違う）
  const d = await page.locator('.tl-deps g.dep[data-link="l2"] .dl').getAttribute('d')
  expect(d).toMatch(/V/)
  // バーに載せると、そのバーの線を濃くする
  await page.locator('.bar[data-seq="412"]').hover()
  await expect(page.locator('.tl-deps g.dep[data-link="l2"]')).toHaveClass(/hot/)
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-deps-${theme}-${width}.png` })
})
}

test('バーの後半から別のバーの前半へ引くと FS を作り、そろえる試算を出す。Esc は依存を残す（pb-332）', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 900 })
  const state = await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline')
  const src = page.locator('.bar[data-seq="414"]')
  const dst = page.locator('.bar[data-seq="421"]')
  await expect(src).toBeVisible()
  const a = (await src.boundingBox())!
  const b = (await dst.boundingBox())!
  // 後半を掴む（終了 F が出る側）→ 相手の前半へ（開始 S が入る側）＝ FS
  await page.mouse.move(a.x + a.width * 0.8, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(a.x + a.width * 0.8 + 20, a.y + a.height / 2 + 20, { steps: 3 })
  await page.mouse.move(b.x + b.width * 0.2, b.y + b.height / 2, { steps: 5 })
  await expect(page.locator('.tl-deptag')).toHaveText('FS pb-414 → pb-421')
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-link-drag.png` })
  await page.mouse.up()
  await expect.poll(() => state.linked.length).toBe(1)
  expect(state.linked[0]).toEqual({ seq: 414, body: { target_seq: 421, link_type: 'FS', lag_days: 0, lag_seconds: 0 } })
  // きっかけを動かさない試算（start_at / due_at を送らない）
  await expect.poll(() => state.aligned.length).toBe(1)
  expect(state.aligned[0]).toEqual({ op: 'move', on_block: 'reject', dry_run: true })
  const notice = page.locator('.tl-notice')
  await expect(notice).toContainText('依存を作りました（FS pb-414 → pb-421）')
  await expect(notice).toContainText('依存に合わせて1件が動きます')
  await expect(page.locator('.ghost')).toHaveCount(1)
  // 詳細は開かない（引いた後の click は詳細を開く操作にしない）
  await expect(page.locator('.tl-detail')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(page.locator('.ghost')).toHaveCount(0)
  await expect(notice).toContainText('予定は動かしていません')
  expect(state.rescheduled).toBe(0)
})

test('自分自身には落とせない。3px 未満の動きはクリックとして詳細を開く（pb-332）', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 900 })
  const state = await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline')
  const bar = page.locator('.bar[data-seq="414"]')
  const a = (await bar.boundingBox())!
  await page.mouse.move(a.x + a.width * 0.8, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(a.x + a.width * 0.3, a.y + a.height / 2, { steps: 4 })
  await expect(page.locator('.tl-deptag')).toHaveText('自分自身には引けません')
  await expect(page.locator('.tl-deptag')).toHaveClass(/ng/)
  await page.mouse.up()
  expect(state.linked).toHaveLength(0)
  await expect(page.locator('.tl-detail')).toHaveCount(0)

  await page.mouse.move(a.x + a.width * 0.5, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(a.x + a.width * 0.5 + 1, a.y + a.height / 2)
  await page.mouse.up()
  await expect(page.locator('.tl-detail')).toHaveCount(1)
  expect(state.linked).toHaveLength(0)
})

test('タイムラインから開いた詳細の「関連チケットを追加」には依存の4種も出る（pb-332）', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 900 })
  await mockApi(page, { theme: 'light' })
  await page.goto('/p/fes/timeline')
  await page.locator('.bar[data-seq="414"]').click()
  const pane = page.locator('.tl-detail')
  await expect(pane).toBeVisible()
  await pane.getByRole('button', { name: '+ 追加' }).last().click()
  const dialog = page.getByRole('dialog', { name: '関連チケットを追加' })
  await expect(dialog.locator('.choice-label')).toHaveText([
    '関連', '重複', '自先行', '自後行',
    'FS 自先行', 'FS 自後行', 'SS 自先行', 'SS 自後行', 'FF 自先行', 'FF 自後行', 'SF 自先行', 'SF 自後行',
  ])
})
