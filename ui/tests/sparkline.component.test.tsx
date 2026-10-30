import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { Component } from '../src/features/dashboard/component'
import { componentValue } from '../src/features/dashboard/values'

vi.mock('@tanstack/react-router', () => ({ Link: () => null }))
afterEach(cleanup)

it('renders an accessible sparkline using the formatted values', () => {
 const value=componentValue.parse({type:'kpi',value:2,text:'€2',tone:'',sub:'',sparkline:[1,3,2],sparkline_text:['€1','€3','€2']})
 render(<Component value={value}/>)
 const chart=screen.getByRole('img',{name:'Trend: €1, €3, €2'})
 expect(chart.querySelector('polyline')?.getAttribute('points')).toBe('2.00,30.00 50.00,2.00 98.00,16.00')
})

it('shows a single point and omits an empty trend', () => {
 const base={type:'kpi' as const,value:0,text:'0',tone:'',sub:''}
 const view=render(<Component value={{...base,sparkline:[0]}}/>)
 expect(screen.getByRole('img',{name:'Trend: 0'}).querySelector('circle')).toBeTruthy()
 view.rerender(<Component value={{...base,sparkline:[]}}/>)
 expect(screen.queryByRole('img')).toBeNull()
})
