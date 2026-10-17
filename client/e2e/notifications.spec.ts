import { expect, test, type Page } from '@playwright/test'

// 通知（GuiDesign.md 5.16）。API はモックし、画面の振る舞いだけを見る。

const session = {
  actor: {
    id: '01K00000000000000000000000', kind: 'user', display_name: '田中',
    email: 'tanaka@example.com', system_role: 'operator', locale: 'ja',
    timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
  },
  permissions: ['project.view'], projects: [], expires_at: 4070908800000,
}

type Row = {
  id: string; kind: string; title: string; body: string; link: string; link_label: string
  project: { key: string; name: string } | null; source: { id: string | null; name: string }
  created_at: number; read_at: number | null
}

function seed(): Row[] {
  return [
    { id: 'n1', kind: 'test.confirm', title: 'ユーザ確認待ち：計画の変更を承認してください', body: '子チケットを2件増やしてよいかを確認したいです。\n2行目は出さない',
      link: '/projects', link_label: 'my-app-52 文化祭の準備', project: { key: 'my-app', name: 'マイアプリ' },
      source: { id: '01K00000000000000000000009', name: 'Claude Code' }, created_at: Date.UTC(2026, 9, 13, 5, 32), read_at: null },
    { id: 'n2', kind: 'test.reset', title: 'パスワードのリセットを求めています', body: '佐藤 がパスワードを変更できなくなりました。',
      link: '/me', link_label: '佐藤 のパスワードのリセット', project: null,
      source: { id: null, name: 'PB' }, created_at: Date.UTC(2026, 9, 13, 2, 5), read_at: null },
    { id: 'n3', kind: 'test.done', title: '評価ができません', body: '',
      link: '/projects', link_label: 'my-app-48 受付画面', project: { key: 'my-app', name: 'マイアプリ' },
      source: { id: null, name: 'PB' }, created_at: Date.UTC(2026, 9, 12, 9, 20), read_at: Date.UTC(2026, 9, 12, 9, 31) },
  ]
}

/** API のモック。`unread` を外から変えると、次の未読数の取得に反映される */
async function mockApi(page: Page, rows: Row[], overrides: { unread?: () => number } = {}) {
  const calls: string[] = []
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname
    calls.push(`${req.method()} ${path}${url.search}`)
    if (path === '/api/v1/me') return route.fulfill({ json: session })
    if (path === '/api/v1/projects') return route.fulfill({ json: { items: [], page: 1, per_page: 25, total: 0, total_pages: 0 } })
    if (path === '/api/v1/me/notifications/unread-count') {
      return route.fulfill({ json: { count: overrides.unread ? overrides.unread() : rows.filter((r) => r.read_at === null).length } })
    }
    if (path === '/api/v1/me/notifications' && req.method() === 'GET') {
      const unreadOnly = url.searchParams.get('filter') === 'unread'
      const items = rows.filter((r) => !unreadOnly || r.read_at === null)
      return route.fulfill({ json: { items, page: 1, per_page: 50, total: items.length, total_pages: items.length ? 1 : 0 } })
    }
    if (path === '/api/v1/me/notifications/read-all') {
      let updated = 0
      for (const r of rows) if (r.read_at === null) { r.read_at = Date.now(); updated++ }
      return route.fulfill({ json: { updated } })
    }
    const one = /^\/api\/v1\/me\/notifications\/([^/]+)$/.exec(path)
    if (one) {
      const row = rows.find((r) => r.id === one[1])
      if (!row) return route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '対象が見つかりません' } } })
      if (req.method() === 'PATCH') {
        const read = (req.postDataJSON() as { read: boolean }).read
        row.read_at = read ? (row.read_at ?? Date.now()) : null
        return route.fulfill({ json: row })
      }
      if (req.method() === 'DELETE') {
        rows.splice(rows.indexOf(row), 1)
        return route.fulfill({ status: 204 })
      }
    }
    return route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '対象が見つかりません' } } })
  })
  return calls
}

const menuRow = (page: Page) => page.locator('nav.menu a.item[href="/notifications"]')

test('メニューの先頭に未読数を出し、一覧から既読・遷移・削除ができる', async ({ page }) => {
  const rows = seed()
  const calls = await mockApi(page, rows)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/projects')

  // 先頭の行で、未読数は右端に出る
  await expect(menuRow(page)).toBeVisible()
  await expect(menuRow(page).locator('.unread-count')).toHaveText('2')
  await expect(menuRow(page)).toHaveAttribute('aria-label', '通知（未読 2件）')
  const first = await page.locator('nav.menu a.item').first().getAttribute('href')
  expect(first).toBe('/notifications')

  await menuRow(page).click()
  await expect(page).toHaveURL(/\/notifications$/)
  await expect(page.getByRole('heading', { name: '通知', level: 1 })).toBeVisible()
  await expect(page.locator('article.row')).toHaveCount(3)
  // 日付で区切る（自分のタイムゾーン）。本文は1行目だけ
  await expect(page.locator('.day-label')).toHaveText(['2026-10-13（火）', '2026-10-12（月）'])
  await expect(page.locator('article.row').first().locator('.body')).toHaveText('子チケットを2件増やしてよいかを確認したいです。')
  await expect(page.locator('article.row').first().locator('.time')).toHaveText('14:32')

  // リンク以外の行の中を押すと既読になるだけで、遷移しない
  await page.locator('article.row[data-id="n2"] .title').click()
  await expect(page.locator('article.row[data-id="n2"]')).toHaveClass(/read/)
  await expect(page).toHaveURL(/\/notifications$/)
  await expect(menuRow(page).locator('.unread-count')).toHaveText('1')

  // 未読に戻す
  await page.locator('article.row[data-id="n2"]').hover()
  await page.locator('article.row[data-id="n2"]').getByRole('button', { name: '未読に戻す' }).click()
  await expect(page.locator('article.row[data-id="n2"]')).not.toHaveClass(/read/)
  await expect(menuRow(page).locator('.unread-count')).toHaveText('2')

  // 削除は確認せずに消す
  await page.locator('article.row[data-id="n3"]').hover()
  await page.locator('article.row[data-id="n3"]').getByRole('button', { name: '削除' }).click()
  await expect(page.locator('article.row')).toHaveCount(2)
  expect(calls).toContain('DELETE /api/v1/me/notifications/n3')

  // 遷移先のリンクは、既読にしてから移る
  await page.locator('article.row[data-id="n1"]').getByRole('link', { name: 'my-app-52 文化祭の準備 →' }).click()
  await expect(page).toHaveURL(/\/projects$/)
  expect(calls).toContain('PATCH /api/v1/me/notifications/n1')
  await expect(menuRow(page).locator('.unread-count')).toHaveText('1')
})

test('すべて既読にすると未読数が消え、0件なら数字を出さない', async ({ page }) => {
  await mockApi(page, seed())
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/notifications?filter=unread')
  await expect(page.locator('article.row')).toHaveCount(2)
  await page.getByRole('button', { name: 'すべて既読にする' }).click()
  await expect(menuRow(page).locator('.unread-count')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'すべて既読にする' })).toBeDisabled()
  await expect(page.getByText('通知はありません')).toBeVisible()
})

test('未読数は展開で 999+、折りたたみと狭い幅では 99+ で止める', async ({ page }, testInfo) => {
  let count = 1234
  await mockApi(page, seed(), { unread: () => count })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/projects')
  await expect(menuRow(page).locator('.unread-count')).toHaveText('999+')
  await page.screenshot({ path: testInfo.outputPath('notifications-1440.png') })

  // 折りたたみ（1280px 未満の既定）
  await page.setViewportSize({ width: 1000, height: 900 })
  await page.goto('/projects')
  await expect(menuRow(page).locator('.unread-count')).toHaveText('99+')
  await page.screenshot({ path: testInfo.outputPath('notifications-1000.png') })

  // 768px 未満：浮遊する ☰ に重ねる
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/notifications')
  await expect(page.locator('.floating .unread-count')).toHaveText('99+')
  const row = page.locator('article.row').first()
  const box = await row.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  await page.screenshot({ path: testInfo.outputPath('notifications-390.png'), fullPage: true })

  // 3桁は展開でそのまま出す
  count = 128
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/projects')
  await expect(menuRow(page).locator('.unread-count')).toHaveText('128')
})

test('ダークテーマでも未読数と一覧が読める', async ({ page }, testInfo) => {
  const dark = { ...session, actor: { ...session.actor, theme: 'dark' } }
  await mockApi(page, seed())
  await page.route('**/api/v1/me', (route) => route.fulfill({ json: dark }))
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/notifications')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(menuRow(page).locator('.unread-count')).toHaveText('2')
  await page.screenshot({ path: testInfo.outputPath('notifications-dark.png') })
})

test('タブが見えるようになった瞬間に未読数を引き直す', async ({ page }) => {
  let count = 1
  await mockApi(page, seed(), { unread: () => count })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/projects')
  await expect(menuRow(page).locator('.unread-count')).toHaveText('1')

  count = 5
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
    document.dispatchEvent(new Event('visibilitychange'))
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await expect(menuRow(page).locator('.unread-count')).toHaveText('5')
})
