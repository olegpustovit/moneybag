import {chromium} from 'playwright'
import {existsSync} from 'node:fs'
import assert from 'node:assert/strict'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
for(const width of [1440,390]) {
 const context=await browser.newContext({viewport:{width,height:900}});let synced=false,attempts=0,countRequests=0
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/settings',route=>route.fulfill({json:{feeds:[{id:'wise',account:'assets:cash',format:'wise_csv'}],agents:[]}}))
 await context.route('**/api/imports/count',route=>{countRequests++;return route.fulfill({json:{count:synced?1:0}})})
 await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/imports',route=>route.fulfill({json:{batches:synced?[{id:'batch1',account:'assets:cash',status:'ready',from:'2026-01-01',to:'2026-01-31'}]:[]}}))
 await context.route('**/api/mcp',route=>{const req=route.request().postDataJSON(),name=req.params.name;let result;if(req.method==='server/discover')result={supportedVersions:['2026-07-28']};else if(['dashboard_list','db_list','accounts_list'].includes(name))result={structuredContent:{items:[]}};else if(name==='feeds_sync'){attempts++;assert.equal(req.params.arguments.feed,'wise');if(attempts===1)result={isError:true,content:[{type:'text',text:'Feed unavailable'}]};else {synced=true;result={structuredContent:{batch_id:'batch1'}}}}else if(name==='import_preview')result={structuredContent:{proposed:[],duplicates:[]}};else throw Error(name);return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})})
 const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto('http://127.0.0.1:5173/imports');await page.getByRole('heading',{name:'Imports',exact:true}).waitFor();await page.goto('http://127.0.0.1:5173/settings');await page.evaluate(()=>{window.syncSentinel=true});await page.getByRole('button',{name:'Sync',exact:true}).click();await page.getByRole('alert').getByText('Feed unavailable').waitFor();assert(page.url().endsWith('/settings'));await page.getByRole('button',{name:'Sync',exact:true}).click();await page.waitForURL('**/imports/batch1');await page.getByRole('heading',{name:'Review import'}).waitFor();assert(await page.getByText(/assets:cash.*ready/).isVisible());assert.equal(await page.evaluate(()=>window.syncSentinel),true);assert(countRequests>=2);assert.deepEqual(errors,[]);console.log(`feed sync ${width}px passed`);await context.close()
}
await browser.close()
