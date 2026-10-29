import { Cell, Pie, PieChart, Sector, Tooltip, type PieSectorShapeProps } from 'recharts'
import { chartNumber } from './chart-data'
import type { ChartValue } from './values'

function PieMark(props:PieSectorShapeProps) {return <Sector {...props} stroke="transparent" strokeWidth={12} paintOrder="stroke fill"/>}
export default function DashboardPie({ value,hidden,onDrilldown }: { value: ChartValue;hidden:string[];onDrilldown?:(x:string)=>void }) {
  const colors=new Map([...new Set(value.points.map(point => point.x))].map((name,index) => [name,index]))
  const points=value.points.map((point,index) => ({...point,index,color_index:colors.get(point.x) ?? 0})).filter(point => !hidden.includes(point.x))
  return <PieChart><Tooltip formatter={amount => chartNumber(Number(amount),value.y_format)}/><Pie isAnimationActive={false} data={points} nameKey="x" dataKey="y" innerRadius="55%" outerRadius="85%" shape={PieMark} label={points.length<=4 ? ({name,x,y,textAnchor}) => <text className="chart-direct-label" x={x} y={y} textAnchor={textAnchor} fill="var(--text2)"><title>{name}</title>{typeof name==='string' && name.length>13 ? `${name.slice(0,12)}…` : name}</text> : false} labelLine={false} onClick={(_,index) => onDrilldown?.(points[index].x)}>{points.map((point) => <Cell key={point.index} fill={`var(--c${point.color_index % 5})`} />)}</Pie></PieChart>
}
