import assert from 'node:assert/strict';
import {mkdtemp} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {existsSync} from 'node:fs';
import {chromium} from 'playwright';
const base=process.env.MONEYBAG_E2E_URL||'http://127.0.0.1:6460';
assert.match(base,/^http:\/\/127\.0\.0\.1:\d+$/,'Run against a dedicated local QA environment with synthetic data.');
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN||(existsSync(process.env.HOME+'/.nix-profile/bin/chromium')?process.env.HOME+'/.nix-profile/bin/chromium':undefined),args:['--no-sandbox']});
const context=await browser.newContext({viewport:{width:1440,height:1100}});
const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
let id=0;
async function call(name,args={}){
 const session=await (await page.request.get(base+'/api/session')).json();const csrf=session.csrf;
 const response=await page.request.post(base+'/api/mcp',{headers:{Origin:base,'X-CSRF-Token':csrf,'MCP-Protocol-Version':'2026-07-28','Mcp-Method':'tools/call','Mcp-Name':name,Accept:'application/json, text/event-stream'},data:{jsonrpc:'2.0',id:++id,method:'tools/call',params:{name,arguments:args,_meta:{'io.modelcontextprotocol/protocolVersion':'2026-07-28','io.modelcontextprotocol/clientInfo':{name:'moneybag-e2e',version:'1'},'io.modelcontextprotocol/clientCapabilities':{}}}}});
 assert.equal(response.status(),200,`${name}: ${await response.text()}`);const envelope=await response.json();assert.ok(!envelope.error,JSON.stringify(envelope.error));assert.ok(!envelope.result.isError,JSON.stringify(envelope.result.content));return envelope.result.structuredContent;
}
try{
 await page.goto(base+'/setup');
 if(await page.locator('[name="token"]').count()){
  await page.locator('[name="token"]').fill('synthetic-preview-token');await page.locator('[name="name"]').fill('Demo');await page.locator('[name="email"]').fill('demo@example.test');await page.locator('[name="password"]').fill('synthetic-preview-password');await page.getByRole('button',{name:'Create account',exact:true}).click();
 }else{
  await page.goto(base+'/login');await page.locator('[name="email"]').fill('demo@example.test');await page.locator('[name="password"]').fill('synthetic-preview-password');await page.getByRole('button',{name:'Sign in',exact:true}).click();
 }
 await page.waitForURL(url=>url.origin===base && !['/setup','/login'].includes(url.pathname));await page.goto(base+'/dashboards');await page.getByRole('heading',{name:'Dashboards',exact:true}).waitFor();
 const catalog=await call('accounts_list',{limit:500});
 if(!catalog.items.some(a=>a.id==='assets:bank')){
  for(const a of [{id:'assets:bank',name:'Everyday account',type:'asset',pot:'Cash',commodities:['EUR']},{id:'assets:savings',name:'Reserve',type:'asset',pot:'Savings',commodities:['USD']},{id:'equity:opening',name:'Opening balances',type:'equity',commodities:['EUR','USD']},{id:'income:salary',name:'Salary',type:'income',commodities:['EUR']}])await call('account_create',a);
  await call('rate_set',{pair:'EUR/USD',at:'2026-01-01T00:00:00Z',value:'1.12',source:'Synthetic example quote'});
  for(const [name,limit] of [['Housing',1000],['Groceries',400],['Dining',180],['Transport',120],['Subscriptions',80],['Other',200]]){
   const key=name.toLowerCase();await call('account_create',{id:'expenses:'+key,name,type:'expense',commodities:['EUR']});await call('budget_set_line',{id:key,name,limit:String(limit),accounts:['expenses:'+key]});
  }
  await call('entry_post',{date:'2026-03-01',payee:'Synthetic opening balances',commit:true,postings:[{account:'assets:bank',amount:{v:'18000',c:'EUR'}},{account:'equity:opening',amount:{v:'-18000',c:'EUR'}},{account:'assets:savings',amount:{v:'42000',c:'USD'}},{account:'equity:opening',amount:{v:'-42000',c:'USD'}}]});
  const now=new Date();
  for(let ago=6;ago>=0;ago--){const d=new Date(Date.UTC(now.getUTCFullYear(),now.getUTCMonth()-ago,5)).toISOString().slice(0,10);
   await call('entry_post',{date:d,payee:'Example salary',commit:true,postings:[{account:'assets:bank',amount:{v:'3400',c:'EUR'}},{account:'income:salary',amount:{v:'-3400',c:'EUR'}}]});
   for(const [key,value] of [['housing',950],['groceries',ago===0?445:350+ago*13],['dining',120+ago*8],['transport',65+ago*4],['subscriptions',55],['other',105+ago*9]])await call('entry_post',{date:d,payee:'Example '+key,commit:true,postings:[{account:'assets:bank',amount:{v:String(-value),c:'EUR'}},{account:'expenses:'+key,amount:{v:String(value),c:'EUR'}}]});
  }
  await call('db_update_cells',{db:'assumptions',updates:Object.entries({start_net_usd:65000,wise_usd:55000,kyiv_usd:5000,income_usd:5000,spend_eur:2500,eurusd:1.12}).map(([row_id,value])=>({row_id,column:'value',value}))});
 }
 const dashboards=await call('dashboard_list');
 for(const [name,title,template]of [['budget','Budget vs actual','budget-vs-actual'],['forecast','Forecast & scenarios','forecast']]){
  if(!dashboards.items.some(d=>d.id===name)){await call('dashboard_create',{id:name,title,from_template:template});const validation=await call('dashboard_validate',{dashboard_id:name});assert.equal(validation.ok,true,JSON.stringify(validation));await call('dashboard_publish',{dashboard_id:name,version:1,note:'Synthetic example dashboard'});}
 }
 await page.goto(base+'/dashboards/budget');await page.getByRole('heading',{name:'Budget vs actual',exact:true}).waitFor();
 await page.getByRole('button',{name:'Edit',exact:true}).click();await page.getByRole('button',{name:'Advanced JSON'}).click();await page.getByLabel('Dashboard specification').waitFor();await page.getByText('Validation passed.',{exact:true}).waitFor();await page.getByRole('button',{name:'Close',exact:true}).last().click();await page.getByRole('dialog').waitFor({state:'hidden'});
 const captures=await mkdtemp(join(tmpdir(),'moneybag-spa-smoke-'));
 await page.emulateMedia({colorScheme:'light'});await page.screenshot({path:join(captures,'dashboard-light.png'),fullPage:true});
 await page.emulateMedia({colorScheme:'dark'});await page.screenshot({path:join(captures,'dashboard-dark.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.emulateMedia({colorScheme:'light'});await page.screenshot({path:join(captures,'dashboard-mobile.png'),fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Mobile page overflows');
 const chart=page.locator('.panel').filter({has:page.getByRole('heading',{name:'All-in spend by month'})});await chart.getByRole('button',{name:'Table',exact:true}).click();const chartTable=chart.getByRole('region',{name:'All-in spend by month data'});await chartTable.waitFor();assert.ok(await chartTable.getByRole('row').count()>1,'Chart data should contain rows');
 await page.setViewportSize({width:1440,height:1100});
 for(const [path,title] of [['/accounts','Accounts'],['/journal','Journal'],['/budgets','Budgets'],['/databases/assumptions','Assumptions'],['/scenarios','Scenarios'],['/imports','Imports'],['/reconcile','Reconcile'],['/shared','Shared dashboards'],['/activity','Activity'],['/settings','Settings']]){await page.goto(base+path);await page.getByRole('heading',{name:title,exact:true}).first().waitFor();assert.equal(await page.getByRole('heading',{name:/Could not load/}).count(),0,`${path}: ${await page.locator('main').innerText()}`);}
 await page.goto(base+'/budgets');await page.getByRole('button',{name:'Edit Limit for Groceries'}).click();const limit=page.getByLabel('Limit for Groceries');const updatedLimit=(await limit.inputValue())==='410'?'411':'410';await limit.fill(updatedLimit);const saved=page.waitForResponse(response=>response.url().endsWith('/api/mcp') && response.request().postDataJSON()?.params?.name==='budget_set_line');await limit.press('Enter');await saved;await page.getByRole('button',{name:'Edit Limit for Groceries'}).waitFor();assert.equal((await call('budget_get',{})).lines.find(line=>line.id==='groceries').limit_exact,updatedLimit);await page.reload();await page.getByRole('button',{name:'Edit Limit for Groceries'}).click();assert.equal(await page.getByLabel('Limit for Groceries').inputValue(),updatedLimit);await page.getByLabel('Limit for Groceries').press('Escape');
 await page.goto(base+'/imports');await page.getByLabel('Account',{exact:true}).selectOption('assets:bank');await page.locator('#statement-file').setInputFiles({name:'synthetic.csv',mimeType:'text/csv',buffer:Buffer.from('Date,Amount,Currency,Description,ID\n2026-10-25,-12.34,EUR,Example coffee,e2e-coffee-1\n')});await page.getByRole('button',{name:'Stage statement',exact:true}).click();await page.waitForURL(/\/imports\/.+/);await page.getByRole('button',{name:'Commit import',exact:true}).click();await page.getByRole('heading',{name:'Undo import'}).waitFor();
 const preview=await call('dashboard_preview',{dashboard_id:'budget',version:'published'});assert.ok(preview.values&&!preview.html_url&&!preview.png_url,'Preview must return values only');
 assert.deepEqual(errors,[]);console.log('Browser smoke passed: setup/login, dashboards, themes/mobile, all routes, cell persistence, import commit, HTML and PNG preview.');
}finally{await browser.close();}
