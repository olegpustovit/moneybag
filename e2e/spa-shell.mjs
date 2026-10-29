import { chromium } from 'playwright'
import { existsSync } from 'node:fs'
import assert from 'node:assert/strict'
const browser = await chromium.launch({executablePath: process.env.CHROMIUM_BIN || (existsSync(process.env.HOME + '/.nix-profile/bin/chromium') ? process.env.HOME + '/.nix-profile/bin/chromium' : undefined),args:['--no-sandbox']})
for (const theme of ['light','dark']) for (const [size,width] of [['desktop',1440],['tablet',900],['mobile',390]]) {
 const context = await browser.newContext({viewport:{width,height:900},colorScheme:theme})
 await context.route('**/api/session', route => route.fulfill({json:{user:{id:'synthetic',name:'Alex',email:'alex@example.test',is_admin:true},csrf:'test',version:'test',mcp_url:'/api/mcp'}}))
 await context.route('**/api/mcp', async route => {
  const req=route.request().postDataJSON(); let data
  if(req.method==='server/discover') data={supportedVersions:['2026-07-28']}
  else if(req.params.name==='dashboard_list') data={structuredContent:{items:[{id:'overview',title:'Overview',draft_version:2,published_version:1,revision:2},{id:'spending',title:'Spending',draft_version:0,published_version:1,revision:1}]}}
  else if(req.params.name==='db_list') data={structuredContent:{items:[{id:'assumptions',name:'Assumptions',columns:[]}]}}
  else throw Error(req.params.name)
  await route.fulfill({json:{jsonrpc:'2.0',id:req.id,result:data}})
 })
 const page=await context.newPage(); const errors=[]; page.on('pageerror',e=>errors.push(e.message))
 await page.goto('http://127.0.0.1:5173/'); await page.getByRole('heading',{name:'Dashboards'}).waitFor(); await page.evaluate(()=>document.fonts.ready)
 const layout=await page.evaluate(()=>({font:getComputedStyle(document.body).fontFamily,loaded:document.fonts.check('14px Geist'),dark:document.documentElement.classList.contains('dark'),width:document.documentElement.scrollWidth, viewport:innerWidth,sidebar:Math.round(document.querySelector('.desktop-sidebar').getBoundingClientRect().width)}))
 assert(layout.loaded && layout.font.includes('Geist')); assert.equal(layout.dark,theme==='dark'); assert(layout.width<=layout.viewport)
 if(size==='desktop') assert.equal(layout.sidebar,248); if(size==='tablet') assert.equal(layout.sidebar,56)
 if(size==='mobile'){await page.getByRole('button',{name:'Open navigation'}).click(); await page.getByRole('dialog',{name:'Workspace navigation'}).waitFor(); assert(await page.getByRole('link',{name:'Overview'}).isVisible()); await page.keyboard.press('Escape')}
 assert.deepEqual(errors,[]); await page.screenshot({path:`/tmp/moneybag-${size}-${theme}.png`}); console.log(JSON.stringify({theme,size,...layout})); await context.close()
}
const context=await browser.newContext(); await context.route('**/api/session',route=>route.fulfill({status:401,json:{error:'sign in'}})); const page=await context.newPage(); await page.goto('http://127.0.0.1:5173/'); await page.waitForURL(/\/login\?next=/); console.log('signed-out redirect passed'); await browser.close()
