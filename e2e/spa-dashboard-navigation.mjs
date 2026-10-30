import { chromium } from 'playwright'
import { existsSync } from 'node:fs'
import assert from 'node:assert/strict'

const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
for (const width of [1440,390]) {
 const context=await browser.newContext({viewport:{width,height:900}})
 const gate=() => {let open,seen;return {started:new Promise(resolve=>{seen=resolve}),wait:new Promise(resolve=>{open=resolve}),signal:() => seen(),release:() => open()}}
 const range=gate(),draft=gate(),beta=gate();let failBeta=false
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/mcp',async route=>{const req=route.request().postDataJSON(),name=req.params.name,args=req.params.arguments ?? {};let result
  if(req.method==='server/discover')result={supportedVersions:['2026-07-28']}
  else if(name==='dashboard_list')result={structuredContent:{items:[{id:'alpha',title:'Alpha',published_version:1,draft_version:2,revision:1},{id:'beta',title:'Beta',published_version:1,draft_version:0,revision:1}]}}
  else if(name==='db_list')result={structuredContent:{items:[]}}
  else if(name==='dashboard_get'){const id=args.id,version=args.version;result={structuredContent:{author:{id:'synthetic',kind:'user'},status:version,version:version==='draft' ? 2 : 1,spec:{id,title:`${id} ${version}`,version:version==='draft' ? 2 : 1,params:{},data:{rows:{from:'journal',where:"params.from == ''"}},pages:[{id:'main',title:'Main',layout:[{cols:[{span:12,component:'k'}]}]}],components:[{id:'k',type:'kpi',title:'Value'}]}}}}
  else if(name==='dashboard_preview') {const id=args.dashboard_id,version=args.version,from=args.params?.from
   if(id==='alpha' && version==='published' && from){range.signal();await range.wait}
   if(id==='alpha' && version==='draft'){draft.signal();await draft.wait}
   if(id==='beta' && !from){beta.signal();await beta.wait}
   if(id==='beta' && from && failBeta)return route.fulfill({json:{jsonrpc:'2.0',id:req.id,error:{code:-32000,message:'Synthetic dashboard failure'}}})
   const text=id==='alpha' ? version==='draft' ? 'Alpha draft value' : from ? 'Alpha filtered value' : 'Alpha original value' : from ? 'Beta filtered value' : 'Beta value'
   result={structuredContent:{validation:{ok:true,errors:[],warnings:[],cost:1},values:{k:{type:'kpi',title:'Value',value:null,text,tone:'',sub:''}}}}
  } else throw Error(name)
  return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.goto('http://127.0.0.1:5173/dashboards/alpha');await page.getByText('Alpha original value',{exact:true}).waitFor();await page.evaluate(()=>{window.routeSentinel='preserved'})
 async function selectRange(from,to){await page.getByRole('button',{name:'Dashboard default'}).click();const dialog=page.getByRole('dialog');await dialog.getByLabel('From',{exact:true}).fill(from);await dialog.getByLabel('To',{exact:true}).fill(to);await dialog.getByRole('button',{name:'Apply'}).click()}
 await selectRange('2026-01-01','2026-01-31');await range.started;await page.getByLabel('Refreshing dashboard').waitFor();assert(await page.getByText('Alpha original value',{exact:true}).isVisible());assert.equal(await page.locator('.dashboard-skeleton').count(),0);range.release();await page.getByText('Alpha filtered value',{exact:true}).waitFor()
 await page.getByLabel('Version').selectOption('draft');await draft.started;await page.getByRole('heading',{name:'alpha draft',exact:true}).waitFor();await page.waitForTimeout(200);assert.equal(await page.getByText('Alpha filtered value',{exact:true}).count(),0);draft.release();await page.getByText('Alpha draft value',{exact:true}).waitFor()
 if(width<700){await page.getByRole('button',{name:'Open navigation'}).click();await page.getByRole('dialog',{name:'Workspace navigation'}).getByRole('link',{name:'Beta',exact:true}).click();await page.getByRole('dialog',{name:'Workspace navigation'}).waitFor({state:'hidden'})}else await page.locator('.desktop-sidebar').getByRole('link',{name:'Beta',exact:true}).click()
 await beta.started;await page.getByRole('heading',{name:'beta published',exact:true}).waitFor();await page.waitForTimeout(200);assert.equal(await page.getByText('Alpha draft value',{exact:true}).count(),0);assert.equal(await page.evaluate(()=>window.routeSentinel),'preserved');assert.equal(new URL(page.url()).search,'');beta.release();await page.getByText('Beta value',{exact:true}).waitFor()
 failBeta=true;await selectRange('2026-02-01','2026-02-28');await page.getByText('Synthetic dashboard failure',{exact:true}).waitFor();failBeta=false;await page.getByRole('button',{name:'Try again'}).click();await page.getByText('Beta filtered value',{exact:true}).waitFor();await page.getByRole('link',{name:'DASHBOARDS',exact:true}).click();await page.getByRole('heading',{name:'Dashboards',exact:true}).waitFor();assert.equal(await page.evaluate(()=>window.routeSentinel),'preserved');await page.locator('main').getByRole('link',{name:/Alpha/}).click();await page.getByText('Alpha original value',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>window.routeSentinel),'preserved');assert.deepEqual(errors,[]);await context.close();console.log(`dashboard navigation ${width}px passed`)
}
await browser.close()
