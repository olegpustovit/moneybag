import { chromium } from 'playwright'
import { existsSync } from 'node:fs'
import assert from 'node:assert/strict'

const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
for(const width of [1440,390]) for(const size of [2,200]) for(const moveAway of [false,true]) {
 const context=await browser.newContext({viewport:{width,height:900}})
 let release,started;const wait=new Promise(resolve=>{release=resolve}),requested=new Promise(resolve=>{started=resolve})
 const entries=Array.from({length:size+2},(_,i)=>({id:`entry-${i}`,seq:size+2-i,date:'2026-01-01',payee:`Payee ${i}`,status:'posted',kind:'entry',tags:[],postings:[{account:'assets:cash',amount:{v:'1',c:'EUR'}}],links:[],actor:{id:'test',kind:'user'},created_at:'2026-01-01T00:00:00Z'}))
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/mcp',async route=>{const req=route.request().postDataJSON(),name=req.params.name,args=req.params.arguments ?? {};let result
  if(req.method==='server/discover')result={supportedVersions:['2026-07-28']}
  else if(name==='dashboard_list'||name==='db_list')result={structuredContent:{items:[]}}
  else if(name==='accounts_list')result={structuredContent:{items:[{id:'assets:cash',name:'Cash',type:'asset',commodities:['EUR']}]}}
  else if(name==='entries_list'){if(args.cursor){started();await wait;result={structuredContent:{items:entries.slice(size)}}}else result={structuredContent:{items:entries.slice(0,size),next_cursor:3}}}
  else throw Error(name)
  return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));await page.goto('http://127.0.0.1:5173/journal');await page.getByText('Payee 0',{exact:true}).waitFor();if(size>2){await page.getByRole('region',{name:'Journal entries'}).evaluate(el=>{el.scrollTop=el.scrollHeight});await requested}await page.getByText(`Payee ${size-1}`,{exact:true}).waitFor()
 const cell=page.locator(`.shared-data-table tbody tr[data-row="${size-1}"] td`).nth(1);await cell.focus();await page.keyboard.press('ArrowDown');await requested
 if(moveAway)await page.getByLabel('Search',{exact:true}).focus()
 release();await page.getByText(`Payee ${size}`,{exact:true}).waitFor()
 if(moveAway)assert(await page.getByLabel('Search',{exact:true}).evaluate(el=>document.activeElement===el))
 else await page.waitForFunction(n=>document.activeElement?.parentElement?.getAttribute('data-row')===String(n) && document.activeElement?.textContent===`Payee ${n}`,size)
 assert.deepEqual(errors,[]);await context.close();console.log(`journal page keys ${width}px ${size} rows ${moveAway ? 'focus moved' : 'continue'} passed`)
}
await browser.close()
