// The speed budget from docs/telemetry.md, measured on the real page against a seeded production build.
// repeat navigation under 100 ms to paint, range change under 50 ms, payload under 100 KB.
import { chromium } from 'playwright'
import assert from 'node:assert/strict'

const base=process.argv[2] ?? process.env.MONEYBAG_E2E_URL ?? 'http://127.0.0.1:16602'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
const context=await browser.newContext({viewport:{width:1440,height:900},colorScheme:'light'}),page=await context.newPage()
page.setDefaultTimeout(3000)
await page.goto(`${base}/login`);await page.locator('[name=email]').fill('demo@example.test');await page.locator('[name=password]').fill('synthetic-preview-password');await page.locator('button[type=submit]').click();await page.waitForURL(u => !u.pathname.startsWith('/login'))
const median=list => [...list].sort((a,b) => a-b)[list.length>>1],worst=list => Math.max(...list)
// Time from the click to the frame after the DOM says the new page is there.
const toPaint=(selector,expected) => page.evaluate(([sel,text]) => new Promise(resolve => {
 const t0=performance.now(),link=document.querySelector(sel);link.click()
 const check=() => {if(document.querySelector('main h1')?.textContent===text)requestAnimationFrame(() => resolve(performance.now()-t0));else if(performance.now()-t0>3000)resolve(9999);else requestAnimationFrame(check)}
 requestAnimationFrame(check)
}),[selector,expected])

await page.goto(`${base}/dashboards/spending`);await page.locator('.u-over').first().waitFor();await page.waitForLoadState('networkidle',{timeout:2000}).catch(() => {})
const nav=[],titles=[['a[href="/dashboards/income"]','What came in, and was it what I expected?'],['a[href="/dashboards/spending"]','Where did the money go, and why did it move?']]
// First visits warm the caches; only repeat navigation has the budget.
for(const [sel,text] of titles)await toPaint(sel,text)
await page.waitForLoadState('networkidle',{timeout:2000}).catch(() => {});await page.waitForTimeout(300)
for(let i=0;i<8;i++){const [sel,text]=titles[i%2];nav.push(await toPaint(sel,text));await page.waitForTimeout(120)}
console.log(`repeat navigation: median ${median(nav).toFixed(0)} ms, worst ${worst(nav).toFixed(0)} ms (${nav.map(n => Math.round(n)).join(' ')})`)

await page.locator('.tm-presets button').first().waitFor();await page.waitForTimeout(300)
const presetCount=await page.locator('.tm-presets button').count(),range=[]
// Time from the click until the page has reacted and painted: the resolved dates change, then one frame.
for(let i=0;i<16;i++){
 const t=await page.evaluate(index => new Promise(resolve => {
  const status=document.querySelector('.tm-time-status'),before=status.textContent,button=document.querySelectorAll('.tm-presets button')[index],t0=performance.now();button.click()
  const check=() => {if(document.querySelector('.tm-time-status').textContent!==before)requestAnimationFrame(() => resolve(performance.now()-t0));else if(performance.now()-t0>2000)resolve(-1);else queueMicrotask(() => setTimeout(check,0))}
  check()
 }),i%presetCount)
 if(t>=0)range.push(t);await page.waitForTimeout(80)
}
console.log(`range change: median ${median(range).toFixed(0)} ms, worst ${worst(range).toFixed(0)} ms (${range.map(n => Math.round(n)).join(' ')})`)

const sizes=new Map();page.on('response',async r => {if(!/\/api\/(dashboards|bootstrap)/.test(r.url()))return;const h=await r.allHeaders().catch(() => ({}));const sized=Number(h['content-length']) || (await r.body().catch(() => Buffer.alloc(0))).length;sizes.set(new URL(r.url()).pathname,Math.max(sizes.get(new URL(r.url()).pathname) ?? 0,sized))})
await page.goto(`${base}/dashboards/spending`);await page.waitForLoadState('networkidle',{timeout:2000}).catch(() => {})
const largest=Math.max(0,...sizes.values());console.log(`payload: ${[...sizes].map(([k,v]) => `${k} ${(v/1024).toFixed(1)} KB`).join(', ')}`)

// Stepping: eight quick clicks on Previous must feel like one smooth move.
await page.goto(`${base}/dashboards/spending`);await page.locator('.u-over').first().waitFor();await page.waitForLoadState('networkidle',{timeout:2000}).catch(() => {})
await page.locator('.tm-presets button').first().click();await page.waitForTimeout(600)
await page.evaluate(() => {window.__cls=0;new PerformanceObserver(list => {for(const e of list.getEntries())if(!e.hadRecentInput)window.__cls+=e.value}).observe({type:'layout-shift',buffered:false})})
let viewRequests=0;const countView=r => {if(/\/api\/dashboards\/[^/]+\/view/.test(r.url()))viewRequests++};page.on('request',countView)
const frames=[]
for(let i=0;i<8;i++){
 const t=await page.evaluate(() => new Promise(resolve => {
  const status=document.querySelector('.tm-time-status'),before=status.textContent,t0=performance.now();document.querySelector('[aria-label="Previous period"]').click()
  const check=() => {if(document.querySelector('.tm-time-status').textContent!==before)requestAnimationFrame(() => resolve(performance.now()-t0));else if(performance.now()-t0>1000)resolve(-1);else setTimeout(check,0)}
  check()
 }))
 if(t>=0)frames.push(t);await page.waitForTimeout(120)
}
await page.waitForTimeout(1200);page.off('request',countView)
await page.locator('[aria-label="Previous period"]').click();await page.waitForTimeout(400)
const cls=await page.evaluate(() => window.__cls),focus=await page.evaluate(() => document.activeElement?.getAttribute('aria-label'))
console.log(`stepping: CLS ${cls.toFixed(4)}, view requests ${viewRequests}, frame median ${median(frames).toFixed(0)} ms worst ${worst(frames).toFixed(0)} ms, focus stays on "${focus}"`)
// After one more step, no Stat figure may change once it has painted for the range.
const stats=() => page.evaluate(() => [...document.querySelectorAll('.tm-stat-value')].map(e => e.textContent).join('|'))
await page.evaluate(() => document.querySelector('[aria-label="Previous period"]').click());await page.waitForTimeout(60)
const first=await stats(),seen=new Set([first]);for(let i=0;i<20;i++){await page.waitForTimeout(50);seen.add(await stats())}
console.log(`stat figures after first paint: ${seen.size===1 ? 'unchanged' : `changed ${seen.size-1} time(s): ${[...seen].join(' -> ')}`}`)
// A slow server on a page with a server-only panel (Now has an entries list): the sync line shows, server-only panels dim, and everything settles.
await page.goto(`${base}/dashboards/now`);await page.locator('.u-over').first().waitFor();await page.waitForLoadState('networkidle',{timeout:2000}).catch(() => {})
await page.route('**/api/dashboards/*/view*',async route => {await new Promise(r => setTimeout(r,800));await route.continue()})
const field=page.getByLabel('Time range. Type anything');await field.click();await field.fill('2026-05-03 to 2026-05-19');await field.press('Enter');await page.waitForTimeout(450)
const slow=await page.evaluate(() => ({line:!!document.querySelector('.tm-sync-line'),status:document.querySelector('.tm-asof')?.textContent,busy:document.querySelectorAll('.panel[aria-busy="true"]').length}))
await page.waitForTimeout(1200)
const settled=await page.evaluate(() => ({line:!!document.querySelector('.tm-sync-line'),status:document.querySelector('.tm-asof')?.textContent,busy:document.querySelectorAll('.panel[aria-busy="true"]').length}))
await page.unroute('**/api/dashboards/*/view*')
console.log(`slow server: during ${JSON.stringify(slow)}, after ${JSON.stringify(settled)}`)
await browser.close()
assert.ok(median(nav)<100,`repeat navigation ${median(nav)} ms`);assert.ok(median(range)<50,`range change ${median(range)} ms`);assert.ok(worst(range)<100,`worst range change ${worst(range)} ms`);assert.ok(largest<100*1024,`payload ${largest} B`)
assert.ok(cls<0.01,`layout shift ${cls}`);assert.ok(viewRequests<=2,`${viewRequests} view requests while stepping`);assert.ok(median(frames)<50,`step frame ${median(frames)} ms`)
assert.equal(seen.size,1,'a figure changed after its first paint');assert.ok(slow.line && slow.busy>0 && slow.status?.includes('Syncing'),'slow server shows the indicator and dims server-only panels');assert.ok(!settled.line && settled.busy===0,'settles after the response')
console.log('speed budget ok')
