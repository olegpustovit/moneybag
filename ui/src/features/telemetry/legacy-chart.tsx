import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { chartNumber, chartRows, foldChart, journalFilter } from '../dashboard/chart-data'
import { DataTable } from '../dashboard/data-table'
import type { ChartValue } from '../dashboard/values'
import { legacyFormat } from './format'
import { TimeChart, type ChartSeries } from './time-chart'
import { Donut, RankedBars } from './marks'
import type { Bucket } from './time-grammar'
import './telemetry.css'

const tokenFor=(index:number) => index>=5 ? '--series-other' : `--c${index}`
const toTime=(x:string,kind:string):number|null => {
 if(kind==='month' && /^\d{4}-(0[1-9]|1[0-2])/.test(x))return Date.UTC(+x.slice(0,4),+x.slice(5,7)-1,1)
 if(kind==='date' && /^\d{4}-\d{2}-\d{2}/.test(x)){const t=Date.parse(x.length===10 ? `${x}T00:00:00Z` : x);return Number.isNaN(t) ? null : t}
 return null
}
// Drop-in for ../dashboard/chart: same props, same fold rules, same table and drilldown, no Recharts.
export default function LegacyChart({value:source,drilldown=false,tableId=source.title || 'chart',sort,onSortChange}:{value:ChartValue;drilldown?:boolean;tableId?:string;sort?:string;onSortChange?:(sort:string|undefined) => void}) {
 const value=foldChart(source),navigate=useNavigate(),[hidden,setHidden]=useState<string[]>([]),[donutTable,setDonutTable]=useState(false)
 const filter=(x:string) => drilldown ? journalFilter(value.x,x) : undefined
 const openJournal=(x:string) => {const search=filter(x);if(search)void navigate({to:'/journal',search})}
 const series=value.series.length ? value.series : [{name:'',color_index:0}]
 const format=legacyFormat(value.y_format)
 const donut=value.mark==='donut'
 const table=(off:string[]) => <DataTable storageKey={tableId} sort={sort} onSortChange={onSortChange} regionLabel={`${value.title || 'Chart'} data`} value={{type:'table',title:value.title,columns:[{key:'category',label:'Category',format:'text',align:'left'},{key:'series',label:'Series',format:'text',align:'left'},{key:'value',label:'Value',format:value.y_format || 'number',align:'right'}],rows:value.points.filter(point => !off.includes(donut ? point.x : point.series)).map(point => ({category:{v:point.x,t:point.x},series:{v:point.series || 'Value',t:point.series || 'Value'},value:{v:point.y,t:point.y_text ?? chartNumber(point.y,value.y_format)}}))}} renderCell={(row,column) => {const text=row[column]?.t ?? '—',search=column==='category' ? filter(text) : undefined;return search ? <Link to="/journal" search={search}>{text}</Link> : text}}/>
 const foldNote=value!==source && <p className="chart-fold-note">{value.points.some(point => (donut ? point.x : point.series)==='Other') ? `Other is the sum of the remaining ${donut ? 'categories' : 'series'}.` : `Showing the five largest ${donut ? 'categories' : 'series'}.`}</p>
 if(!value.points.length)return <div className="dashboard-chart">{foldNote}<div className="chart-controls"><button type="button" className="tm-btn small" aria-pressed={donutTable} onClick={() => setDonutTable(!donutTable)}>{donutTable ? 'Chart' : 'Table'}</button></div>{donutTable ? table(hidden) : <p className="muted">No data for this chart.</p>}</div>
 if(donut){
  const categories=[...new Set(value.points.map(point => point.x))],color=(name:string) => value!==source && name==='Other' ? 'var(--series-other)' : `var(--c${categories.indexOf(name)%5})`
  const slices=value.points.filter(point => !hidden.includes(point.x)).map(point => ({key:point.x,label:point.x,value:point.y,text:point.y_text ?? chartNumber(point.y,value.y_format),color:color(point.x)}))
  return <div className="dashboard-chart">{foldNote}<div className="chart-controls"><button type="button" className="tm-btn small" aria-pressed={donutTable} onClick={() => setDonutTable(!donutTable)}>{donutTable ? 'Chart' : 'Table'}</button></div>
   {donutTable ? table(hidden) : <><Donut slices={slices} onSelect={drilldown ? openJournal : undefined} ariaLabel={value.title}/>{categories.length>1 && <div className="chart-legend tm-legend" aria-label="Chart categories">{categories.map(name => <button key={name} type="button" aria-pressed={!hidden.includes(name)} onClick={() => setHidden(hidden.includes(name) ? hidden.filter(x => x!==name) : [...hidden,name])}><i style={{background:color(name)}}/>{name}</button>)}</div>}</>}
  </div>
 }
 const rows=chartRows(value.points,series),times=rows.map(row => toTime(String(row.x),value.x.kind)),isTime=times.every(t => t!==null) && rows.length>0
 // A single categorical series reads better sorted with direct labels than as columns.
 if(!isTime && value.mark==='bar' && series.length===1 && !value.rule && value.x.kind==='category')return <div className="dashboard-chart">{foldNote}<div className="chart-controls"><button type="button" className="tm-btn small" aria-pressed={donutTable} onClick={() => setDonutTable(!donutTable)}>{donutTable ? 'Chart' : 'Table'}</button></div>
  {donutTable ? table(hidden) : <RankedBars ariaLabel={value.title} rows={value.points.map((point,i) => ({key:`${point.x}#${i}`,label:point.x,value:point.y,text:point.y_text ?? chartNumber(point.y,value.y_format),color:`var(${tokenFor(series[0].color_index)})`}))} onSelect={drilldown ? key => openJournal(key.slice(0,key.lastIndexOf('#'))) : undefined} selectLabel="See entries for"/>}
 </div>
 const additive=value.mark==='bar' || value.mark==='stack'
 let order=rows.map((_,i) => i)
 if(isTime)order=order.sort((a,b) => (times[a] as number)-(times[b] as number))
 const xs:number[]=[],labelsX:string[]=[],cols:ChartSeries[]=series.map(item => ({key:item.name,label:item.name || 'Value',color:tokenFor(item.color_index),values:[] as (number|null)[],text:[] as (string|undefined)[]}))
 for(const at of order){
  const row=rows[at],t=times[at],same=isTime && xs.length>0 && xs[xs.length-1]===t
  if(!same){xs.push(isTime ? t as number : xs.length);labelsX.push(String(row.x))}
  cols.forEach((col,index) => {
   const v=row[`s${index}`],y=typeof v==='number' ? v : null,text=row[`t${index}`],arr=col.values as (number|null)[],texts=col.text!
   // A repeated x on a time axis cannot draw twice: additive marks sum it, lines keep the latest.
   if(same){const last=arr.length-1;if(y!==null){arr[last]=additive && arr[last]!==null ? (arr[last] as number)+y : y;texts[last]=additive ? undefined : typeof text==='string' ? text : undefined}}
   else {arr.push(y);texts.push(typeof text==='string' ? text : undefined)}
  })
 }
 const bucket:Bucket|undefined=isTime && value.x.kind==='month' ? 'month' : undefined
 const baseline=additive || /^(usd0?|eur0?|pct0?|int|mo)$/.test(value.y_format) ? 'zero' as const : 'auto' as const
 const visible=cols.filter(c => !hidden.includes(c.key)).length
 return <div className="dashboard-chart">{foldNote}
  <TimeChart title={value.title || 'Chart'} heading={<span/>} mark={value.mark==='donut' ? 'line' : value.mark} format={format} series={cols} xs={isTime ? xs : undefined} categories={isTime ? undefined : labelsX} bucket={bucket} baseline={baseline} height={240} rule={value.rule} direct={visible>0 && visible<=4 && cols.length>1} legend="toggle" hidden={hidden} onHiddenChange={setHidden} syncKey="dashboard" onPick={drilldown ? index => openJournal(labelsX[index]) : undefined} renderTable={table}/>
 </div>
}
