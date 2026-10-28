import { expect, test, type Page } from '@playwright/test'
import { login } from './fixtures/session'

/** バックログが出す行（ストーリーとタスク）の題名。画面と同じ条件で API から引く */
async function backlogTitles(page: Page, key: string): Promise<string[]> {
  const res = await page.request.get(
    `/api/v1/projects/${key}/tickets?search_mode=backlog&type=story,task&per_page=200`,
  )
  expect(res.ok()).toBe(true)
  const body = (await res.json()) as { items: { title: string }[] }
  return body.items.map((t) => t.title)
}

// pb-357：メニューのプロジェクト切替でバックログの行が前のプロジェクトのまま残り、
// 番号の接頭辞だけが切替先のキーに変わっていた。**クエリが同じまま切り替える**のが
// 再現の条件である（クエリが変われば、クエリの watch が一覧を取り直していた）。
// 切替元は demo、切替先はこの試験で作って最後に消す（他の試験の消し残しに頼らない）。
test('メニューでプロジェクトを切り替えると、バックログの行が切替先のチケットになる', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page)

  const headers = { 'X-PB-CSRF': (await page.context().cookies()).find((c) => c.name === 'pb_csrf')!.value }
  const suffix = Date.now().toString(36)
  const target = { key: `sw-e2e-${suffix}`, name: `Switch target ${suffix}` }
  const created = await page.request.post('/api/v1/projects', {
    headers, data: { key: target.key, name: target.name, workflow_template: 'simple' },
  })
  expect(created.status(), await created.text()).toBe(201)

  try {
    const ticket = await page.request.post(`/api/v1/projects/${target.key}/tickets`, {
      headers, data: { type: 'task', title: `Switch target row ${suffix}` },
    })
    expect(ticket.status()).toBe(201)
    const from = { key: 'demo', titles: await backlogTitles(page, 'demo') }
    const to = { ...target, titles: await backlogTitles(page, target.key) }
    expect(from.titles.length).toBeGreaterThan(0)
    await switchProject(page, from, to)
  } finally {
    await page.request.post(`/api/v1/projects/${target.key}/archive`, { headers })
    const removed = await page.request.delete(`/api/v1/projects/${target.key}`, { headers, data: { confirm_key: target.key } })
    expect(removed.status()).toBe(204)
  }
})

async function switchProject(
  page: Page,
  from: { key: string },
  to: { key: string; name: string; titles: string[] },
) {
  await page.goto(`/p/${from.key}/backlog`)
  const ids = page.locator('table code.seq')
  await expect(ids.first()).toHaveText(new RegExp(`^${from.key}-\\d+$`))

  await page.locator('.switcher button.current').click()
  await page.locator('.switcher .dropdown .list').getByRole('button', { name: to.name, exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/p/${to.key}/backlog$`))

  await expect(ids.first()).toHaveText(new RegExp(`^${to.key}-\\d+$`))
  // 接頭辞だけでなく中身が切替先であること（不具合では題名が切替元のまま残った）
  await expect
    .poll(async () => {
      const shown = await page.locator('table .title-text').allTextContents()
      return shown.length > 0 && shown.every((t) => to.titles.includes(t))
    })
    .toBe(true)
}
