import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { DashboardGetOutput } from '../../lib/tools'
import { Skeleton } from '../../components/ui/skeleton'

type Spec = DashboardGetOutput['spec']
type Page = NonNullable<Spec['pages']>[number]
type Item = NonNullable<Spec['components']>[number]

export function useDashboardSkeleton(pending:boolean):boolean {
 const [visible,setVisible]=useState(false),started=useRef(0)
 useLayoutEffect(() => {if(visible)started.current=performance.now()},[visible])
 useEffect(() => {
  if(pending && !visible) {const timer=setTimeout(() => setVisible(true),150);return () => clearTimeout(timer)}
  if(!pending && visible) {const timer=setTimeout(() => setVisible(false),Math.max(0,300-(performance.now()-started.current)));return () => clearTimeout(timer)}
 },[pending,visible])
 return visible
}

export function DashboardSkeleton({page,components}:{page:Page|undefined;components:Map<string,Item>}) {
 return <div className="dashboard-grid dashboard-skeleton" aria-hidden="true">{(page?.layout ?? []).flatMap((row,ri) => (row.cols ?? []).map((placement,ci) => {
  const component=components.get(placement.component),type=component?.type,columns=component?.columns ?? []
  return <section className="panel" key={`${ri}-${ci}`} style={{gridColumn:`span ${Math.max(1,Math.min(12,placement.span))}`}}><h2>{component?.title || placement.component}</h2>{type==='kpi' ? <div className="skeleton-kpi"><Skeleton className="skeleton-bar"/><Skeleton className="skeleton-bar"/></div> : type==='chart' ? <svg className="skeleton-chart" viewBox="0 0 240 120" preserveAspectRatio="none"><path d="M18 4V105H238"/><path className="skeleton-chart-line" d="M18 91L60 82L100 87L140 52L180 61L238 25"/></svg> : type==='table' || type==='scenario_compare' ? <div className="skeleton-table"><table><thead><tr>{(columns.length ? columns : [{label:'Value'}]).map((column,index) => <th key={index}>{column.label || 'Value'}</th>)}</tr></thead><tbody>{Array.from({length:8},(_,rowIndex) => <tr key={rowIndex}>{(columns.length ? columns : [{label:'Value'}]).map((column,index) => <td key={index}><Skeleton className={`skeleton-bar ${'format' in column && /^(eur|usd|int|number|pct)/.test(column.format || '') ? 'numeric' : ''}`}/></td>)}</tr>)}</tbody></table></div> : <div className="skeleton-lines">{Array.from({length:type==='budget_bars' ? 4 : 2},(_,index) => <Skeleton className="skeleton-bar" key={index}/>)}</div>}</section>
 }))}</div>
}
