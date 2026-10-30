// Publish is gated on /render/{token} reporting, so it must report however badly the page goes. Forces each failure and reads what was posted.
// Also captures the error and loading states: spa-render-report.mjs <out-dir> [base-url]
import { chromium } from 'playwright'
import assert from 'node:assert/strict'

const out=process.argv[2] ?? '.',base=process.argv[3] ?? 'http://127.0.0.1:16602'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
const context=await browser.newContext({viewport:{width:1200,height:900}}),page=await context.newPage();page.setDefaultTimeout(3000)
await page.goto(`${base}/login`);await page.locator('[name=email]').fill('demo@example.test');await page.locator('[name=password]').fill('synthetic-preview-password');await page.locator('button[type=submit]').click();await page.waitForURL(u => !u.pathname.startsWith('/login'))
const shell=await (await page.request.get(`${base}/`)).text(),good=await (await page.request.get(`${base}/api/dashboards/spending/view`)).json()
const keys=['layout','checks','timing_ms'],checkKeys=['overflow','label_collisions','empty_panels','time_scope_applies']
let failed=0
async function run(name,{view,cube,expect}){
 const p=await context.newPage();p.setDefaultTimeout(3000);let report=null
 await p.route('**/render/fake',r => r.fulfill({contentType:'text/html',body:shell}))
 await p.route('**/api/render/fake/view',r => view===null ? r.fulfill({status:500,body:'{}'}) : r.fulfill({json:view}))
 await p.route('**/api/render/fake/cube',r => r.fulfill({status:404,body:'{}'}))
 await p.route('**/api/render/fake/report',r => {report=JSON.parse(r.request().postData());return r.fulfill({status:204})})
 await p.goto(`${base}/render/fake`);const t0=Date.now();while(!report && Date.now()-t0<7000)await p.waitForTimeout(50)
 try {
  assert.ok(report,'never reported');assert.deepEqual(Object.keys(report).sort(),[...keys].sort());assert.deepEqual(Object.keys(report.checks).sort(),[...checkKeys].sort())
  for(const box of report.layout)assert.deepEqual(Object.keys(box).sort(),['component','h','w','x','y'])
  expect(report);console.log(`ok   ${name} (${report.layout.length} panels, empty: ${report.checks.empty_panels.join() || 'none'}, ${report.timing_ms} ms)`)
 } catch(error){failed++;console.log(`FAIL ${name}: ${error.message.split('\n')[0]} ${JSON.stringify(report)?.slice(0,300)}`)}
 await p.close();void cube
}
await run('a healthy page',{view:good,expect:r => {assert.ok(r.layout.length>=5);assert.equal(r.checks.time_scope_applies,true)}})
await run('the view request fails',{view:null,expect:r => assert.deepEqual(r.checks.empty_panels,['page'])})
await run('one panel has a value no renderer understands',{view:{...good,values:{...good.values,total:{type:'nonsense',metric:7}}},expect:r => assert.ok(r.checks.empty_panels.includes('total'))})
await run('one panel has no value at all',{view:{...good,values:Object.fromEntries(Object.entries(good.values).filter(([k]) => k!=='ranked'))},expect:r => assert.ok(r.checks.empty_panels.includes('ranked'))})
await run('a panel value makes a renderer throw',{view:{...good,values:{...good.values,ranked:{type:'ranked_bars',metric:'nope',series:'not a list'}}},expect:r => assert.ok(r.layout.some(b => b.component==='ranked'))})
await run('no clock declared',{view:{...good,spec:{...good.spec,scope:undefined}},expect:r => assert.equal(r.checks.time_scope_applies,null)})

// Error and loading states, in the real shell.
await page.route('**/api/dashboards/income/view*',r => r.fulfill({status:500,body:'{}'}));await page.goto(`${base}/dashboards/income`);await page.getByRole('alert').waitFor({timeout:8000});await page.screenshot({path:`${out}/state-error.png`})
await page.unroute('**/api/dashboards/income/view*');await page.route('**/api/dashboards/trips/view*',async r => {await new Promise(done => setTimeout(done,2500));await r.continue()});await page.goto(`${base}/dashboards/trips`);await page.locator('.tm-page-skeleton').waitFor({timeout:8000});await page.screenshot({path:`${out}/state-loading.png`})
await browser.close()
console.log(failed ? `${failed} failed` : 'render report ok')
process.exit(failed ? 1 : 0)
