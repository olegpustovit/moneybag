import { CatchBoundary, Link, useNavigate } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { componentValue, type ComponentValue } from '../dashboard/values'
import { tableTotal } from '../dashboard/table-total'
import { annotationBands, balanceField, balanceSeries, colorOf, compareSeries, cubeCanCompute, goodWhen, metricTotal, timeseries, whatMoved, type Cube, type CubeMetric, type Dimension, type EstimatePolicy, type Pill } from './cube'
import { compareWindow, dayFromIso, isoDay, rangeLabel, type Bounds, type Bucket, type CompareWindow, type Range, parseRange } from './time-grammar'
import { compactNumber, money, percent, unitFormat, type Unit } from './format'
import { TimeChart, type ChartSeries } from './time-chart'
import LegacyChart from './legacy-chart'
import { RankedBars, Sankey, type RankedRow } from './marks'
import { Stat, changeBetween, type StatChange } from './stat'
import { DataGrid, type ColumnType, type GridColumn, type GridRow } from './data-grid'
import { openDetails, type MetricRequest } from './shell-store'
import type { SpecComponent } from './view-data'
import type { DetailsContent } from './details-drawer'
import './telemetry.css'

export type PanelCtx={cube:Cube|null;range:Range;win:CompareWindow|null;compareMode:string;defaultCompare:string;pills:Pill[];filtering:boolean;dashboard:string;syncKey:string;bounds:Bounds;explore:string[];readOnly?:boolean;
 setRange:(range:Range,meta:{final:boolean}) => void;stepOut:() => void;addPill:(pill:Pill) => void;tableLink?:(id:string) => ReactNode}
type Comparison={from?:string;to?:string;total?:string;text?:string;delta_pct?:number|null;direction?:string;good?:boolean}
type Point={bucket?:string;name:string;value:string;text:string;estimated?:boolean}
type MetricValue={type:string;metric:string;from?:string;to?:string;unit?:string;label?:string;total?:string|number;text?:string;title?:string;fixed?:string;compare?:Comparison|null;estimates?:{share_pct?:number;text?:string};formula?:string;entries?:{from?:string;to?:string;accounts?:string[]|null;exclude?:string[]|null};series?:Point[]|null;sparkline?:string[]|null;mark?:string;x?:{type?:string};annotations?:{from:string;to:string;label:string}[]|null;nodes?:string[];links?:{from:string;to:string;value:string;text:string;estimated?:boolean}[];rows?:{entry:string;date:string;payee:string;account:string;value:string;text:string;tags?:string[]|null}[]|null;ref?:{from?:string;to?:string;accounts?:string[]|null}}
const UNITS:Record<string,Unit>={eur:'eur',usd:'usd',int:'int',pct:'pct',mo:'mo',days:'days'}
const isMetric=(v:unknown):v is MetricValue => typeof v==='object' && v!==null && typeof (v as MetricValue).metric==='string' && ['stat','time_chart','ranked_bars','sankey','entries'].includes((v as MetricValue).type)
const tryNumber=(s:unknown) => {const n=Number(s);return Number.isFinite(n) ? n : 0}
const fromIso=(s:string|undefined) => s ? dayFromIso(s) : NaN
const isExact=(v:MetricValue,ctx:PanelCtx) => !ctx.filtering && !v.fixed && fromIso(v.from)===ctx.range.from && fromIso(v.to)===ctx.range.to && ctx.compareMode===ctx.defaultCompare
const metricOf=(ctx:PanelCtx,id:string|undefined) => ctx.cube?.metrics.find(m => m.id===id)
const metricText=(ctx:PanelCtx,m:CubeMetric,v:number) => m.unit==='int' ? Math.round(v).toLocaleString('en-US') : money(v,ctx.cube?.base)
const fixedRange=(c:SpecComponent,ctx:PanelCtx):Range|null => {if(!c.fixed)return null;return parseRange(c.fixed.range ?? 'all',ctx.bounds)}
const windowFor=(c:SpecComponent,ctx:PanelCtx,range:Range):CompareWindow|null => {const mode=c.compare ?? ctx.compareMode;return mode==='none' || c.fixed ? null : compareWindow(range,mode as 'previous_period',ctx.bounds)}
const policyOf=(c:SpecComponent,m:CubeMetric):EstimatePolicy => (c.estimates ?? m.estimates ?? 'include') as EstimatePolicy
const drillSearch=(entries:MetricValue['entries'],range?:Range) => ({t:range ? `${isoDay(range.from)} to ${isoDay(range.to)}` : entries?.from && entries?.to ? `${entries.from} to ${entries.to}` : undefined,accounts:entries?.accounts?.length ? entries.accounts : undefined})

function metricDetails(c:SpecComponent,v:MetricValue,ctx:PanelCtx,text:string):void {
 const compare=(c.compare ?? ctx.compareMode)==='none' ? undefined : (c.compare ?? ctx.compareMode)
 const metric:MetricRequest={id:v.metric,from:isoDay(c.fixed ? (fromIso(v.from) || ctx.range.from) : ctx.range.from),to:isoDay(c.fixed ? (fromIso(v.to) || ctx.range.to) : ctx.range.to),compare,filters:ctx.pills.flatMap(toServerFilter)}
 const content:DetailsContent={title:v.title ?? v.label ?? 'This number',value:text,subtitle:c.fixed ? `Fixed range: ${c.fixed.reason}` : rangeLabel(ctx.range)}
 openDetails({content,metric,target:{dashboard:ctx.dashboard,component:c.id,metric:v.metric,value:text,params:{from:metric.from ?? '',to:metric.to ?? ''}}})
}
// metric_query filters by inclusion. Text and amount pills have no exact server form, so Details says the figure before those filters.
const toServerFilter=(p:Pill):{field:string;op:string;values:string[]}[] => p.op==='is' && p.field!=='amount' && p.field!=='estimate' ? [{field:p.field,op:'in',values:p.values}] : p.op==='is not' && p.field!=='amount' && p.field!=='estimate' ? [{field:p.field,op:'not_in',values:p.values}] : []
function Head({title,fixed,onDetails,note,children}:{title?:string;fixed?:string;onDetails?:() => void;note?:ReactNode;children?:ReactNode}) {
 if(!title && !fixed && !onDetails && !note && !children)return null
 return <header className="tm-panel-head"><div>{title && <h2>{title}</h2>}{note && <p>{note}</p>}</div><div className="tm-panel-tools">{fixed && <span className="tm-badge" title={fixed}>Fixed range</span>}{children}{onDetails && <button type="button" className="tm-details-link" aria-label={`How was ${title ?? 'this'} calculated?`} onClick={onDetails}>Details</button>}</div></header>
}
function StatPanel({c,v,ctx}:{c:SpecComponent;v:MetricValue;ctx:PanelCtx}) {
 const m=metricOf(ctx,v.metric),canCube=!!ctx.cube && cubeCanCompute(m) && !c.fixed,exact=isExact(v,ctx)
 const label=v.title ?? v.label ?? ''
 let text=v.text ?? '',change:StatChange|null=null,comparison:string|undefined,spark:ArrayLike<number|null>=(v.sparkline ?? []).map(tryNumber),share=(v.estimates?.share_pct ?? 0)/100
 if(canCube && m && ctx.cube){
  const win=windowFor(c,ctx,ctx.range),q={range:ctx.range,pills:ctx.pills},now=metricTotal(ctx.cube,q,m,policyOf(c,m)),prev=win?.available ? metricTotal(ctx.cube,{range:win,pills:ctx.pills},m,policyOf(c,m)).total : null
  const fmt=(x:number) => metricText(ctx,m,x)
  if(!exact)text=fmt(now.total)
  change=changeBetween(now.total,prev,goodWhen(m),fmt);comparison=win?.label;share=now.total && m.unit!=='int' ? now.estimated/now.total : 0
  spark=balanceField(m) ? balanceSeries(ctx.cube,ctx.range,{field:balanceField(m)!,maxPoints:60}).values : timeseries(ctx.cube,q,{measure:m,estimates:policyOf(c,m)}).total
 } else if(v.compare && v.compare.direction){
  const pct=v.compare.delta_pct,dir=v.compare.direction==='up' ? 'up' as const : v.compare.direction==='down' ? 'down' as const : 'flat' as const
  change={direction:dir,good:dir==='flat' ? null : v.compare.good ?? null,text:pct===null || pct===undefined ? 'New' : `${dir==='up' ? '+' : dir==='down' ? '−' : ''}${percent(Math.abs(pct))}`,detail:v.compare.text ? `from ${v.compare.text}` : undefined};comparison=(c.compare ?? ctx.compareMode)==='previous_year' ? 'same period last year' : 'previous period'
 }
 const showNo=(c.compare ?? ctx.compareMode)==='none'
 return <Stat label={label} value={text} change={showNo ? null : change} comparison={showNo ? undefined : comparison} spark={spark} sparkColor={m && goodWhen(m)==='down' ? 'var(--c4)' : 'var(--c0)'} estimatedShare={share>.005 ? share : undefined} fixedRange={c.fixed?.reason} onDetails={() => metricDetails(c,v,ctx,text)} entriesTo={v.entries?.accounts?.length ? <Link to="/ledger" search={drillSearch(v.entries,c.fixed ? undefined : ctx.range)} className="tm-stat-open" aria-label={`${label} ${text}. See the entries.`}><span className="tm-stat-value data">{text}</span></Link> : undefined}/>
}
const bucketFromIso=(buckets:string[]):Bucket => {if(buckets[0]?.length===7)return 'month';if(buckets.length>1 && dayFromIso(buckets[1])-dayFromIso(buckets[0])>=6*864e5)return 'week';return 'day'}
const toMs=(b:string) => dayFromIso(b.length===7 ? `${b}-01` : b)
function serverSeries(points:Point[]):{xs:number[];series:ChartSeries[];bucket:Bucket} {
 const buckets=[...new Set(points.map(p => p.bucket ?? ''))].sort(),names=[...new Set(points.map(p => `${p.name}${p.estimated ? '\u0000' : ''}`))],index=new Map(buckets.map((b,i) => [b,i]))
 return {xs:buckets.map(toMs),bucket:bucketFromIso(buckets),series:names.map((key,i) => {const estimated=key.endsWith('\u0000'),name=estimated ? key.slice(0,-1) : key,values=buckets.map(():number|null => null),text=buckets.map(():string|undefined => undefined);for(const p of points)if(p.name===name && !!p.estimated===estimated){const at=index.get(p.bucket ?? '')!;values[at]=tryNumber(p.value);text[at]=p.text}return {key,label:name || 'Value',color:estimated ? '--hatch' : i<5 ? `--c${i}` : '--series-other',values,text,estimated}})}
}
function ChartPanel({c,v,ctx}:{c:SpecComponent;v:MetricValue;ctx:PanelCtx}) {
 const navigate=useNavigate(),m=metricOf(ctx,v.metric),cube=ctx.cube,live=!!cube && cubeCanCompute(m,c.split) && v.x?.type!=='category'
 const fixed=fixedRange(c,ctx),range=fixed ?? ctx.range,title=v.title ?? v.label ?? '',mark=(v.mark ?? c.mark ?? 'line') as 'line'|'area'|'stack'|'bar'
 const details=() => metricDetails(c,v,ctx,v.text ?? '')
 const interactive=c.fixed ? {} : {range,bounds:ctx.bounds,onRangeChange:ctx.setRange,onStepOut:ctx.stepOut}
 if(live && cube && m){
  const field=balanceField(m),win=windowFor(c,ctx,range),q={range,pills:ctx.pills},policy=policyOf(c,m),band=c.annotate ? annotationBands(cube,range) : undefined
  const compareName=win?.mode==='previous_year' ? 'Same period last year' : 'Previous period'
  if(field){
   const s=balanceSeries(cube,range,{field}),sign=m.sign || 1,values=sign===1 ? s.values : s.values.map(x => x*sign),prev=win?.available ? balanceSeries(cube,win,{field}) : null
   return <TimeChart title={title} heading={<Head title={title} fixed={c.fixed?.reason} onDetails={details} note={ctx.filtering ? 'Balances are not affected by filters.' : undefined}/>} mark={mark==='stack' || mark==='bar' ? 'line' : mark} baseline="auto" format={unitFormat('eur',{minor:true,code:cube.base})} xs={s.xs} series={[{key:'v',label:m.label,color:'--c0',values}]} syncKey={ctx.syncKey} {...interactive} compare={prev ? {label:compareName,values:s.xs.map((_,i) => prev.values[i]===undefined ? null : prev.values[i]*sign)} : null} bands={band} bandLabel={band?.length ? 'Trips' : undefined} estimatedUntil={cube.balances.estimatedUntil}/>
  }
  const split=c.split as Dimension|undefined,bucket=undefined as Bucket|undefined,ts=timeseries(cube,q,{measure:m,bucket,split,estimates:policy}),isInt=m.unit==='int'
  const cmp=compareSeries(cube,q,win,{measure:m,bucket:ts.bucket,estimates:policy==='split' ? 'include' : policy})
  return <TimeChart title={title} heading={<Head title={title} fixed={c.fixed?.reason} onDetails={details} note={`By ${ts.bucket}. Bucket size follows the range.`}/>} mark={mark} format={isInt ? unitFormat('int') : unitFormat('eur',{minor:true,code:cube.base})} xs={ts.xs} bucket={ts.bucket} series={ts.series} syncKey={ctx.syncKey} {...interactive} compare={cmp ? {label:compareName,values:cmp} : null} bands={band} bandLabel={band?.length ? 'Trips' : undefined} onPick={i => {const from=Math.max(ts.xs[i],range.from),next=ts.xs[i+1] ?? range.to+864e5;void navigate({to:'/ledger',search:drillSearch(v.entries,{from,to:Math.min(next-864e5,range.to)})})}}/>
 }
 const unit=UNITS[v.unit ?? ''] ?? 'number',{xs,series,bucket}=serverSeries(v.series ?? []),format=unit==='eur' || unit==='usd' ? {...unitFormat(unit),full:(x:number) => money(Math.round(x*100),unit.toUpperCase())} : unitFormat(unit),isCategory=v.x?.type==='category'
 const bands=(v.annotations ?? []).map(a => ({from:dayFromIso(a.from),to:dayFromIso(a.to),label:a.label}))
 return <TimeChart title={title} heading={<Head title={title} fixed={c.fixed?.reason} onDetails={details} note={ctx.filtering ? 'Not affected by filters.' : undefined}/>} mark={mark} format={format} {...(isCategory ? {categories:[...new Set((v.series ?? []).map(p => p.bucket ?? ''))]} : {xs,bucket,...interactive})} series={series} syncKey={ctx.syncKey} bands={bands} bandLabel={bands.length ? 'Trips' : undefined}/>
}
function RankedPanel({c,v,ctx}:{c:SpecComponent;v:MetricValue;ctx:PanelCtx}) {
 const navigate=useNavigate(),m=metricOf(ctx,v.metric),cube=ctx.cube,split=(c.split ?? 'category') as Dimension,title=v.title ?? v.label,fixed=fixedRange(c,ctx),range=fixed ?? ctx.range
 const select=(key:string) => {if(ctx.explore.includes(split))ctx.addPill({field:split,op:'is',values:[key]});else void navigate({to:'/ledger',search:drillSearch(v.entries,range)})}
 let rows:RankedRow[]
 if(cube && cubeCanCompute(m,split) && !balanceField(m)){
  const win=windowFor(c,ctx,range),moved=whatMoved(cube,{range,pills:ctx.pills},win,{by:split,measure:m,limit:40}).sort((a,b) => Math.abs(b.now)-Math.abs(a.now))
  rows=moved.filter(x => x.now || x.before).map(x => ({key:x.key,label:x.label,value:x.now,text:metricText(ctx,m,x.now),compare:win?.available ? x.before : undefined,compareText:win?.available ? metricText(ctx,m,x.before) : undefined,color:`var(${colorOf(cube,split,x.key)})`,estimated:x.key==='Estimated'}))
 } else rows=(v.series ?? []).map(p => ({key:p.name,label:p.name,value:tryNumber(p.value),text:p.text,estimated:p.estimated}))
 return <><Head title={title} fixed={c.fixed?.reason} onDetails={() => metricDetails(c,v,ctx,v.text ?? '')}/><RankedBars rows={rows} onSelect={select} selectLabel={ctx.explore.includes(split) ? 'Filter by' : 'See entries for'} noun={split==='payee' ? 'payees' : split==='account' ? 'accounts' : 'categories'} ariaLabel={title}/></>
}
function SankeyPanel({c,v,ctx}:{c:SpecComponent;v:MetricValue;ctx:PanelCtx}) {
 const m=metricOf(ctx,v.metric),cube=ctx.cube,split=(c.split ?? 'category') as Dimension,title=v.title ?? v.label,center=v.label ?? 'Total'
 let links:{from:string;to:string;value:number;text:string}[]
 if(cube && cubeCanCompute(m,split) && !balanceField(m)){
  links=whatMoved(cube,{range:ctx.range,pills:ctx.pills},null,{by:split,measure:m,limit:12}).filter(x => x.now).map(x => {const inward=(m.sign===-1)===(x.now>0),value=Math.abs(x.now),text=metricText(ctx,m,value);return inward ? {from:x.label,to:center,value,text} : {from:center,to:x.label,value,text}})
 } else links=(v.links ?? []).map(l => ({from:l.from,to:l.to,value:tryNumber(l.value),text:l.text}))
 const names=[...new Set(links.flatMap(l => [l.from,l.to]))],text=new Map(links.flatMap(l => [[l.from===center ? l.to : l.from,l.text]])),totalIn=links.filter(l => l.to===center).reduce((n,l) => n+l.value,0),totalOut=links.filter(l => l.from===center).reduce((n,l) => n+l.value,0)
 const unitIsCents=!!(cube && cubeCanCompute(m,split) && !balanceField(m)),fmt=(x:number) => unitIsCents ? money(x,cube!.base) : compactNumber(x)
 const nodes=names.map(name => ({key:name,label:name,color:name===center ? 'var(--ink)' : cube && cube.dims ? `var(${colorOf(cube,split,name)})` : undefined,text:name===center ? fmt(Math.max(totalIn,totalOut)) : text.get(name)}))
 return <><Head title={title} fixed={c.fixed?.reason} onDetails={() => metricDetails(c,v,ctx,v.text ?? '')}/>{links.length ? <Sankey nodes={nodes} links={links} formatValue={fmt} ariaLabel={title}/> : <p className="tm-empty" data-empty="1">Nothing flowed in this range.</p>}</>
}
function EntriesPanel({c,v,ctx}:{c:SpecComponent;v:MetricValue;ctx:PanelCtx}) {
 const rows:GridRow[]=(v.rows ?? []).map(r => ({id:r.entry+r.account,date:r.date,payee:r.payee,account:ctx.cube?.accounts.find(a => a.id===r.account)?.label ?? r.account,amount:{v:tryNumber(r.value),t:r.text}}))
 const title=v.title ?? v.label
 return <><Head title={title} fixed={c.fixed?.reason} note={typeof v.total==='number' && v.total>rows.length ? `Latest ${rows.length} of ${v.total.toLocaleString('en-US')}` : undefined}><Link className="tm-details-link" to="/ledger" search={drillSearch(v.ref ?? v.entries,c.fixed ? undefined : ctx.range)}>See all in Ledger</Link></Head><DataGrid label={title ?? 'Entries'} rows={rows} rowKey={(r,i) => `${String(r.id)}${i}`} columns={[{key:'date',label:'Date',type:'date'},{key:'payee',label:'Payee',type:'text'},{key:'account',label:'Category',type:'text'},{key:'amount',label:'Amount',type:'money'}]} emptyText="No entries in this range."/></>
}
const COLUMN:Record<string,ColumnType>={usd:'money',usd0:'money',eur:'money',eur0:'money',USD:'money',EUR:'money',int:'number',number:'number',pct:'pct',pct0:'pct',date:'date',days:'days',mo:'number'}
export function legacyColumns(columns:{key:string;label:string;format:string;align:string;type?:string;total?:boolean}[]):GridColumn[] {
 return columns.map(c => ({key:c.key,label:c.label,type:(c.type as ColumnType) || COLUMN[c.format] || (c.align==='right' ? 'number' : 'text'),total:c.total,format:c.format}))
}
const legacyTotal=(c:GridColumn,rows:GridRow[]) => {const values=rows.map(r => {const cell=r[c.key];return typeof cell==='object' && cell!==null && 'v' in cell ? cell.v : cell});return tableTotal(values,c.format ?? '') ?? null}
// Over 50 rows a table stops being something to read on a page; the panel shows the top and links to the rest.
function TablePanel({c,v,ctx,title}:{c:SpecComponent;v:Extract<ComponentValue,{type:'table'|'scenario_compare'}>;ctx:PanelCtx;title?:string}) {
 const columns=legacyColumns(v.columns),rows=v.rows as GridRow[],long=rows.length>50,shown=long ? rows.slice(0,8) : rows
 return <><Head title={title} note={long ? `First 8 of ${rows.length.toLocaleString('en-US')} rows` : undefined}>{long && ctx.tableLink?.(c.id)}</Head><DataGrid label={title ?? 'Table'} columns={columns} rows={shown} rowKey={(_,i) => String(i)} totalText={long ? undefined : legacyTotal} emptyText="No matching rows."/></>
}
export function FullTable({value,title}:{value:unknown;title:string}) {
 const parsed=componentValue.safeParse(value)
 if(!parsed.success || (parsed.data.type!=='table' && parsed.data.type!=='scenario_compare'))return <p className="tm-empty">This table is no longer on the page.</p>
 return <DataGrid label={title} columns={legacyColumns(parsed.data.columns)} rows={parsed.data.rows as GridRow[]} rowKey={(_,i) => String(i)} totalText={legacyTotal}/>
}
function LegacyPanel({c,value,ctx}:{c:SpecComponent;value:ComponentValue;ctx:PanelCtx}) {
 const title=value.title
 const details=() => {
  const content:DetailsContent={title:title ?? 'This panel',subtitle:'How this panel reads the ledger.',calculation:{summary:'Worked out by the expression the agent wrote for this panel.',formula:c.value ?? c.rows ?? c.metric},machine:c.columns?.flatMap(col => col.value ? [{label:col.label ?? 'Column',value:col.value}] : [])}
  openDetails({content,target:{dashboard:ctx.dashboard,component:c.id}})
 }
 switch(value.type){
  case 'kpi':{
   const d=value.delta,direction=d?.direction==='up' ? 'up' as const : d?.direction==='down' ? 'down' as const : 'flat' as const,good=value.tone==='ok' ? true : value.tone==='neg' ? false : null
   return <Stat label={title ?? ''} value={value.text} change={d ? {text:d.text,direction,good} : null} note={value.sub_text ?? value.sub} spark={value.sparkline ?? []} hideEmptyCompare={!d} onDetails={details}/>
  }
  case 'table':case 'scenario_compare':return <TablePanel c={c} v={value} ctx={ctx} title={title}/>
  case 'budget':{
   const peak=Math.max(1,...value.rows.flatMap(r => [r.actual,r.limit]))
   return <><Head title={title} onDetails={details}/><div className="tm-budget-bars">{value.rows.map(r => <div key={r.label} className="budget-row"><span className="n" title={r.label}>{r.label}</span><span className="budget-track" title={`${r.actual_text} of ${r.limit_text}`}><i className={r.status} style={{width:`${r.actual/peak*100}%`}}/><b style={{left:`${r.limit/peak*100}%`}}/></span><span className="t">{r.actual_text} / {r.limit_text}</span></div>)}</div></>
  }
  case 'text':return <><Head title={title}/><div className="note" dangerouslySetInnerHTML={{__html:value.html}}/></>
  case 'chart':return <LegacyChart value={value} drilldown tableId={c.id} syncKey={ctx.syncKey} head={<Head title={title} onDetails={details}/>}/>
 }
}
function PanelError({c,dashboard}:{c:SpecComponent;dashboard:string}) {
 return <div className="tm-panel-error" data-empty="1" role="status"><p>This panel could not be worked out.</p><button type="button" className="tm-details-link" onClick={() => openDetails({content:{title:c.title && !c.title.includes('{{') ? c.title : 'This panel',subtitle:'An agent can fix this. Flag it and it gets the context.'},target:{dashboard,component:c.id}})}>Flag this</button></div>
}
export function Panel({c,value,ctx,span}:{c:SpecComponent;value:unknown;ctx:PanelCtx;span:number}) {
 const body=():ReactNode => {
  if(isMetric(value)){
   switch(value.type){
    case 'stat':return <StatPanel c={c} v={value} ctx={ctx}/>
    case 'time_chart':return <ChartPanel c={c} v={value} ctx={ctx}/>
    case 'ranked_bars':return <RankedPanel c={c} v={value} ctx={ctx}/>
    case 'sankey':return <SankeyPanel c={c} v={value} ctx={ctx}/>
    default:return <EntriesPanel c={c} v={value} ctx={ctx}/>
   }
  }
  const parsed=componentValue.safeParse(value)
  return parsed.success ? <LegacyPanel c={c} value={parsed.data} ctx={ctx}/> : <PanelError c={c} dashboard={ctx.dashboard}/>
 }
 const flush=value && typeof value==='object' && ['stat','kpi'].includes((value as {type?:string}).type ?? '')
 return <section className={`panel${flush ? ' flush' : ''}`} data-panel={c.id} style={{gridColumn:`span ${Math.max(1,Math.min(12,span))}`}}>
  <CatchBoundary getResetKey={() => JSON.stringify([ctx.range,ctx.pills])} errorComponent={() => <PanelError c={c} dashboard={ctx.dashboard}/>}>{body()}</CatchBoundary>
 </section>
}
