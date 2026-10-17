import { expect, test, type Page } from '@playwright/test'

/**
 * プロジェクト設定「表示するビュー」は変えたその場で保存する（`GuiDesign.md` 5.9.1。pb-319）。
 * API はすべてモックする。サーバは `If-Match` を `version` と突き合わせ、合わなければ 409 を返す。
 *
 * 基本情報の `[保存]` はこのブロックを送らず、このブロックの保存は基本情報の入力中の値を送らない。
 */

const browserErrors = new WeakMap<Page, string[]>()
test.beforeEach(async ({ page }) => {
  const errors: string[] = []
  browserErrors.set(page, errors)
  page.on('pageerror', (error) => errors.push(error.message))
})
test.afterEach(async ({ page }) => {
  expect(browserErrors.get(page) ?? []).toEqual([])
})

const todo = { key: 'todo', name: '未着手', category: 'todo', sort_order: 1 }
const epics = [
  { seq: 301, title: '体育館ステージ' },
  { seq: 302, title: '中庭ステージ' },
]

interface Patch {
  ifMatch: string
  body: { name?: string; settings?: Record<string, unknown> }
}

async function setup(page: Page, opts: { delayMs?: number } = {}) {
  const server = {
    version: 1,
    name: 'Demo',
    settings: { repositories: [{ url: 'https://example.com/old.git' }], keep_me: 'x' } as Record<string, unknown>,
    patches: [] as Patch[],
    inFlight: 0,
    maxInFlight: 0,
    /** 次の PATCH を 409 にする（他の誰かが先に更新した） */
    conflictNext: false,
  }
  const project = () => ({
    key: 'demo', name: server.name, description: null, status: 'active', timezone: 'Asia/Tokyo', version: server.version,
    settings: server.settings, members: [], my_permissions: ['project.view', 'project.edit', 'ticket.view'],
    workflow: { key: 'simple', name: 'シンプル', statuses: [todo] },
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname
    const reply = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (path === '/api/v1/me') {
      return reply({
        actor: { id: 'admin', kind: 'user', display_name: '検証', email: 'test@example.com', system_role: 'administrator',
          locale: 'ja', timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false },
        permissions: [],
        projects: [{ key: 'demo', name: server.name, role: 'project_admin', permissions: ['project.view', 'project.edit', 'ticket.view'] }],
        expires_at: 4070908800000,
      })
    }
    if (path === '/api/v1/projects/demo' && req.method() === 'PATCH') {
      const body = req.postDataJSON() as Patch['body']
      const ifMatch = req.headers()['if-match'] ?? ''
      server.patches.push({ ifMatch, body })
      server.inFlight++
      server.maxInFlight = Math.max(server.maxInFlight, server.inFlight)
      if (opts.delayMs) await new Promise((r) => setTimeout(r, opts.delayMs))
      server.inFlight--
      if (server.conflictNext || ifMatch !== `"${server.version}"`) {
        server.conflictNext = false
        // 他の誰かの更新：タイムラインを出し、吸着を5分にした
        server.settings = { ...server.settings, views: { timeline: true }, timeline_snap_seconds: 300 }
        server.version += 1
        return reply({ error: { code: 'conflict', message: '他のユーザーが先に更新しました', details: [] } }, 409)
      }
      if (body.name !== undefined) server.name = body.name
      if (body.settings !== undefined) server.settings = body.settings
      server.version += 1
      return reply(project())
    }
    if (path === '/api/v1/projects/demo') return reply(project())
    if (path === '/api/v1/projects/demo/tickets' && url.searchParams.get('type') === 'epic') {
      const items = epics.map((e) => ({
        id: `01K0000000000000000000${e.seq}0`, seq: e.seq, type: 'epic', title: e.title, status: todo, sort_key: `0|${e.seq}`,
        parent_seq: null, closed_at: null, version: 1,
      }))
      return reply({ items, total: items.length, page: 1, per_page: 200, total_pages: 1 })
    }
    if (path === '/api/v1/roles') return reply({ items: [] })
    return reply({ items: [], total: 0, page: 1, per_page: 50, total_pages: 0, next_cursor: null })
  })
  await page.goto('/p/demo/settings')
  await expect(page.locator('.views-block')).toBeVisible()
  return server
}

const viewsBlock = (page: Page) => page.locator('.views-block')
const timelineBox = (page: Page) => viewsBlock(page).locator('.view-choice', { hasText: 'タイムライン' }).locator('.view-line input[type="checkbox"]')

test('チェックを変えたその場で保存する。基本情報の入力中の値と未保存のリポジトリは送らない', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 })
  const server = await setup(page)
  // 基本情報を入力中（未保存）：名前を変え、リポジトリを1件足す
  const nameInput = page.locator('label.field', { hasText: 'プロジェクト名' }).locator('input')
  await nameInput.fill('Demo改')
  await page.getByRole('button', { name: '+ 追加' }).click()
  const dlg = page.getByRole('dialog')
  await dlg.locator('label.field', { hasText: 'URL' }).locator('input').fill('https://example.com/new.git')
  await dlg.getByRole('button', { name: '追加', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  // ［保存］を押さずに、チェックだけで保存される
  await timelineBox(page).check()
  await expect(viewsBlock(page).getByRole('status')).toHaveText('✓ 保存しました')
  expect(server.patches).toHaveLength(1)
  const first = server.patches[0]!
  expect(first.ifMatch).toBe('"1"')
  expect(first.body.name).toBeUndefined()
  expect((first.body.settings!['views'] as Record<string, boolean>)['timeline']).toBe(true)
  // 土台はサーバの settings：未保存のリポジトリは送らず、知らないキーは落とさない
  expect(first.body.settings!['repositories']).toEqual([{ url: 'https://example.com/old.git' }])
  expect(first.body.settings!['keep_me']).toBe('x')
  // メニューが追随し、入力中の名前は残る
  await expect(page.locator('nav .item', { hasText: 'タイムライン' })).toHaveCount(1)
  await expect(nameInput).toHaveValue('Demo改')
  await expect(page.getByText('変更は上の［保存］で保存します')).toHaveCount(0)
  await viewsBlock(page).screenshot({ path: test.info().outputPath('views-saved.png') })

  // 基本情報の［保存］：新しい version で送り（409 にならない）、表示するビューはサーバの値のまま
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.locator('form .ok')).toHaveText('✓ 保存しました')
  expect(server.patches).toHaveLength(2)
  const second = server.patches[1]!
  expect(second.ifMatch).toBe('"2"')
  expect(second.body.name).toBe('Demo改')
  expect((second.body.settings!['repositories'] as { url: string }[]).map((r) => r.url)).toEqual([
    'https://example.com/old.git', 'https://example.com/new.git',
  ])
  expect((second.body.settings!['views'] as Record<string, boolean>)['timeline']).toBe(true)
  await expect(timelineBox(page)).toBeChecked()
})

test('基本情報が変わっていなければ［保存］は押せない。ビューを変えても押せるようにならない', async ({ page }) => {
  const server = await setup(page)
  const save = page.getByRole('button', { name: '保存', exact: true })
  await expect(save).toBeDisabled()
  await timelineBox(page).check()
  await expect(viewsBlock(page).getByRole('status')).toHaveText('✓ 保存しました')
  await expect(save).toBeDisabled()
  expect(server.patches).toHaveLength(1)
})

test('続けて変えても1本ずつ送り、古い version で 409 にならない。最後の値が残る', async ({ page }) => {
  const server = await setup(page, { delayMs: 400 })
  await timelineBox(page).check()
  // 1本目の応答を待たずに、吸着と対象エピックを変える
  const panel = viewsBlock(page).locator('.view-choice', { hasText: 'タイムライン' })
  await expect(viewsBlock(page).getByRole('status')).toHaveText('保存中…')
  await panel.locator('select').selectOption('300')
  await panel.locator('label.tl-epic', { hasText: '中庭ステージ' }).locator('input').check()
  await expect(viewsBlock(page).getByRole('status')).toHaveText('✓ 保存しました', { timeout: 5000 })

  expect(server.maxInFlight).toBe(1)
  expect(server.patches.map((p) => p.ifMatch)).toEqual(server.patches.map((_, i) => `"${i + 1}"`))
  expect(server.patches.length).toBeLessThanOrEqual(3)
  expect(server.settings['views']).toMatchObject({ timeline: true })
  expect(server.settings['timeline_snap_seconds']).toBe(300)
  expect(server.settings['timeline_epics']).toEqual([302])
  await expect(panel.locator('select')).toHaveValue('300')
})

test('保存に失敗したら画面の値をサーバの値に戻す。409 は最新を取り直す導線を出す', async ({ page }) => {
  const server = await setup(page)
  const nameInput = page.locator('label.field', { hasText: 'プロジェクト名' }).locator('input')
  await nameInput.fill('入力中の名前')
  server.conflictNext = true
  await timelineBox(page).check()
  const alert = viewsBlock(page).getByRole('alert')
  await expect(alert).toContainText('ほかの変更と競合しました')
  // 保存されていない選択を残さない（手元のサーバの値＝タイムラインを出さない）
  await expect(timelineBox(page)).not.toBeChecked()
  await viewsBlock(page).screenshot({ path: test.info().outputPath('views-conflict.png') })
  await expect(viewsBlock(page).getByRole('status')).toHaveCount(0)

  await alert.getByRole('button', { name: '最新の内容を取得' }).click()
  await expect(alert).toHaveCount(0)
  // 他の誰かの更新（タイムラインを出す・吸着5分）を取り込み、入力中の名前は残す
  await expect(timelineBox(page)).toBeChecked()
  await expect(viewsBlock(page).locator('.view-choice', { hasText: 'タイムライン' }).locator('select')).toHaveValue('300')
  await expect(nameInput).toHaveValue('入力中の名前')
})
