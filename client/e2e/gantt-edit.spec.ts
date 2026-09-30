import { expect, test, type Page, type Route } from '@playwright/test'

/**
 * ガントの編集（GuiDesign.md 5.14「編集」。pb-221）。
 *
 * **ドラッグは本物のマウス入力で回す**（`page.mouse` は CDP の `Input.dispatchMouseEvent`
 * を通す。GuiDesign.md 6.8）。合成した PointerEvent を dispatch すると自分のハンドラを
 * 呼ぶだけで、ポインタの捕捉（`setPointerCapture`）もクリックの抑止も通らない。
 *
 * **API はすべてモックする**（gantt.spec.ts と同じ）。「今」は 2026-09-29 22:10 JST、
 * 基準タイムゾーンは Asia/Tokyo、開いたときは日の段階（1日 44px）。**題名は多バイト文字で書く**。
 *
 * 走らせ方：`make build` した client を配っているサーバ（`vite preview` でもよい）に向ける
 * （PB_E2E_BASE_URL）。
 */

const JST = 9 * 3600_000
const D = (m: number, d: number, h = 0, mi = 0) => Date.UTC(2026, m - 1, d, h, mi) - JST
const NOW = D(9, 29, 22, 10)
const PPD = 44

const statuses = [
  { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 },
  { key: 'in_progress', name: '進行中', category: 'in_progress', sort_order: 2 },
  { key: 'done', name: '完了', category: 'done', sort_order: 3 },
]

type T = Record<string, unknown> & { seq: number; start_at: number | null; due_at: number | null; all_day: boolean; version: number }

function tk(seq: number, title: string, o: { s?: number; e?: number; allDay?: boolean } = {}): T {
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type: 'task', title, status: statuses[0],
    priority: null, assignee: null, reporter: null, working_agent: null,
    parent_seq: null, has_children: false, sort_key: `0|${String(seq).padStart(6, '0')}:`,
    staged_at: null, tags: [], sprint: null, estimate_point: null, estimate_hours: null,
    actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: o.s ?? null, due_at: o.e ?? null, all_day: o.allDay ?? true,
    closed_at: null, version: 1, created_at: D(9, 1), updated_at: D(9, 1),
  }
}

let linkId = 0
const ln = (s: number, t: number, type: string) => ({
  id: `01K00000000000000000LNK${String(++linkId).padStart(3, '0')}`, source_seq: s, target_seq: t,
  link_type: type, lag_days: 0, origin: 'human',
})

function sample() {
  return {
    tickets: [
      tk(301, '要件を固める', { s: D(9, 24), e: D(9, 26) }),
      tk(302, '設計を書く', { s: D(9, 28), e: D(10, 2) }),
      tk(303, '実装する', { s: D(10, 1), e: D(10, 7) }),
      tk(304, '試験する', { s: D(10, 8), e: D(10, 11) }),
      tk(305, '日付の無い作業'),
      tk(306, '受け入れ（終了日だけ）', { e: D(10, 13) }),
    ],
    // 開いたときに見えている範囲（およそ 9/22〜10/18）に収める。
    // 302 → 303 は FS で、303 が 302 の終わり（10/2）より前（10/1）に始まる＝違反
    links: [ln(301, 302, 'FS'), ln(302, 303, 'FS'), ln(303, 304, 'FS')],
  }
}

interface World {
  tickets: T[]
  links: ReturnType<typeof ln>[]
  patches: { seq: number; ifMatch: string | null; body: Record<string, unknown> }[]
  posts: { seq: number; body: Record<string, unknown> }[]
  gets: number[]
  /** 次の PATCH を 409 にし、GET はこの値を返す（他の人が先に更新した） */
  conflict?: { seq: number; fresh: Partial<T> }
  /** POST をこの相手のとき 422 link_cycle にする（絞り込みで見えない輪） */
  serverCycle?: number
}

async function reply(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

function detailOf(t: T) {
  return { ...t, body_md: '', parent: null, epic: null, children: [], dod: [], links: [], references: [], comment_count: 0, execution_mode: 'agent_draft', readiness: null, readiness_note: null, scope: {} }
}

async function mockApi(page: Page, o: { edit?: boolean; snap?: number } = {}): Promise<World> {
  const s = sample()
  const w: World = { tickets: s.tickets, links: s.links, patches: [], posts: [], gets: [] }
  await page.clock.install({ time: NOW })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname
    const method = req.method()
    if (path === '/api/v1/me') {
      return reply(route, {
        actor: {
          id: '01K00000000000000000000000', kind: 'user', display_name: '検証 太郎', email: 'e2e@example.com',
          system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
        },
        permissions: [],
        projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', ...(o.edit === false ? [] : ['ticket.edit'])] }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/demo') {
      return reply(route, {
        key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses }, version: 1,
        settings: o.snap === undefined ? {} : { gantt_snap_minutes: o.snap },
      })
    }
    if (path === '/api/v1/projects/demo/tickets') {
      if (url.searchParams.get('type') === 'epic') return reply(route, { items: [], total: 0, page: 1, per_page: 200, total_pages: 0 })
      return reply(route, { items: w.tickets, links: w.links, total: w.tickets.length, page: 1, per_page: 5000, total_pages: 1 })
    }
    const one = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)$/.exec(path)
    if (one) {
      const seq = Number(one[1])
      const i = w.tickets.findIndex((x) => x.seq === seq)
      if (i < 0) return reply(route, {}, 404)
      if (method === 'PATCH') {
        const body = req.postDataJSON() as Record<string, unknown>
        w.patches.push({ seq, ifMatch: req.headers()['if-match'] ?? null, body })
        if (w.conflict?.seq === seq) {
          w.tickets[i] = { ...w.tickets[i]!, ...w.conflict.fresh, version: w.tickets[i]!.version + 1 }
          w.conflict = undefined
          return reply(route, { error: { code: 'conflict', message: '他の人が先に更新しました', details: [] } }, 409)
        }
        w.tickets[i] = { ...w.tickets[i]!, ...body, version: w.tickets[i]!.version + 1 } as T
        return reply(route, detailOf(w.tickets[i]!))
      }
      w.gets.push(seq)
      return reply(route, detailOf(w.tickets[i]!))
    }
    const lk = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)\/links$/.exec(path)
    if (lk && method === 'POST') {
      const seq = Number(lk[1])
      const body = req.postDataJSON() as Record<string, unknown>
      w.posts.push({ seq, body })
      if (w.serverCycle === body['target_seq']) {
        return reply(route, { error: { code: 'validation_failed', message: '入力内容に誤りがあります', details: [{ field: 'target_seq', code: 'link_cycle', message: '依存が輪になるため追加できません' }] } }, 422)
      }
      const l = ln(seq, body['target_seq'] as number, body['link_type'] as string)
      w.links.push(l)
      return reply(route, { ...l, direction: 'outgoing' }, 201)
    }
    if (path.endsWith('/sprints') || path.endsWith('/tags') || path.endsWith('/comments') || path.endsWith('/activity') || path.endsWith('/links')) {
      return reply(route, { items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
    }
    if (path.endsWith('/calendar/days')) return reply(route, { days: [] })
    await route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })
  return w
}

async function openGantt(page: Page) {
  // 本体の幅を取り、帯が左右の端の自動送り（32px）に掛からないようにする
  await page.setViewportSize({ width: 1800, height: 900 })
  await page.goto('/p/demo/gantt')
  await expect(page.getByRole('heading', { level: 1, name: 'ガント' })).toBeVisible()
  await expect(page.locator('.gantt-svg rect.b').first()).toBeAttached()
}

/**
 * チケットの帯の、ページ上の位置。**帯の右の ID の文字から行を引き、同じ行のバー
 * （またはマイルストーン）を探す**——render.ts が出す DOM をそのまま読む。
 */
async function barOf(page: Page, seq: number): Promise<{ x0: number; x1: number; y: number }> {
  return page.evaluate((id) => {
    const svg = document.querySelector('.gantt-svg') as SVGSVGElement
    const r = svg.getBoundingClientRect()
    const lbl = [...svg.querySelectorAll('text.lbl')].find((t) => t.querySelector('.lid')?.textContent === id)
    if (!lbl) throw new Error(`label ${id} not found`)
    const cy = Number(lbl.getAttribute('y')) - 3.5
    const bar = [...svg.querySelectorAll('g.bar:not(.ghost g) rect.b')].find((b) => Math.abs(Number(b.getAttribute('y')) + 6 - cy) < 0.6)
    if (bar) {
      const x = Number(bar.getAttribute('x'))
      return { x0: r.left + x, x1: r.left + x + Number(bar.getAttribute('width')), y: r.top + cy }
    }
    const dot = [...svg.querySelectorAll('g.bar circle.ms-dot')].find((c) => Math.abs(Number(c.getAttribute('cy')) - cy) < 0.6)
    if (!dot) throw new Error(`bar ${id} not found`)
    const x = r.left + Number(dot.getAttribute('cx'))
    return { x0: x, x1: x, y: r.top + cy }
  }, `demo-${seq}`)
}

/** 行（ツリー側）の縦の中心。日付の無い行の上をなぞるのに使う */
async function rowY(page: Page, seq: number): Promise<number> {
  const b = (await page.locator(`.gantt-tr[data-seq="${seq}"]`).boundingBox())!
  return b.y + b.height / 2
}

async function drag(page: Page, from: { x: number; y: number }, to: { x: number; y: number }, o: { beforeUp?: () => Promise<void> } = {}) {
  await page.mouse.move(from.x, from.y)
  await page.mouse.down()
  await page.mouse.move(from.x + (to.x - from.x) / 2, from.y + (to.y - from.y) / 2, { steps: 4 })
  await page.mouse.move(to.x, to.y, { steps: 4 })
  if (o.beforeUp) await o.beforeUp()
  await page.mouse.up()
}

test('バーを動かすと日に吸着し、開始と終了を同じだけずらす', async ({ page }) => {
  const w = await mockApi(page)
  await openGantt(page)
  const b = await barOf(page, 302)
  // 2日と少し（吸着で2日ちょうどに寄る）
  await drag(page, { x: (b.x0 + b.x1) / 2, y: b.y }, { x: (b.x0 + b.x1) / 2 + 2 * PPD + 9, y: b.y + 3 })
  await expect.poll(() => w.patches.length).toBe(1)
  expect(w.patches[0]).toMatchObject({ seq: 302, ifMatch: '"1"', body: { start_at: D(9, 30), due_at: D(10, 4), all_day: true } })
  // 応答の後は新しい位置に描き、詳細は開かない（ドラッグの後の click は捨てる）
  await expect.poll(async () => (await barOf(page, 302)).x0).toBeCloseTo(b.x0 + 2 * PPD, 0)
  await expect(page).toHaveURL(/\/p\/demo\/gantt/)
})

test('端をつかむと開始だけ・終了だけを変え、反対の端は越えない', async ({ page }) => {
  const w = await mockApi(page)
  await openGantt(page)
  let b = await barOf(page, 302)
  await drag(page, { x: b.x1 - 2, y: b.y }, { x: b.x1 - 2 + PPD, y: b.y })
  await expect.poll(() => w.patches.length).toBe(1)
  expect(w.patches[0]!.body).toMatchObject({ start_at: D(9, 28), due_at: D(10, 3) })

  b = await barOf(page, 302)
  // 開始を終了より右へ引いても、1日（1単位）残して止まる
  await drag(page, { x: b.x0 + 2, y: b.y }, { x: b.x1 + 3 * PPD, y: b.y })
  await expect.poll(() => w.patches.length).toBe(2)
  expect(w.patches[1]!.body).toMatchObject({ start_at: D(10, 2), due_at: D(10, 3) })
})

test('日付の無い行をなぞると、なぞった日を包むバーを作る', async ({ page }) => {
  const w = await mockApi(page)
  await openGantt(page)
  const ref = await barOf(page, 304) // 10/8 の頭
  const y = await rowY(page, 305)
  // 10/10 の昼 → 10/8 の昼（右から左へなぞっても同じ）
  await drag(page, { x: ref.x0 + 2.5 * PPD, y }, { x: ref.x0 + 0.5 * PPD, y })
  await expect.poll(() => w.patches.length).toBe(1)
  expect(w.patches[0]).toMatchObject({ seq: 305, body: { start_at: D(10, 8), due_at: D(10, 11), all_day: true } })
})

test('マイルストーンは持っている端だけを動かす', async ({ page }) => {
  const w = await mockApi(page)
  await openGantt(page)
  const m = await barOf(page, 306)
  await drag(page, { x: m.x0 - 10, y: m.y }, { x: m.x0 - 10 - 3 * PPD, y: m.y })
  await expect.poll(() => w.patches.length).toBe(1)
  expect(w.patches[0]!.body).toMatchObject({ start_at: null, due_at: D(10, 10) })
})

test('単位が1時間なら時刻に吸着し、0時から外れた終日は時刻付きになる（札で予告する）', async ({ page }) => {
  const w = await mockApi(page, { snap: 60 })
  await openGantt(page)
  const b = await barOf(page, 302)
  const mid = (b.x0 + b.x1) / 2
  // 3時間と少し右へ（1日 44px なので 1時間 ≒ 1.83px）
  await drag(page, { x: mid, y: b.y }, { x: mid + (3.2 * PPD) / 24, y: b.y }, {
    beforeUp: async () => {
      await expect(page.locator('.gantt-svg .dtag-t')).toContainText('時刻付きになります')
    },
  })
  await expect.poll(() => w.patches.length).toBe(1)
  expect(w.patches[0]!.body).toMatchObject({ start_at: D(9, 28, 3), due_at: D(10, 2, 3), all_day: false })
})

test('Alt を押している間は吸着を外す', async ({ page }) => {
  const w = await mockApi(page)
  await openGantt(page)
  const b = await barOf(page, 302)
  const mid = (b.x0 + b.x1) / 2
  await page.mouse.move(mid, b.y)
  await page.mouse.down()
  await page.keyboard.down('Alt')
  await page.mouse.move(mid + PPD * 0.5, b.y, { steps: 5 })
  await page.keyboard.up('Alt')
  // Alt を離すと、同じ位置で日に吸着し直す（半日なので最寄りは翌日）
  await expect(page.locator('.gantt-svg .dtag-t')).not.toContainText('時刻付き')
  await page.keyboard.down('Alt')
  await page.mouse.move(mid + PPD * 0.5 + 1, b.y)
  await page.mouse.up()
  await page.keyboard.up('Alt')
  await expect.poll(() => w.patches.length).toBe(1)
  const body = w.patches[0]!.body
  expect(body['all_day']).toBe(false)
  // 1分に丸め、長さ（4日）を保つ。半日ほど右（日の頭ではない）
  const s = body['start_at'] as number
  expect(s % 60_000).toBe(0)
  expect(s - D(9, 28)).toBeGreaterThan(10 * 3600_000)
  expect(s - D(9, 28)).toBeLessThan(14 * 3600_000)
  expect((body['due_at'] as number) - s).toBe(4 * 86_400_000)
})

test('Esc で取り消すと何も送らず、動かさずに離せば詳細を開く', async ({ page }) => {
  const w = await mockApi(page)
  await openGantt(page)
  const b = await barOf(page, 302)
  const mid = (b.x0 + b.x1) / 2
  await page.mouse.move(mid, b.y)
  await page.mouse.down()
  await page.mouse.move(mid + 3 * PPD, b.y, { steps: 5 })
  await page.keyboard.press('Escape')
  await page.mouse.up()
  // 描き直しは次のフレームなので、元の位置に戻るのを待つ
  await expect.poll(async () => (await barOf(page, 302)).x0).toBeCloseTo(b.x0, 0)
  expect(w.patches).toHaveLength(0)
  await expect(page).toHaveURL(/\/p\/demo\/gantt/)
  // 3px 未満の動きはクリック
  await page.mouse.move(mid, b.y)
  await page.mouse.down()
  await page.mouse.move(mid + 1, b.y)
  await page.mouse.up()
  await expect(page).toHaveURL(/\/p\/demo\/tickets\/302\?.*from=gantt/)
  expect(w.patches).toHaveLength(0)
})

/** 取っ手の位置（帯の両端の外側 9px） */
async function handle(page: Page, seq: number, end: 'S' | 'F'): Promise<{ x: number; y: number }> {
  const b = await barOf(page, seq)
  // ポインタを帯に載せると取っ手が出る
  await page.mouse.move((b.x0 + b.x1) / 2, b.y)
  await expect(page.locator('.gantt-svg circle.hdl').first()).toBeAttached()
  return { x: end === 'S' ? b.x0 - 9 : b.x1 + 9, y: b.y }
}

test('端から端へ引くと、どの端からどの端かで4種の依存を作る', async ({ page }) => {
  const w = await mockApi(page)
  await openGantt(page)
  const cases: [number, 'S' | 'F', number, 'left' | 'right', string][] = [
    [301, 'F', 304, 'left', 'FS'],
    [301, 'S', 304, 'left', 'SS'],
    [301, 'F', 304, 'right', 'FF'],
    [301, 'S', 304, 'right', 'SF'],
  ]
  for (const [src, from, dst, side, type] of cases) {
    const h = await handle(page, src, from)
    const t = await barOf(page, dst)
    const x = side === 'left' ? t.x0 + (t.x1 - t.x0) * 0.2 : t.x0 + (t.x1 - t.x0) * 0.8
    await drag(page, h, { x, y: t.y }, {
      beforeUp: async () => {
        await expect(page.locator('.gantt-svg .dtag-t')).toHaveText(`${type} demo-${src} → demo-${dst}`)
        await expect(page.locator('.gantt-svg circle.lring')).toHaveCount(1)
      },
    })
    await expect.poll(() => w.posts.length).toBe(cases.findIndex((c) => c[4] === type) + 1)
    expect(w.posts.at(-1)).toEqual({ seq: src, body: { target_seq: dst, link_type: type, lag_days: 0 } })
    // 一覧を取り直して線が増える
    await expect.poll(() => page.locator('.gantt-svg g.dep').count()).toBe(3 + w.posts.length)
  }
})

test('輪になる相手・同じ種別がある相手には落とせず、離しても送らない', async ({ page }) => {
  const w = await mockApi(page)
  await openGantt(page)
  // 304 → 302 は 302 → 303 → 304 と輪になる
  const h = await handle(page, 304, 'F')
  const t = await barOf(page, 302)
  await drag(page, h, { x: t.x0 + 3, y: t.y }, {
    beforeUp: async () => {
      await expect(page.locator('.gantt-svg .dtag-t.no')).toHaveText('依存が輪になるため引けません')
      expect(await page.locator('.gantt-chart').evaluate((el) => el.style.cursor)).toBe('not-allowed')
    },
  })
  // 302 → 303 の FS は既にある
  const h2 = await handle(page, 302, 'F')
  const t2 = await barOf(page, 303)
  await drag(page, h2, { x: t2.x0 + 3, y: t2.y }, {
    beforeUp: async () => {
      await expect(page.locator('.gantt-svg .dtag-t.no')).toHaveText('FS の依存はすでにあります')
    },
  })
  expect(w.posts).toHaveLength(0)
})

test('画面から見えない輪はサーバが弾き、通知の行に理由を出す', async ({ page }) => {
  const w = await mockApi(page)
  w.serverCycle = 306
  await openGantt(page)
  const h = await handle(page, 301, 'F')
  const t = await barOf(page, 306)
  await drag(page, h, { x: t.x0 + 2, y: t.y })
  await expect(page.locator('.gantt-edit-notice')).toContainText('依存が輪になるため追加できません')
  expect(w.posts).toHaveLength(1)
})

test('他の人が先に更新していたら上書きせず、最新の位置に戻して知らせる', async ({ page }) => {
  const w = await mockApi(page)
  w.conflict = { seq: 302, fresh: { start_at: D(10, 5), due_at: D(10, 6) } }
  await openGantt(page)
  const b = await barOf(page, 302)
  await drag(page, { x: (b.x0 + b.x1) / 2, y: b.y }, { x: (b.x0 + b.x1) / 2 + PPD, y: b.y })
  await expect(page.locator('.gantt-edit-notice')).toContainText('demo-302 は他の人が先に更新していました。最新の予定を表示しています')
  expect(w.patches).toHaveLength(1)
  expect(w.gets).toContain(302)
  // バーは他の人が入れた位置（10/5）に描かれる
  await expect.poll(async () => (await barOf(page, 302)).x0).toBeCloseTo(b.x0 + 7 * PPD, 0)
  // ✕ で消える
  await page.locator('.gantt-edit-notice .notice-close').click()
  await expect(page.locator('.gantt-edit-notice')).toHaveCount(0)
})

test('依存に反した線は危険色と ⚠ で描き、ドラッグ中も描き直す', async ({ page }) => {
  await mockApi(page)
  await openGantt(page)
  // 302（〜10/2）FS 303（10/1〜）は反している
  await expect(page.locator('.gantt-svg g.dep.bad')).toHaveCount(1)
  await expect(page.locator('.gantt-svg g.dep.bad .d-tagt')).toHaveText('⚠')
  const b = await barOf(page, 303)
  await drag(page, { x: (b.x0 + b.x1) / 2, y: b.y }, { x: (b.x0 + b.x1) / 2 + 2 * PPD, y: b.y }, {
    beforeUp: async () => {
      // 303 を 10/3 へ送ると 302 → 303 は直り、303（〜10/9）→ 304（10/8〜）が反する
      await expect(page.locator('.gantt-svg g.dep.bad')).toHaveCount(1)
      await expect(page.locator('.gantt-svg g.ghost')).toHaveCount(1)
    },
  })
})

test('ticket.edit が無ければ取っ手も出さず、動かせない', async ({ page }) => {
  const w = await mockApi(page, { edit: false })
  await openGantt(page)
  const b = await barOf(page, 302)
  await page.mouse.move((b.x0 + b.x1) / 2, b.y)
  await expect(page.locator('.gantt-svg circle.hdl')).toHaveCount(0)
  expect(await page.locator('.gantt-chart').evaluate((el) => el.style.cursor)).toBe('')
  await drag(page, { x: (b.x0 + b.x1) / 2, y: b.y }, { x: (b.x0 + b.x1) / 2 + 2 * PPD, y: b.y })
  expect(w.patches).toHaveLength(0)
})
