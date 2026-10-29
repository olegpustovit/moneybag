import { chromium } from 'playwright'
import assert from 'node:assert/strict'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
for(const width of [1440,390]) {
 const context=await browser.newContext({viewport:{width,height:900},reducedMotion:'reduce'});let release,requested,fast=false,fail=false
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/mcp',async route=>{const req=route.request().postDataJSON(),name=req.params?.name;let result
  if(req.method==='server/discover') result={supportedVersions:['2026-07-28']}
  else if(name==='dashboard_list'||name==='db_list'||name==='accounts_list') result={structuredContent:{items:[]}}
  else if(name==='entries_list'){requested=Date.now();if(!fast)await new Promise(resolve=>{release=resolve});result=fail ? {isError:true,content:[{type:'text',text:'Synthetic journal failure'}]} : {structuredContent:{items:[]}}}
  else throw Error(name)
  await route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));await page.addInitScript(()=>{window.loadingTimes=[];document.addEventListener('DOMContentLoaded',()=>{let visible=false;new MutationObserver(()=>{const now=!!document.querySelector('.journal-loading-table');if(now!==visible){window.loadingTimes.push(performance.now());visible=now}}).observe(document.body,{childList:true,subtree:true})})})
 await page.goto('http://127.0.0.1:5173/journal');const skeleton=page.locator('.journal-loading-table');await skeleton.waitFor({timeout:5000}).catch(async error=>{console.log('PAGE',await page.locator('body').innerText(), 'ERRORS',errors);throw error});assert(Date.now()-requested>=120);assert.deepEqual(await skeleton.locator('th').allTextContents(),['Date','Payee','Accounts','Posting amount','Status','Tags']);assert.equal(await skeleton.locator('tbody tr').count(),8);assert.equal(await skeleton.locator('.skeleton-bar').first().evaluate(el=>getComputedStyle(el).animationName),'none');assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:`/tmp/moneybag-journal-loading-${width}.png`});release();await skeleton.waitFor({state:'detached'});const times=await page.evaluate(()=>window.loadingTimes);assert(times[1]-times[0]>=290,`visible for ${times[1]-times[0]} ms`);await page.getByText('No entries match these filters.').waitFor();assert.deepEqual(errors,[])
 fast=true;fail=true;const retry=await context.newPage();await retry.goto('http://127.0.0.1:5173/journal');await retry.getByText('Synthetic journal failure').waitFor();fail=false;await retry.getByRole('button',{name:'Try again'}).click();await retry.getByText('No entries match these filters.').waitFor();await context.close();console.log(`journal loading ${width}px passed`)
}
await browser.close()
