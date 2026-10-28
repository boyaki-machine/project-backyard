import { test, expect } from '@playwright/test'
import { login } from './fixtures/session'

test('human manages agent stop, attributes and monitoring in both management screens', async ({ page, playwright, baseURL }, info) => {
 test.setTimeout(120_000)
 await login(page)
 const csrf=(await page.context().cookies()).find(c=>c.name==='pb_csrf')!.value
 const headers={'X-PB-CSRF':csrf}, key=`ac-${Date.now()}`, base=`/api/v1/projects/${key}`, name=`0000-control-${Date.now()}`
 const project=await page.request.post('/api/v1/projects',{headers,data:{key,name:'Agent control regression',workflow_template:'simple'}})
 expect(project.status()).toBe(201)
 let agentID=''
 try {
  const agent=await page.request.post('/api/v1/me/agents',{headers,data:{project_key:key,display_name:name,client_kind:'other',model_name:'ReviewModel'}})
  expect(agent.status()).toBe(201);agentID=(await agent.json()).id
  const issued=await page.request.post(`/api/v1/me/agents/${agentID}/tokens`,{headers,data:{expires_in_days:1}})
  expect(issued.status()).toBe(201);const token=(await issued.json()).token
  const me=await (await page.request.get('/api/v1/me')).json()
  const ticketResponse=await page.request.post(`${base}/tickets`,{headers,data:{type:'task',title:'Explicit execution outcomes'}})
  expect(ticketResponse.status()).toBe(201)
  const ticket=await ticketResponse.json()
  const agentAPI=await playwright.request.newContext({baseURL,extraHTTPHeaders:{Authorization:`Bearer ${token}`}})
  try {
   const rpc=await agentAPI.post(`/mcp/${key}`,{headers:{Accept:'application/json, text/event-stream'},data:{jsonrpc:'2.0',id:1,method:'tools/call',params:{name:'pb_update_ticket',arguments:{seq:ticket.seq,assignee_id:'me'}}}})
   expect(rpc.status()).toBe(200)
   const rpcText=await rpc.text();expect(rpcText).toContain('"isError":true');expect(rpcText).toContain('forbidden')
   const assigned=await page.request.patch(`${base}/tickets/${ticket.seq}`,{headers:{...headers,'If-Match':`"${ticket.version}"`},data:{assignee_id:me.actor.id}})
   expect(assigned.status()).toBe(200)
   for(const outcome of ['failed','abandoned']){
    const workPath=`${base}/tickets/${ticket.seq}/work-sessions`
    const start=await agentAPI.post(workPath,{data:{request_id:crypto.randomUUID()}})
    expect(start.status()).toBe(200)
    const work=await start.json()
    const end=await agentAPI.post(`${workPath}/${work.id}/events`,{data:{request_id:crypto.randomUUID(),action:outcome==='failed'?'finish':'abandon',...(outcome==='failed'?{result:'blocked',outcome}:{}),note:'Explicit failure regression'}})
    expect(end.status()).toBe(200)
   }
  }finally{await agentAPI.dispose()}
  await page.goto(`/p/${key}/tickets/${ticket.seq}`)
  const work=page.getByRole('region',{name:'作業実績'})
  await expect(work.getByText('実行結果: 失敗',{exact:true})).toBeVisible()
  await expect(work.getByText('実行結果: 放棄',{exact:true})).toBeVisible()
  for(const width of [1440,768,360]){
   await page.setViewportSize({width,height:1000});await work.scrollIntoViewIfNeeded()
   const b=await work.boundingBox();expect(b!.x).toBeGreaterThanOrEqual(0);expect(b!.x+b!.width).toBeLessThanOrEqual(width+1)
   await page.screenshot({path:info.outputPath(`ticket-work-${width}.png`),fullPage:true})
  }
  await page.goto(`/p/${key}/insights`)
  await expect(page.locator('.totals')).toContainText('失敗')
  await expect(page.locator('.table-scroll[aria-label="実行明細"] tbody')).toContainText('放棄')
  for(const width of [1440,768,360]){
   await page.setViewportSize({width,height:1000});expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width+1)
   await page.screenshot({path:info.outputPath(`insights-${width}.png`),fullPage:true})
  }
  await page.setViewportSize({width:1440,height:1000})
  await page.goto(`/p/${key}/settings`)
  await page.getByRole('tab',{name:'エージェント',exact:true}).click()
  const monitorSettings=page.locator('.monitor-settings')
  await monitorSettings.getByLabel('連続失敗上限').fill('2')
  await monitorSettings.getByRole('button',{name:'保存',exact:true}).click()
  await expect(monitorSettings.getByRole('status')).toHaveText('停止条件を保存しました')
  await expect(monitorSettings.getByLabel('連続失敗上限')).toHaveValue('2')
  expect((await (await page.request.get(`${base}/agent-monitor`)).json()).max_consecutive_failures).toBe(2)
  const row=page.locator('.managed-agents tbody tr').filter({hasText:name})
  await row.getByRole('button',{name:'評価と実行監視'}).click()
  const controls=page.locator('.agent-controls')
  await controls.getByLabel('信頼度').selectOption('3')
  await controls.getByLabel('能力ラベル（カンマ区切り）').fill('review, tests')
  await controls.getByRole('button',{name:'保存',exact:true}).click()
  await expect(controls.getByRole('status')).toHaveText('評価属性を保存しました')
  await expect(controls.getByText('停止条件に到達',{exact:true})).toBeVisible()
  const monitoredAPI=await playwright.request.newContext({baseURL,extraHTTPHeaders:{Authorization:`Bearer ${token}`}})
  try{
   const rpc=await monitoredAPI.post(`/mcp/${key}`,{headers:{Accept:'application/json, text/event-stream'},data:{jsonrpc:'2.0',id:2,method:'tools/call',params:{name:'pb_update_ticket',arguments:{seq:ticket.seq,title:'Must reject when stopped'}}}})
   const text=await rpc.text();expect(text).toContain('"isError":true');expect(text).toContain('agent_monitor_stopped')
  }finally{await monitoredAPI.dispose()}
  for(const width of [1440,768,360]){
   await page.setViewportSize({width,height:1000})
   const bounds=await controls.boundingBox();expect(bounds!.x).toBeGreaterThanOrEqual(0);expect(bounds!.x+bounds!.width).toBeLessThanOrEqual(width+1)
   expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width+1)
   expect((await page.getByRole('tab',{name:'エージェント',exact:true}).boundingBox())!.height).toBeLessThan(65)
   await page.locator('.monitor-settings h2').scrollIntoViewIfNeeded()
   await page.screenshot({path:info.outputPath(`project-agents-${width}.png`),fullPage:true})
  }
  await page.setViewportSize({width:1440,height:1000})
  await page.goto('/me/agents')
  const card=page.locator('article.card').filter({hasText:name})
  await card.locator('summary').filter({hasText:'評価と実行監視'}).click()
  await expect(card.locator('.agent-controls').getByLabel('信頼度')).toHaveValue('3')
  await card.locator('.agent-controls').getByLabel('解除理由').fill('Reviewed execution conditions')
  await card.locator('.agent-controls').getByRole('button',{name:'監視を解除'}).click()
  await expect(card.locator('.agent-controls').getByRole('status')).toHaveText('実行監視を解除しました')
  for(const width of [1440,768,360]){
   await page.setViewportSize({width,height:1000});await card.locator('.agent-controls').scrollIntoViewIfNeeded()
   const b=await card.locator('.agent-controls').boundingBox();expect(b!.x).toBeGreaterThanOrEqual(0);expect(b!.x+b!.width).toBeLessThanOrEqual(width+1)
   await page.screenshot({path:info.outputPath(`my-agents-${width}.png`),fullPage:true})
  }
  await page.goto('/admin/users')
  await page.getByRole('tab',{name:'エージェント',exact:true}).click()
  const adminRow=page.locator('.managed-agents tbody tr').filter({hasText:name})
  await expect(adminRow).toContainText(key)
  for(const width of [1440,768,360]){
   await page.setViewportSize({width,height:1000})
   expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width+1)
   await page.screenshot({path:info.outputPath(`admin-agents-${width}.png`),fullPage:true})
  }
  await adminRow.getByRole('button',{name:'停止',exact:true}).click()
  const dialog=page.getByRole('dialog',{name:'エージェントを停止',exact:true})
  await expect(dialog).toContainText('全トークンを失効し、履歴は保持します。')
  for(const width of [360,768,1440]){
   await page.setViewportSize({width,height:1000});const b=await dialog.boundingBox();expect(b!.x).toBeGreaterThanOrEqual(0);expect(b!.x+b!.width).toBeLessThanOrEqual(width+1)
   await page.screenshot({path:info.outputPath(`stop-confirm-${width}.png`)})
  }
  await dialog.getByRole('button',{name:'停止',exact:true}).click()
  await expect(adminRow).toContainText('無効')
  await expect(adminRow.getByRole('button',{name:'停止',exact:true})).toHaveCount(0)
  const retainedUsage=await (await page.request.get(`${base}/agent-usage`)).json()
  expect(retainedUsage.totals.failed_count).toBe(1);expect(retainedUsage.totals.abandoned_count).toBe(1)
  expect((await (await page.request.get(`${base}/tickets/${ticket.seq}/work-sessions`)).json()).total).toBe(2)
  const api=await playwright.request.newContext({baseURL,extraHTTPHeaders:{Authorization:`Bearer ${token}`}})
  try {expect((await api.get(base)).status()).toBe(401)}finally{await api.dispose()}
 } finally {
  await page.request.post(`${base}/archive`,{headers})
  const agentRemoved=agentID?(await page.request.delete(`/api/v1/me/agents/${agentID}`,{headers})).status():204
  // アーカイブだけでは行が残り、同名のプロジェクトが溜まって backlog-project-switch の選択を曖昧にする
  expect((await page.request.delete(base,{headers,data:{confirm_key:key}})).status()).toBe(204)
  expect(agentRemoved).toBe(204)
 }
})
