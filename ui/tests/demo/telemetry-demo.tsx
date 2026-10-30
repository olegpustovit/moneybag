import { StrictMode, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from '@tanstack/react-router'
import '../../src/index.css'
import './demo.css'
import { syntheticCube } from '../fixtures/synthetic-cube'
import { annotationBands, balanceAt, balanceSeries, colorOf, compareSeries, decodeCube, dim, entityColors, localFacts, rateSeries, rowsIn, suggestValues, timeseries, totals, whatMoved, type Pill } from '../../src/features/telemetry/cube'
import { dayLabel, isoDay, rangeLabel } from '../../src/features/telemetry/time-grammar'
import { money, moneyExact, unitFormat } from '../../src/features/telemetry/format'
import { useBrowserSearch, useTimeState } from '../../src/features/telemetry/use-time-state'
import { decodePills, encodePills, pillsToCel } from '../../src/features/telemetry/filters'
import { TimeBar } from '../../src/features/telemetry/time-bar'
import { FilterBar } from '../../src/features/telemetry/filter-bar'
import { TimeChart, type ChartSeries } from '../../src/features/telemetry/time-chart'
import { Stat, StatSkeleton, changeBetween } from '../../src/features/telemetry/stat'
import { Answer, WhatMoved } from '../../src/features/telemetry/answer'
import { Donut, RankedBars, Sankey } from '../../src/features/telemetry/marks'
import { DataGrid, type GridColumn, type GridRow } from '../../src/features/telemetry/data-grid'
import { DetailsDrawer, type DetailsContent } from '../../src/features/telemetry/details-drawer'
import { CommandBar } from '../../src/features/telemetry/command-bar'
import LegacyChart from '../../src/features/telemetry/legacy-chart'
import { readCached, writeCached } from '../../src/features/telemetry/cube-store'
import type { ChartValue } from '../../src/features/dashboard/values'

const params=new URLSearchParams(location.search)
const wire=syntheticCube({density:Number(params.get('density') ?? 1)})
const cube=decodeCube(wire),bounds=cube.bounds,code=cube.base
const eur=unitFormat('eur',{minor:true,code}),full=balanceSeries(cube,{from:bounds.start,to:bounds.today},{maxPoints:500}),trips=annotationBands(cube)
const dictionary={category:dim(cube,'category').values,trip:dim(cube,'trip').values,account:dim(cube,'account').values,payee:dim(cube,'payee').values}
const ASKS=['Groceries over €50 last 3 months','Travel this year','Everything except rent, no estimates','Charges above €500 since march']
const m=(c:number) => money(c,code)
// Arguments make the React Compiler re-run this whenever the inputs change, instead of caching one reading forever.
const clock=(..._inputs:unknown[]) => performance.now()
declare global {interface Window {__demo:{readCached:typeof readCached;writeCached:typeof writeCached;wire:typeof wire;flags:unknown[];saved:number};__router?:unknown}}
window.__demo={readCached,writeCached,wire,flags:[],saved:0}
const legacy:Record<string,ChartValue>={
 line:{type:'chart',title:'Scenario runway',mark:'line',x:{field:'month',kind:'month'},y_format:'eur0',rule:{y:42000,label:'Safety floor'},series:[{name:'Base',color_index:0},{name:'Card trend',color_index:1},{name:'Cut travel',color_index:2}],points:Array.from({length:24},(_,i) => ['Base','Card trend','Cut travel'].map((s,k) => ({x:`20${25+Math.floor(i/12)}-${String(i%12+1).padStart(2,'0')}`,series:s,y:60000-i*(700+k*220)+k*900}))).flat()},
 bar:{type:'chart',title:'Rent by month',mark:'bar',x:{field:'month',kind:'month'},y_format:'eur0',series:[{name:'',color_index:0}],points:Array.from({length:12},(_,i) => ({x:`2026-${String(i+1).padStart(2,'0')}`,series:'',y:1250}))},
 stack:{type:'chart',title:'Seven series fold to Other',mark:'stack',x:{field:'month',kind:'month'},y_format:'eur0',series:Array.from({length:7},(_,k) => ({name:`Group ${k+1}`,color_index:k%5})),points:Array.from({length:6},(_,i) => Array.from({length:7},(_,k) => ({x:`2026-${String(i+1).padStart(2,'0')}`,series:`Group ${k+1}`,y:(k+2)*50+i*9}))).flat()},
 donut:{type:'chart',title:'Where cash sits',mark:'donut',x:{field:'pot',kind:'category'},y_format:'eur0',series:[{name:'',color_index:0}],points:[{x:'Everyday',series:'',y:6200},{x:'Savings',series:'',y:18000},{x:'Gold',series:'',y:9400},{x:'Home cash',series:'',y:1800}]},
 ranked:{type:'chart',title:'Top categories',mark:'bar',x:{field:'category',kind:'category'},y_format:'eur0',series:[{name:'',color_index:0}],points:[{x:'Rent',series:'',y:12400},{x:'Groceries',series:'',y:4300},{x:'Travel',series:'',y:6100},{x:'Internet',series:'',y:480},{x:'Mobile',series:'',y:32},{x:'Parking',series:'',y:28},{x:'Stamps',series:'',y:12}]},
 rate:{type:'chart',title:'Gold with intraday quotes',mark:'line',x:{field:'at',kind:'date'},y_format:'number',series:[{name:'',color_index:1}],points:[...Array.from({length:60},(_,i) => ({x:`2022-0${1+Math.floor(i/30)}-${String(i%28+1).padStart(2,'0')}T16:00:00Z`,series:'',y:78+Math.sin(i/6)*3})),...Array.from({length:12},(_,i) => ({x:`2022-03-12T${String(i+6).padStart(2,'0')}:00:00Z`,series:'',y:81+i*.05}))]},
 category:{type:'chart',title:'Runway by plan',mark:'line',x:{field:'plan',kind:'number'},y_format:'mo',series:[{name:'Runway',color_index:0}],points:Array.from({length:12},(_,i) => ({x:String(i),series:'Runway',y:40-i*2.5}))},
}
function Demo() {
 const [search,writeSearch]=useBrowserSearch()
 const time=useTimeState({bounds,search,onSearch:writeSearch,defaultText:'12mo'})
 const pills=decodePills(search.f),setPills=(next:Pill[]) => writeSearch({f:encodePills(next) || undefined},{push:true})
 const [hiddenSpend,setHiddenSpend]=useState<string[]>([]),[details,setDetails]=useState<DetailsContent|null>(null),[drawerOpen,setDrawerOpen]=useState(false),[message,setMessage]=useState('')
 const range=time.range,win=time.window,q={range,pills},t0=clock(range,pills,win)
 const t=totals(cube,q),prev=win?.available ? totals(cube,{range:win,pills}) : null
 const spend=timeseries(cube,q,{split:'category',estimates:'split'}),out=timeseries(cube,q,{measure:'spending',bucket:spend.bucket}),inn=timeseries(cube,q,{measure:'income',bucket:spend.bucket}),compare=compareSeries(cube,q,win,{bucket:spend.bucket})
 const nw=balanceSeries(cube,range),nwStart=balanceAt(cube,range.from) ?? 0,nwEnd=balanceAt(cube,range.to) ?? 0,nwCompare=win?.available ? balanceSeries(cube,win) : null
 const facts=localFacts(cube,q,win),moved=whatMoved(cube,q,win,{limit:12}),rows=rowsIn(cube,q)
 const usd=rateSeries(cube,'EUR/USD',range),ms=clock(rows,usd,moved,compare)-t0
 const compareName=win?.label ?? ''
 const saved=t.in-t.out,rate=t.in ? saved/t.in*100 : 0
 const onCursorPick=(i:number,xs:number[]) => {const from=xs[i];setMessage(`Picked ${dayLabel(from)}`)}
 const openDetails=(c:DetailsContent) => {setDetails(c);setDrawerOpen(true)}
 const statDetails=(title:string,value:string,formula:string):DetailsContent => ({title,value,subtitle:rangeLabel(range),calculation:{summary:'Added up from the entries in this range and filter. The exact figure comes from the server’s metric query.',formula,rates:[{label:'EUR/USD',value:'1.0843 on '+dayLabel(range.to)}],entries:<a href="#entries">See the entries</a>},history:[{at:'Yesterday',actor:'Codex',summary:'Rebuilt this page. Estimated history now labelled.'}],machine:[{label:'Metric ID',value:'metric:spending.v2'},{label:'Revision',value:String(cube.revision)},{label:'Hash',value:'9f3c1ab2e7d44b06'}]})
 const grid:GridRow[]=Array.from(rows,i => {const a=cube.accounts[cube.a[i]],inc=a.class==='income',cents=inc ? -cube.v[i] : cube.v[i];return {date:isoDay(cube.startMs+cube.d[i]*864e5),payee:cube.payees[cube.p[i]],category:a.category,source:a.estimated ? 'Estimated' : 'Statement',amount:{v:inc ? cents : -cents,t:`${inc ? '+' : ''}${moneyExact(cents,code)}`,tone:inc ? 'ok' : undefined},id:a.id,trip:cube.tagSets[cube.t[i]]?.[0]?.slice(5) ?? ''}})
 const columns:GridColumn[]=[{key:'date',label:'Date',type:'date'},{key:'payee',label:'Payee',type:'text'},{key:'category',label:'Category',type:'text'},{key:'source',label:'Source',type:'text'},{key:'amount',label:'Amount',type:'money'},{key:'id',label:'Account ID',machine:true}]
 const leaders=whatMoved(cube,q,win,{limit:30})
 const ranked=leaders.map(x => ({key:x.key,label:x.label,value:x.now,text:m(x.now),compare:win?.available ? x.before : undefined,compareText:win?.available ? m(x.before) : undefined,color:`var(${colorOf(cube,'category',x.key)})`,estimated:x.key==='Estimated'}))
 const payees=whatMoved(cube,q,win,{by:'payee',limit:40}).map(x => ({key:x.key,label:x.label,value:x.now,text:m(x.now),compare:win?.available ? x.before : undefined,compareText:win?.available ? m(x.before) : undefined}))
 const inBy=whatMoved(cube,q,null,{measure:'income',limit:6}).filter(x => x.now>0),outBy=whatMoved(cube,q,null,{limit:7}).filter(x => x.now>0)
 const flowNodes=[...inBy.map(x => ({key:`in:${x.key}`,label:x.label,color:'var(--c2)',text:m(x.now)})),{key:'pool',label:'Money in',color:'var(--ink)',text:m(t.in)},...outBy.map(x => ({key:`out:${x.key}`,label:x.label,color:`var(${colorOf(cube,'category',x.key)})`,text:m(x.now)})),...(saved>0 ? [{key:'saved',label:'Saved',color:'var(--ok)',text:m(saved)}] : [])]
 const flowLinks=[...inBy.map(x => ({from:`in:${x.key}`,to:'pool',value:x.now})),...outBy.map(x => ({from:'pool',to:`out:${x.key}`,value:x.now})),...(saved>0 ? [{from:'pool',to:'saved',value:saved}] : [])]
 const spendSeries:ChartSeries[]=spend.series.map(s => ({...s}))
 const pots=entityColors(cube,'category')
 const donut=outBy.map(x => ({key:x.key,label:x.label,value:x.now,text:m(x.now),color:`var(${colorOf(cube,'category',x.key)})`,estimated:x.key==='Estimated'}))
 const summary=`${t.entries.toLocaleString('en-US')} entries · ${m(t.out)} out · ${m(t.in)} in`
 const commandGroups=[{heading:'Pages',items:['Now','Spending','Income','Budget','Future','Ledger','Data','Changes'].map(p => ({id:p,label:p}))},{heading:'Accounts',items:cube.accounts.map(a => ({id:a.id,label:a.label,hint:a.category}))},{heading:'Payees',items:cube.payees.slice(0,60).map(p => ({id:p,label:p}))},{heading:'Saved views',items:[{id:'v1',label:'Trips since 2024'},{id:'v2',label:'Everyday spending, no estimates'}]}]
 return <main className="demo">
  <div className="demo-bar"><span>MONEYBAG · SPENDING &amp; NET WORTH</span><span>SYNTHETIC LEDGER · {cube.size.toLocaleString('en-US')} ROWS</span></div>
  <TimeBar state={time} bounds={bounds} navigator={{xs:full.xs,values:full.values,bands:trips,label:'Net worth across all history'}}/>
  <FilterBar pills={pills} onChange={setPills} suggest={(field,query) => suggestValues(cube,q,field,query)} formatTotal={m} countAmount={(op,amount) => totals(cube,{range,pills:[...pills,{field:'amount',op,values:[String(amount)]}]}).entries} asks={ASKS} ask={{dictionary,bounds,onRange:text => time.setText(text)}} summary={summary} onSaveView={() => {window.__demo.saved++;setMessage('View saved.')}}/>
  <div className="tm-stats">
   <Stat label="Net worth, end of range" value={m(nwEnd)} change={changeBetween(nwEnd,nwStart,'up',m)} comparison="start of range" spark={nw.values.filter((_,i) => i%Math.ceil(nw.values.length/60)===0)} sparkColor="var(--c0)" sparkLabel="Net worth trend" href="#entries" onDetails={() => openDetails(statDetails('Net worth',m(nwEnd),'sum(balances.assets) + sum(balances.liabilities)'))}/>
   <Stat label="Money in" value={m(t.in)} change={changeBetween(t.in,prev?.in,'up',m)} comparison={compareName} spark={inn.total} sparkColor="var(--c2)" href="#entries" onDetails={() => openDetails(statDetails('Money in',m(t.in),'row.class == "income" && row.date >= params.from && row.date <= params.to'))}/>
   <Stat label="Money out" value={m(t.out)} change={changeBetween(t.out,prev?.out,'down',m)} comparison={compareName} spark={out.total} sparkColor="var(--c4)" estimatedShare={t.out ? t.estimated/t.out : 0} href="#entries" onDetails={() => openDetails(statDetails('Money out',m(t.out),'row.class == "expense" && row.date >= params.from && row.date <= params.to'))}/>
   <Stat label="Saved" value={m(saved)} change={changeBetween(saved,prev ? prev.in-prev.out : null,'up',m)} comparison={compareName} note={`${rate.toFixed(0)}% of income`} spark={Array.from(inn.total,(v,i) => v-out.total[i])} sparkColor="var(--c3)" href="#entries"/>
  </div>
  <Answer facts={facts} hideMoved status={`recalculated in ${ms.toFixed(1)} ms · no server call`}/>
  <div className="demo-grid">
   <section className="demo-panel s7"><TimeChart title="Net worth" note="Not affected by filters. Hatched strip marks reconstructed history." mark="line" format={eur} xs={nw.xs} series={[{key:'nw',label:'Net worth',color:'--c0',values:nw.values}]} range={range} bounds={bounds} onRangeChange={(r,{final}) => time.setRange(r,{final})} onStepOut={time.zoomOut} syncKey="demo" compare={nwCompare ? {label:win!.mode==='previous_year' ? 'Same period last year' : 'Previous period',values:nw.xs.map((_,i) => nwCompare.values[i] ?? null)} : null} bands={annotationBands(cube,range)} bandLabel="Trips" estimatedUntil={cube.balances.estimatedUntil} baseline="auto" onPick={i => onCursorPick(i,nw.xs)}/></section>
   <section className="demo-panel s5"><WhatMoved facts={facts} onContributor={key => setPills([...pills.filter(p => p.field!=='category'),{field:'category',op:'is',values:[key]}])}/></section>
   <section className="demo-panel s7"><TimeChart title="Spending by category" note={`By ${spend.bucket}. Bucket size follows the range.`} mark="stack" format={eur} xs={spend.xs} bucket={spend.bucket} series={spendSeries} range={range} bounds={bounds} onRangeChange={(r,{final}) => time.setRange(r,{final})} onStepOut={time.zoomOut} syncKey="demo" hidden={hiddenSpend} onHiddenChange={setHiddenSpend} compare={compare ? {label:win!.mode==='previous_year' ? 'Same period last year' : 'Previous period',values:compare} : null} onPick={i => onCursorPick(i,spend.xs)}/></section>
   <section className="demo-panel s5"><TimeChart title="Money in and out" note="Internal transfers excluded" mark="bar" format={eur} xs={inn.xs} bucket={inn.bucket} series={[{key:'in',label:'Money in',color:'--c2',values:inn.total},{key:'out',label:'Money out',color:'--c4',values:out.total}]} range={range} bounds={bounds} onRangeChange={(r,{final}) => time.setRange(r,{final})} onStepOut={time.zoomOut} syncKey="demo"/></section>
   <section className="demo-panel s6"><h3 className="demo-h">Top categories</h3><RankedBars rows={ranked} noun="categories" onSelect={key => setPills([...pills.filter(p => p.field!=='category'),{field:'category',op:'is',values:[key]}])} ariaLabel="Spending by category, largest first"/></section>
   <section className="demo-panel s6"><h3 className="demo-h">Top payees</h3><RankedBars rows={payees} noun="payees" maxRows={8} collapseBelow={.04} onSelect={key => setPills([...pills.filter(p => p.field!=='payee'),{field:'payee',op:'contains',values:[key]}])} ariaLabel="Spending by payee, largest first"/></section>
   <section className="demo-panel s8"><h3 className="demo-h">Cash flow</h3><Sankey nodes={flowNodes} links={flowLinks} formatValue={m}/></section>
   <section className="demo-panel s4"><h3 className="demo-h">Where it went</h3><Donut slices={donut} center={m(t.out)} centerLabel="Money out" size={200} onSelect={key => setPills([{field:'category',op:'is',values:[key]}])}/><div className="tm-legend demo-legend">{donut.map(s => <span key={s.key} className="tm-legend-key"><i className={s.estimated ? 'tm-hatch' : undefined} style={{background:s.estimated ? undefined : s.color}}/>{s.label}</span>)}</div></section>
   <section className="demo-panel s6"><TimeChart title="EUR/USD" note="A rate leaves zero so its moves stay visible." mark="line" format={unitFormat('rate')} xs={usd.xs} series={[{key:'usd',label:'EUR/USD',color:'--c3',values:usd.values}]} range={range} bounds={bounds} onRangeChange={(r,{final}) => time.setRange(r,{final})} onStepOut={time.zoomOut} syncKey="demo" height={200}/></section>
   <section className="demo-panel s6"><TimeChart title="Biggest moves" note="Category axis" mark="bar" format={eur} categories={moved.slice(0,8).map(x => x.label)} series={[{key:'now',label:'This period',color:'--c0',values:moved.slice(0,8).map(x => x.now)},...(win?.available ? [{key:'then',label:compareName.replace(/^./,c => c.toUpperCase()),color:'--series-other',values:moved.slice(0,8).map(x => x.before)}] : [])]} height={200}/></section>
  </div>
  <section className="demo-section" data-testid="stat-pair"><div className="tm-stats"><StatSkeleton label="Loading stat"/><Stat label="Loaded stat" value="€1,234" change={changeBetween(90,100,'down',v => `€${v}`)} comparison="previous period" spark={[1,3,2,5]}/><Stat label="Rising cost" value="€1,234" change={changeBetween(110,100,'down',v => `€${v}`)} comparison="previous period" spark={[5,3,4,8]} sparkColor="var(--c4)" estimatedShare={.12}/><Stat label="No history" value="€0" comparison="previous period"/></div></section>
  <section className="demo-section"><h2 className="demo-h2">Legacy charts, same props as today</h2><div className="demo-grid flush">{Object.entries(legacy).map(([k,value]) => <div key={k} className="demo-panel s6"><h3 className="demo-h">{value.title}</h3><LegacyChart value={value} drilldown tableId={`legacy-${k}`}/></div>)}</div></section>
  <section className="demo-section"><h2 className="demo-h2">Grid with spark, bar and declared totals</h2><DataGrid label="Categories" rowKey={r => String((r.name as string))} columns={[{key:'name',label:'Category',type:'text'},{key:'spend',label:'Spend',type:'money',total:true},{key:'share',label:'Share',type:'pct',cell:'bar'},{key:'trend',label:'Trend',cell:'spark'},{key:'days',label:'Days covered',type:'days'},{key:'n',label:'Entries',type:'count',total:true}]} rows={leaders.slice(0,8).map(x => {const ts=timeseries(cube,{range,pills:[{field:'category',op:'is',values:[x.key]}]},{bucket:spend.bucket});return {name:x.label,spend:x.now,share:t.out ? x.now/t.out*100 : 0,trend:Array.from(ts.total),days:Math.round((range.to-range.from)/864e5)+1,n:totals(cube,{range,pills:[{field:'category',op:'is',values:[x.key]}]}).entries}})}/></section>
  <section className="demo-section" id="entries"><h2 className="demo-h2">The entries behind these numbers</h2><p className="demo-note">{rows.length.toLocaleString('en-US')} rows, virtualized against the page scroll. {pots.size} categories. CEL: <code>{pillsToCel(pills) || 'no filter'}</code></p><DataGrid label="Entries" rowKey={(r,i) => `${r.date}${r.payee}${r.id}${i}`} columns={columns} rows={grid} initialSort={{key:'date',desc:true}} onDetails={row => openDetails({title:String(row.payee),value:typeof row.amount==='object' && row.amount && 't' in row.amount ? row.amount.t : '',subtitle:`${String(row.date)} · ${String(row.category)}`,calculation:{summary:'One line of the ledger, in base currency at that day’s rate.',rates:[{label:'EUR/USD',value:'1.0843'}]},sourceRows:{columns:['Field','Value'],rows:[['Bank text',`${String(row.payee).toUpperCase()} 0042`],['Imported from','Statement, October']]},machine:[{label:'Account',value:String(row.id)},{label:'Entry',value:'01J9YH7Q3ZK8P2N6'}]})}/></section>
  <DetailsDrawer open={drawerOpen} onOpenChange={setDrawerOpen} content={details} target={{metric:details?.title,value:details?.value}} onFlag={async flag => {window.__demo.flags.push(flag)}}/>
  <CommandBar groups={commandGroups} onSelect={item => setMessage(`Jumped to ${item.label}`)}/>
  <div className="demo-toast" role="status">{message}</div>
 </main>
}
const root=createRootRoute({component:Demo}),router=createRouter({routeTree:root,history:createMemoryHistory()})
window.__router=router
createRoot(document.getElementById('root')!).render(<StrictMode><RouterProvider router={router as never}/></StrictMode>)
