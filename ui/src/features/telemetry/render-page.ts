import { emptyReport, sendReport, type RenderReport } from './render-report'

const SETTLE_MS=4500,QUIET_MS=700

// A real app page has no dashboard spec: panels are whatever marks itself, else the whole main area. Only a crashed page counts as empty.
export function collectPageReport():RenderReport {
 const report=emptyReport()
 try {
  const panels=[...document.querySelectorAll<HTMLElement>('[data-panel]')]
  const boxes:[string,Element][]=panels.length ? panels.map(p => [p.dataset.panel!,p]) : [['page',document.querySelector('main') ?? document.body]]
  for(const [id,el] of boxes){
   const r=el.getBoundingClientRect()
   report.layout.push({component:id,x:Math.round(r.left+scrollX),y:Math.round(r.top+scrollY),w:Math.round(r.width),h:Math.round(r.height)})
   if(el.scrollWidth>el.clientWidth+1)report.checks.overflow.push(id)
  }
  if(document.querySelector('[data-route-error]'))report.checks.empty_panels.push('page')
 } catch {report.checks.empty_panels.push('page')}
 return report
}

// Waits for the network to go quiet (or the settle budget), then posts one report, even when the page errored.
export function startPageReport(token:string):() => void {
 const started=performance.now(),count=() => performance.getEntriesByType('resource').length
 let sent=false,last=count(),lastChange=performance.now(),timer=0
 const send=() => {if(sent)return;sent=true;void sendReport(token,{...collectPageReport(),timing_ms:Math.round(performance.now()-started)})}
 const tick=() => {
  const now=performance.now(),n=count()
  if(n!==last){last=n;lastChange=now}
  if(now-started>=SETTLE_MS || (now-lastChange>=QUIET_MS && now-started>=1000 && !document.querySelector('.tm-plot-skeleton')))void (document.fonts?.ready ?? Promise.resolve()).then(send)
  else timer=window.setTimeout(tick,100)
 }
 timer=window.setTimeout(tick,100)
 const onError=() => {window.setTimeout(send,100)}
 window.addEventListener('error',onError);window.addEventListener('unhandledrejection',onError)
 return () => {clearTimeout(timer);window.removeEventListener('error',onError);window.removeEventListener('unhandledrejection',onError)}
}
