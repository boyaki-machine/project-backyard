import { expect, test, type Page } from '@playwright/test'

async function login(page: Page) {
  const email = process.env.PB_E2E_EMAIL
  const password = process.env.PB_E2E_PASSWORD
  if (!email || !password) {
    throw new Error('PB_E2E_EMAIL と PB_E2E_PASSWORD を設定してください')
  }
  await page.goto('/login')
  await page.locator('input[name="email"]').fill(email)
  await page.locator('input[name="password"]').fill(password)
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/projects$/)
}

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
test('メニューでプロジェクトを切り替えると、バックログの行が切替先のチケットになる', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page)

  const me = (await (await page.request.get('/api/v1/me')).json()) as {
    projects: { key: string; name: string }[]
  }
  // 行を持ち、題名の集合が互いに違う2件を選ぶ
  const candidates: { key: string; name: string; titles: string[] }[] = []
  for (const p of me.projects) {
    const titles = await backlogTitles(page, p.key)
    if (titles.length > 0) candidates.push({ ...p, titles })
    if (candidates.length === 2) break
  }
  test.skip(candidates.length < 2, 'チケットを持つプロジェクトが2件要る')
  const [from, to] = candidates

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
})
