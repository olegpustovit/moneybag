import { Cell, Pie, PieChart, Tooltip } from 'recharts'
import { chartNumber } from './chart-data'
import type { ChartValue } from './values'

export default function DashboardPie({ value,hidden }: { value: ChartValue;hidden:string[] }) {
  const colors=new Map([...new Set(value.points.map(point => point.x))].map((name,index) => [name,index]))
  const points=value.points.map((point,index) => ({...point,index,color_index:colors.get(point.x) ?? 0})).filter(point => !hidden.includes(point.x))
  return <PieChart><Tooltip formatter={amount => chartNumber(Number(amount),value.y_format)}/><Pie isAnimationActive={false} data={points} nameKey="x" dataKey="y" innerRadius="55%" outerRadius="85%">{points.map((point) => <Cell key={point.index} fill={`var(--c${point.color_index % 5})`} />)}</Pie></PieChart>
}
