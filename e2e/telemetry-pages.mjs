// Visits every page at phone and desktop width in both themes against a seeded server. Captures it, and fails on overflow, machine text or page errors.
// Usage: node telemetry-pages.mjs <out-dir> [base-url]. Seed with seed-telemetry.mjs first.
import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const out=process.argv[2] ?? 'telemetry-shots',base=process.argv[3] ?? process.env.MONEYBAG_E2E_URL ?? 'http://127.0.0.1:16602',only=process.env.PAGES?.split(','),widths=(process.env.WIDTHS ?? '1440,390').split(',').map(Number),themes=(process.env.THEMES ?? 'light,dark').split(',')
mkdirSync(out,{recursive:true})
const PAGES=[['now','/'],['spending','/dashboards/spending'],['income','/dashboards/income'],['cash','/dashboards/cash'],['trips','/dashboards/trips'],['budget','/budget'],['future','/future'],['ledger','/ledger'],['accounts','/ledger/accounts'],['data','/data'],['rates','/data/rates'],['imports','/data/imports'],['reconcile','/data/reconcile'],['changes','/changes'],['settings','/settings'],['shared','/shared']]
// Machine text must never reach a person: ULIDs, account ids, snake_case keys, unresolved templates, raw timestamps.
const MACHINE=[/\b[0-9A-HJKMNP-TV-Z]{26}\b/,/\b(assets|expenses|income|liabilities|equity):[a-z]/,/\b[a-z]+_[a-z_]+\b/,/\{\{|\}\}/,/\d{4}-\d{2}-\d{2}T\d{2}/]
let failed=0
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
for(const width of widths)for(const theme of themes){
 const context=await browser.newContext({viewport:{width,height:900},deviceScaleFactor:1,colorScheme:theme,hasTouch:width<500})
 const page=await context.newPage(),errors=[]
 page.setDefaultTimeout(3000)
 page.on('pageerror',e => errors.push(e.message));page.on('console',m => {if(m.type()==='error' && !/404|Failed to load resource/.test(m.text()))errors.push(m.text())})
 await page.goto(`${base}/login`);await page.locator('[name=email]').fill('demo@example.test');await page.locator('[name=password]').fill('synthetic-preview-password');await page.locator('button[type=submit]').click();await page.waitForURL(u => !u.pathname.startsWith('/login'))
 for(const [name,path] of PAGES){
  if(only && !only.includes(name))continue
  errors.length=0
  await page.goto(base+path);await page.locator('main h1').first().waitFor().catch(() => {});await page.waitForLoadState('networkidle',{timeout:2000}).catch(() => {});await page.waitForTimeout(250)
  const facts=await page.evaluate(() => {
   const w=document.documentElement.clientWidth,bad=[];for(const el of document.querySelectorAll('main *')){const r=el.getBoundingClientRect();if(r.width && r.right>w+1 && !el.closest('.sr-only,.tm-grid-head,.page-tabs') && getComputedStyle(el).position!=='fixed')bad.push(`${el.tagName}.${String(el.className.baseVal ?? el.className).slice(0,40)}`)}
   return {scroll:document.documentElement.scrollWidth,client:w,bad:bad.slice(0,5),text:document.querySelector('main')?.innerText ?? '',spinners:document.querySelectorAll('.tm-plot-skeleton').length}
  })
  const machine=MACHINE.map(re => facts.text.match(re)?.[0]).filter(Boolean),problems=[]
  if(facts.scroll>facts.client)problems.push(`overflow ${facts.scroll}/${facts.client} ${facts.bad.join(' ')}`)
  if(machine.length)problems.push(`machine text: ${machine.join(' | ')}`)
  if(errors.length)problems.push(`errors: ${errors.slice(0,2).join(' | ').slice(0,300)}`)
  if(problems.length)failed++
  console.log(`${problems.length ? 'FAIL' : 'ok  '} ${name} ${width} ${theme}${problems.length ? `: ${problems.join('; ')}` : ''}`)
  await page.screenshot({path:`${out}/${name}-${width}-${theme}.png`,fullPage:true})
 }
 await context.close()
}
await browser.close()
console.log(failed ? `${failed} pages with problems` : 'all pages clean')
process.exit(failed ? 1 : 0)
