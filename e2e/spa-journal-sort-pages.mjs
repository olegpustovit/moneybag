import {chromium} from 'playwright'
import {existsSync} from 'node:fs'
import assert from 'node:assert/strict'

const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
for(const width of [1440,390]){
 const context=await browser.newContext({viewport:{width,height:900}});let attempts=0,hold=false,release
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 const names=['Zulu','Yankee','Mike','Mike','Alpha','Bravo']
 const items=names.map((payee,i)=>({id:`entry-${i}`,seq:6-i,date:'2026-01-01',payee,status:'posted',kind:'entry',tags:[],postings:[{account:'assets:cash',amount:{v:i===2 ? '5' : '1',c:'EUR'}}],links:[],actor:{id:'test',kind:'user'},created_at:'2026-01-01T00:00:00Z'}))
 await context.route('**/api/mcp',async route=>{const req=route.request().postDataJSON(),name=req.params.name,args=req.params.arguments ?? {};let result
  if(req.method==='server/discover')result={supportedVersions:['2026-07-28']}
  else if(name==='dashboard_list'||name==='db_list')result={structuredContent:{items:[]}}
  else if(name==='accounts_list')result={structuredContent:{items:[{id:'assets:cash',name:'Cash',type:'asset',commodities:['EUR']}]}}
  else if(name==='entries_list'){
   const cursor=args.cursor ?? 0,start=cursor ? 7-cursor : 0
   if(cursor===5 && hold)await new Promise(resolve=>{release=resolve})
   if(cursor===5 && attempts++<2)return route.fulfill({status:503,json:{error:'synthetic failure'}})
   const list=items.slice(start,start+2);result={structuredContent:{items:list,next_cursor:start+2<items.length ? list.at(-1).seq : undefined}}
  }else throw Error(name)
  return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.goto('http://127.0.0.1:5173/journal');const table=page.getByRole('region',{name:'Journal entries'});await table.getByText('Zulu',{exact:true}).waitFor();await table.getByRole('button',{name:'Payee'}).click();await page.waitForURL(/sort=payee/)
 const alert=page.getByRole('alert');await alert.getByText(/Request failed/).waitFor();assert.equal(await table.count(),0,'partial sort should not be displayed');await alert.getByRole('button',{name:'Retry'}).click()
 await table.getByText('Alpha',{exact:true}).waitFor();assert.equal(attempts,3);assert.deepEqual(await table.locator('tbody tr[data-row] td:nth-child(2)').allTextContents(),['Alpha','Bravo','Mike','Mike','Yankee','Zulu']);await table.getByRole('button',{name:'Posting amount'}).click({modifiers:['Shift']});await page.waitForURL(url=>url.searchParams.get('sort')==='payee,amount');const mike=table.locator('tbody tr[data-row]').filter({hasText:'Mike'});await page.waitForFunction(()=>{const rows=[...document.querySelectorAll('[aria-label="Journal entries"] tbody tr[data-row]')].filter(row=>row.textContent.includes('Mike'));return rows.map(row=>row.cells[3].textContent.trim()).join('|')==='1 €|5 €'});assert.deepEqual(await mike.locator('td:nth-child(4)').allTextContents(),['1 €','5 €']);await table.getByRole('button',{name:'Date'}).click();await page.waitForURL(url=>url.searchParams.get('sort')==='date');await table.getByText('Zulu',{exact:true}).waitFor();await table.getByRole('button',{name:'Payee'}).click({modifiers:['Shift']});await page.waitForURL(url=>url.searchParams.get('sort')==='date,payee');await table.getByText('Alpha',{exact:true}).waitFor();assert.equal(await table.locator('tbody tr[data-row]').count(),6);await page.goto('http://127.0.0.1:5173/journal');await page.reload();await table.getByText('Zulu',{exact:true}).waitFor();hold=true;await table.getByRole('button',{name:'Payee'}).click();await page.getByText(/Loading all matching entries to sort/).waitFor();await page.getByRole('button',{name:'Cancel sorting'}).click();await page.waitForURL(url=>!url.searchParams.has('sort'));await table.getByText('Zulu',{exact:true}).waitFor();release?.();assert.deepEqual(errors,[])
 await context.close();console.log(`journal complete sort ${width}px passed`)
}
await browser.close()
