// What the shipped app must do against a seeded server. Action timeouts stay short so a hang fails fast.
import { chromium } from 'playwright'

const base=process.argv[2] ?? process.env.MONEYBAG_E2E_URL ?? 'http://127.0.0.1:16602',USER='demo@example.test',PASS='synthetic-preview-password'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
const context=await browser.newContext({viewport:{width:1440,height:900},colorScheme:'light'}),page=await context.newPage();page.setDefaultTimeout(3000)
let failed=0,passed=0
const check=(name,ok,detail='') => {if(ok)passed++;else failed++;console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${detail ? ` (${detail})` : ''}`)}
// SECTION=a|b|c runs part of the script, because each browser run must fit its time limit.
const SECTIONS={a:['home','data layer','nav','fixed','no chart library leftovers','details'],b:['filter and view','command bar','usage','hover prefetch'],c:['entries','changes','sign out'],d:['editing gone']},wanted=process.env.SECTION ? SECTIONS[process.env.SECTION] : null
const attempt=async (name,work) => {if(wanted && !wanted.includes(name) && !(process.env.SECTION==='d' && name.startsWith('redirect')))return;try {await work()} catch(error){failed++;console.log(`FAIL ${name}: ${String(error.message).split('\n')[0].slice(0,160)}`)}}
page.on('response',async r => {if(r.url().endsWith('/api/feedback'))console.log('  feedback answered',r.status(),(await r.text().catch(() => '')).slice(0,160))})
const errors=[],requests=[];page.on('pageerror',e => errors.push(e.message));page.on('request',r => {if(r.url().startsWith(base))requests.push({url:r.url().slice(base.length),method:r.method(),body:r.postData() ?? ''})})
await page.goto(`${base}/login`);await page.locator('[name=email]').fill(USER);await page.locator('[name=password]').fill(PASS);await page.locator('button[type=submit]').click();await page.waitForURL(u => !u.pathname.startsWith('/login'))
const firstView=Date.now();await page.locator('main h1').first().waitFor({timeout:12000});console.log(`  first view with no cache: ${Date.now()-firstView} ms`)

await attempt('home',async () => {check('/ opens the home dashboard',new URL(page.url()).pathname==='/dashboards/now',page.url())})
await attempt('data layer',async () => {
 const mcp=requests.filter(r => r.url==='/api/mcp');check('no server/discover',!mcp.some(r => r.body.includes('server/discover')))
 check('bootstrap replaces the lists',requests.some(r => r.url==='/api/bootstrap') && !mcp.some(r => /dashboard_list|db_list/.test(r.body)))
 check('view replaces dashboard_get and preview',requests.some(r => r.url.startsWith('/api/dashboards/now/view')) && !mcp.some(r => /dashboard_get|dashboard_preview/.test(r.body)))
 check('cube is requested',requests.some(r => r.url==='/api/cube'))
})
await attempt('nav',async () => {const links=await page.getByRole('navigation',{name:'Workspace'}).getByRole('link').allInnerTexts();const names=links.map(l => l.split('\n')[0].replace(/,.*/,'').trim());check('navigation follows bootstrap',names.join()==='Now,Spending,Income,Budget,Future,Ledger,Data,Changes',names.join())})
for(const [from,to] of [['/activity?who=Demo','/changes?who=Demo'],['/journal?text=Lidl','/ledger?q=Lidl'],['/journal?from=2026-01-01&to=2026-02-01','/ledger?t=2026-01-01%20to%202026-02-01'],['/accounts','/ledger/accounts'],['/budgets?month=2026-09','/budget?month=2026-09'],['/scenarios','/future'],['/databases','/data'],['/imports','/data/imports'],['/reconcile','/data/reconcile']]){
 await attempt(`redirect ${from}`,async () => {await page.goto(base+from);await page.locator('main h1').first().waitFor({timeout:8000});const u=new URL(page.url());check(`${from} redirects with its filters`,decodeURIComponent(u.pathname+u.search).replaceAll('+',' ')===decodeURIComponent(to),u.pathname+u.search)})
}
if(!process.env.SECTION || process.env.SECTION==='a' || process.env.SECTION==='b'){await page.goto(`${base}/dashboards/spending`);await page.locator('.u-over').first().waitFor({timeout:8000})}
await attempt('fixed',async () => {check('fixed range is labelled',await page.getByText('Fixed range').count()===1)})
await attempt('no chart library leftovers',async () => {check('no Recharts in the DOM',await page.locator('.recharts-surface').count()===0)})
await attempt('details',async () => {
 await page.getByRole('button',{name:'How was Spending calculated?'}).click();const d=page.getByRole('dialog');await d.waitFor()
 // The one wait on a server computation rather than a UI action; the drawer says it's working meanwhile.
 const t0=Date.now();await d.getByRole('link',{name:/See the entries/}).waitFor({timeout:10000});console.log(`  metric_query answered in ${Date.now()-t0} ms`)
 check('Details calls metric_query for the exact figure',requests.some(r => r.url==='/api/mcp' && r.body.includes('metric_query')))
 check('Details shows the formula and entries link',/expenses|sum|metric/i.test(await d.innerText()))
 await d.getByRole('button',{name:'Flag this'}).click();await d.getByLabel('What looks wrong?',{exact:false}).fill('Looks high');await d.getByRole('button',{name:'Send to an agent'}).click();await d.getByRole('status').waitFor({timeout:8000})
 check('Flag this posts feedback',requests.some(r => r.url==='/api/feedback' && r.body.includes('Looks high')))
 await page.keyboard.press('Escape');await d.waitFor({state:'detached'})
})
await attempt('filter and view',async () => {
 const combo=page.getByRole('combobox',{name:'Add a filter'});await combo.focus();await page.keyboard.type('gro');await page.keyboard.press('Enter');await page.locator('.tm-pill').waitFor()
 check('filter pill reaches the URL',new URL(page.url()).searchParams.get('f')?.startsWith('category:is:') ?? false,page.url().slice(-40))
 await page.getByRole('button',{name:'Save view'}).click();await page.getByLabel('Name').fill('Groceries view');await page.getByRole('dialog').getByRole('button',{name:'Save view'}).click();await page.getByRole('dialog').waitFor({state:'detached',timeout:8000})
 check('saved view is stored',requests.some(r => r.url==='/api/saved-views' && r.method==='PUT'))
})
await attempt('command bar',async () => {
 await page.keyboard.press('Control+k');await page.getByRole('option',{name:/Groceries view/}).waitFor();check('command bar lists saved views',true)
 await page.keyboard.type('Now');await page.getByRole('option',{name:/^Now/}).first().waitFor();await page.keyboard.press('Escape');await page.getByRole('dialog').waitFor({state:'detached'})
 await page.keyboard.press('Control+k');await page.keyboard.type('why did rent go up');await page.getByRole('option',{name:/Ask:/}).click()
 const dialog=page.getByRole('dialog',{name:'Ask an agent'});await dialog.waitFor();const prompt=await dialog.getByLabel('What gets copied').inputValue();check('question becomes a prompt with page context',prompt.includes('why did rent go up') && prompt.includes('Time range:') && prompt.includes('/dashboards/spending'),prompt.split('\n')[0]);await page.keyboard.press('Escape')
})
await attempt('usage',async () => {await page.goto(`${base}/dashboards/income`);await page.locator('main h1').first().waitFor({timeout:8000});await page.getByRole('navigation',{name:'Workspace'}).getByRole('link',{name:'Budget'}).click();await page.waitForURL(/budget/);await page.waitForTimeout(300);check('usage beacon on navigation',requests.filter(r => r.url==='/api/usage').length>=2)})
await attempt('hover prefetch',async () => {await page.goto(`${base}/data`);await page.locator('main h1').first().waitFor({timeout:8000});requests.length=0;await page.getByRole('navigation',{name:'Workspace'}).getByRole('link',{name:'Income'}).hover();await page.waitForTimeout(500);check('hovering a link prefetches its view',requests.some(r => r.url.startsWith('/api/dashboards/income/view')))})
await attempt('entries',async () => {
 await page.goto(`${base}/ledger`);const grid=page.getByRole('table',{name:'Ledger entries'});await grid.waitFor();await grid.locator('[role=row][data-row]').first().waitFor()
 const dates=await grid.locator('[data-row] [role=cell]:first-child').allInnerTexts(),ms=dates.map(d => Date.parse(d));check('entries are date sorted',ms.every((x,i) => !i || ms[i-1]>=x),dates[0])
 const box=await grid.locator('[data-row]').first().locator('[role=cell].num').boundingBox();check('amount is visible at 1440',box.x+box.width<=1440,`right edge ${Math.round(box.x+box.width)}`)
 check('payees use aliases',!(await grid.innerText()).includes('KONZUM'))
 check('provenance badges',/Added by|From statement|Estimated/.test(await grid.innerText()))
})
await attempt('changes',async () => {await page.goto(`${base}/changes`);await page.locator('.tm-feed li').first().waitFor();await page.getByRole('button',{name:'See what changed'}).first().click();const d=page.getByRole('dialog');await d.waitFor();check('hashes live in the drawer',/Hash/.test(await d.innerText()));await page.keyboard.press('Escape')})
await attempt('editing gone',async () => {for(const path of ['/dashboards/spending','/ledger','/data/imports','/budget']){await page.goto(base+path);await page.locator('main h1').first().waitFor({timeout:8000});const text=await page.locator('main').innerText();check(`no editing on ${path}`,!/\b(Edit|Publish|Post entry|New dashboard|Commit|Undo import)\b/.test(text))}})
await attempt('sign out',async () => {
 await page.goto(`${base}/dashboards/spending`);await page.locator('.u-over').first().waitFor({timeout:8000});await page.waitForTimeout(400)
 const before=await page.evaluate(() => new Promise(resolve => {const open=indexedDB.open('moneybag-telemetry');open.onsuccess=() => {const q=open.result.transaction('cubes').objectStore('cubes').count();q.onsuccess=() => {open.result.close();resolve(q.result)}}}))
 check('cube is persisted',before>=1,String(before))
 await page.goto(`${base}/settings`);await page.getByRole('button',{name:'Sign out'}).click();await page.waitForURL(/login/)
 const after=await page.evaluate(() => new Promise(resolve => {const open=indexedDB.open('moneybag-telemetry');open.onsuccess=() => {const t=open.result.transaction(['cubes','views']),a=t.objectStore('cubes').count(),b=t.objectStore('views').count();t.oncomplete=() => {open.result.close();resolve(a.result+b.result)}}}))
 check('sign out clears the cached ledger',after===0,String(after))
})
check('no page errors',errors.length===0,errors.slice(0,2).join(' | ').slice(0,240))
await browser.close()
console.log(`${passed} passed, ${failed} failed`)
process.exit(failed ? 1 : 0)
