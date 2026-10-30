// Calls the real dashboard_render tool and saves the PNG: the SPA's /render/{token} route must report for the tool to answer.
// Usage: [ROUTE=/budget THEME=dark] node render-proof.mjs <dashboard-id> <out.png> [width] [base-url]
import { writeFileSync } from 'node:fs'

const [id='spending',out='render.png',width='1200',base='http://127.0.0.1:16602']=process.argv.slice(2)
const login=await fetch(`${base}/login`,{method:'POST',redirect:'manual',headers:{'Content-Type':'application/x-www-form-urlencoded',Origin:base},body:new URLSearchParams({email:'demo@example.test',password:'synthetic-preview-password'})})
const cookie=login.headers.getSetCookie().find(c => c.startsWith('moneybag_session=')).split(';')[0],{csrf}=await (await fetch(`${base}/api/session`,{headers:{cookie}})).json()
const started=Date.now(),response=await fetch(`${base}/api/mcp`,{method:'POST',signal:AbortSignal.timeout(40000),headers:{cookie,Origin:base,'X-CSRF-Token':csrf,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2026-07-28','Mcp-Method':'tools/call','Mcp-Name':'dashboard_render'},body:JSON.stringify({jsonrpc:'2.0',id:1,method:'tools/call',params:{name:'dashboard_render',arguments:{id,width:Number(width),...(process.env.ROUTE ? {route:process.env.ROUTE} : {}),...(process.env.THEME ? {theme:process.env.THEME} : {})},_meta:{'io.modelcontextprotocol/protocolVersion':'2026-07-28','io.modelcontextprotocol/clientInfo':{name:'proof',version:'1'},'io.modelcontextprotocol/clientCapabilities':{}}}})})
const envelope=await response.json(),result=envelope.result
if(!result || result.isError){console.log('render failed:',JSON.stringify(envelope.error ?? result?.content).slice(0,600));process.exit(1)}
const image=result.content?.find(c => c.type==='image')
if(image)writeFileSync(out,Buffer.from(image.data,'base64'))
const s=result.structuredContent ?? {}
console.log(JSON.stringify({ms:Date.now()-started,image:!!image,checks:s.checks,panels:s.layout?.length,timing_ms:s.timing_ms,render_ms:s.render_ms,message:s.message}))
