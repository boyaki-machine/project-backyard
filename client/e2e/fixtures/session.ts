import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, type Cookie, type Page } from '@playwright/test'

/**
 * 実サーバに繋ぐ e2e のログイン（`Development.md` 8.8）。
 *
 * **アカウントごとに1回だけ画面からログインし、Cookie を使い回す。** ログインは IP あたり
 * 10回/分（`ApiDesign.md` 2.9）で、spec ごとに画面からログインすると全件の実行が 429 で落ちる。
 * Cookie は `test-results/.auth/` に置く。Playwright が実行のたびに `test-results/` を消すので、
 * 前の実行のセッションは持ち越さない。
 *
 * パスワードは `PB_E2E_PASSWORD`（`make dev-info` の共通パスワード）。メールアドレスは
 * 既定でデモアカウントを使い、環境変数で差し替えられる。
 */
export type Account = 'admin' | 'pm'

const accounts: Record<Account, { env: string; fallback: string }> = {
  admin: { env: 'PB_E2E_EMAIL', fallback: 'admin@example.com' },
  pm: { env: 'PB_E2E_PM_EMAIL', fallback: 'pm@example.com' },
}

const stateDir = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'test-results', '.auth')

export function accountEmail(account: Account): string {
  const { env, fallback } = accounts[account]
  return process.env[env] || fallback
}

export function hasPassword(): boolean {
  return !!process.env.PB_E2E_PASSWORD
}

function savedCookies(file: string): Cookie[] | null {
  try {
    return JSON.parse(readFileSync(file, 'utf8')) as Cookie[]
  } catch {
    return null
  }
}

/** ログインした状態で `/projects` を開く。 */
export async function login(page: Page, account: Account = 'admin'): Promise<void> {
  const password = process.env.PB_E2E_PASSWORD
  if (!password) throw new Error('PB_E2E_PASSWORD を設定してください（make dev-info の共通パスワード）')
  const file = join(stateDir, `${account}.json`)

  const cookies = savedCookies(file)
  if (cookies) {
    await page.context().addCookies(cookies)
    // セッションが生きているかを API で確かめる。切れていれば画面からログインし直す
    if ((await page.request.get('/api/v1/me')).ok()) {
      await page.goto('/projects')
      await expect(page).toHaveURL(/\/projects$/)
      return
    }
    await page.context().clearCookies()
  }

  await page.goto('/login')
  await page.locator('input[name="email"]').fill(accountEmail(account))
  await page.locator('input[name="password"]').fill(password)
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/projects$/)
  mkdirSync(stateDir, { recursive: true })
  writeFileSync(file, JSON.stringify(await page.context().cookies()))
}
