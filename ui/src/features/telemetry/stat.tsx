import type { MouseEvent, ReactNode } from 'react'
import { percent } from './format'
import { Sparkline } from './marks'
import './telemetry.css'

export type StatChange={text:string;detail?:string;direction:'up'|'down'|'flat';good:boolean|null}
// Tone follows meaning, not direction: spending down is good news, so `goodWhen` belongs to the metric.
export function changeBetween(current:number,previous:number|null|undefined,goodWhen:'up'|'down'|'neutral',format:(value:number) => string):StatChange|null {
 if(previous===null || previous===undefined || previous===0)return null
 const delta=current-previous,direction=delta>0 ? 'up' as const : delta<0 ? 'down' as const : 'flat' as const
 const good=goodWhen==='neutral' || delta===0 ? null : (delta>0)===(goodWhen==='up')
 return {direction,good,text:`${delta>0 ? '+' : delta<0 ? '−' : ''}${percent(Math.abs(delta)/Math.abs(previous)*100)}`,detail:`${delta>0 ? '+' : delta<0 ? '−' : ''}${format(Math.abs(delta))}`}
}
export type StatProps={label:string;value:string;change?:StatChange|null;comparison?:string;note?:ReactNode;spark?:ArrayLike<number|null>;sparkColor?:string;sparkLabel?:string;estimatedShare?:number;fixedRange?:string;href?:string;onOpen?:(event:MouseEvent) => void;openLabel?:string;onDetails?:() => void}
// Every line keeps its height with or without content, so a loading page and a loaded page occupy the same space.
export function Stat({label,value,change,comparison,note,spark,sparkColor,sparkLabel,estimatedShare,fixedRange,href,onOpen,openLabel,onDetails}:StatProps) {
 const tone=change?.good===true ? 'good' : change?.good===false ? 'bad' : 'flat'
 const open=href || onOpen
 const number=<div className="tm-stat-value data">{value}</div>
 return <div className="tm-stat">
  <div className="tm-stat-label"><span>{label}</span>{fixedRange && <span className="tm-badge">Fixed range</span>}{onDetails && <button type="button" className="tm-details-link" aria-label={`How was ${label} calculated?`} onClick={onDetails}>Details</button>}</div>
  {open ? <a className="tm-stat-open" href={href ?? '#'} onClick={event => {if(onOpen){if(!href)event.preventDefault();onOpen(event)}}} aria-label={`${label} ${value}. ${openLabel ?? 'See the entries'}.`}>{number}</a> : number}
  <div className="tm-stat-change">
   {change ? <><span className={tone}><span aria-hidden="true">{change.direction==='up' ? '▲' : change.direction==='down' ? '▼' : '='}</span> <span className="sr-only">{change.direction==='up' ? 'Up' : change.direction==='down' ? 'Down' : 'No change'} </span>{change.text}</span>{change.detail && <span className="muted"> {change.detail}</span>}{comparison && <span className="muted"> vs {comparison}</span>}</> : <span className="muted">{comparison ? `No earlier data to compare with ${comparison}` : 'No comparison'}</span>}
  </div>
  <div className="tm-stat-note">{note}{estimatedShare ? <span className="tm-badge warn">{Math.round(estimatedShare*100)}% estimated</span> : null}</div>
  <Sparkline values={spark ?? []} color={sparkColor} label={sparkLabel}/>
 </div>
}
export function StatSkeleton({label}:{label?:string}) {
 return <div className="tm-stat" aria-busy="true"><div className="tm-stat-label"><span>{label ?? <i className="tm-skel" style={{width:'60%'}}/>}</span></div><div className="tm-stat-value"><i className="tm-skel" style={{width:'55%',height:28}}/></div><div className="tm-stat-change"><i className="tm-skel" style={{width:'70%'}}/></div><div className="tm-stat-note"/><div className="tm-spark"><i className="tm-skel" style={{height:30,display:'block'}}/></div></div>
}
