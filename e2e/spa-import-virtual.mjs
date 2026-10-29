import { chromium } from 'playwright'
import { existsSync } from 'node:fs'
import assert from 'node:assert/strict'

const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
const context=await browser.newContext({viewport:{width:390,height:900}}),calls=[]
const batch={id:'batch1',account:'assets:bank',format:'generic_csv',status:'ready',from:'2026-01-01',to:'2026-01-31',created_at:'2026-01-31T00:00:00Z'}
const rows=Array.from({length:220},(_,i)=>({id:`r${i}`,batch_id:'batch1',skip:false,account:'assets:bank',kind:'entry',raw:{},dedupe_key:`key${i}`,proposed:{date:'2026-01-01',payee:`Payee ${i}`,postings:[{account:'assets:bank',amount:{v:'-1',c:'EUR'}},{account:'expenses:other',amount:{v:'1',c:'EUR'}}],tags:[]}}))
await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Alex',email:'alex@example.test',is_admin:true},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
await context.route('**/api/imports',route=>route.fulfill({json:{batches:[batch]}}))
await context.route('**/api/mcp',route=>{const req=route.request().postDataJSON(),name=req.params.name,args=req.params.arguments ?? {};calls.push({name,args});let result;if(req.method==='server/discover')result={supportedVersions:['2026-07-28']};else if(name==='dashboard_list'||name==='db_list')result={structuredContent:{items:[]}};else if(name==='accounts_list')result={structuredContent:{items:[{id:'assets:bank',name:'Bank',type:'asset',commodities:['EUR']},{id:'expenses:food',name:'Food',type:'expense',commodities:['EUR']}]}};else if(name==='import_preview')result={structuredContent:{proposed:rows,duplicates:[],unmatched:[]}};else if(name==='import_edit'){const row=rows.find(item=>item.id===args.row_id);row.proposed.postings[1].account=args.set.account;row.proposed.tags=args.set.tags;row.skip=args.set.skip;result={structuredContent:row}}else throw Error(name);return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})})
const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto('http://127.0.0.1:5173/imports/batch1')
const table=page.getByRole('region',{name:'Statement rows'}),first=table.getByRole('row').filter({hasText:'Payee 0'})
await first.getByLabel('Counterpart account').fill('expenses:food');await first.getByLabel('Tags').fill('groceries');await first.getByLabel('Skip row').check()
assert(await page.getByRole('button',{name:'Commit import'}).isDisabled());assert((await table.locator('tbody tr').count())<=60)
await table.evaluate(el=>{el.scrollTop=el.scrollHeight});await table.getByText('Payee 219',{exact:true}).waitFor();assert((await table.locator('tbody tr').count())<=60)
await table.evaluate(el=>{el.scrollTop=0});await first.getByLabel('Counterpart account').waitFor();assert.equal(await first.getByLabel('Counterpart account').inputValue(),'expenses:food');assert.equal(await first.getByLabel('Tags').inputValue(),'groceries');assert(await first.getByLabel('Skip row').isChecked());assert(await page.getByRole('button',{name:'Commit import'}).isDisabled())
await first.getByRole('button',{name:'Save row'}).click();await page.waitForFunction(()=>!document.querySelector('.import-commit button')?.disabled);assert(calls.some(call=>call.name==='import_edit' && call.args.row_id==='r0' && call.args.set.account==='expenses:food' && call.args.set.skip));assert.deepEqual(errors,[])
await context.close();await browser.close();console.log('import drafts survive virtual scrolling')
