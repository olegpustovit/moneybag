import {chromium} from 'playwright'
import {existsSync} from 'node:fs'
import assert from 'node:assert/strict'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
const context=await browser.newContext({viewport:{width:1200,height:900}})
await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
await context.route('**/api/mcp',route=>{const req=route.request().postDataJSON(),name=req.params.name,args=req.params.arguments ?? {};let result
 if(req.method==='server/discover')result={supportedVersions:['2026-07-28']}
 else if(name==='dashboard_list')result={structuredContent:{items:['text','chart'].map(id=>({id,title:`${id} dashboard`,draft_version:0,published_version:1,revision:1}))}}
 else if(name==='db_list'||name==='accounts_list'||name==='entries_list')result={structuredContent:{items:[]}}
 else if(name==='dashboard_get'){const chart=args.id==='chart';result={structuredContent:{author:{id:'synthetic',kind:'user'},status:'published',version:1,spec:{id:args.id,title:`${args.id} dashboard`,version:1,data:{},pages:[{id:'main',layout:[{cols:[{component:'value',span:12}]}]}],components:[{id:'value',type:chart ? 'chart' : 'text',title:'Content'}]}}}}
 else if(name==='dashboard_preview'){const chart=args.dashboard_id==='chart';result={structuredContent:{validation:{ok:true,errors:[],warnings:[],cost:0},values:{value:chart ? {type:'chart',title:'Chart',mark:'line',x:{field:'date',kind:'date'},series:[{name:'Series',color_index:0}],points:[{x:'2026-01-01',y:1,series:'Series'},{x:'2026-01-02',y:2,series:'Series'}],y_format:'number'} : {type:'text',title:'Note',html:'Plain note'}}}}}
 else throw Error(name)
 return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
})
const page=await context.newPage(),requests=[],errors=[];page.on('request',request=>requests.push(new URL(request.url()).pathname));page.on('pageerror',error=>errors.push(error.message))
await page.goto('http://127.0.0.1:5173/journal');await page.getByText('No entries match these filters.').waitFor()
const chartRequests=()=>requests.filter(path=>/^\/assets\/chart-[^/]+\.js$/.test(path))
assert.deepEqual(chartRequests(),[],'journal loaded chart code')
await page.getByRole('link',{name:'text dashboard',exact:true}).first().click();await page.getByText('Plain note').waitFor();assert.deepEqual(chartRequests(),[],'text-only dashboard loaded chart code')
await page.getByRole('link',{name:'chart dashboard',exact:true}).first().click();await page.locator('.dashboard-chart').waitFor();await page.waitForFunction(()=>document.querySelector('.recharts-surface'));assert(chartRequests().length>0,'chart code did not load for the chart');assert.deepEqual(errors,[])
await browser.close();console.log('chart chunks load only for chart views')
