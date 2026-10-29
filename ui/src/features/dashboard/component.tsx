import { Suspense, lazy } from 'react'
import type { ComponentValue } from './values'
const DashboardChart = lazy(() => import('./chart'))

export function Component({ value }: { value: ComponentValue }) {
  switch (value.type) {
    case 'kpi': return <div className="kpi"><div className={`kpi-value ${value.tone}`}>{value.text}</div>{value.sub && <p>{value.sub}</p>}</div>
    case 'text': return <div className="note" dangerouslySetInnerHTML={{ __html: value.html }} />
    case 'table': case 'scenario_compare': return value.rows.length === 0 ? <p className="muted">No matching rows.</p> : <div className="table-scroll" tabIndex={0} role="region" aria-label={`${value.title || 'Dashboard'} table`}><table><thead><tr>{value.columns.map((column) => <th key={column.key} className={column.align === 'right' ? 'numeric' : ''}>{column.label}</th>)}</tr></thead><tbody>{value.rows.map((row, index) => <tr key={index}>{value.columns.map((column) => <td key={column.key} className={`${column.align === 'right' ? 'numeric ' : ''}${row[column.key]?.tone || ''}`}>{row[column.key]?.t ?? '—'}</td>)}</tr>)}</tbody></table></div>
    case 'budget': { const max = Math.max(1, ...value.rows.flatMap((row) => [row.actual, row.limit])); return <div className="budget-bars">{value.rows.map((row, index) => { return <div className="budget-row" key={index}><span>{row.label}</span><span className="budget-track"><i className={row.status} style={{ width: `${row.actual / max * 100}%` }}/><b style={{ left: `${row.limit / max * 100}%` }}/></span><span className="numeric">{row.actual_text} / {row.limit_text}</span></div> })}</div> }
    case 'chart': return <Suspense fallback={<div className="dashboard-pending" aria-busy="true">Loading chart…</div>}><DashboardChart value={value} /></Suspense>
  }
}
