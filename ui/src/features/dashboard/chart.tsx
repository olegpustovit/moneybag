import { lazy, Suspense, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { Area, Bar, BarChart, Brush, CartesianGrid, ComposedChart, Line, Rectangle, ReferenceLine, Tooltip, XAxis, YAxis, usePlotArea, useXAxisScale, useYAxisScale, type BarShapeProps, type TooltipContentProps } from 'recharts'
import { ChartContainer } from '../../components/ui/chart'
import { Button } from '../../components/ui/button'
import { chartNumber, chartRows, foldChart, journalFilter, previousValue, type ChartRow } from './chart-data'
import { DataTable } from './data-table'
import type { ChartValue } from './values'

const chartColor=(index:number) => index>=5 ? 'var(--other)' : `var(--c${index})`
function BarMark(props:BarShapeProps) {
 return <g><rect className="chart-mark-hit" x={props.x} y={props.y-4} width={props.width} height={Math.max(8,props.height+8)} fill="transparent"/><Rectangle {...props}/></g>
}
const DashboardPie = lazy(() => import('./pie-chart'))
function ChartTip({active,payload,label,rows,format,stack}:{active:boolean;payload:TooltipContentProps<number,string>['payload'];label?:string|number;rows:ChartRow[];format:string;stack:boolean}) {
 if(!active || !payload.length)return null
 const row=payload[0].payload,index=rows.indexOf(row),total=payload.reduce((sum,item) => sum+(typeof item.value==='number' ? item.value : 0),0)
 return <div className="chart-tooltip"><strong>{label ?? row?.x}</strong>{payload.map((item,i) => {const key=String(item.dataKey),current=item.value,previous=previousValue(rows,index,key),text=row?.[`t${key.slice(1)}`];return <div key={`${key}-${i}`}><span>{item.name || 'Value'}</span><span>{typeof current==='number' ? typeof text==='string' ? text : chartNumber(current,format) : '—'}{typeof current==='number' && previous!==undefined && <small> · Δ {chartNumber(current-previous,format)}</small>}</span></div>})}{stack && payload.length>1 && <div><strong>Total</strong><strong>{chartNumber(total,format)}</strong></div>}</div>
}
function DirectLabels({rows,series,hidden,start,end,stack=false,bars=false}:{rows:ChartRow[];series:ChartValue['series'];hidden:string[];start:number;end:number;stack?:boolean;bars?:boolean}) {
 const xScale=useXAxisScale(),yScale=useYAxisScale(),plot=usePlotArea()
 if(!xScale || !yScale || !plot)return null
 const labels=series.flatMap((item,index) => {if(hidden.includes(item.name))return [];const row=rows.slice(start,end+1).findLast(row => typeof row[`s${index}`]==='number');if(!row)return [];const value=Number(row[`s${index}`]),base=stack ? series.slice(0,index).reduce((sum,previous,i) => sum+(hidden.includes(previous.name) ? 0 : Number(row[`s${i}`] ?? 0)),0) : 0;let x=xScale(row.x);const y=yScale(stack ? base+value/2 : value);if(x!==undefined && bars){const band=plot.width/(end-start+1),count=stack ? 1 : series.filter(s => !hidden.includes(s.name)).length,offset=band*0.1,gap=band-2*offset-(count-1)*2<=0 ? 0 : 2,raw=(band-2*offset-(count-1)*gap)/count,size=raw>1 ? Math.round(raw) : raw,order=stack ? 0 : series.slice(0,index).filter(s => !hidden.includes(s.name)).length;x+=offset+(size+gap)*order+size}return x===undefined || y===undefined ? [] : [{...item,index,x,y,placed:y}]}).sort((a,b) => a.y-b.y)
 const top=plot.y+9,bottom=plot.y+plot.height-9,gap=14
 for(let i=0;i<labels.length;i++)labels[i].placed=Math.max(top,labels[i].y,i ? labels[i-1].placed+gap : top)
 for(let i=labels.length-1;i>=0;i--)labels[i].placed=Math.min(labels[i].placed,i===labels.length-1 ? bottom : labels[i+1].placed-gap)
 return <g pointerEvents="none">{labels.map(label => <g key={label.index}><line x1={label.x} y1={label.y} x2={plot.x+plot.width+3} y2={label.placed} stroke={chartColor(label.color_index)} strokeWidth={1}/><text className="chart-direct-label" x={plot.x+plot.width+6} y={label.placed} fill={chartColor(label.color_index)} dominantBaseline="middle"><title>{label.name || 'Value'}</title>{label.name.length>13 ? `${label.name.slice(0,12)}…` : label.name || 'Value'}</text></g>)}</g>
}
export default function DashboardChart({ value:source, drilldown = false, tableId = source.title || 'chart', sort, onSortChange }: { value: ChartValue; drilldown?: boolean; tableId?:string; sort?:string; onSortChange?:(sort:string|undefined)=>void }) {
  const value=foldChart(source)
  const navigate=useNavigate()
  const filter=(x:string) => drilldown ? journalFilter(value.x,x) : undefined
  const openJournal=(x:string) => {const search=filter(x);if(search)void navigate({to:'/journal',search})}
  const [table, setTable] = useState(false)
  const [hidden, setHidden] = useState<string[]>([])
  const [brushRange,setBrushRange]=useState<{startIndex:number;endIndex:number}>()
  const toggle = (name: string) => setHidden((current) => current.includes(name) ? current.filter((item) => item !== name) : [...current, name])
  const series = value.series.length ? value.series : [{ name: '', color_index: 0 }]
  const rows=chartRows(value.points,series)
  const color = chartColor
  const axes = <><CartesianGrid vertical={false} stroke="var(--bd)"/><XAxis dataKey="x" tickLine={false} axisLine={false} minTickGap={20}/><YAxis tickLine={false} axisLine={false} width={55}/><Tooltip cursor={{stroke:'var(--text3)',strokeDasharray:'3 3'}} shared={value.mark==='bar' ? false : undefined} content={props => <ChartTip {...props} rows={rows} format={value.y_format} stack={value.mark==='stack'}/>} />{value.rule && <ReferenceLine y={value.rule.y} stroke="var(--text2)" strokeDasharray="4 4" label={value.rule.label}/>}</>
  const timeSeries=(value.x.kind==='date' || value.x.kind==='month' || value.x.kind==='number' && value.x.field==='month') && rows.length>24
  const visibleSeries=series.filter(item => !hidden.includes(item.name)).length
  const direct=visibleSeries>0 && visibleSeries<=4
  const lines = series.map((item, index) => !hidden.includes(item.name) && (value.mark === 'area' ? <Area isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} stroke={color(item.color_index)} fill={color(item.color_index)} fillOpacity={0.12} strokeWidth={2}/> : <Line isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} stroke={color(item.color_index)} strokeWidth={2} dot={false}/>))
  const categories=[...new Set(value.points.map(point => point.x))]
  return <div className="dashboard-chart">
    {value!==source && <p className="chart-fold-note">{value.points.some(point => (value.mark==='donut' ? point.x : point.series)==='Other') ? `Other is the sum of the remaining ${value.mark==='donut' ? 'categories' : 'series'}.` : `Showing the five largest ${value.mark==='donut' ? 'categories' : 'series'}.`}</p>}
    <div className="chart-controls"><Button variant="ghost" size="xs" aria-pressed={table} onClick={() => setTable(!table)}>{table ? 'Chart' : 'Table'}</Button></div>
    {table ? <DataTable storageKey={tableId} sort={sort} onSortChange={onSortChange} regionLabel={`${value.title || 'Chart'} data`} value={{type:'table',title:value.title,columns:[{key:'category',label:'Category',format:'text',align:'left'},{key:'series',label:'Series',format:'text',align:'left'},{key:'value',label:'Value',format:value.y_format || 'number',align:'right'}],rows:value.points.filter(point => !hidden.includes(value.mark==='donut' ? point.x : point.series)).map(point => ({category:{v:point.x,t:point.x},series:{v:point.series || 'Value',t:point.series || 'Value'},value:{v:point.y,t:point.y_text ?? chartNumber(point.y,value.y_format)}}))}} renderCell={(row,column) => {const text=row[column]?.t ?? '—',search=column==='category' ? filter(text) : undefined;return search ? <Link to="/journal" search={search}>{text}</Link> : text}}/> : value.points.length === 0 ? <p className="muted">No data for this chart.</p> : <div className="chart-scroll"><ChartContainer config={{}} className="chart-canvas">
      {value.mark === 'donut' ? <Suspense fallback={<div>Loading chart…</div>}><DashboardPie value={value} folded={value!==source} hidden={hidden} onDrilldown={drilldown ? openJournal : undefined} /></Suspense> : value.mark === 'bar' || value.mark === 'stack' ? <BarChart data={rows} barGap={2} barCategoryGap="10%" margin={{top:20,right:direct ? 95 : 5}}>{axes}{series.map((item, index) => !hidden.includes(item.name) && <Bar shape={BarMark} isAnimationActive={false} key={index} dataKey={`s${index}`} name={item.name || 'Value'} fill={color(item.color_index)} stackId={value.mark === 'stack' ? 'stack' : undefined} label={value.mark==='bar' && rows.length*series.length<=4 ? {position:'top',formatter:(amount) => typeof amount==='number' ? chartNumber(amount,value.y_format) : ''} : false} onClick={data => {if(typeof data.payload?.x==='string')openJournal(data.payload.x)}} />)}{direct && <DirectLabels rows={rows} series={series} hidden={hidden} bars stack={value.mark==='stack'} start={timeSeries ? Math.min(brushRange?.startIndex ?? 0,rows.length-1) : 0} end={timeSeries ? Math.min(brushRange?.endIndex ?? rows.length-1,rows.length-1) : rows.length-1}/>}{timeSeries && <Brush dataKey="x" height={20} startIndex={Math.min(brushRange?.startIndex ?? 0,rows.length-1)} endIndex={Math.min(brushRange?.endIndex ?? rows.length-1,rows.length-1)} onChange={setBrushRange}/>}</BarChart> : <ComposedChart data={rows} margin={{right:direct ? 95 : 5}} onClick={(state,event) => {if(state.isTooltipActive && state.activeLabel!==undefined && event.target instanceof Element && event.target.closest('.recharts-line-curve, .recharts-area-area, .recharts-active-dot'))openJournal(String(state.activeLabel))}}>{axes}{lines}{direct && <DirectLabels rows={rows} series={series} hidden={hidden} start={timeSeries ? Math.min(brushRange?.startIndex ?? 0,rows.length-1) : 0} end={timeSeries ? Math.min(brushRange?.endIndex ?? rows.length-1,rows.length-1) : rows.length-1}/>} {timeSeries && <Brush dataKey="x" height={20} startIndex={Math.min(brushRange?.startIndex ?? 0,rows.length-1)} endIndex={Math.min(brushRange?.endIndex ?? rows.length-1,rows.length-1)} onChange={setBrushRange}/>}</ComposedChart>}
    </ChartContainer></div>}
    {!table && value.mark==='donut' && categories.length>1 && <div className="chart-legend" aria-label="Chart categories">{categories.map(name => <button key={name} type="button" aria-pressed={!hidden.includes(name)} onClick={() => toggle(name)}><i style={{background:value!==source && name==='Other' ? 'var(--other)' : color(categories.indexOf(name))}}/>{name}</button>)}</div>}
    {!table && value.mark !== 'donut' && series.length > 1 && <div className="chart-legend" aria-label="Chart series">{series.map((item) => <button key={item.name} type="button" aria-pressed={!hidden.includes(item.name)} onClick={() => toggle(item.name)}><i style={{ background: color(item.color_index) }} />{item.name || 'Value'}</button>)}</div>}
  </div>
}
