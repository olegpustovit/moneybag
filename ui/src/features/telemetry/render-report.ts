import type { ViewBody } from './view-data'

export type RenderReport={layout:{component:string;x:number;y:number;w:number;h:number}[];checks:{overflow:string[];label_collisions:string[];empty_panels:string[];time_scope_applies:boolean|null};timing_ms:number}
export const emptyReport=(empty:string[]=[]):RenderReport => ({layout:[],checks:{overflow:[],label_collisions:[],empty_panels:empty,time_scope_applies:null},timing_ms:0})
const overlap=(a:DOMRect,b:DOMRect) => a.left<b.right-1 && b.left<a.right-1 && a.top<b.bottom-1 && b.top<a.bottom-1
// Text inside a panel that a person can't read because another label sits on it. Canvas axes space their own ticks, so only DOM and SVG text is checked.
function collides(panel:Element):boolean {
 const labels=[...panel.querySelectorAll('svg text, .tm-legend button, .tm-legend-key')].map(el => el.getBoundingClientRect()).filter(r => r.width && r.height)
 for(let i=0;i<labels.length;i++)for(let k=i+1;k<labels.length;k++)if(overlap(labels[i],labels[k]))return true
 return false
}
const isEmpty=(panel:Element) => !!panel.querySelector('[data-empty],.tm-empty') || (!panel.querySelector('canvas,svg,table,[role=table],.tm-stat,.note,.budget-row,ol') && !panel.textContent?.trim())
// Panels whose number follows the page clock. A page with a clock and none of these is a broken promise the gate should catch.
function followsClock(view:ViewBody):boolean {
 for(const c of view.spec.components ?? []){
  if(c.fixed)continue
  if(c.metric)return true
  const text=JSON.stringify(c)
  if(/params\.(from|to|compare_from|compare_to)/.test(text))return true
 }
 const data=JSON.stringify((view.spec as {data?:unknown}).data ?? {})+JSON.stringify((view.spec as {computed?:unknown}).computed ?? {})
 return /params\.(from|to)/.test(data)
}
export function collectReport(view:ViewBody|null):RenderReport {
 const report=emptyReport()
 if(!view){report.checks.empty_panels.push('page');return report}
 try {
  for(const panel of document.querySelectorAll<HTMLElement>('[data-panel]')){
   const id=panel.dataset.panel!,r=panel.getBoundingClientRect()
   report.layout.push({component:id,x:Math.round(r.left+scrollX),y:Math.round(r.top+scrollY),w:Math.round(r.width),h:Math.round(r.height)})
   if(panel.scrollWidth>panel.clientWidth+1)report.checks.overflow.push(id)
   if(collides(panel))report.checks.label_collisions.push(id)
   if(isEmpty(panel))report.checks.empty_panels.push(id)
  }
  report.checks.time_scope_applies=view.spec.scope ? followsClock(view) : null
 } catch {/* A partial report still beats none: publish is waiting on it. */}
 return report
}
export async function sendReport(token:string,report:RenderReport):Promise<void> {
 try {await fetch(`/api/render/${encodeURIComponent(token)}/report`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(report)})} catch {/* The server reports a page that never answered as an error. */}
}
