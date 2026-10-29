import { Cell, Pie, PieChart, Tooltip } from 'recharts'
import type { ChartValue } from './values'

export default function DashboardPie({ value }: { value: ChartValue }) {
  return <PieChart><Tooltip/><Pie isAnimationActive={false} data={value.points} nameKey="x" dataKey="y" innerRadius="55%" outerRadius="85%">{value.points.map((_, index) => <Cell key={index} fill={`var(--c${index % 5})`} />)}</Pie></PieChart>
}
