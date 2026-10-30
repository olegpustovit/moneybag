// Build with `cd ui && bunx vite build --mode profile --outDir /tmp/moneybag-navigation-profile --emptyOutDir`, then serve that directory on port 5174.
import {chromium} from 'playwright'
import {existsSync} from 'node:fs'
import assert from 'node:assert/strict'

const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || (existsSync(process.env.HOME+'/.nix-profile/bin/chromium') ? process.env.HOME+'/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
for(const width of [1440,390]) {
 const context=await browser.newContext({viewport:{width,height:900}}),calls=[]
 await context.route('**/api/session',route=>route.fulfill({json:{user:{id:'synthetic',name:'Test',email:'test@example.test'},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/imports/count',route=>route.fulfill({json:{count:0}}));await context.route('**/api/reconcile/count',route=>route.fulfill({json:{count:0}}))
 await context.route('**/api/mcp',route=>{const req=route.request().postDataJSON(),name=req.params.name,args=req.params.arguments ?? {};calls.push(name);let result
  if(req.method==='server/discover')result={supportedVersions:['2026-07-28']}
  else if(name==='dashboard_list')result={structuredContent:{items:['alpha','beta'].map(id=>({id,title:id,published_version:1,revision:1}))}}
  else if(name==='db_list')result={structuredContent:{items:[]}}
  else if(name==='dashboard_get')result={structuredContent:{author:{id:'synthetic',kind:'human'},status:'published',version:1,spec:{id:args.id,title:args.id,version:1,pages:[{id:'main',layout:[{cols:[{span:12,component:'k'}]}]}],components:[{id:'k',type:'kpi',title:'Value'}]}}}
  else if(name==='dashboard_preview')result={structuredContent:{validation:{ok:true,errors:[],warnings:[],cost:0},values:{k:{type:'kpi',title:'Value',value:null,text:`${args.dashboard_id} value`,tone:'',sub:''}}}}
  else throw Error(name)
  return route.fulfill({json:{jsonrpc:'2.0',id:req.id,result}})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>{window.profileCommits=[];window.addEventListener('moneybag:react-commit',event=>window.profileCommits.push(event.detail))})
 await page.goto('http://127.0.0.1:5174/');await page.getByRole('heading',{name:'Dashboards'}).waitFor();await page.locator('main').getByRole('link',{name:/alpha/}).click();await page.getByText('alpha value',{exact:true}).waitFor()
 async function openNav(){if(width<700)await page.getByRole('button',{name:'Open navigation'}).click()}
 await openNav();await page.getByRole('link',{name:'beta',exact:true}).filter({visible:true}).click();await page.getByText('beta value',{exact:true}).waitFor()
 const cachedCalls=calls.filter(name=>name==='dashboard_get'||name==='dashboard_preview').length,results=[]
 for(let i=0;i<20;i++) {
  const target=i%2===0?'alpha':'beta';await openNav()
  const result=await page.evaluate(async target=>{const root=document.querySelector('main'),link=[...document.querySelectorAll('a')].find(el=>el.textContent.trim()===target && el.getClientRects().length);if(!root || !link)throw Error('missing navigation link');const start=performance.now(),commits=window.profileCommits.length;const updated=new Promise((resolve,reject)=>{const observer=new MutationObserver(()=>{if(root.textContent.includes(`${target} value`)){clearTimeout(timer);observer.disconnect();resolve()}});observer.observe(root,{childList:true,subtree:true,characterData:true});const timer=setTimeout(()=>{observer.disconnect();reject(Error('navigation timeout'))},3000)});link.click();await updated;const content=performance.now()-start;await new Promise(requestAnimationFrame);const frame=performance.now()-start;await new Promise(requestAnimationFrame);return {content,frame,paint:performance.now()-start,commits:window.profileCommits.slice(commits)}},target)
  assert(result.commits.length>0,'React Profiler must record the navigation');assert(result.commits.some(commit=>commit.actualDuration>=0));results.push(result)
 }
 assert.equal(calls.filter(name=>name==='dashboard_get'||name==='dashboard_preview').length,cachedCalls,'cached transitions should not request dashboard data');assert.deepEqual(errors,[])
 const paint=results.map(item=>item.paint).sort((a,b)=>a-b),frame=results.map(item=>item.frame).sort((a,b)=>a-b),content=results.map(item=>item.content).sort((a,b)=>a-b),render=results.flatMap(item=>item.commits.map(commit=>commit.actualDuration));console.log(`navigation profile ${width}px: content p95 ${content[18].toFixed(1)}ms, first-frame p95 ${frame[18].toFixed(1)}ms, two-frame proxy p95 ${paint[18].toFixed(1)}ms, React max commit ${Math.max(...render).toFixed(1)}ms; ${paint.filter(value=>value>50).length}/20 above 50ms`)
 await context.close()
}
await browser.close()
