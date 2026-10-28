import { execFileSync } from 'node:child_process'
import { expect, test } from '@playwright/test'
import { login } from './fixtures/session'

test('agent applies an approved plan through MCP and sees dates and dependencies in gantt', async ({ page, playwright, baseURL }, info) => {
  test.setTimeout(90_000)
  await login(page)
  const csrf = (await page.context().cookies()).find(c => c.name === 'pb_csrf')!.value
  const headers = { 'X-PB-CSRF': csrf }
  const key = `plan-${Date.now()}`
  const base = `/api/v1/projects/${key}`
  const created = await page.request.post('/api/v1/projects', { headers, data: { key, name: 'Agent planning regression', workflow_template: 'simple' } })
  expect(created.status()).toBe(201)
  let agentID = ''
  try {
    const agentResponse = await page.request.post('/api/v1/me/agents', { headers, data: { project_key: key, display_name: key, client_kind: 'other' } })
    expect(agentResponse.status()).toBe(201)
    agentID = (await agentResponse.json()).id
    await page.goto('/me/agents')
    const card = page.locator('article.card').filter({ hasText: key })
    await card.getByRole('button', { name: 'トークンを発行', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'エージェント用トークンを発行', exact: true })
    const grant = dialog.getByLabel('依存・順序・日程の変更を許す')
    await expect(grant).not.toBeChecked()
    await grant.check()
    await expect(dialog.getByText('憲章を書き換えられるようになります。')).toHaveCount(0)
    for (const width of [1440, 768, 360]) {
      await page.setViewportSize({ width, height: 900 })
      const bounds = await dialog.boundingBox()
      expect(bounds!.x).toBeGreaterThanOrEqual(0)
      expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width + 1)
      await expect(grant).toBeVisible()
      await page.screenshot({ path: info.outputPath(`plan-grant-${width}.png`) })
    }
    const issuing = page.waitForResponse(r => r.url().endsWith(`/me/agents/${agentID}/tokens`) && r.request().method() === 'POST')
    await dialog.getByRole('button', { name: '発行', exact: true }).click()
    const tokenResponse = await issuing
    expect(tokenResponse.status()).toBe(201)
    const issued = await tokenResponse.json()
    expect(issued.scopes).toContain('ticket.plan.edit')
    expect(issued.scopes).not.toContain('ticket.edit')
    await page.getByRole('dialog', { name: 'エージェント用トークンを発行しました' }).getByRole('button', { name: '閉じる', exact: true }).last().click()
    const auth = { Authorization: `Bearer ${issued.token}`, Accept: 'application/json, text/event-stream' }
    const agentAPI = await playwright.request.newContext({ baseURL, extraHTTPHeaders: auth })
    let id = 0
    const mcp = async (name: string, args: object, failure = false) => {
      const response = await agentAPI.post(`/mcp/${key}`, { data: { jsonrpc: '2.0', id: ++id, method: 'tools/call', params: { name, arguments: args } } })
      expect(response.status()).toBe(200)
      const wire = await response.text()
      const rpc = JSON.parse(wire.startsWith('event:') || wire.startsWith('data:') ? wire.split('\n').find(l => l.startsWith('data:'))!.slice(5).trim() : wire)
      expect(rpc.error).toBeUndefined()
      expect(Boolean(rpc.result.isError)).toBe(failure)
      return failure ? rpc.result : JSON.parse(rpc.result.content[0].text || '{}')
    }
    const a = await mcp('pb_create_ticket', { type: 'task', title: 'Planning A' })
    const b = await mcp('pb_create_ticket', { type: 'task', title: 'Planning B' })
    // ガントは今日の前後しか描かないので、日付は今日から数える（固定の日付は日が経つと範囲から外れる）
    const day = (offset: number) => new Date(Date.now() + offset * 86_400_000).toLocaleDateString('sv-SE', { timeZone: 'Asia/Tokyo' })
    await mcp('pb_update_ticket', { seq: a.seq, start_date: day(1), due_date: day(1), estimate_point: 5 })
    await mcp('pb_update_ticket', { seq: b.seq, start_date: day(2), due_date: day(3), estimate_point: 8 })
    const links = []
    for (const link_type of ['FS', 'SS', 'FF', 'SF']) links.push(await mcp('pb_add_link', { seq: a.seq, target_seq: b.seq, link_type, lag_days: 0 }))
    await mcp('pb_update_link', { seq: b.seq, id: links[1].id, lag_days: 1 })
    await mcp('pb_move_task', { seq: b.seq, position: 'first' })
    await mcp('pb_move_task', { seq: a.seq, after_seq: b.seq })
    await mcp('pb_add_link', { seq: b.seq, target_seq: a.seq, link_type: 'FS' }, true)
    const plan = await mcp('pb_get_planning_data', {})
    expect(plan.truncated).toBe(false)
    expect(plan.items.map((t: { seq: number }) => t.seq)).toEqual([b.seq, a.seq])
    expect(plan.items.find((t: { seq: number }) => t.seq === a.seq).due_date).toBe(day(1))
    expect(plan.links).toHaveLength(4)
    expect(plan.items.every((t: object) => !('body_md' in t))).toBe(true)
    // The chart renders only the visible time range; inspect both bars at desktop width.
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(`/p/${key}/gantt`)
    await expect(page.locator('.gantt-svg rect.b')).toHaveCount(2)
    await expect(page.locator('.gantt-svg g.dep')).toHaveCount(4)
    for (const width of [1440, 768, 360]) {
      await page.setViewportSize({ width, height: 900 })
      await expect(page.locator('.gantt-tr[data-seq]').filter({ hasText: 'Planning A' })).toBeVisible()
      await page.screenshot({ path: info.outputPath(`plan-gantt-${width}.png`) })
    }
    await mcp('pb_delete_link', { seq: a.seq, id: links[3].id })
    await agentAPI.dispose()
  } finally {
    execFileSync('make', ['psql'], { cwd: process.env.PB_DEV_ROOT ?? '..', input: `DELETE FROM project WHERE key = '${key}' AND name = 'Agent planning regression';\n${agentID ? `DELETE FROM audit_log WHERE actor_id = '${agentID}'; DELETE FROM actor WHERE id = '${agentID}' AND display_name = '${key}';` : ''}\n`, stdio: ['pipe', 'pipe', 'pipe'] })
  }
})
