import { expect, test, type Page } from '@playwright/test'

/**
 * アプリケーション設定の「作業記録」タブ（`GuiDesign.md` 5.12.3、`ApiDesign.md` 11.14）。
 *
 * **API はモックする。** 浮きは「24時間動きの無い記録」なので、実サーバで作るには
 * DB の時刻をずらすしかない。サーバ側の判定は結合テスト（work_sweep_integration_test.go）が持つ。
 */
const session = {
  actor: {
    id: '01K00000000000000000000000', kind: 'user', display_name: '管理者',
    email: 'admin@example.com', system_role: 'administrator', locale: 'ja',
    timezone: 'Asia/Tokyo', theme: 'light', hue: 'blue', must_change_password: false,
  },
  permissions: ['system.settings'], projects: [], expires_at: 4070908800000,
}

const setting = {
  key: 'work_stale_hours', display_name: '作業記録を浮きとみなす時間', description: '', layer: 2,
  value: '24', value_type: 'enum', allowed: ['0', '6', '12', '24', '48', '72', '168'], default_value: '24',
  source: 'default', editable: true, restart_required: false, secret: false,
  env_key: 'PB_WORK_STALE_HOURS', config_file_key: 'work_stale_hours', needs_confirm: false,
}

const items = [
  {
    work_id: '01K00000000000000000000011', project_key: 'pb', project_name: 'Project Backyard',
    ticket_seq: 345, ticket_title: 'とても長い題名のチケットで、狭い幅では折り返して表示されることを確かめるための行',
    actor: { id: '01K00000000000000000000012', display_name: 'Claude Code 4', kind: 'agent' },
    state: 'running', started_at: Date.UTC(2026, 9, 14, 1), last_active_at: Date.UTC(2026, 9, 14, 2), reason: 'stale',
  },
  {
    work_id: '01K00000000000000000000013', project_key: 'pb', project_name: 'Project Backyard',
    ticket_seq: 301, ticket_title: '汎用の通知の仕組みを作る',
    actor: { id: session.actor.id, display_name: '管理者', kind: 'user' },
    state: 'waiting', started_at: Date.UTC(2026, 9, 16, 0), last_active_at: Date.UTC(2026, 9, 16, 0), reason: 'ticket_closed',
  },
]

type Calls = { posted: unknown[]; settings: unknown[] }

async function mock(page: Page, initialItems = items): Promise<Calls> {
  const calls: Calls = { posted: [], settings: [] }
  let current = [...initialItems]
  let lastRun: unknown = { ran_at: Date.UTC(2026, 9, 17, 0), trigger: 'daily', actor_label: null, closed_count: 2 }
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    if (url.pathname === '/api/v1/me') return route.fulfill({ json: session })
    if (url.pathname === '/api/v1/admin/settings/pending') return route.fulfill({ json: { pending_confirmation: null } })
    if (url.pathname === '/api/v1/admin/settings') {
      if (req.method() === 'PUT') calls.settings.push(req.postDataJSON())
      return route.fulfill({ json: { items: [setting], config_file_path: null, pending_confirmation: null } })
    }
    if (url.pathname === '/api/v1/admin/work-sweep') {
      const view = () => ({ threshold_hours: 24, checked_at: Date.UTC(2026, 9, 17, 1), items: current, last_run: lastRun })
      if (req.method() === 'POST') {
        const body = req.postDataJSON() as { work_ids: string[] }
        calls.posted.push(body)
        // 1件は押す前に再開していた、という場面にする（skipped_count）。
        const closed = body.work_ids.slice(1)
        current = current.filter((it) => !closed.includes(it.work_id))
        lastRun = { ran_at: Date.UTC(2026, 9, 17, 2), trigger: 'manual', actor_label: '管理者', closed_count: closed.length }
        return route.fulfill({ json: { ...view(), closed_ids: closed, skipped_count: 1 } })
      }
      return route.fulfill({ json: view() })
    }
    return route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '対象が見つかりません' } } })
  })
  return calls
}

test('作業記録タブで浮きを数え、確認して片付ける', async ({ page }, testInfo) => {
  const calls = await mock(page)
  await page.goto('/admin/settings')
  await page.getByRole('tab', { name: '作業記録' }).click()

  const list = page.getByTestId('work-sweep-items')
  await expect(list.locator('li')).toHaveCount(2)
  await expect(list).toContainText('最後の活動から24時間')
  await expect(list).toContainText('チケットが完了')
  await expect(list.getByRole('link', { name: 'pb #345' })).toHaveAttribute('href', '/p/pb/tickets/345')
  await expect(page.getByTestId('work-sweep-last-run')).toContainText('日次の処理')
  await expect(page.getByTestId('work-sweep-last-run')).toContainText('2件を閉じた')

  // 一般タブには閾値を出さない（このタブで扱う）。
  await page.getByRole('tab', { name: '一般' }).click()
  await expect(page.getByText('作業記録を浮きとみなす時間')).toHaveCount(0)
  await page.getByRole('tab', { name: '作業記録' }).click()

  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    // 画面全体の横スクロールを出さない。
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    const shot = testInfo.outputPath(`work-sweep-${width}.png`)
    await page.screenshot({ path: shot, fullPage: true })
    await testInfo.attach(`work-sweep-${width}`, { path: shot, contentType: 'image/png' })
  }

  // 押しただけでは閉じない。確認を挟む。
  await page.getByRole('button', { name: '2件を片付ける' }).click()
  expect(calls.posted).toHaveLength(0)
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('最後の区間は作業時間に数えません')
  await dialog.getByRole('button', { name: '片付ける' }).click()

  await expect(page.getByRole('status')).toContainText('1件を閉じました（1件は浮きでなくなっていたので閉じていません）')
  expect(calls.posted).toEqual([{ work_ids: items.map((it) => it.work_id) }])
  await expect(list.locator('li')).toHaveCount(1)
  await expect(page.getByTestId('work-sweep-last-run')).toContainText('画面から（管理者）')

  // 閾値を変えて保存する。
  const select = page.getByRole('combobox', { name: '浮きとみなす時間' })
  await expect(select.locator('option')).toHaveCount(7)
  await select.selectOption('48')
  await page.getByTestId('work-stale-setting').getByRole('button', { name: '保存' }).click()
  await expect(page.getByRole('status')).toContainText('浮きとみなす時間を保存しました')
  expect(calls.settings).toEqual([{ items: [{ key: 'work_stale_hours', value: '48' }] }])
})

test('浮きが無ければ片付けのボタンを出さない', async ({ page }) => {
  await mock(page, [])
  await page.goto('/admin/settings')
  await page.getByRole('tab', { name: '作業記録' }).click()
  await expect(page.getByTestId('work-sweep-empty')).toHaveText('浮きはありません')
  await expect(page.getByRole('button', { name: /片付ける/ })).toHaveCount(0)
})
