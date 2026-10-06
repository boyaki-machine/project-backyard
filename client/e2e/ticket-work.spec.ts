import { execFileSync } from 'node:child_process'
import { expect, test } from '@playwright/test'
test('common work saves actuals and shows records and correction at multiple widths', async ({page},info)=>{
 test.setTimeout(90_000)
 await page.goto('/login')
 await page.locator('input[name="email"]').fill(process.env.PB_E2E_EMAIL!)
 await page.locator('input[name="password"]').fill(process.env.PB_E2E_PASSWORD!)
 await page.locator('button[type="submit"]').click(); await expect(page).toHaveURL(/\/projects$/)
 const headers={'X-PB-CSRF':(await page.context().cookies()).find(c=>c.name==='pb_csrf')!.value}
 const key=`work-${Date.now()}`;const base=`/api/v1/projects/${key}`
 const created=await page.request.post('/api/v1/projects',{headers,data:{key,name:'Work timer regression',workflow_template:'simple'}})
 expect(created.status(),await created.text()).toBe(201)
 try {
  const createdTicket=await page.request.post(`${base}/tickets`,{headers,data:{type:'task',title:'Common work fixture'}});expect(createdTicket.status()).toBe(201)
  const ticket=await createdTicket.json(); const path=`${base}/tickets/${ticket.seq}`
  for(let i=0;i<11;i++) {
   const started=await page.request.post(`${path}/work-sessions`,{headers,data:{request_id:`start-${i}`}});expect(started.status(),await started.text()).toBe(200)
   const row=await started.json()
   const finished=await page.request.post(`${path}/work-sessions/${row.id}/events`,{headers,data:{request_id:`finish-${i}`,action:'finish',result:'completed',...(i===0?{tokens:0}:{})}});expect(finished.status(),await finished.text()).toBe(200)
  }
  const actual=await(await page.request.get(path)).json(); expect(actual.actual_hours).toBeGreaterThanOrEqual(0);expect(actual.actual_point).toBe(1);expect(actual.actual_point_version).toBe('actual-v1')
  await page.goto(`/p/${key}/tickets/${ticket.seq}`)
  const work=page.getByRole('region',{name:'作業実績',exact:true})
  await expect(work).toBeVisible();await expect(work).toContainText('未計測 10件');await expect(work.locator('article')).toHaveCount(10)
  await work.getByRole('button',{name:'次へ',exact:true}).click();await expect(work.locator('article')).toHaveCount(1);await expect(work).toContainText('トークン: 0')
  await work.getByRole('button',{name:'記録を訂正',exact:true}).click()
  await work.getByLabel('稼働時間（分）', {exact:true}).fill('0')
  await work.getByLabel('訂正理由・取得元').fill('Measured zero active time for fixture')
  await work.getByRole('button',{name:'保存',exact:true}).click();await expect(work.locator('form')).toHaveCount(0);await expect(work).toContainText('取得元の申告')
  for(const width of [1440,768,360]) {
   await page.setViewportSize({width,height:1000});await work.scrollIntoViewIfNeeded()
   const bounds=await work.evaluate(e=>({right:e.getBoundingClientRect().right,width:e.getBoundingClientRect().width,scroll:document.documentElement.scrollWidth,viewport:innerWidth}))
   expect(bounds.width).toBeGreaterThan(0);expect(bounds.right).toBeLessThanOrEqual(width+1);expect(bounds.scroll).toBeLessThanOrEqual(bounds.viewport+1)
   await page.screenshot({path:info.outputPath(`work-${width}.png`),fullPage:true})
   await work.getByRole('button',{name:'記録を訂正',exact:true}).click();await work.locator('form').scrollIntoViewIfNeeded()
   expect(await work.locator('form').evaluate(e=>e.getBoundingClientRect().right)).toBeLessThanOrEqual(width+1)
   await page.screenshot({path:info.outputPath(`work-form-${width}.png`),fullPage:true})
   await work.getByRole('button',{name:'キャンセル',exact:true}).click()
  }
 } finally {
  execFileSync('make',['psql'],{cwd:process.env.PB_DEV_ROOT??'..',input:`DELETE FROM project WHERE key='${key}' AND name='Work timer regression';\n`,stdio:['pipe','pipe','pipe']})
 }
})
