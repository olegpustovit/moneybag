import { useEffect, useId, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { DAY, dayLabel, parseRange, parseTime, rangeDays, rangeLabel, type Bounds, type CompareMode, type Range } from './time-grammar'
import { ignoreShortcut, type TimeState } from './use-time-state'
import type { Band } from './chart-core'
import './telemetry.css'

export type Preset={label:string;text:string}
const presetName=(text:string) => {const m=text.match(/^(\d+)(d|mo|w|y)$/);if(m)return `${m[1]}${m[2]==='mo' ? 'M' : m[2].toUpperCase()}`;return text==='ytd' ? 'YTD' : text==='all' ? 'All' : text.replace(/^./,c => c.toUpperCase())}
export function defaultPresets(bounds:Bounds,spec?:string[]):Preset[] {
 if(spec?.length)return spec.map(text => ({label:presetName(text),text}))
 const year=new Date(bounds.today).getUTCFullYear(),first=new Date(bounds.start).getUTCFullYear(),years=[year-1,year-2].filter(y => y>=first).map(y => String(y))
 return ['30d','90d','6mo','12mo','ytd','last month','last quarter',...years,'all'].map(text => ({label:presetName(text),text}))
}
const MIN_DAYS=7
const snap=(t:number) => Math.round(t/DAY)*DAY
const percent=(t:number,b:Bounds) => (t-b.start)/Math.max(DAY,b.today-b.start)*100

export function HistoryNavigator({bounds,range,onChange,xs,values,bands,label='Full history'}:{bounds:Bounds;range:Range;onChange:(range:Range,meta:{final:boolean}) => void;xs:number[];values:ArrayLike<number>;bands?:Band[];label?:string}) {
 const box=useRef<HTMLDivElement>(null),drag=useRef<{mode:'l'|'r'|'m'|'new';x0:number;a0:number;b0:number;moved:boolean;last:Range}|null>(null),frame=useRef(0)
 const span=Math.max(DAY,bounds.today-bounds.start),years:number[]=[]
 for(let y=new Date(bounds.start).getUTCFullYear()+1;Date.UTC(y,0,1)<=bounds.today;y++)years.push(y)
 const n=xs.length;let path=''
 if(n>1){
  let min=Infinity,max=-Infinity;const stride=Math.max(1,Math.ceil(n/500))
  for(let i=0;i<n;i+=stride){if(values[i]<min)min=values[i];if(values[i]>max)max=values[i]}
  const top=max>min ? max-min : 1
  path=`M0 72`;for(let i=0;i<n;i+=stride)path+=` L${((xs[i]-bounds.start)/span*1000).toFixed(1)} ${(64-(values[i]-min)/top*50).toFixed(1)}`
  path+=` L1000 ${(64-(values[n-1]-min)/top*50).toFixed(1)} L1000 72 Z`
 }
 const timeAt=(px:number) => {const w=box.current?.clientWidth ?? 1;return px/w*span}
 const emit=(next:Range,final:boolean) => {
  if(drag.current)drag.current.last=next
  if(final){cancelAnimationFrame(frame.current);frame.current=0;onChange(next,{final:true});return}
  if(!frame.current)frame.current=requestAnimationFrame(() => {frame.current=0;if(drag.current)onChange(drag.current.last,{final:false})})
 }
 const down=(e:ReactPointerEvent<HTMLDivElement>) => {
  if(e.button)return
  const el=e.target as HTMLElement,grip=el.closest<HTMLElement>('[data-grip]')
  const mode=grip ? grip.dataset.grip as 'l'|'r' : el.closest('[data-window]') ? 'm' as const : 'new' as const
  const r=box.current!.getBoundingClientRect(),at=snap(bounds.start+timeAt(e.clientX-r.left))
  drag.current={mode,x0:e.clientX,a0:mode==='new' ? at : range.from,b0:mode==='new' ? at : range.to,moved:false,last:range}
  box.current!.setPointerCapture(e.pointerId)
 }
 const move=(e:ReactPointerEvent<HTMLDivElement>) => {
  const d=drag.current;if(!d)return
  const dt=timeAt(e.clientX-d.x0);if(!d.moved && Math.abs(e.clientX-d.x0)<3)return
  d.moved=true;let a=range.from,b=range.to
  if(d.mode==='m'){const l=d.b0-d.a0;a=Math.max(bounds.start,Math.min(bounds.today-l,snap(d.a0+dt)));b=a+l}
  else if(d.mode==='l'){a=Math.max(bounds.start,Math.min(snap(d.a0+dt),d.b0-(MIN_DAYS-1)*DAY));b=d.b0}
  else if(d.mode==='r'){b=Math.min(bounds.today,Math.max(snap(d.b0+dt),d.a0+(MIN_DAYS-1)*DAY));a=d.a0}
  else {const t=snap(d.a0+dt);a=Math.max(bounds.start,Math.min(d.a0,t));b=Math.min(bounds.today,Math.max(d.a0,t));if(b-a<(MIN_DAYS-1)*DAY)return}
  emit({from:a,to:b},false)
 }
 const up=(e:ReactPointerEvent<HTMLDivElement>) => {
  const d=drag.current;if(!d)return
  if(!d.moved){if(d.mode==='new'){const l=range.to-range.from;let a=d.a0-Math.round(l/2/DAY)*DAY;a=Math.max(bounds.start,Math.min(bounds.today-l,a));onChange({from:a,to:a+l},{final:true})}}
  else emit(d.last,true)
  drag.current=null;if(box.current?.hasPointerCapture(e.pointerId))box.current.releasePointerCapture(e.pointerId)
 }
 const keys=(side:'l'|'r'|'m') => (e:KeyboardEvent) => {
  const step=(e.shiftKey ? 30 : 7)*DAY,dir=e.key==='ArrowLeft' || e.key==='ArrowDown' ? -1 : e.key==='ArrowRight' || e.key==='ArrowUp' ? 1 : 0
  let {from:a,to:b}=range
  if(dir){
   if(side==='l')a=Math.max(bounds.start,Math.min(a+dir*step,b-(MIN_DAYS-1)*DAY))
   else if(side==='r')b=Math.min(bounds.today,Math.max(b+dir*step,a+(MIN_DAYS-1)*DAY))
   else {const l=b-a;a=Math.max(bounds.start,Math.min(bounds.today-l,a+dir*step));b=a+l}
  } else if(e.key==='Home'){if(side==='l')a=bounds.start;else if(side==='r')b=a+(MIN_DAYS-1)*DAY;else {b=bounds.start+(b-a);a=bounds.start}}
  else if(e.key==='End'){if(side==='r')b=bounds.today;else if(side==='l')a=b-(MIN_DAYS-1)*DAY;else {a=bounds.today-(b-a);b=bounds.today}}
  else return
  e.preventDefault();e.stopPropagation();onChange({from:a,to:b},{final:true})
 }
 const left=percent(range.from,bounds),width=Math.max(.6,percent(range.to,bounds)-left),slider=(side:'l'|'r'|'m',name:string,value:number,text:string) => ({role:'slider',tabIndex:0,'aria-label':name,'aria-valuemin':0,'aria-valuemax':Math.round(span/DAY),'aria-valuenow':Math.round((value-bounds.start)/DAY),'aria-valuetext':text,'aria-orientation':'horizontal' as const,onKeyDown:keys(side)})
 return <div ref={box} className="tm-nav" role="group" aria-label={`${label}. Drag to choose a window.`} onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerCancel={up}>
  <svg viewBox="0 0 1000 72" preserveAspectRatio="none" aria-hidden="true">{path && <path d={path} className="tm-nav-area" vectorEffect="non-scaling-stroke"/>}{bands?.map(b => <rect key={`${b.label}${b.from}`} x={(b.from-bounds.start)/span*1000} y="64" width={Math.max(3,(b.to-b.from)/span*1000)} height="8" className="tm-nav-band"><title>{b.label}</title></rect>)}</svg>
  {years.map(y => <span key={y} className="tm-nav-year" style={{left:`${percent(Date.UTC(y,0,1),bounds)}%`}}>{y}</span>)}
  <div className="tm-nav-win" data-window="" style={{left:`${left}%`,width:`${width}%`}} {...slider('m','Move the time window',range.from,rangeLabel(range))}>
   <span className="tm-grip l" data-grip="l" {...slider('l','Start of the time window',range.from,dayLabel(range.from))}/>
   <span className="tm-grip r" data-grip="r" {...slider('r','End of the time window',range.to,dayLabel(range.to))}/>
  </div>
 </div>
}

export function TimeBar({state,bounds,presets,navigator,hideCompare,label='Time range. Type anything'}:{state:TimeState;bounds:Bounds;presets?:Preset[];navigator?:{xs:number[];values:ArrayLike<number>;bands?:Band[];label?:string};hideCompare?:boolean;label?:string}) {
 const [draft,setDraft]=useState<string|null>(null),input=useRef<HTMLInputElement>(null),fieldId=useId(),statusId=useId(),compareId=useId()
 const list=presets ?? defaultPresets(bounds)
 const isCanonical=/^\d{4}-\d{2}-\d{2} to \d{4}-\d{2}-\d{2}$/.test(state.text),activePreset=list.find(p => {const r=parseRange(p.text,bounds);return r && r.from===state.range.from && r.to===state.range.to})
 const shown=draft ?? (activePreset && isCanonical ? activePreset.label : isCanonical ? rangeLabel(state.range) : state.text)
 const parsed=draft===null ? null : parseTime(draft,bounds)
 const settled=`${rangeLabel(state.range)} · ${rangeDays(state.range).toLocaleString('en-US')} ${rangeDays(state.range)===1 ? 'day' : 'days'}`
 const status=!parsed ? settled : parsed.ok ? `${rangeLabel(parsed.range)} · ${rangeDays(parsed.range).toLocaleString('en-US')} ${rangeDays(parsed.range)===1 ? 'day' : 'days'} · press Enter` : parsed.message
 const invalid=!!parsed && !parsed.ok && parsed.reason!=='empty'
 const latest=useRef(state);latest.current=state
 useEffect(() => {
  const onKey=(e:globalThis.KeyboardEvent) => {
   if(ignoreShortcut(e))return
   if(e.key==='[')latest.current.step(-1);else if(e.key===']')latest.current.step(1);else if(e.key==='z')latest.current.zoomOut()
   else if(e.key==='t'){e.preventDefault();input.current?.focus()}
  }
  document.addEventListener('keydown',onKey);return () => document.removeEventListener('keydown',onKey)
 },[])
 return <section className="tm-time" aria-label="Time range">
  <div className="tm-time-top">
   <div className={`tm-time-entry${invalid ? ' invalid' : ''}`}>
    <label htmlFor={fieldId}>{label}</label>
    <input id={fieldId} ref={input} value={shown} autoComplete="off" spellCheck={false} enterKeyHint="go" aria-invalid={invalid} aria-describedby={statusId}
     onFocus={e => e.currentTarget.select()} onChange={e => setDraft(e.target.value)} onBlur={() => setDraft(null)}
     onKeyDown={e => {if(e.key==='Enter'){if(parsed?.ok){state.setText(draft!);setDraft(null);e.currentTarget.blur()}else if(draft===null)e.currentTarget.blur()}else if(e.key==='Escape'){setDraft(null);e.currentTarget.blur()}}}/>
    <div id={statusId} className={`tm-time-status${invalid ? ' err' : ''}`} aria-live="polite">{status}</div>
   </div>
   <div className="tm-time-side">
    <div className="tm-row">
     <button type="button" className="tm-btn square" aria-label="Previous period" title="Previous period ( [ )" onClick={() => state.step(-1)}>←</button>
     <button type="button" className="tm-btn square" aria-label="Next period" title="Next period ( ] )" onClick={() => state.step(1)}>→</button>
     <button type="button" className="tm-btn" title="Zoom out ( z )" onClick={state.zoomOut}>Zoom out</button>
     <button type="button" className="tm-btn" disabled={!state.canUndo} onClick={state.undo}>Undo zoom</button>
    </div>
    {!hideCompare && <div className="tm-row"><label className="tm-compare-label" htmlFor={compareId}>Compare to</label><select id={compareId} className="tm-btn tm-select" value={state.compare} onChange={e => state.setCompare(e.target.value as CompareMode)}><option value="previous_period">Previous period</option><option value="previous_year">Same period last year</option><option value="none">Nothing</option></select></div>}
   </div>
  </div>
  <div className="tm-presets" role="group" aria-label="Quick ranges">{list.map(p => <button key={p.text} type="button" className="tm-btn" aria-pressed={activePreset?.text===p.text} onClick={() => state.setText(p.text)}>{p.label}</button>)}</div>
  {navigator && <HistoryNavigator bounds={bounds} range={state.range} onChange={(range,{final}) => state.setRange(range,{final})} xs={navigator.xs} values={navigator.values} bands={navigator.bands} label={navigator.label}/>}
 </section>
}
