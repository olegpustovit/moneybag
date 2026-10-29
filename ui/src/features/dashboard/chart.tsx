import { lazy, Suspense, useState } from 'react'
import { Area, Bar, Brush, CartesianGrid, ComposedChart, Line, ReferenceLine, Tooltip, XAxis, YAxis } from 'recharts'
import { ChartContainer } from '../../components/ui/chart'
import { Button } from '../../components/ui/button'
import type { ChartValue } from './values'

const DashboardPie = lazy(() => import('./pie-chart'))

export default function DashboardChart({ value }: { value: ChartValue }) {
  const [table, setTable] = useState(false)
  const [hidden, setHidden] = useState<string[]>([])
  const toggle = (name: string) => setHidden((current) => current.includes(name) ? current.filter((item) => item !== name) : [...current, name])
  const series = value.series.length ? value.series : [{ name: '', color_index: 0 }]
  const rows: Record<string, string | number>[] = []
  for (const point of value.points) {
    const index = Math.max(0, series.findIndex((item) => item.name === point.series))
    const key = `s${index}`
    let row = rows.findLast((item) => item.x === point.x && item[key] === undefined)
    if (!row) { row = { x: point.x }; rows.push(row) }
    row[key] = point.y
  }
  const color = (index: number) => `var(--c${index % 5})`
  const axes = <><CartesianGrid vertical={false} stroke="var(--bd)"/><XAxis dataKey="x" tickLine={false} axisLine={false} minTickGap={20}/><YAxis tickLine={false} axisLine={false} width={55}/><Tooltip />{value.rule && <ReferenceLine y={value.rule.y} stroke="var(--text2)" strokeDasharray="4 4" label={value.rule.label}/>}</>
  const lines = series.map((item, index) => !hidden.includes(item.name) && (value.mark === 'area' ? <Area isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} stroke={color(item.color_index)} fill={color(item.color_index)} fillOpacity={0.12} strokeWidth={2} /> : <Line isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} stroke={color(item.color_index)} strokeWidth={2} dot={false} />))
  return <div className="dashboard-chart">
    <div className="chart-controls"><Button variant="ghost" size="xs" aria-pressed={table} onClick={() => setTable(!table)}>{table ? 'Chart' : 'Table'}</Button></div>
    {table ? <div className="table-scroll" tabIndex={0} role="region" aria-label={`${value.title || 'Chart'} data`}><table><thead><tr><th>Category</th><th>Series</th><th className="numeric">Value</th></tr></thead><tbody>{value.points.map((point, index) => <tr key={index}><td>{point.x}</td><td>{point.series || 'Value'}</td><td className="numeric">{point.y}</td></tr>)}</tbody></table></div> : value.points.length === 0 ? <p className="muted">No data for this chart.</p> : <div className="chart-scroll"><ChartContainer config={{}} className="chart-canvas">
      {value.mark === 'donut' ? <Suspense fallback={<div>Loading chart…</div>}><DashboardPie value={value} /></Suspense> : value.mark === 'bar' || value.mark === 'stack' ? <ComposedChart data={rows} barGap={2}>{axes}{series.map((item, index) => !hidden.includes(item.name) && <Bar isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} fill={color(item.color_index)} stackId={value.mark === 'stack' ? 'stack' : undefined} />)}{value.points.length > 24 && <Brush dataKey="x" height={20}/>}</ComposedChart> : value.mark === 'area' ? <ComposedChart data={rows}>{axes}{lines}{value.points.length > 24 && <Brush dataKey="x" height={20}/>}</ComposedChart> : <ComposedChart data={rows}>{axes}{lines}{value.points.length > 24 && <Brush dataKey="x" height={20}/>}</ComposedChart>}
    </ChartContainer></div>}
    {!table && value.mark !== 'donut' && series.length > 1 && <div className="chart-legend" aria-label="Chart series">{series.map((item) => <button key={item.name} type="button" aria-pressed={!hidden.includes(item.name)} onClick={() => toggle(item.name)}><i style={{ background: color(item.color_index) }} />{item.name || 'Value'}</button>)}</div>}
  </div>
}
