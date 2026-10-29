import { chromium } from 'playwright'
import assert from 'node:assert/strict'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
for(const width of [1440,390]) {
 const context=await browser.newContext({viewport:{width,height:900}});let release,fail=false,hold=true
 await context.route('**/api/session',async route=>{await new Promise(resolve=>setTimeout(resolve,500));await route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}})})
 await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/mcp',async route=>{const req=route.request().postDataJSON(),name=req.params?.name;let result
  if(req.method==='server/discover')result={supportedVersions:['2026-07-28']}
  else if(name==='dashboard_list')result={structuredContent:{items:[]}}
  else if(name==='db_list'){await new Promise(resolve=>setTimeout(resolve,600));result=fail ? {isError:true,content:[{type:'text',text:'Synthetic workspace failure'}]} : {structuredContent:{items:[{id:'sample',name:'Sample',columns:[]}]}}}
  else if(name==='db_query'){if(hold)await new Promise(resolve=>{release=resolve});result={structuredContent:{columns:[],rows:[]}}}
  else throw Error(name)
  await route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));await page.goto('http://127.0.0.1:5173/databases/sample');await page.getByText('Loading workspace…').waitFor();for(let i=0;!release && i<100;i++)await new Promise(resolve=>setTimeout(resolve,100));assert(release,'database request did not start');await page.locator('.grid-loading-table').waitFor();assert.equal(await page.getByText('Loading workspace…').count(),0);hold=false;release();await page.getByText('No rows match this filter. Add a row to start.').waitFor();assert.deepEqual(errors,[])
 fail=true;const retry=await context.newPage();await retry.goto('http://127.0.0.1:5173/databases/sample');await retry.getByText('Synthetic workspace failure').waitFor();fail=false;await retry.getByRole('button',{name:'Try again'}).click();await retry.getByText('No rows match this filter. Add a row to start.').waitFor();await context.close();console.log(`workspace loading ${width}px passed`)
}
await browser.close()
