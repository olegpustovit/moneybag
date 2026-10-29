import { chromium } from 'playwright'
import assert from 'node:assert/strict'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
const context=await browser.newContext({viewport:{width:1000,height:800}})
await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
await context.route('**/api/mcp',route=>{
 const req=route.request().postDataJSON();let result
 if(req.method==='server/discover')return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result:{supportedVersions:['2026-07-28']}}})
 if(req.params.name==='dashboard_list')result={items:[{id:'test',title:'Test',draft_version:0,published_version:1,revision:1}]}
 else if(req.params.name==='db_list')result={items:[]}
 else if(req.params.name==='dashboard_get')result={author:{id:'synthetic',kind:'user'},note:'',status:'published',version:1,spec:{id:'test',title:'Test',version:1,pages:[{id:'main',title:'Main',layout:[{cols:[{component:'t',span:12}]}]}],components:[{id:'t',type:'table',title:'Test table'}]}}
 else if(req.params.name==='dashboard_preview')result={validation:{ok:true,errors:[],warnings:[],cost:1},values:{t:{type:'table',title:'Test table',columns:[{key:'name',label:'Name',format:'text',align:'left'},{key:'number',label:'Number',format:'int',align:'right'}],rows:Array.from({length:250},(_,i)=>({name:{v:`Item ${String(i).padStart(3,'0')}`,t:`Item ${String(i).padStart(3,'0')}`},number:{v:249-i,t:String(249-i)}}))}}}
 else throw Error(req.params.name)
 return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result:{structuredContent:result}}})
})
const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message))
await page.goto('http://127.0.0.1:5173/dashboards/test');const region=page.getByRole('region',{name:'Test table table'});await region.waitFor();assert((await region.locator('tbody tr').count())<50)
await page.getByRole('button',{name:'Number'}).click();await page.getByText('Item 249',{exact:true}).waitFor();assert.equal(await page.locator('th').filter({hasText:'Number'}).getAttribute('aria-sort'),'ascending')
await page.getByRole('button',{name:'Name'}).click({modifiers:['Shift']});assert.equal(await page.locator('th').filter({hasText:'Name'}).getAttribute('aria-sort'),'ascending')
const resize=page.getByRole('separator',{name:'Resize Number column'});const oldSize=Number(await resize.getAttribute('aria-valuenow'));const oldWidth=(await page.locator('th').filter({hasText:'Number'}).boundingBox()).width;await resize.focus();await page.keyboard.press('ArrowRight');assert.equal(Number(await resize.getAttribute('aria-valuenow')),oldSize+16);assert((await page.locator('th').filter({hasText:'Number'}).boundingBox()).width>oldWidth)
await page.getByText('Columns',{exact:true}).click();await page.getByRole('checkbox',{name:'Number'}).uncheck();assert.equal(await page.getByRole('button',{name:'Number'}).count(),0)
await page.reload();await region.waitFor();assert.equal(await page.getByRole('button',{name:'Number'}).count(),0);await page.getByText('Columns',{exact:true}).click();await page.getByRole('checkbox',{name:'Number'}).check();assert.equal(Number(await page.getByRole('separator',{name:'Resize Number column'}).getAttribute('aria-valuenow')),oldSize+16)
await region.evaluate(el=>el.scrollTop=el.scrollHeight);await page.getByText('Item 249',{exact:true}).waitFor();assert((await region.locator('tbody tr').count())<50)
assert.deepEqual(errors,[]);await browser.close();console.log('dashboard data table passed')
