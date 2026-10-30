import { lazy, Suspense, useId, useState, type ReactNode } from 'react'
import { DAY, zoomAround, zoomOutRange, type Bounds, type Bucket, type Range } from './time-grammar'
import { bucketGeometry, pointLabel, summarize, type Band, type ChartSeries, type Mark } from './chart-core'
import type { ValueFormat } from './format'
import './telemetry.css'

// uPlot only ever loads in this lazy chunk, so a page without a chart never pays for it.
const Canvas=lazy(() => import('./time-chart-canvas'))
export type { Band, ChartSeries, Mark } from './chart-core'
export type TimeChartProps={
 title:string;heading?:ReactNode;note?:ReactNode;mark?:Mark;format:ValueFormat;series:ChartSeries[];
 /** Bucket starts in ms for a time axis. */ xs?:number[];
 /** Labels for a category axis. Giving these switches the chart to category mode. */ categories?:string[];
 bucket?:Bucket;range?:Range;bounds?:Bounds;onRangeChange?:(range:Range,meta:{final:boolean}) => void;onStepOut?:() => void;
 syncKey?:string;compare?:{label:string;values:ArrayLike<number|null>}|null;bands?:Band[];bandLabel?:string;estimatedUntil?:number|null;rule?:{y:number;label:string};
 /** Balance and rate charts leave zero so small moves stay visible; the default follows the mark and unit. */ baseline?:'zero'|'auto';
 height?:number;direct?:boolean;legend?:'isolate'|'toggle';hidden?:string[];onHiddenChange?:(hidden:string[]) => void;onPick?:(index:number) => void;
 pointLabels?:string[];renderTable?:(hidden:string[]) => ReactNode;tableLabel?:string;emptyText?:string;className?:string;
}
const swatch=(s:ChartSeries) => s.estimated ? 'tm-hatch' : s.dashed ? 'tm-dash' : undefined
export function ChartSkeleton({height=232}:{height?:number}) {return <div className="tm-plot-skeleton" style={{height}} aria-hidden="true"/>}
export function TimeChart({title,heading,note,mark='line',format,series,xs=[],categories,bucket,range,bounds,onRangeChange,onStepOut,syncKey,compare,bands,bandLabel,estimatedUntil,rule,baseline,height=232,direct,legend='isolate',hidden:hiddenProp,onHiddenChange,onPick,pointLabels,renderTable,tableLabel,emptyText='No data in this range.',className}:TimeChartProps) {
 const [table,setTable]=useState(false),[hiddenLocal,setHiddenLocal]=useState<string[]>([]),[zoom,setZoom]=useState<Range|null>(null),[live,setLive]=useState('')
 const summary=useId()
 const hidden=hiddenProp ?? hiddenLocal,setHidden=onHiddenChange ?? setHiddenLocal
 const kind=categories ? 'category' as const : 'time' as const
 const axis=categories ? categories.map((_,i) => i) : xs,visible=series.filter(s => !hidden.includes(s.key))
 const intraday=kind==='time' && axis.some(x => x%DAY!==0)
 const labels=pointLabels ?? (categories ?? axis.map(x => pointLabel(x,bucket,intraday)))
 const view=range ?? zoom,geometry=categories ? {edges:axis.map(i => i-.5).concat(axis.length-.5),mids:axis} : bucketGeometry(axis,bucket,view ?? undefined)
 const extent:Bounds|undefined=bounds ?? (axis.length && kind==='time' ? {start:Math.floor(axis[0]/DAY)*DAY,today:Math.floor(axis[axis.length-1]/DAY)*DAY} : undefined)
 const controlled=!!onRangeChange
 const apply=(next:Range,final:boolean) => {if(controlled)onRangeChange(next,{final});else setZoom(next)}
 const wide:Range|null=view ?? (extent ? {from:extent.start,to:extent.today} : null)
 const zero=baseline ? baseline==='zero' : mark==='bar' || mark==='stack' || mark==='area' || format.zero
 const toggle=(key:string,only:boolean) => {
  if(legend==='toggle' || !only){setHidden(hidden.includes(key) ? hidden.filter(x => x!==key) : [...hidden,key]);return}
  const isolated=visible.length===1 && visible[0].key===key;setHidden(isolated ? [] : series.filter(s => s.key!==key).map(s => s.key))
 }
 const onDrag=kind==='time' && extent ? (a:number,b:number) => {const from=Math.max(extent.start,Math.round(a/DAY)*DAY),to=Math.min(extent.today,Math.round(b/DAY)*DAY);if(to>from)apply({from,to},true)} : undefined
 const onWheelZoom=kind==='time' && extent && wide ? (center:number,factor:number,final:boolean) => apply(zoomAround(wide,center,factor,extent),final) : undefined
 const onDouble=kind==='time' && extent && wide ? () => {if(onStepOut)onStepOut();else if(controlled)apply(zoomOutRange(wide,extent),true);else setZoom(null)} : undefined
 const isEmpty=!axis.length || !series.length
 const showLegend=series.length>1 || !!compare || !!bandLabel || !!estimatedUntil
 return <figure className={`tm-chart${className ? ` ${className}` : ''}`} aria-describedby={summary}>
  {(heading ?? note ?? true) && <figcaption className="tm-chart-head"><div>{heading ?? <h3>{title}</h3>}{note && <p>{note}</p>}</div><button type="button" className="tm-btn small" aria-pressed={table} onClick={() => setTable(!table)}>{table ? 'Chart' : 'Table'}</button></figcaption>}
  {table ? (renderTable ? renderTable(hidden) : <div className="tm-chart-table" tabIndex={0} role="region" aria-label={tableLabel ?? `${title} data`}><table><thead><tr><th scope="col">{kind==='time' ? 'Period' : 'Category'}</th>{visible.map(s => <th key={s.key} scope="col" className="numeric">{s.label || 'Value'}</th>)}{compare && <th scope="col" className="numeric">{compare.label}</th>}</tr></thead><tbody>{labels.map((label,i) => <tr key={label+i}><td>{label}</td>{visible.map(s => {const v=s.values[i];return <td key={s.key} className="numeric">{v===null || v===undefined ? '—' : s.text?.[i] ?? format.full(v)}</td>})}{compare && <td className="numeric">{compare.values[i]===null || compare.values[i]===undefined ? '—' : format.full(compare.values[i] as number)}</td>}</tr>).reverse()}</tbody></table></div>)
  : isEmpty ? <p className="tm-empty" style={{minHeight:height}}>{emptyText}</p>
  : <Suspense fallback={<ChartSkeleton height={height}/>}><Canvas kind={kind} xs={axis} edges={geometry.edges} mids={geometry.mids} categories={categories} labels={labels} series={visible} mark={mark} format={format} zero={zero} height={height} title={title} view={kind==='time' ? view : null} compare={compare} bands={bands} estimatedUntil={estimatedUntil} rule={rule} syncKey={syncKey} direct={direct} onDrag={onDrag} onWheelZoom={onWheelZoom} onDouble={onDouble} onPick={onPick} onLive={setLive}/></Suspense>}
  {!table && showLegend && <div className="tm-legend" role="group" aria-label={`${title} series`}>
   {series.length>1 && series.map(s => <button key={s.key} type="button" aria-pressed={!hidden.includes(s.key)} title={legend==='isolate' ? 'Click to show only this. Shift-click to hide it.' : undefined} onClick={event => toggle(s.key,!(event.shiftKey || event.ctrlKey || event.metaKey))}><i className={swatch(s)} style={{background:s.estimated ? undefined : `var(${s.color})`}}/>{s.label || 'Value'}</button>)}
   {series.length===1 && (compare || bandLabel || estimatedUntil) && <span className="tm-legend-key"><i className="line" style={{background:`var(${series[0].color})`}}/>{series[0].label}</span>}
   {compare && <span className="tm-legend-key"><i className="tm-dash"/>{compare.label}</span>}
   {bandLabel && bands?.length ? <span className="tm-legend-key"><i style={{background:'var(--c1)',opacity:.45}}/>{bandLabel}</span> : null}
   {estimatedUntil ? <span className="tm-legend-key"><i className="tm-hatch"/>Estimated history</span> : null}
  </div>}
  <p className="sr-only" id={summary}>{summarize(title,mark,labels,visible,format)}</p>
  <div className="sr-only" aria-live="polite">{live}</div>
 </figure>
}
