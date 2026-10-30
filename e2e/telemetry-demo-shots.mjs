// Captures the telemetry kit demo at phone and desktop width in both themes. Needs `vite --port 5199` running in ui/.
import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const out=process.argv[2] ?? 'telemetry-shots',base=process.env.DEMO_URL ?? 'http://127.0.0.1:5199/telemetry-demo.html'
mkdirSync(out,{recursive:true})
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
for(const width of [1440,390])for(const theme of ['light','dark']){
 const context=await browser.newContext({viewport:{width,height:900},deviceScaleFactor:1,colorScheme:theme,hasTouch:width<500})
 const page=await context.newPage(),errors=[]
 page.on('pageerror',e => errors.push(e.message));page.on('console',m => {if(m.type()==='error')errors.push(m.text())})
 await page.goto(`${base}?theme=${theme}`);await page.locator('.u-over').first().waitFor();await page.waitForTimeout(400)
 const overflow=await page.evaluate(() => {const w=document.documentElement.clientWidth,bad=[];for(const el of document.querySelectorAll('body *')){const r=el.getBoundingClientRect();if(r.width && r.right>w+1 && !el.closest('.sr-only') && !el.closest('.tm-grid-head') && getComputedStyle(el).position!=='fixed')bad.push(`${el.tagName}.${el.className}`.slice(0,60))}return {scroll:document.documentElement.scrollWidth,client:w,bad:bad.slice(0,6)}})
 console.log(width,theme,JSON.stringify(overflow),errors.slice(0,3).join(' | '))
 await page.screenshot({path:`${out}/kit-${width}-${theme}.png`,fullPage:true})
 for(let i=0,step=width>500 ? 1000 : 1300;i<(width>500 ? 5 : 7);i++)await page.screenshot({path:`${out}/s${i}-${width}-${theme}.png`,fullPage:true,clip:{x:0,y:i*step,width,height:step}})
 await context.close()
}
await browser.close()
