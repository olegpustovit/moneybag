import {chromium} from 'playwright'
import {existsSync} from 'node:fs'
import assert from 'node:assert/strict'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
for(const width of [1440,390]) {
 const context=await browser.newContext({viewport:{width,height:900}}),checks=[],calls=[]
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/settings',route=>route.fulfill({json:{feeds:[],agents:[]}}))
 await context.route('**/api/expr/catalog',route=>route.fulfill({json:{revision:1,variables:['row'],sources:[{name:'table:sample',label:'Sample',fields:{amount:'double'}},{name:'journal',label:'Journal',fields:{payee:'string'}}],functions:[],aggregates:[]}}))
 await context.route('**/api/expr/check',route=>{checks.push(route.request().postDataJSON());return route.fulfill({json:{ok:true,type:'bool',issues:[]}})})
 await context.route('**/api/mcp',route=>{const req=route.request().postDataJSON();let result;if(req.method==='server/discover')result={supportedVersions:['2026-07-28']};else if(req.method==='tools/list')result={tools:['db_query','db_delete_rows','tags_set','expr_eval','view_upsert'].map(name=>({name,inputSchema:{properties:name==='view_upsert'?{id:{type:'string'},query:{type:'object'}}:name==='expr_eval'?{expr:{type:'string'}}:name==='tags_set'?{where:{type:'string'}}:{db:{type:'string'},where:{type:'string'}},required:name==='view_upsert'?['id','query']:name==='expr_eval'?['expr']:name==='tags_set'?[]:['db']}}))};else if(['dashboard_list','db_list'].includes(req.params.name))result={structuredContent:{items:[]}};else {calls.push(req.params);result={structuredContent:{ok:true}}}return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})})
 const page=await context.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));await page.goto('http://127.0.0.1:5173/settings');await page.getByText('MCP console').click()
 for(const name of ['db_query','db_delete_rows','tags_set','expr_eval']) {
  await page.getByLabel('Tool',{exact:true}).click();await page.getByRole('option',{name,exact:true}).click()
  if(name.startsWith('db_'))await page.getByLabel('db',{exact:false}).fill('sample')
  const field=name==='expr_eval'?'expr':'where',expression=name==='expr_eval'?'1 + 2':'row.amount > 1'
  await page.getByRole('textbox',{name:field,exact:true}).fill(expression)
  const source=name.startsWith('db_')?'table:sample':name==='tags_set'?'journal':''
  await page.waitForResponse(response=>response.url().endsWith('/api/expr/check') && response.request().postDataJSON().from===source && response.request().postDataJSON().expr===expression);assert(checks.some(c=>c.from===source && c.expr===expression))
  await page.getByRole('button',{name:'Run tool'}).click();await page.getByText(/"ok": true/).waitFor()
  assert.equal(calls.at(-1).name,name);assert.equal(calls.at(-1).arguments[field],expression);if(name.startsWith('db_'))assert.equal(calls.at(-1).arguments.db,'sample')
 }
 await page.getByLabel('Tool',{exact:true}).click();await page.getByRole('option',{name:'view_upsert',exact:true}).click();await page.getByLabel('id',{exact:true}).fill('sample_view');const raw=page.locator('#tool-query');await page.getByText('Edit view query',{exact:true}).click();await page.waitForFunction(()=>document.querySelector('#tool-query')?.value===JSON.stringify({from:'journal'}));await raw.fill('{');await page.getByText('Enter a valid query object to edit its expressions.').waitFor();await page.getByRole('button',{name:'Run tool'}).click();await page.getByText('Enter valid JSON for non-text values.').waitFor();await raw.fill(JSON.stringify({from:'journal',where:'true',derive:{keep:"'ok'"},sort:['date'],limit:3,hide_zero:true}));const where=page.getByRole('textbox',{name:'Query where'});await where.fill("row.payee != ''");await page.waitForResponse(response=>response.url().endsWith('/api/expr/check') && response.request().postDataJSON().from==='journal' && response.request().postDataJSON().expr==="row.payee != ''");assert.equal(await page.getByRole('textbox',{name:'Derived fields keep'}).count(),1);await page.screenshot({path:`/tmp/moneybag-view-editor-${width}.png`});await page.getByRole('button',{name:'Run tool'}).click();await page.getByText(/"ok": true/).waitFor();assert.equal(calls.at(-1).name,'view_upsert');assert.deepEqual(calls.at(-1).arguments,{id:'sample_view',query:{from:'journal',where:"row.payee != ''",derive:{keep:"'ok'"},sort:['date'],limit:3,hide_zero:true}});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 assert.deepEqual(errors,[]);console.log(`console formula ${width}px passed`);await context.close()
}
await browser.close()
