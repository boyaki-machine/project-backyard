import { expect, test, type Page, type Route, type TestInfo } from '@playwright/test'

/**
 * 集中モード（GuiDesign.md 2.3.2。pb-222）。
 *
 * **操作は本物のマウスとキーで回す**（`page.mouse` / `page.keyboard` は CDP の Input を通す）。
 * 左端に「とどまる」の判定は時間で決まるので、待ちは `waitForTimeout` で実際に置く。
 *
 * **API はすべてモックする**（gantt.spec.ts と同じ）。題名は多バイト文字で書く。
 * 走らせ方：`make build` した client を配っているサーバ（`vite preview` でもよい）に向ける。
 */

const JST = 9 * 3600_000
const D = (m: number, d: number) => Date.UTC(2026, m - 1, d) - JST
const NOW = Date.UTC(2026, 8, 29, 13, 10)

const statuses = [
  { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 },
  { key: 'done', name: '完了', category: 'done', sort_order: 2 },
]

function tk(seq: number, title: string, s: number | null, e: number | null) {
  return {
    id: `01K0000000000000000000${String(seq).padStart(4, '0')}`, seq, type: 'task', title, status: statuses[0],
    priority: null, assignee: null, reporter: null, working_agent: null, parent_seq: null, has_children: false,
    sort_key: `0|${String(seq).padStart(6, '0')}:`, staged_at: null, tags: [], sprint: null,
    estimate_point: null, estimate_hours: null, actual_hours: null, actual_point: null, actual_point_version: null,
    start_at: s, due_at: e, all_day: true, closed_at: null, version: 1, created_at: D(9, 1), updated_at: D(9, 1),
  }
}

const tickets = [
  tk(401, '集中して計画を立てる', D(9, 28), D(10, 2)),
  tk(402, '時間軸を広く見る', D(10, 1), D(10, 6)),
  tk(403, '日付の無い作業', null, null),
]

async function reply(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockApi(page: Page, theme: 'light' | 'dark' = 'light') {
  await page.clock.install({ time: NOW })
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path === '/api/v1/me') {
      return reply(route, {
        actor: {
          id: '01K00000000000000000000000', kind: 'user', display_name: '検証 花子', email: 'e2e@example.com',
          system_role: 'administrator', locale: 'ja', timezone: 'Asia/Tokyo', theme, hue: 'blue', must_change_password: false,
        },
        permissions: [],
        projects: [{ key: 'demo', name: 'Demo', role: 'project_admin', permissions: ['project.view', 'ticket.view', 'ticket.edit'] }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/demo') {
      return reply(route, { key: 'demo', name: 'Demo', timezone: 'Asia/Tokyo', members: [], workflow: { statuses }, settings: {}, version: 1 })
    }
    if (path === '/api/v1/projects/demo/tickets') {
      if (url.searchParams.get('type') === 'epic') return reply(route, { items: [], total: 0, page: 1, per_page: 200, total_pages: 0 })
      return reply(route, { items: tickets, links: [], total: tickets.length, page: 1, per_page: 200, total_pages: 1 })
    }
    const one = /^\/api\/v1\/projects\/demo\/tickets\/(\d+)$/.exec(path)
    if (one) {
      const t = tickets.find((x) => x.seq === Number(one[1]))
      if (!t) return reply(route, {}, 404)
      return reply(route, { ...t, body_md: '', parent: null, epic: null, children: [], dod: [], links: [], references: [], comment_count: 0, execution_mode: 'agent_draft', readiness: null, readiness_note: null, scope: {} })
    }
    if (path.endsWith('/calendar/days')) return reply(route, { days: [] })
    // 一覧の形で空を返す（スプリント・タグ・コメント・履歴・関連など）
    return reply(route, { items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
}

async function openGantt(page: Page, width = 1440, height = 900) {
  await page.setViewportSize({ width, height })
  await page.goto('/p/demo/gantt')
  await expect(page.getByRole('heading', { level: 1, name: 'ガント' })).toBeVisible()
  await expect(page.locator('.gantt-svg rect.b').first()).toBeAttached()
}

// 通常のメニュー（重ねたメニューは消えるアニメーションの間、同時に居ることがある）
const menu = (page: Page) => page.locator('nav.menu.pane')
const peekMenu = (page: Page) => page.locator('.peek nav.menu')
const focusButton = (page: Page) => page.locator('.gantt-focus')
const contentLeft = async (page: Page) => (await page.locator('main.content').boundingBox())!.x

async function shot(page: Page, testInfo: TestInfo, name: string) {
  const path = testInfo.outputPath(`${name}.png`)
  await page.screenshot({ path })
  await testInfo.attach(name, { path, contentType: 'image/png' })
}

test('ボタンで入り、メニューを 0px まで畳む。Esc で戻る', async ({ page }) => {
  await mockApi(page)
  await openGantt(page)
  expect(await contentLeft(page)).toBeGreaterThan(0)
  await focusButton(page).click()
  await expect(focusButton(page)).toHaveAttribute('aria-pressed', 'true')
  await expect(menu(page)).toHaveCount(0)
  expect(await contentLeft(page)).toBe(0)
  await page.keyboard.press('Escape')
  await expect(menu(page)).toBeVisible()
  await expect(focusButton(page)).toHaveAttribute('aria-pressed', 'false')
})

test('Shift + [ で入り、もう一度で戻る', async ({ page }) => {
  await mockApi(page)
  await openGantt(page)
  await page.locator('.gantt-chart').click({ position: { x: 5, y: 700 } })
  await page.keyboard.press('Shift+BracketLeft')
  await expect(menu(page)).toHaveCount(0)
  await page.keyboard.press('Shift+BracketLeft')
  await expect(menu(page)).toBeVisible()
  // [ だけなら折りたたみ（集中モードには入らない）
  await page.keyboard.press('BracketLeft')
  await expect(menu(page)).toBeVisible()
  await page.keyboard.press('BracketLeft')
})

/**
 * 左端の記録をページの中に取る。**待ち時間で測らない**——並列で走ると `waitForTimeout` が
 * 伸び、200ms の境目を試験の側が越えてしまう。左端に入った時刻・出た時刻・メニューが
 * 出た時刻を同じ時計（`performance.now`）で残し、差で判定する。
 */
async function recordEdge(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __edge: { enter: number | null; leave: number | null; peek: number | null; down: boolean }[] }
    w.__edge = []
    let cur: (typeof w.__edge)[number] | null = null
    document.addEventListener('pointermove', (e) => {
      const at = e.clientX <= 8
      if (at && !cur) w.__edge.push((cur = { enter: performance.now(), leave: null, peek: null, down: e.buttons !== 0 }))
      if (!at && cur) {
        cur.leave = performance.now()
        cur = null
      }
    }, true)
    new MutationObserver(() => {
      const last = w.__edge.at(-1)
      if (document.querySelector('.peek') && last && last.peek === null) last.peek = performance.now()
    }).observe(document.body, { childList: true, subtree: true })
  })
}

const edgeLog = (page: Page) =>
  page.evaluate(() => (window as unknown as { __edge: { enter: number; leave: number | null; peek: number | null; down: boolean }[] }).__edge)

test('左端 8px に 200ms とどまると重ねて出し、離れると隠す。通り過ぎ・ドラッグ中は出さない', async ({ page }) => {
  await mockApi(page)
  await openGantt(page)
  await focusButton(page).click()
  await page.mouse.move(600, 500)
  await recordEdge(page)

  // 1. 素早く通り過ぎる
  await page.mouse.move(3, 500)
  await page.mouse.move(120, 500)
  await page.waitForTimeout(400)
  // 2. ボタンを押したまま左端へ寄せる（ガントのドラッグ）。空いた所を押す（帯の無い行の右）
  await page.mouse.move(700, 850)
  await page.mouse.down()
  await page.mouse.move(3, 850, { steps: 4 })
  await page.waitForTimeout(400)
  await page.mouse.up()
  await page.mouse.move(600, 500)
  await page.waitForTimeout(100)
  // 3. とどまる
  await page.mouse.move(3, 500)
  await expect(peekMenu(page)).toBeVisible()

  const log = await edgeLog(page)
  expect(log).toHaveLength(3)
  const [pass, dragged, dwell] = log as [typeof log[number], typeof log[number], typeof log[number]]
  // 通り過ぎた回は、とどまった時間が 200ms 未満で、メニューは出ていない
  if (pass.leave! - pass.enter < 200) expect(pass.peek).toBeNull()
  // ボタンを押したまま寄せた回は、400ms とどまっても出ていない
  expect(dragged.down).toBe(true)
  expect(dragged.peek).toBeNull()
  // とどまった回は、入ってから 200ms 以上たってから出た
  expect(dwell.peek! - dwell.enter).toBeGreaterThanOrEqual(190)

  // 出ても集中モードは続く（コンテンツは 0px から）
  expect(await contentLeft(page)).toBe(0)
  await expect(focusButton(page)).toHaveAttribute('aria-pressed', 'true')
  // メニューの上に居る間は出たまま、離れると隠れる
  await page.mouse.move(100, 500)
  await page.waitForTimeout(500)
  await expect(peekMenu(page)).toBeVisible()
  await page.mouse.move(900, 500)
  await expect(peekMenu(page)).toHaveCount(0)
  await expect(focusButton(page)).toHaveAttribute('aria-pressed', 'true')
})

/**
 * **メニューは静止したポインタの下へスライドして現れる**ので、ブラウザは「入った」を
 * 記録しない。上を通らずに離れても隠れること（pb-233）。
 */
test('出たメニューの上で動かさずに離れても隠す。外から 300ms 以内に戻れば出たまま', async ({ page }) => {
  await mockApi(page)
  await openGantt(page)
  await focusButton(page).click()
  await page.mouse.move(600, 500)

  await page.mouse.move(3, 500)
  await expect(peekMenu(page)).toBeVisible()
  await page.waitForTimeout(400)
  await page.mouse.move(900, 500)
  await expect(peekMenu(page)).toHaveCount(0, { timeout: 1000 })
  await expect(focusButton(page)).toHaveAttribute('aria-pressed', 'true')

  // 外へ出てすぐ戻る：隠す待ちが止まる（スライドが終わってから x=100 に載せる）
  await page.mouse.move(3, 500)
  await expect(peekMenu(page)).toBeVisible()
  await page.waitForTimeout(250)
  await page.mouse.move(100, 500)
  await page.mouse.move(900, 500)
  await page.mouse.move(100, 500)
  await page.waitForTimeout(600)
  await expect(peekMenu(page)).toBeVisible()
  await page.mouse.move(900, 500)
  await expect(peekMenu(page)).toHaveCount(0, { timeout: 1000 })
})

test('Esc はドラッグの取り消しに先に使われ、集中モードは続く', async ({ page }) => {
  await mockApi(page)
  await openGantt(page)
  await focusButton(page).click()
  // SVG は毎フレーム描き直すので、要素を掴まず位置を数値で読む
  const b = await page.evaluate(() => {
    const r = document.querySelector('.gantt-svg rect.b')!.getBoundingClientRect()
    return { x: r.left, y: r.top, width: r.width, height: r.height }
  })
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2)
  await page.mouse.down()
  await page.mouse.move(b.x + b.width / 2 + 60, b.y + b.height / 2, { steps: 4 })
  await page.keyboard.press('Escape')
  await page.mouse.up()
  await page.waitForTimeout(100)
  await expect(menu(page)).toHaveCount(0)
  // 使われなかった Esc で戻る
  await page.keyboard.press('Escape')
  await expect(menu(page)).toBeVisible()
})

test('リロードで解除し、保存しない', async ({ page }) => {
  await mockApi(page)
  await openGantt(page)
  await focusButton(page).click()
  await expect(menu(page)).toHaveCount(0)
  const stored = await page.evaluate(() => Object.keys(localStorage).filter((k) => /focus/i.test(k)))
  expect(stored).toEqual([])
  await page.reload()
  await expect(page.locator('.gantt-svg rect.b').first()).toBeAttached()
  await expect(menu(page)).toBeVisible()
})

test('ガントで詳細を開いても続き、別の画面へ移ると解除する', async ({ page }) => {
  await mockApi(page)
  await openGantt(page)
  await focusButton(page).click()
  await page.locator('.gantt-tr', { hasText: 'demo-401' }).click()
  await expect(page).toHaveURL(/\/p\/demo\/tickets\/401\?.*from=gantt/)
  await expect(page.locator('.gantt-detail')).toBeVisible()
  await expect(menu(page)).toHaveCount(0)
  // 左端から出したメニューでバックログへ
  await page.mouse.move(3, 400)
  await expect(peekMenu(page)).toBeVisible()
  await peekMenu(page).getByRole('link', { name: 'バックログ' }).click()
  await expect(page).toHaveURL(/\/p\/demo\/backlog/)
  await expect(page.locator('.peek')).toHaveCount(0)
  await expect(menu(page)).toBeVisible()
  expect(await contentLeft(page)).toBeGreaterThan(0)
})

test('768px 未満ではボタンを出さず、Shift + [ も効かない。狭めたら解除する', async ({ page }) => {
  await mockApi(page)
  await openGantt(page, 1024, 768)
  await focusButton(page).click()
  await expect(menu(page)).toHaveCount(0)
  await page.setViewportSize({ width: 700, height: 800 })
  await expect(focusButton(page)).toHaveCount(0)
  // 浮遊の ☰ が戻っている（解除された）
  await expect(page.locator('.floating')).toBeVisible()
  await page.locator('.gantt-chart').click({ position: { x: 5, y: 600 } })
  await page.keyboard.press('Shift+BracketLeft')
  await expect(page.locator('.floating')).toBeVisible()
  await page.setViewportSize({ width: 1024, height: 768 })
  await expect(menu(page)).toBeVisible()
})

test('1000px の窓のバックログは、集中モードで一覧と詳細が並ぶ（2.4 はコンテンツの幅で決まる）', async ({ page }) => {
  await mockApi(page)
  await page.setViewportSize({ width: 1000, height: 800 })
  await page.goto('/p/demo/tickets/401')
  await expect(page.locator('.split-secondary')).toBeVisible()
  // 畳んだレール（56px）のままでは 944px で並ばない
  await expect(page.locator('.split .divider')).toHaveCount(0)
  await expect(page.locator('.split-secondary')).toHaveClass(/full/)
  // 入力欄にフォーカスがあると横取りしないので、外してから押す
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  await page.keyboard.press('Shift+BracketLeft')
  await expect(menu(page)).toHaveCount(0)
  await expect(page.locator('.split .divider')).toHaveCount(1)
  await expect(page.locator('.split-secondary')).not.toHaveClass(/full/)
  // 戻すとまた重ならない形（全幅の詳細）に戻る
  await page.keyboard.press('Shift+BracketLeft')
  await expect(page.locator('.split .divider')).toHaveCount(0)
})

test('集中モードの見え方をライト／ダークと画面幅で撮る', async ({ page }, testInfo) => {
  for (const theme of ['light', 'dark'] as const) {
    await mockApi(page, theme)
    for (const [w, h] of [[1440, 900], [1024, 768]] as const) {
      await openGantt(page, w, h)
      await focusButton(page).click()
      await expect(menu(page)).toHaveCount(0)
      // 押したボタンにポインタが載ったままでも、文字が地と見分けられる
      const [fg, bg] = await focusButton(page).evaluate((el) => [getComputedStyle(el).color, getComputedStyle(el).backgroundColor])
      expect(fg).not.toBe(bg)
      await page.waitForTimeout(200)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      await shot(page, testInfo, `focus-${theme}-${w}`)
      await page.mouse.move(3, 400)
      await expect(peekMenu(page)).toBeVisible()
      await page.waitForTimeout(200)
      await shot(page, testInfo, `focus-peek-${theme}-${w}`)
      await page.mouse.move(w - 100, 400)
      await page.keyboard.press('Escape')
      await expect(menu(page)).toBeVisible()
    }
    await page.unrouteAll({ behavior: 'ignoreErrors' })
  }
})
