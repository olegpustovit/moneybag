import { lazy, Suspense, useState } from 'react'
import { Area, Bar, BarChart, Brush, CartesianGrid, ComposedChart, Line, ReferenceLine, Tooltip, XAxis, YAxis, type TooltipContentProps } from 'recharts'
import { ChartContainer } from '../../components/ui/chart'
import { Button } from '../../components/ui/button'
import { chartNumber, chartRows, previousValue, type ChartRow } from './chart-data'
import type { ChartValue } from './values'

const DashboardPie = lazy(() => import('./pie-chart'))
function ChartTip({active,payload,label,rows,format,stack}:{active:boolean;payload:TooltipContentProps<number,string>['payload'];label?:string|number;rows:ChartRow[];format:string;stack:boolean}) {
 if(!active || !payload.length)return null
 const row=payload[0].payload,index=rows.indexOf(row),total=payload.reduce((sum,item) => sum+(typeof item.value==='number' ? item.value : 0),0)
 return <div className="chart-tooltip"><strong>{label ?? row?.x}</strong>{payload.map((item,i) => {const key=String(item.dataKey),current=item.value,previous=previousValue(rows,index,key);return <div key={`${key}-${i}`}><span>{item.name || 'Value'}</span><span>{typeof current==='number' ? chartNumber(current,format) : '—'}{typeof current==='number' && previous!==undefined && <small> · Δ {chartNumber(current-previous,format)}</small>}</span></div>})}{stack && payload.length>1 && <div><strong>Total</strong><strong>{chartNumber(total,format)}</strong></div>}</div>
}
export default function DashboardChart({ value }: { value: ChartValue }) {
  const [table, setTable] = useState(false)
  const [hidden, setHidden] = useState<string[]>([])
  const toggle = (name: string) => setHidden((current) => current.includes(name) ? current.filter((item) => item !== name) : [...current, name])
  const series = value.series.length ? value.series : [{ name: '', color_index: 0 }]
  const rows=chartRows(value.points,series)
  const color = (index: number) => `var(--c${index % 5})`
  const timeSeries=(value.x.kind==='date' || value.x.kind==='month' || value.x.kind==='number' && value.x.field==='month') && rows.length>24
  const axes = <><CartesianGrid vertical={false} stroke="var(--bd)"/><XAxis dataKey="x" tickLine={false} axisLine={false} minTickGap={20}/><YAxis tickLine={false} axisLine={false} width={55}/><Tooltip cursor={{stroke:'var(--text3)',strokeDasharray:'3 3'}} shared={value.mark==='bar' ? false : undefined} content={props => <ChartTip {...props} rows={rows} format={value.y_format} stack={value.mark==='stack'}/>} />{value.rule && <ReferenceLine y={value.rule.y} stroke="var(--text2)" strokeDasharray="4 4" label={value.rule.label}/>}</>
  const lines = series.map((item, index) => !hidden.includes(item.name) && (value.mark === 'area' ? <Area isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} stroke={color(item.color_index)} fill={color(item.color_index)} fillOpacity={0.12} strokeWidth={2} /> : <Line isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} stroke={color(item.color_index)} strokeWidth={2} dot={false} />))
  const categories=[...new Set(value.points.map(point => point.x))]
  return <div className="dashboard-chart">
    <div className="chart-controls"><Button variant="ghost" size="xs" aria-pressed={table} onClick={() => setTable(!table)}>{table ? 'Chart' : 'Table'}</Button></div>
    {table ? <div className="table-scroll" tabIndex={0} role="region" aria-label={`${value.title || 'Chart'} data`}><table><thead><tr><th>Category</th><th>Series</th><th className="numeric">Value</th></tr></thead><tbody>{value.points.map((point, index) => <tr key={index}><td>{point.x}</td><td>{point.series || 'Value'}</td><td className="numeric">{point.y}</td></tr>)}</tbody></table></div> : value.points.length === 0 ? <p className="muted">No data for this chart.</p> : <div className="chart-scroll"><ChartContainer config={{}} className="chart-canvas">
      {value.mark === 'donut' ? <Suspense fallback={<div>Loading chart…</div>}><DashboardPie value={value} hidden={hidden} /></Suspense> : value.mark === 'bar' || value.mark === 'stack' ? <BarChart data={rows} barGap={2}>{axes}{series.map((item, index) => !hidden.includes(item.name) && <Bar isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} fill={color(item.color_index)} stackId={value.mark === 'stack' ? 'stack' : undefined} />)}{timeSeries && <Brush dataKey="x" height={20}/>}</BarChart> : <ComposedChart data={rows}>{axes}{lines}{timeSeries && <Brush dataKey="x" height={20}/>}</ComposedChart>}
    </ChartContainer></div>}
    {!table && value.mark==='donut' && categories.length>1 && <div className="chart-legend" aria-label="Chart categories">{categories.map(name => <button key={name} type="button" aria-pressed={!hidden.includes(name)} onClick={() => toggle(name)}><i style={{background:color(categories.indexOf(name))}}/>{name}</button>)}</div>}
    {!table && value.mark !== 'donut' && series.length > 1 && <div className="chart-legend" aria-label="Chart series">{series.map((item) => <button key={item.name} type="button" aria-pressed={!hidden.includes(item.name)} onClick={() => toggle(item.name)}><i style={{ background: color(item.color_index) }} />{item.name || 'Value'}</button>)}</div>}
  </div>
}
