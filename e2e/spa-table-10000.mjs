import { chromium } from 'playwright'
import { existsSync } from 'node:fs'
import assert from 'node:assert/strict'

const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
for(const width of [1440,390]) {
 const context=await browser.newContext({viewport:{width,height:900}})
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/mcp',route=>{const req=route.request().postDataJSON();let result
  if(req.method==='server/discover')return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result:{supportedVersions:['2026-07-28']}}})
  const name=req.params.name
  if(name==='dashboard_list')result={items:[{id:'large',title:'Large',draft_version:0,published_version:1,revision:1}]}
  else if(name==='db_list')result={items:[]}
  else if(name==='dashboard_get')result={author:{id:'synthetic',kind:'user'},status:'published',version:1,spec:{id:'large',title:'Large',version:1,pages:[{id:'main',title:'Main',layout:[{cols:[{component:'table',span:12}]}]}],components:[{id:'table',type:'table',title:'Large table'}]}}
  else if(name==='dashboard_preview')result={validation:{ok:true,errors:[],warnings:[],cost:1},values:{table:{type:'table',title:'Large table',columns:[{key:'name',label:'Name',format:'text',align:'left'},{key:'value',label:'Value',format:'int',align:'right'}],rows:Array.from({length:10000},(_,i)=>({name:{v:`Item ${String(i).padStart(5,'0')}`,t:`Item ${String(i).padStart(5,'0')}`},value:{v:i,t:String(i)}}))}}}
  else throw Error(name)
  return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result:{structuredContent:result}}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.goto('http://127.0.0.1:5173/dashboards/large')
 const region=page.getByRole('region',{name:'Large table table'});await region.getByText('Item 00000',{exact:true}).waitFor()
 assert.equal(await region.locator('tr[data-row]').first().evaluate(row=>row.getBoundingClientRect().height),32)
 await region.getByRole('button',{name:'Name',exact:true}).click();await page.waitForURL(/sort=name/)
 assert.equal(await region.getByRole('columnheader',{name:/Name/}).getAttribute('aria-sort'),'ascending')
 assert((await region.locator('tbody tr').count())<=60)
 assert((await region.locator('tfoot').textContent()).includes('49,995,000'))
 const result=await region.evaluate(async el=>{const samples=[];let maxRows=0,last=performance.now();for(let i=1;i<=120;i++){await new Promise(requestAnimationFrame);const now=performance.now();samples.push(now-last);last=now;el.scrollTop=(el.scrollHeight-el.clientHeight)*i/120;maxRows=Math.max(maxRows,el.querySelectorAll('tbody tr').length)}return {maxRows,frames:samples.length,slow:samples.filter(ms=>ms>25).length}})
 await region.evaluate(el=>{el.scrollTop=el.scrollHeight});await region.getByText('Item 09999',{exact:true}).waitFor();assert(result.maxRows<=60);assert((await region.locator('tbody tr').count())<=60)
 await page.getByLabel('Filter',{exact:true}).fill('Item 09999');await region.getByText('Item 09999',{exact:true}).waitFor();assert.equal(await region.locator('tbody tr[data-row]').count(),1)
 assert.deepEqual(errors,[]);console.log(`10,000 rows ${width}px: max ${result.maxRows} DOM rows, ${result.slow}/${result.frames} frame intervals over 25ms`)
 await context.close()
}
await browser.close()
