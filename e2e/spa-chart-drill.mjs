import { chromium } from 'playwright'
import assert from 'node:assert/strict'

const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
const context=await browser.newContext()
let mark='bar', kind='month', field='month', points=[{x:'2024-02',y:10,series:''},{x:'2024-03',y:20,series:''}]
await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
await context.route('**/api/mcp',route=>{
 const req=route.request().postDataJSON()
 let result
 if(req.method==='server/discover')return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result:{supportedVersions:['2026-07-28']}}})
 if(req.params.name==='dashboard_list')result={items:[{id:'test',title:'Test',draft_version:0,published_version:1,revision:1}]}
 else if(req.params.name==='dashboard_get')result={author:{id:'synthetic',kind:'user'},note:'',status:'published',version:1,spec:{id:'test',title:'Test',version:1,pages:[{id:'main',title:'Main',layout:[{cols:[{component:'chart',span:12}]}]}],components:[{id:'chart',type:'chart',title:'Chart'}]}}
 else if(req.params.name==='dashboard_preview')result={validation:{ok:true,errors:[],warnings:[],cost:1},values:{chart:{type:'chart',title:'Chart',mark,x:{kind,field},series:[{name:'',color_index:0}],points,y_format:'int'}}}
 else if(req.params.name==='entries_list')result={items:[]}
 else if(['accounts_list','db_list'].includes(req.params.name))result={items:[]}
 else throw Error(req.params.name)
 return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result:{structuredContent:result}}})
})
const page=await context.newPage()
const open=async()=>{await page.goto('http://127.0.0.1:5173/dashboards/test');await page.locator('.recharts-surface').waitFor()}
const check=async expected=>{await page.waitForURL(/\/journal\?/);const url=new URL(page.url());for(const [key,value] of Object.entries(expected))assert.equal(url.searchParams.get(key),value)}
await open();await page.locator('.recharts-bar-rectangle').first().click();await check({from:'2024-02-01',to:'2024-02-29'})
mark='donut';kind='category';field='account';points=[{x:'expenses:food',y:10,series:''},{x:'expenses:travel',y:20,series:''}]
await open();await page.locator('.recharts-sector').first().waitFor();const box=await page.locator('.recharts-surface').boundingBox();await page.mouse.click(box.x+box.width/2+40,box.y+box.height/2-70);await check({account:'expenses:food'})
mark='line';kind='date';field='date';points=[{x:'2024-02-29',y:10,series:''},{x:'2024-03-01',y:20,series:''}]
await open();await page.locator('.recharts-line-curve').waitFor();const linePoint=await page.locator('.recharts-line-curve').evaluate(el=>{const p=el.getPointAtLength(0),m=el.getScreenCTM();return {x:p.x*m.a+p.y*m.c+m.e,y:p.x*m.b+p.y*m.d+m.f}});await page.mouse.move(linePoint.x,linePoint.y);await page.mouse.click(linePoint.x,linePoint.y);await check({from:'2024-02-29',to:'2024-02-29'})
mark='area';await open();await page.locator('.recharts-area-area').waitFor();const areaPoint=await page.locator('.recharts-area-area').evaluate(el=>{const p=el.getPointAtLength(0),m=el.getScreenCTM();return {x:p.x*m.a+p.y*m.c+m.e,y:p.x*m.b+p.y*m.d+m.f}});await page.mouse.move(areaPoint.x,areaPoint.y);await page.mouse.click(areaPoint.x,areaPoint.y);await check({from:'2024-02-29',to:'2024-02-29'})
await open();await page.getByRole('button',{name:'Table',exact:true}).click();await page.getByRole('link',{name:'2024-02-29'}).click();await check({from:'2024-02-29',to:'2024-02-29'})
field='label';kind='category';points=[{x:'Food',y:10,series:''}];await open();await page.getByRole('button',{name:'Table',exact:true}).click();assert.equal(await page.getByRole('link',{name:'Food'}).count(),0)
await browser.close();console.log('chart drilldowns passed')
