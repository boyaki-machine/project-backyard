import { expect, test, type Page } from '@playwright/test'

/**
 * `/me/agents` の接続パネルで、Codex に接続方式を案内する（pb-160・pb-202）。
 *
 * **実サーバに向け、demo プロジェクトの参加者で走らせる**——準備でエージェントを
 * demo に登録するので、参加していないアカウントは 422 になる。**dev なら
 * `PB_E2E_EMAIL=pm@example.com`**（`admin@example.com` は demo の参加者でない）。
 * パスワードは `make dev-info` で見る。続けて流すとログインの回数制限
 * （IP あたり10回／分）に掛かるので、落ちたら1分待つ。
 */

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

async function writeAPI<T>(page: Page, path: string, method: 'POST' | 'DELETE', body?: unknown) {
  return page.evaluate(
    async ({ path, method, body }) => {
      const csrf = document.cookie
        .split('; ')
        .find((part) => part.startsWith('pb_csrf='))
        ?.slice('pb_csrf='.length)
      if (!csrf) throw new Error('pb_csrf Cookie がありません')

      const response = await fetch(`/api/v1${path}`, {
        method,
        headers: {
          'Content-Type': 'application/json',
          'X-PB-CSRF': decodeURIComponent(csrf),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      })
      if (!response.ok) {
        throw new Error(`${method} ${path}: ${response.status} ${await response.text()}`)
      }
      return response.status === 204 ? null : ((await response.json()) as T)
    },
    { path, method, body },
  ) as Promise<T>
}

test('Codexの自己署名証明書にはstdioブリッジを案内する', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await login(page)

  const suffix = String(Date.now())
  const displayName = `PB-160 Codex ${suffix}`
  const agent = await writeAPI<{ id: string }>(page, '/me/agents', 'POST', {
    display_name: displayName,
    project_key: 'demo',
    client_kind: 'codex',
    model_name: 'codex',
    token_env_suffix: `PB160${suffix}`,
  })

  try {
    await page.goto('/me/agents')
    const card = page.locator('article.card').filter({ hasText: displayName })
    await expect(card).toBeVisible()
    await card.getByRole('button', { name: '接続の手順を開く' }).click()

    await expect(card.getByText('HTTPS へ直接接続（公開 CA）')).toBeVisible()
    await expect(
      card.getByText('ローカル stdio ブリッジを使う（自己署名・社内 CA）'),
    ).toBeVisible()
    await expect(card.getByText(/OS の信頼ストアへ登録しても/)).toBeVisible()

    await card.getByLabel('ローカル stdio ブリッジを使う（自己署名・社内 CA）').check()
    await expect(card.getByText(/ブリッジも TLS 検証を行う/)).toBeVisible()
    await expect(card.getByText(/PB_MCP_CA_FILE/)).toBeVisible()
    await expect(card.getByText(/PB-README\.md/)).toBeVisible()

    const mobileOverflow = await card.evaluate((element) => element.scrollWidth - element.clientWidth)
    expect(mobileOverflow).toBeLessThanOrEqual(1)
    const mobileScreenshot = testInfo.outputPath('codex-connect-390.png')
    await page.screenshot({ path: mobileScreenshot, fullPage: true })
    await testInfo.attach('codex-connect-390', {
      path: mobileScreenshot,
      contentType: 'image/png',
    })

    await page.setViewportSize({ width: 1024, height: 900 })
    await expect(card.getByText(/PB_MCP_CA_FILE/)).toBeVisible()
    const desktopOverflow = await card.evaluate((element) => element.scrollWidth - element.clientWidth)
    expect(desktopOverflow).toBeLessThanOrEqual(1)
    const desktopScreenshot = testInfo.outputPath('codex-connect-1024.png')
    await page.screenshot({ path: desktopScreenshot, fullPage: true })
    await testInfo.attach('codex-connect-1024', {
      path: desktopScreenshot,
      contentType: 'image/png',
    })
  } finally {
    await writeAPI(page, `/me/agents/${agent.id}`, 'DELETE')
  }
})
