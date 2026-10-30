import { Suspense, lazy } from 'react'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import type { ComponentValue } from './values'
import { DataTable } from './data-table'
const DashboardChart = lazy(() => import('./chart'))

function Sparkline({values,labels}:{values:number[];labels?:string[]}) {
  if (!values.length) return null
  const scale=Math.max(1,...values.map(Math.abs)), normalized=values.map(value => value/scale), min=Math.min(...normalized), max=Math.max(...normalized)
  const points=normalized.map((value,index) => `${(2+96*index/Math.max(1,values.length-1)).toFixed(2)},${(max>min ? 30-28*(value-min)/(max-min) : 16).toFixed(2)}`).join(' ')
  return <svg className="kpi-sparkline" width="100" height="32" viewBox="0 0 100 32" role="img" aria-label={`Trend: ${(labels?.length===values.length ? labels : values).join(', ')}`}>{values.length===1 ? <circle cx="2" cy="16" r="2" fill="currentColor"/> : <polyline fill="none" stroke="currentColor" strokeWidth="2" points={points}/>}</svg>
}

export function Component({ value, drilldown = false, tableId = value.title || 'dashboard', sort, onSortChange }: { value: ComponentValue; drilldown?: boolean; tableId?: string; sort?:string; onSortChange?:(sort:string|undefined)=>void }) {
  switch (value.type) {
    case 'kpi': { const sub=value.sub_text ?? value.sub,delta=value.delta;return <div className="kpi"><div className="kpi-heading"><div className={`kpi-value ${value.tone}`}>{value.text}</div>{delta && <span role="img" className="kpi-delta" aria-label={delta.direction==='neutral' ? delta.text : `${delta.direction==='unchanged' ? 'No change' : delta.direction} ${delta.text}`}>{delta.direction==='up' ? <ArrowUp aria-hidden="true"/> : delta.direction==='down' ? <ArrowDown aria-hidden="true"/> : null}<span aria-hidden="true">{delta.text}</span></span>}</div>{sub && <p>{sub}</p>}{value.sparkline && <Sparkline values={value.sparkline} labels={value.sparkline_text}/>}</div> }
    case 'text': return <div className="note" dangerouslySetInnerHTML={{ __html: value.html }} />
    case 'table': case 'scenario_compare': return value.rows.length === 0 ? <p className="muted">No matching rows.</p> : <DataTable key={tableId} value={value} storageKey={tableId} sort={sort} onSortChange={onSortChange}/>
    case 'budget': { const max = Math.max(1, ...value.rows.flatMap((row) => [row.actual, row.limit])); return <div className="budget-bars">{value.rows.map((row, index) => { const details=[`${row.label}: ${row.actual_text} actual, ${row.limit_text} limit, ${row.status}`,row.average_text && `${row.average_label ?? '6-month average'}: ${row.average_text}`,row.accounts?.length && `Accounts: ${row.accounts.join(', ')}`].filter(Boolean).join(' · '),bar=<><i className={row.status} style={{ width: `${row.actual / max * 100}%` }}/><b style={{ left: `${row.limit / max * 100}%` }}/></>;return <div className="budget-row" key={index}><span>{row.label}</span>{drilldown && row.accounts?.length && row.accounts.length<=1000 && row.accounts.every(id => /^(assets|liabilities|income|expenses|equity):[a-z0-9_:-]{1,150}$/.test(id)) ? <Link className="budget-track" to="/journal" search={{accounts:row.accounts}} aria-label={`${details} · Open journal`} title={details}>{bar}</Link> : <span className="budget-track" role="img" tabIndex={0} aria-label={details} title={details}>{bar}</span>}<span className="numeric">{row.actual_text} / {row.limit_text}</span></div> })}</div> }
    case 'chart': return <Suspense fallback={<div className="dashboard-pending" aria-busy="true">Loading chart…</div>}><DashboardChart value={value} drilldown={drilldown} tableId={tableId} sort={sort} onSortChange={onSortChange} /></Suspense>
  }
}
