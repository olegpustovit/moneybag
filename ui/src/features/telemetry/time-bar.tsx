import { Fragment, useEffect, useId, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { DAY, dayFromIso, dayLabel, isoDay, parseRange, parseTime, rangeDays, rangeLabel, stepRange, type Bounds, type CompareMode, type Range } from './time-grammar'
import { ignoreShortcut, type TimeState } from './use-time-state'
import type { Band } from './chart-core'
import { CurrencySwitch, type Currency } from './currency'
import './telemetry.css'

export type Preset={label:string;text:string}
const presetName=(text:string) => {const m=text.match(/^(\d+)(d|mo|w|y)$/);if(m)return `${m[1]}${m[2]==='mo' ? 'M' : m[2].toUpperCase()}`;return text==='ytd' ? 'YTD' : text==='all' ? 'All' : text.replace(/^./,c => c.toUpperCase())}
export function defaultPresets(bounds:Bounds,spec?:string[]):Preset[] {
 // This month is always offered, right after the 30-day preset, so a page that omits it still has it.
 if(spec?.length){const at=spec.indexOf('30d')+1,list=spec.includes('this month') ? spec : [...spec.slice(0,at),'this month',...spec.slice(at)];return list.map(text => ({label:presetName(text),text}))}
 const year=new Date(bounds.today).getUTCFullYear(),first=new Date(bounds.start).getUTCFullYear(),years=[year-1,year-2].filter(y => y>=first).map(y => String(y))
 return ['30d','this month','90d','6mo','12mo','ytd','last month','last quarter',...years,'all'].map(text => ({label:presetName(text),text}))
}
// What the field shows for a saved range. Anything else someone typed stays exactly as they typed it.
const UNITS:Record<string,string>={d:'day',w:'week',mo:'month',y:'year'}
export function friendlyTime(text:string):string {
 const m=text.match(/^(\d+)(d|w|mo|y)$/);if(m)return `Last ${m[1]} ${UNITS[m[2]]}${m[1]==='1' ? '' : 's'}`
 return text==='ytd' ? 'Year to date' : text==='all' ? 'All time' : text.length<3 ? text : text.replace(/^[a-z]/,c => c.toUpperCase())
}
const MIN_DAYS=7
const snap=(t:number) => Math.round(t/DAY)*DAY
const dash=(r:Range) => r.from===r.to ? dayLabel(r.from) : `${dayLabel(r.from)} – ${dayLabel(r.to)}`
const percent=(t:number,b:Bounds) => (t-b.start)/Math.max(DAY,b.today-b.start)*100
type Drag={mode:'l'|'r'|'m'|'new'|'sel';x0:number;a0:number;b0:number;moved:boolean;last:Range}

export function HistoryNavigator({bounds,range,onChange,xs,values,bands,label='Full history'}:{bounds:Bounds;range:Range;onChange:(range:Range,meta:{final:boolean}) => void;xs:number[];values:ArrayLike<number>;bands?:Band[];label?:string}) {
 const box=useRef<HTMLDivElement>(null),tip=useRef<HTMLSpanElement>(null),sel=useRef<HTMLDivElement>(null),drag=useRef<Drag|null>(null),frame=useRef(0)
 // A zoomed scale is only ever a view: if the range leaves it (presets, stepping) the strip goes back to full history.
 const [scale,setScale]=useState<Bounds|null>(null),view=scale && range.from>=scale.start && range.to<=scale.today ? scale : null
 if(scale && !view)setScale(null)
 const vb=view ?? bounds,span=Math.max(DAY,vb.today-vb.start),years:number[]=[]
 for(let y=new Date(vb.start).getUTCFullYear()+1;Date.UTC(y,0,1)<=vb.today;y++)if(Date.UTC(y,0,1)>vb.start)years.push(y)
 let path=''
 {
  // The curve fits its own min to max inside the band under the year labels, so its shape shows.
  const pad=span*.06,seen:number[]=[];let min=Infinity,max=-Infinity
  for(let i=0;i<xs.length;i++)if(xs[i]>=vb.start-pad && xs[i]<=vb.today+pad){seen.push(i);if(values[i]<min)min=values[i];if(values[i]>max)max=values[i]}
  if(seen.length>1){
   const top=max>min ? max-min : 1,stride=Math.max(1,Math.ceil(seen.length/500)),y=(v:number) => (36-(v-min)/top*20).toFixed(1),x=(i:number) => ((xs[i]-vb.start)/span*1000).toFixed(1)
   path=`M${x(seen[0])} 44`
   for(let k=0;k<seen.length;k+=stride)path+=` L${x(seen[k])} ${y(values[seen[k]])}`
   const last=seen[seen.length-1];path+=` L${x(last)} ${y(values[last])} L${x(last)} 44 Z`
  }
 }
 const timeAt=(px:number) => px/(box.current?.clientWidth || 1)*span
 const showTip=(text:string,center:number) => {
  const t=tip.current,w=box.current?.clientWidth ?? 0;if(!t)return
  t.textContent=text;t.hidden=false;t.style.transform=`translateX(${Math.max(0,Math.min(w-t.offsetWidth,center-t.offsetWidth/2))}px)`
 }
 const hideTip=() => {if(tip.current)tip.current.hidden=true}
 const px=(t:number) => (t-vb.start)/span*(box.current?.clientWidth ?? 0)
 const windowTip=(r:Range) => showTip(dash(r),(px(r.from)+px(r.to))/2)
 const emit=(next:Range,final:boolean) => {
  if(drag.current)drag.current.last=next
  if(final){cancelAnimationFrame(frame.current);frame.current=0;onChange(next,{final:true});return}
  if(!frame.current)frame.current=requestAnimationFrame(() => {frame.current=0;if(drag.current)onChange(drag.current.last,{final:false})})
 }
 const down=(e:ReactPointerEvent<HTMLDivElement>) => {
  if(e.button || (e.target as HTMLElement).closest('button'))return
  const el=e.target as HTMLElement,grip=el.closest<HTMLElement>('[data-grip]'),shift=e.shiftKey && e.pointerType!=='touch'
  const mode=shift ? 'sel' as const : grip ? grip.dataset.grip as 'l'|'r' : el.closest('[data-window]') ? 'm' as const : 'new' as const
  const r=box.current!.getBoundingClientRect(),at=snap(vb.start+timeAt(e.clientX-r.left)),fresh=mode==='new' || mode==='sel'
  drag.current={mode,x0:e.clientX,a0:fresh ? at : range.from,b0:fresh ? at : range.to,moved:false,last:range}
  box.current!.dataset.drag=mode;box.current!.setPointerCapture(e.pointerId)
 }
 const move=(e:ReactPointerEvent<HTMLDivElement>) => {
  const d=drag.current,r=box.current!.getBoundingClientRect()
  if(!d){
   if(e.pointerType==='touch')return
   if((e.target as HTMLElement).closest('[data-grip]'))windowTip(range);else showTip(dayLabel(snap(vb.start+timeAt(e.clientX-r.left))),e.clientX-r.left)
   return
  }
  const dt=timeAt(e.clientX-d.x0);if(!d.moved && Math.abs(e.clientX-d.x0)<3)return
  d.moved=true;let a=range.from,b=range.to
  if(d.mode==='m'){const l=d.b0-d.a0;a=Math.max(vb.start,Math.min(vb.today-l,snap(d.a0+dt)));b=a+l}
  else if(d.mode==='l'){a=Math.max(vb.start,Math.min(snap(d.a0+dt),d.b0-(MIN_DAYS-1)*DAY));b=d.b0}
  else if(d.mode==='r'){b=Math.min(vb.today,Math.max(snap(d.b0+dt),d.a0+(MIN_DAYS-1)*DAY));a=d.a0}
  else {
   const t=snap(d.a0+dt);a=Math.max(vb.start,Math.min(d.a0,t));b=Math.min(vb.today,Math.max(d.a0,t))
   if(d.mode==='sel'){d.last={from:a,to:b};const s=sel.current;if(s){s.hidden=false;s.style.left=`${px(a)}px`;s.style.width=`${Math.max(2,px(b+DAY)-px(a))}px`}windowTip(d.last);return}
   if(b-a<(MIN_DAYS-1)*DAY)return
  }
  emit({from:a,to:b},false);windowTip({from:a,to:b})
 }
 const up=(e:ReactPointerEvent<HTMLDivElement>) => {
  const d=drag.current;if(!d)return
  if(d.mode==='sel'){
   if(d.moved){
    let {from:a,to:b}=d.last;if(b-a<(MIN_DAYS-1)*DAY){b=Math.min(vb.today,a+(MIN_DAYS-1)*DAY);a=b-(MIN_DAYS-1)*DAY}
    // The strip's own scale shows the pick with about three times its span around it.
    const len=Math.round(Math.max((b-a)*3,21*DAY)/DAY)*DAY,full=bounds.today-bounds.start
    if(len>=full)setScale(null);else {const s=Math.max(bounds.start,Math.min(bounds.today-len,snap((a+b)/2-len/2)));setScale({start:s,today:s+len})}
    onChange({from:a,to:b},{final:true})
   }
  } else if(!d.moved){if(d.mode==='new' && !scale){const l=range.to-range.from;let a=d.a0-Math.round(l/2/DAY)*DAY;a=Math.max(vb.start,Math.min(vb.today-l,a));onChange({from:a,to:a+l},{final:true})}}
  else emit(d.last,true)
  if(sel.current)sel.current.hidden=true
  hideTip();delete box.current!.dataset.drag;drag.current=null;if(box.current?.hasPointerCapture(e.pointerId))box.current.releasePointerCapture(e.pointerId)
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
 const left=percent(range.from,vb),width=Math.max(.6,percent(range.to,vb)-left),slider=(side:'l'|'r'|'m',name:string,value:number,text:string) => ({role:'slider',tabIndex:0,'aria-label':name,'aria-valuemin':0,'aria-valuemax':Math.round((bounds.today-bounds.start)/DAY),'aria-valuenow':Math.round((value-bounds.start)/DAY),'aria-valuetext':text,'aria-orientation':'horizontal' as const,onKeyDown:keys(side)})
 return <div ref={box} className="tm-nav" role="group" aria-label={`${label}. Drag to choose a window.`} title="Drag to move. Shift-drag to zoom in." onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerCancel={up} onPointerLeave={() => {if(!drag.current)hideTip()}} onDoubleClick={() => setScale(null)}>
  <svg viewBox="0 0 1000 44" preserveAspectRatio="none" aria-hidden="true">{path && <path d={path} className="tm-nav-area" vectorEffect="non-scaling-stroke"/>}{bands?.filter(b => b.to>=vb.start && b.from<=vb.today).map(b => <rect key={`${b.label}${b.from}`} x={(b.from-vb.start)/span*1000} y="41" width={Math.max(3,(b.to-b.from)/span*1000)} height="3" className="tm-nav-band"><title>{b.label}</title></rect>)}</svg>
  {years.map(y => <span key={y} className="tm-nav-year" style={{left:`${percent(Date.UTC(y,0,1),vb)}%`}}>{y}</span>)}
  <div ref={sel} className="tm-nav-sel" hidden/>
  <div className="tm-nav-win" data-window="" style={{'--l':left,'--w':width} as CSSProperties} {...slider('m','Move the time window',range.from,rangeLabel(range))}>
   <span className="tm-grip l" data-grip="l" {...slider('l','Start of the time window',range.from,dayLabel(range.from))}/>
   <span className="tm-grip r" data-grip="r" {...slider('r','End of the time window',range.to,dayLabel(range.to))}/>
  </div>
  <span ref={tip} className="tm-nav-tip" aria-hidden="true" hidden/>
  {view && <button type="button" className="tm-nav-reset" onClick={() => setScale(null)}>Full history</button>}
 </div>
}

export function TimeBar({state,bounds,presets,navigator,hideCompare,currency,prefetch,label='Time range. Type anything'}:{state:TimeState;bounds:Bounds;presets?:Preset[];navigator?:{xs:number[];values:ArrayLike<number>;bands?:Band[];label?:string};hideCompare?:boolean;currency?:Currency;prefetch?:(range:Range) => void;label?:string}) {
 const [draft,setDraft]=useState<string|null>(null),[edit,setEdit]=useState<{side:'from'|'to';value:string}|null>(null),input=useRef<HTMLInputElement>(null),fieldId=useId(),statusId=useId(),compareId=useId()
 const list=presets ?? defaultPresets(bounds)
 const isCanonical=/^\d{4}-\d{2}-\d{2} to \d{4}-\d{2}-\d{2}$/.test(state.text),activePreset=list.find(p => {const r=parseRange(p.text,bounds);return r && r.from===state.range.from && r.to===state.range.to})
 const shown=draft ?? (activePreset && isCanonical ? friendlyTime(activePreset.text) : isCanonical ? `${rangeDays(state.range).toLocaleString('en-US')} ${rangeDays(state.range)===1 ? 'day' : 'days'}` : friendlyTime(state.text))
 const parsed=draft===null ? null : parseTime(draft,bounds)
 const settled=rangeLabel(state.range)
 const status=!parsed ? settled : parsed.ok ? `${rangeLabel(parsed.range)} · ${rangeDays(parsed.range).toLocaleString('en-US')} ${rangeDays(parsed.range)===1 ? 'day' : 'days'} · press Enter` : parsed.message
 const invalid=!!parsed && !parsed.ok && parsed.reason!=='empty'
 const warm=(r:Range|null) => {if(r)prefetch?.(r)},hot=(direction:number) => ({onPointerEnter:() => warm(stepRange(state.range,direction,bounds)),onFocus:() => warm(stepRange(state.range,direction,bounds))})
 // A date commits as soon as it is a real day inside the history; the other end follows so from never passes to.
 const pickDate=(side:'from'|'to',value:string) => {
  setEdit({side,value});if(!/^\d{4}-\d{2}-\d{2}$/.test(value))return
  const t=dayFromIso(value);if(Number.isNaN(t) || t<bounds.start || t>bounds.today)return
  const {from,to}=state.range;state.setRange(side==='from' ? {from:t,to:Math.max(t,to)} : {from:Math.min(t,from),to:t},{final:true})
 }
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
    <label htmlFor={fieldId}>{label.split('. ')[0]}{label.includes('. ') && <span className="sr-only">. {label.split('. ').slice(1).join('. ')}</span>}</label>
    <input id={fieldId} ref={input} value={shown} autoComplete="off" spellCheck={false} enterKeyHint="go" aria-invalid={invalid} aria-describedby={statusId}
     onFocus={e => e.currentTarget.select()} onChange={e => setDraft(e.target.value)} onBlur={() => setDraft(null)}
     onKeyDown={e => {if(e.key==='Enter'){if(parsed?.ok){state.setText(draft!);setDraft(null);e.currentTarget.blur()}else if(draft===null)e.currentTarget.blur()}else if(e.key==='Escape'){setDraft(null);e.currentTarget.blur()}}}/>
   </div>
   <div className="tm-dates">
    <div className={`tm-dates-pair${draft!==null ? ' typing' : ''}`}>
     {(['from','to'] as const).map(side => <Fragment key={side}>
      {side==='to' && <span aria-hidden="true">–</span>}
      <input type="date" aria-label={side==='from' ? 'Start date' : 'End date'} min={isoDay(bounds.start)} max={isoDay(bounds.today)} value={edit?.side===side ? edit.value : isoDay(state.range[side])} onChange={e => pickDate(side,e.target.value)} onBlur={() => setEdit(null)}/>
     </Fragment>)}
    </div>
    <div id={statusId} className={`tm-time-status${invalid ? ' err' : ''}${draft!==null ? ' on' : ''}`} aria-live="polite" title={status}>{status}</div>
   </div>
   <div className="tm-arrows">
    <button type="button" className="tm-btn square" aria-label="Previous period" title="Previous period ( [ )" {...hot(-1)} onClick={() => state.step(-1)}>←</button>
    <button type="button" className="tm-btn square" aria-label="Next period" title="Next period ( ] )" {...hot(1)} onClick={() => state.step(1)}>→</button>
   </div>
   <div className="tm-presets" role="group" aria-label="Quick ranges">{list.map(p => <button key={p.text} type="button" className="tm-btn" aria-pressed={activePreset?.text===p.text} onPointerEnter={() => warm(parseRange(p.text,bounds))} onFocus={() => warm(parseRange(p.text,bounds))} onClick={() => state.setText(p.text)}>{p.label}</button>)}</div>
   <div className="tm-zoom">
    {currency && <CurrencySwitch value={currency}/>}
    <button type="button" className="tm-btn" title="Zoom out ( z )" onClick={state.zoomOut}>Zoom out</button>
    <button type="button" className="tm-btn" disabled={!state.canUndo} onClick={state.undo}>Undo zoom</button>
    {!hideCompare && <><label className="sr-only" htmlFor={compareId}>Compare to</label><select id={compareId} title="Compare to" className="tm-btn tm-select" value={state.compare} onChange={e => state.setCompare(e.target.value as CompareMode)}><option value="previous_period">Previous period</option><option value="previous_year">Same period last year</option><option value="none">Nothing</option></select></>}
   </div>
  </div>
  {navigator && <HistoryNavigator bounds={bounds} range={state.range} onChange={(range,{final}) => state.setRange(range,{final})} xs={navigator.xs} values={navigator.values} bands={navigator.bands} label={navigator.label}/>}
 </section>
}
