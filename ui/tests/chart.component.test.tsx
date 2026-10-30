import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import DashboardChart from '../src/features/dashboard/chart'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => () => {}, Link: ({children}:{children:ReactNode}) => <span>{children}</span> }))
vi.mock('../src/components/ui/chart', () => ({ ChartContainer: ({children}:{children:ReactNode}) => <div>{children}</div> }))
vi.mock('recharts', () => {
 const Box=({children}:{children?:ReactNode}) => <div>{children}</div>
 return {Area:Box,Bar:Box,BarChart:Box,Brush:Box,CartesianGrid:Box,ComposedChart:Box,Line:Box,Rectangle:Box,ReferenceLine:Box,Tooltip:Box,XAxis:Box,YAxis:Box,usePlotArea:() => undefined,useXAxisScale:() => undefined,useYAxisScale:() => undefined}
})
afterEach(() => { cleanup(); localStorage.clear() })

it('shows the server-formatted chart value in its data table', () => {
 const value={type:'chart' as const,title:'Rounding',mark:'line' as const,x:{field:'date',kind:'date'},y_format:'int',series:[{name:'',color_index:0}],points:[{x:'2024-01-01',series:'',y:2.5,y_text:'Server rounded value'}]}
 render(<DashboardChart value={value}/>)
 fireEvent.click(screen.getByRole('button',{name:'Table'}))
 expect(within(screen.getByRole('table')).getByText('Server rounded value',{exact:true})).toBeTruthy()
})

it('keeps series colors and filters chart-table rows when toggled', () => {
 const value={type:'chart' as const,title:'Test chart',mark:'line' as const,x:{field:'date',kind:'date'},y_format:'number',series:[{name:'Alpha',color_index:0},{name:'Beta',color_index:1}],points:[{x:'2024-01-01',series:'Alpha',y:1},{x:'2024-01-01',series:'Beta',y:2}]}
 render(<DashboardChart value={value}/>)
 const alpha=screen.getByRole('button',{name:'Alpha'}), beta=screen.getByRole('button',{name:'Beta'})
 const alphaColor=alpha.querySelector('i')?.getAttribute('style'),betaColor=beta.querySelector('i')?.getAttribute('style')
 expect(alphaColor).toContain('--c0')
 expect(betaColor).toContain('--c1')
 fireEvent.click(alpha)
 expect(alpha.getAttribute('aria-pressed')).toBe('false')
 expect(beta.querySelector('i')?.getAttribute('style')).toBe(betaColor)
 fireEvent.click(screen.getByRole('button',{name:'Table'}))
 const table=screen.getByRole('table')
 expect(within(table).queryByText('Alpha')).toBeNull()
 expect(within(table).getByText('Beta')).toBeTruthy()
 fireEvent.click(screen.getByRole('button',{name:'Chart'}))
 fireEvent.click(screen.getByRole('button',{name:'Alpha'}))
 expect(screen.getByRole('button',{name:'Alpha'}).querySelector('i')?.getAttribute('style')).toBe(alphaColor)
 expect(screen.getByRole('button',{name:'Beta'}).querySelector('i')?.getAttribute('style')).toBe(betaColor)
})
