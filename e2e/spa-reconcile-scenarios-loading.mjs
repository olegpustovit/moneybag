import { chromium } from 'playwright'
import assert from 'node:assert/strict'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
for(const width of [1440,390]) for(const mode of ['reconcile','scenarios']) {
 const context=await browser.newContext({viewport:{width,height:900},reducedMotion:'reduce'});let release,requested,hold=true,fail=false
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/mcp',async route=>{const req=route.request().postDataJSON(),name=req.params?.name;let result
  if(req.method==='server/discover')result={supportedVersions:['2026-07-28']}
  else if(name==='dashboard_list'||name==='db_list'||name==='accounts_list')result={structuredContent:{items:[]}}
  else if(name===(mode==='reconcile' ? 'reconcile_status' : 'model_get')){if(hold){requested=Date.now();await new Promise(resolve=>{release=resolve})}result=fail ? {isError:true,content:[{type:'text',text:'Synthetic loading failure'}]} : {structuredContent:mode==='reconcile' ? {accounts:[]} : {scenarios:[]}}}
  else throw Error(name)
  await route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));await page.addInitScript(()=>{window.loadingTimes=[];document.addEventListener('DOMContentLoaded',()=>{let visible=false;new MutationObserver(()=>{const now=!!document.querySelector('.reconcile-loading-card, .scenario-loading-controls');if(now!==visible){window.loadingTimes.push(performance.now());visible=now}}).observe(document.body,{childList:true,subtree:true})})})
 const url=`http://127.0.0.1:5173/${mode}`;await page.goto(url);const skeleton=page.locator(mode==='reconcile' ? '.reconcile-loading-card' : '.scenario-loading-controls').first();await skeleton.waitFor();assert(Date.now()-requested>=120);assert.equal(await page.locator('.skeleton-bar').first().evaluate(el=>getComputedStyle(el).animationName),'none');assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));if(mode==='scenarios'){assert.equal(await page.locator('.skeleton-chart').count(),1);assert.deepEqual(await page.locator('.loading-table th').allTextContents(),['Scenario','Monthly spend','End net worth','Runway','Edit'])}else assert.equal(await page.locator('.reconcile-loading-card').count(),3);await page.screenshot({path:`/tmp/moneybag-${mode}-loading-${width}.png`});hold=false;release();await skeleton.waitFor({state:'detached'});const times=await page.evaluate(()=>window.loadingTimes);assert(times[1]-times[0]>=290);await page.getByText(mode==='reconcile' ? 'Add an asset or liability account to begin reconciling.' : 'No scenarios yet. Add one to forecast your plan.').waitFor();assert.deepEqual(errors,[])
 fail=true;const retry=await context.newPage();await retry.goto(url);await retry.getByText('Synthetic loading failure').waitFor();fail=false;await retry.getByRole('button',{name:'Try again'}).click();await retry.getByText(mode==='reconcile' ? 'Add an asset or liability account to begin reconciling.' : 'No scenarios yet. Add one to forecast your plan.').waitFor();await context.close();console.log(`${mode} loading ${width}px passed`)
}
await browser.close()
