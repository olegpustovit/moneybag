import { Suspense, lazy } from 'react'
import type { ComponentValue } from './values'
import { DataTable } from './data-table'
const DashboardChart = lazy(() => import('./chart'))

export function Component({ value, drilldown = false, tableId = value.title || 'dashboard' }: { value: ComponentValue; drilldown?: boolean; tableId?: string }) {
  switch (value.type) {
    case 'kpi': return <div className="kpi"><div className={`kpi-value ${value.tone}`}>{value.text}</div>{value.sub && <p>{value.sub}</p>}</div>
    case 'text': return <div className="note" dangerouslySetInnerHTML={{ __html: value.html }} />
    case 'table': case 'scenario_compare': return value.rows.length === 0 ? <p className="muted">No matching rows.</p> : <DataTable key={tableId} value={value} storageKey={tableId}/>
    case 'budget': { const max = Math.max(1, ...value.rows.flatMap((row) => [row.actual, row.limit])); return <div className="budget-bars">{value.rows.map((row, index) => { return <div className="budget-row" key={index}><span>{row.label}</span><span className="budget-track"><i className={row.status} style={{ width: `${row.actual / max * 100}%` }}/><b style={{ left: `${row.limit / max * 100}%` }}/></span><span className="numeric">{row.actual_text} / {row.limit_text}</span></div> })}</div> }
    case 'chart': return <Suspense fallback={<div className="dashboard-pending" aria-busy="true">Loading chart…</div>}><DashboardChart value={value} drilldown={drilldown} /></Suspense>
  }
}
