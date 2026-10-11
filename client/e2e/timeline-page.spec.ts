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

async function mockApi(page: Page, opts: { theme: 'light' | 'dark'; views?: Record<string, boolean>; epics?: number[]; role?: 'admin' | 'viewer'; outside?: boolean }) {
  let items = baseItems()
  let etagN = 1
  const settings = { views: opts.views ?? { timeline: true }, timeline_snap_seconds: 60, timeline_epics: opts.epics ?? [301, 302, 303] }
  const perms = opts.role === 'viewer' ? ['project.view', 'ticket.view'] : ['project.view', 'ticket.view', 'ticket.edit', 'project.edit']
  const state = { rescheduled: 0, changeNext: false }
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
        const shown = items.filter((t) => rowSeqs.includes(t.epic_seq))
        return reply({
          items: shown,
          epics: epics.filter((e) => rowSeqs.includes(e.seq)),
          links: [{ id: 'l1', source_seq: 411, target_seq: 412, link_type: 'FS', lag_days: 0, origin: 'human' }],
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
    if (path.startsWith('/api/v1/projects/fes/tags')) return reply({ items: [] })
    return reply({ items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
  return state
}

const shotDir = process.env.PB_SHOT_DIR

for (const theme of ['light', 'dark'] as const) {
  for (const width of [1280, 1920]) {
    test(`行・レーン・バー・当初計画を描く（${theme} ${width}px）`, async ({ page }) => {
      const errors: string[] = []
      page.on('pageerror', (e) => errors.push(e.message))
      await page.setViewportSize({ width, height: 800 })
      await mockApi(page, { theme })
      await page.goto('/p/fes/timeline')
      await expect(page.locator('.tl-row')).toHaveCount(3)

      // 体育館：本番 + 裏2本（椅子並べと養生が重なる）
      const gym = page.locator('.tl-row').first()
      await expect(gym.locator('.lanetag')).toHaveText(['本番', '裏×2'])
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

test('載せるエピックが無ければ選ぶ導線。見るだけの人は依頼の文', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 700 })
  await mockApi(page, { theme: 'light', epics: [], role: 'viewer' })
  await page.goto('/p/fes/timeline')
  await expect(page.getByText('タイムラインに載せるエピックを選んでください')).toBeVisible()
  await expect(page.getByText('プロジェクト管理者に依頼してください')).toBeVisible()
  await expect(page.getByRole('button', { name: '計画を保存' })).toHaveCount(0)
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

test('対象から外したエピック・消したエピックも、版を重ねる・送るときは版の行で読める', async ({ page }) => {
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
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-mock-outside-overlay.png` })

  // 版を送るとき：版のバーとして描く
  await page.getByRole('button', { name: '版を送る', exact: true }).click()
  await expect(page.locator('.tl-notice')).toContainText('版を送る 1/2')
  await expect(page.locator('.bar[data-seq="420"]')).toHaveCount(1)
  await expect(page.locator('.bar[data-seq="420"]')).toHaveClass(/st-plan/)
  await expect(page.locator('.bar[data-seq="431"]')).toHaveCount(1)
  if (shotDir) await page.screenshot({ path: `${shotDir}/timeline-mock-outside-step.png` })
  await page.locator('.tl-notice button', { hasText: '終える' }).click()

  // 見る人の絞り込み（parent）で外した行は、版の表示でも出さない
  await page.goto('/p/fes/timeline?zoom=hour&parent=301')
  await expect(page.locator('.tl-row')).toHaveCount(1)
  await expect(page.locator('.tl-row', { hasText: '中庭ステージ' })).toHaveCount(0)
  await page.getByRole('button', { name: '版を送る', exact: true }).click()
  await expect(page.locator('.tl-notice')).toContainText('版を送る 1/2')
  await expect(page.locator('.tl-row')).toHaveCount(1)
  await expect(page.locator('.bar[data-seq="420"]')).toHaveCount(0)
})

test('載せるエピックが空でも、保存した版と比較の影は読める。版も行も無ければ空状態', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  await mockApi(page, { theme: 'light', epics: [], outside: true })
  // 重ねるとき（既定は最後に保存した版）
  await page.goto('/p/fes/timeline?zoom=hour')
  await expect(page.locator('.tl-row')).toHaveCount(3)
  await expect(page.locator('.tl-row .gonelbl', { hasText: '旧ステージの前説' })).toBeVisible()
  // 何も選んでいないことは、行の上の帯で伝え続ける（全体を隠さない）
  await expect(page.locator('.tl-select-band')).toContainText('タイムラインに載せるエピックを選んでください')
  // 版を送るとき
  await page.getByRole('button', { name: '版を送る', exact: true }).click()
  await expect(page.locator('.tl-notice')).toContainText('版を送る 1/2')
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
  await expect(page.getByText('タイムラインに載せるエピックを選んでください')).toBeVisible()
  await expect(page.locator('.tl-body')).toBeHidden()
})

test('設定に残った不在のエピック番号も、版の行の情報で読める。parent で外せば出さない', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 800 })
  await mockApi(page, { theme: 'light', epics: [301, 303, 309], outside: true })
  await page.goto('/p/fes/timeline?zoom=hour')
  await page.getByRole('button', { name: '版を送る', exact: true }).click()
  await expect(page.locator('.tl-notice')).toContainText('版を送る 1/2')
  await expect(page.locator('.bar[data-seq="431"]')).toHaveCount(1)
  await expect(page.locator('.tl-row', { hasText: '旧ステージ' }).locator('.tl-outside')).toHaveCount(1)
  await page.goto('/p/fes/timeline?zoom=hour&parent=301')
  await page.getByRole('button', { name: '版を送る', exact: true }).click()
  await expect(page.locator('.tl-notice')).toContainText('版を送る 1/2')
  await expect(page.locator('.bar[data-seq="431"]')).toHaveCount(0)
  await expect(page.locator('.tl-row')).toHaveCount(1)
})
