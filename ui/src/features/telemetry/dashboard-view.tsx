import { useState, type ReactNode } from 'react'
import type { Cube, Pill } from './cube'
import { dim, localFacts, suggestValues, totals, whatMoved, type Fact } from './cube'
import { addPill, decodePills, encodePills, FIELDS, type FieldDef } from './filters'
import { DAY, canonicalText, dayLabel, defaultBounds, isoDay, type Range } from './time-grammar'
import { currencySymbol, money } from './format'
import { useTimeState, type TelemetrySearch } from './use-time-state'
import type { DashboardSearch } from './search'
import { TimeBar, defaultPresets } from './time-bar'
import { useCurrencyCube } from './currency'
import { FilterBar } from './filter-bar'
import { Answer } from './answer'
import { FullTable, Panel, type PanelCtx } from './panels'
import { promptSaveView } from './shell-store'
import type { SpecComponent, ViewBody, ViewPage } from './view-data'
import './telemetry.css'

export type Sync={fetching:boolean;failed:boolean;retry:() => void;at:number}
const LABELS:Record<string,string>={category:'Category',payee:'Payee',account:'Account',trip:'Trip',type:'Type',pot:'Pot',source:'Source',class:'Class'}
function fieldsFor(dimensions:string[]):FieldDef[] {
 const known=FIELDS.filter(f => dimensions.includes(f.key) || f.kind==='amount' || f.kind==='flag')
 const extra=dimensions.filter(d => !FIELDS.some(f => f.key===d) && LABELS[d]).map(d => ({key:d as FieldDef['key'],label:LABELS[d],ops:['is','is not'] as FieldDef['ops'],path:`row.${d}`,kind:'value' as const}))
 return [...known,...extra]
}
type ServerFact={kind:string;sentence:string;value?:string;text?:string;direction?:string;good?:boolean;contributors?:{name:string;delta:string;text:string;share:number}[]|null}
export function serverFacts(answer:unknown):Fact[]|null {
 const a=answer as {facts?:ServerFact[]|null;sentence?:string}|undefined
 if(!a || typeof a!=='object' || !a.facts?.length)return a?.sentence ? [{kind:'period_change',sentence:a.sentence}] : null
 return a.facts.map(f => ({kind:f.kind,sentence:f.sentence,text:f.text,direction:f.direction==='up' ? 'up' : f.direction==='down' ? 'down' : 'flat',good:f.direction==='flat' || !f.direction ? null : f.good,contributors:f.contributors?.map(c => ({name:c.name,key:c.name,delta:Number(c.delta) || 0,text:c.text,share:c.share}))}))
}
// Everything between the page heading and the footer, shared by the app route and the headless render route.
export function DashboardBody({view,cube:fullCube,search,onSearch,dashboardId,headerExtra,render,sync,prefetch}:{view:ViewBody;cube:Cube|null;search:DashboardSearch;onSearch:(next:Partial<DashboardSearch>,options:{push:boolean}) => void;dashboardId:string;headerExtra?:ReactNode;render?:boolean;sync?:Sync;prefetch?:(range:Range) => void}) {
 const {cube,currency,switchable}=useCurrencyCube(fullCube),spec=view.spec,scope=spec.scope,bounds=cube?.bounds ?? defaultBounds(),hasClock=!!scope,allFixed=!!spec.components?.length && spec.components.every(c => c.fixed),defaultCompare=scope?.compare ?? 'previous_period'
 const time=useTimeState({bounds,search,onSearch,defaultText:scope?.time?.default ?? '12mo',defaultCompare})
 const [optimisticPills,setOptimisticPills]=useState<Pill[]|null>(null),[seenF,setSeenF]=useState(search.f)
 if(seenF!==search.f){setSeenF(search.f);setOptimisticPills(null)}
 const pills=optimisticPills ?? decodePills(search.f)
 const setPills=(next:Pill[]) => {setOptimisticPills(next);onSearch({f:encodePills(next) || undefined},{push:true})}
 const dims=spec.explore?.dimensions ?? [],fields=fieldsFor(dims),pages=view.pages?.length ? view.pages : spec.pages ?? [],page:ViewPage|undefined=pages.find(p => p.id===search.page) ?? pages[0]
 const components=new Map<string,SpecComponent>((spec.components ?? []).map(c => [c.id,c])),range=time.range,win=time.window
 const q={range,pills},metric=spec.answer ? cube?.metrics.find(m => m.id===spec.answer!.metric) : undefined
 const fresh=!search.t,exactAnswer=fresh && currency==='EUR' && !pills.length && time.compare===defaultCompare && (!search.t || isoDay(range.from)===firstFrom(view) && isoDay(range.to)===firstTo(view))
 const server=serverFacts(view.values?.answer),facts:Fact[]|null=exactAnswer && server ? server : cube && (spec.answer || dims.length) ? localFacts(cube,q,win,metric) : server
 const ctx:PanelCtx={cube,range,win,compareMode:time.compare,defaultCompare,pills,filtering:pills.length>0,dashboard:dashboardId,syncKey:`dash-${dashboardId}`,bounds,explore:dims,readOnly:render,fresh,syncing:sync?.fetching,setRange:(r,{final}) => time.setRange(r,{final}),stepOut:time.zoomOut,addPill:p => setPills(addPill(pills,p)),
  tableLink:id => <button type="button" className="tm-details-link" onClick={() => onSearch({table:id},{push:true})}>See all rows</button>}
 const nav=cube ? {xs:netDays(cube).xs,values:netDays(cube).values,bands:cube.annotations.map(a => ({from:Date.parse(a.from),to:Date.parse(a.to),label:a.label})),label:'Net worth across all history'} : undefined
 const t=cube ? totals(cube,q) : null
 const title=spec.question || view.head.title
 const movedTop=cube && !render ? whatMoved(cube,{range},null,{limit:1})[0]?.label : undefined
 const asks=movedTop ? [`${movedTop} this year`,`Everything except ${movedTop}, no estimates`,`Charges above ${currencySymbol(cube!.base)}500 last 3 months`] : undefined
 const suggest=(field:Parameters<typeof suggestValues>[2],query:string) => cube ? suggestValues(cube,q,field,query) : []
 const tableComponent=search.table ? components.get(search.table) : undefined
 if(tableComponent){
  const value=view.values?.[tableComponent.id] as {title?:string}|undefined,name=value?.title ?? 'All rows'
  return <><header className="page-header"><div><p className="breadcrumb"><button type="button" className="tm-crumb" onClick={() => onSearch({table:undefined},{push:true})}>← {title}</button></p><h1>{name}</h1></div></header><FullTable value={value} title={name}/></>
 }
 return <>
  <header className="page-header tm-dash-head">
   <div><h1>{title}</h1>{spec.question && view.head.title!==spec.question && <p className="description">{view.head.title}</p>}</div>
   <div className="tm-dash-tools">
    {render ? (view.stale ? <span className="tm-asof">Refreshing prices…</span> : null) : <span className="tm-asof" role="status" title={view.as_of ? `Numbers from ${new Date(view.as_of).toLocaleString()}` : undefined}>{sync?.failed ? <>Couldn’t sync · <button type="button" className="tm-plain" onClick={sync.retry}>Retry</button></> : sync?.fetching ? 'Syncing…' : view.stale ? 'Refreshing prices…' : sync?.at ? `Up to date · ${new Date(sync.at).toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'})}` : view.as_of ? `As of ${asOfLabel(view.as_of)}` : ''}</span>}
    {headerExtra}
   </div>
  </header>
  <div className="tm-sheet">
   {sync?.fetching && <i className="tm-sync-line" aria-hidden="true"/>}
   {hasClock || (!render && !allFixed) ? <TimeBar state={time} bounds={bounds} presets={defaultPresets(bounds,scope?.time?.presets)} navigator={nav} currency={switchable ? currency : undefined} prefetch={prefetch}/> : null}
   {!hasClock && !render && !allFixed && <p className="tm-clock-note">The agent who built this page hasn’t connected its panels to the clock yet, so some may ignore the range.</p>}
   {dims.length>0 && !render && <FilterBar pills={pills} onChange={setPills} fields={fields} suggest={suggest} formatTotal={c => money(c,cube?.base)} countAmount={(op,amount) => cube ? totals(cube,{range,pills:[...pills,{field:'amount',op,values:[String(amount)]}]}).entries : 0} asks={cube ? asks : undefined} ask={cube ? {dictionary:{category:dim(cube,'category').values,trip:dim(cube,'trip').values,account:dim(cube,'account').values,payee:dim(cube,'payee').values},bounds,onRange:text => time.setText(text)} : undefined} summary={t ? `${t.entries.toLocaleString('en-US')} entries · ${money(t.out,cube?.base)} out · ${money(t.in,cube?.base)} in` : undefined} onSaveView={() => promptSaveView(location.pathname,cleanState(search,canonicalText(range),pills),title)}/>}
   {facts && <Answer facts={facts} reserve={dims.includes('category')} onContributor={!render && dims.includes('category') ? key => setPills(addPill(pills.filter(p => p.field!=='category'),{field:'category',op:'is',values:[key]})) : undefined}/>}
  </div>
  {pages.length>1 && <nav className="page-tabs" aria-label="Pages in this dashboard">{pages.map(p => <button key={p.id} type="button" className={p.id===page?.id ? 'active' : ''} aria-current={p.id===page?.id ? 'page' : undefined} onClick={() => onSearch({page:p.id},{push:true})}>{p.title}</button>)}</nav>}
  {page ? <div className="dashboard-grid">{page.layout.flatMap((row,ri) => (row.cols ?? []).map((col,ci) => {const c=components.get(col.component) ?? {id:col.component,type:'unknown'};return <Panel key={`${ri}-${ci}-${col.component}`} c={c} value={view.values?.[col.component]} ctx={ctx} span={col.span}/>}))}</div> : <div className="empty"><h2>Nothing on this page yet</h2><p>Ask an agent to add panels to it.</p></div>}
 </>
}
const netDays=(c:Cube) => {const b=c.balances,xs=Array.from(b.d,d => c.startMs+d*DAY);return {xs,values:b.net}}
const firstValue=(view:ViewBody,key:'from'|'to') => {for(const v of Object.values(view.values ?? {})){const got=(v as Record<string,unknown>|null)?.[key];if(typeof got==='string' && !(v as {fixed?:unknown}).fixed)return got}return undefined}
const firstFrom=(view:ViewBody) => firstValue(view,'from'),firstTo=(view:ViewBody) => firstValue(view,'to')
function asOfLabel(iso:string):string {const d=new Date(iso),now=new Date();if(Number.isNaN(d.getTime()))return '';const time=d.toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'});return d.toDateString()===now.toDateString() ? time : dayLabel(Math.floor(d.getTime()/DAY)*DAY)}
const cleanState=(search:TelemetrySearch,t:string,pills:Pill[]):Record<string,string> => {const out:Record<string,string>={t};if(search.cmp)out.cmp=search.cmp;const f=encodePills(pills);if(f)out.f=f;return out}
