// Private JSON on stdin; no cookies in process arguments. Only read requests reach the server.
import { chromium } from 'playwright'
const chunks=[]
for await (const chunk of process.stdin)chunks.push(chunk)
const {base,cookie,id,readonly}=JSON.parse(Buffer.concat(chunks).toString())
const browser=await chromium.launch({headless:true,...(process.env.MONEYBAG_CHROMIUM_BIN ? {executablePath:process.env.MONEYBAG_CHROMIUM_BIN} : {})})
try {
 const context=await browser.newContext({viewport:{width:1440,height:1000},locale:'en-US',timezoneId:'Europe/Zagreb',serviceWorkers:'block'})
 await context.addCookies([{name:'moneybag_session',value:cookie,url:base}])
 await context.route('**/*',async route => {
  const request=route.request(),url=new URL(request.url())
  if(url.origin!==new URL(base).origin)return route.abort()
  if(['GET','HEAD'].includes(request.method()))return route.continue()
  if(url.pathname==='/api/mcp' && request.method()==='POST') {
   const body=request.postDataJSON()
   if(body.method==='tools/call' && readonly.includes(body.params?.name))return route.continue()
  }
  return route.abort()
 })
 const page=await context.newPage(),path=`/api/shared/${encodeURIComponent(id)}`
 const loaded=page.waitForResponse(response => new URL(response.url()).pathname===path)
 await page.goto(`${base}/shared/${encodeURIComponent(id)}`,{waitUntil:'networkidle'})
 await loaded
 await page.evaluate(() => document.fonts.ready)
 await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
 process.stdout.write(JSON.stringify({html:await page.content()}))
} finally { await browser.close() }
