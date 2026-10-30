import { chromium } from 'playwright'
import assert from 'node:assert/strict'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
for(const width of [1440,390]) {
 const context=await browser.newContext({viewport:{width,height:900}}),calls=[];let fail=false,status='draft'
 const account={id:'assets:cash',name:'Cash',type:'asset',commodities:['EUR']},entry={id:'draft1',date:'2026-01-01',payee:'Example',status:'draft',kind:'entry',postings:[{account:'assets:cash',amount:{v:'1',c:'EUR'}},{account:'assets:cash',amount:{v:'-1',c:'EUR'}}]}
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}));await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/mcp',route=>{const req=route.request().postDataJSON(),name=req.params?.name,args=req.params?.arguments ?? {};let result;calls.push({name,args})
  if(req.method==='server/discover')result={supportedVersions:['2026-07-28']}
  else if(name==='dashboard_list'||name==='db_list')result={structuredContent:{items:[]}}
  else if(name==='accounts_list')result={structuredContent:{items:[account]}}
  else if(name==='entries_list')result={structuredContent:{items:[{...entry,status}]}}
  else if(name==='entry_get')result={structuredContent:{entry:{...entry,status}}}
  else if(name==='history_of')result={structuredContent:{events:[]}}
  else if(name==='entry_post'){status='posted';result={structuredContent:{entry:{...entry,status},balanced:true,event_id:'event-post'}}}
  else if(name==='undo'){if(!fail)status='reversed';result=fail ? {isError:true,content:[{type:'text',text:'Already reversed'}]} : {structuredContent:{inverse_event_ids:['inverse']}}}
  else throw Error(name)
  return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto('http://127.0.0.1:5173/journal?entry=draft1');await page.getByRole('button',{name:'Post draft'}).click();await page.getByText('Posted entry',{exact:true}).waitFor();const before=calls.filter(c=>c.name==='entries_list').length;await page.getByRole('button',{name:'Undo',exact:true}).click();await page.getByText('Entry reversed',{exact:true}).waitFor();assert(calls.some(c=>c.name==='undo' && c.args.event_id==='event-post' && c.args.reason==='Undo from posting notification'));assert(calls.filter(c=>c.name==='entries_list').length>before,'undo should refresh journal');assert.deepEqual(errors,[])
 status='draft';fail=true;await page.reload();await page.getByRole('button',{name:'Post draft'}).click();await page.getByText('Posted entry',{exact:true}).waitFor();await page.getByRole('button',{name:'Undo',exact:true}).click();await page.getByText('Could not undo entry').waitFor();await page.getByText('Already reversed').waitFor();assert.equal(status,'posted');fail=false;await page.keyboard.press('Escape');await page.getByRole('button',{name:'New entry'}).click();const form=page.locator('.entry-form');await form.getByLabel('Date',{exact:true}).fill('2026-01-01');for(let i=0;i<2;i++){await form.getByRole('combobox',{name:'Account',exact:true}).nth(i).click();await page.getByRole('dialog',{name:'Choose account'}).getByRole('option',{name:/Cash.*assets:cash/}).click();await page.getByRole('dialog',{name:'Choose account'}).waitFor({state:'hidden'})}await form.getByLabel('Amount').nth(0).fill('1');await form.getByLabel('Amount').nth(1).fill('-1');await form.getByLabel('Post now').check();await form.getByRole('button',{name:'Save entry'}).click();await page.getByText('Posted entry',{exact:true}).waitFor();await page.getByRole('button',{name:'Undo',exact:true}).click();await page.getByText('Entry reversed',{exact:true}).waitFor();assert.equal(calls.filter(c=>c.name==='undo').length,3);await context.close();console.log(`post undo ${width}px passed`)
}
await browser.close()
