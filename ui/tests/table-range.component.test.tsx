import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { useState } from 'react'
import { DataTable } from '../src/features/dashboard/data-table'
import { DateRange, presetRange } from '../src/features/dashboard/date-range'

HTMLElement.prototype.scrollTo = () => {}

afterEach(() => { cleanup(); localStorage.clear() })

describe('dashboard controls', () => {
 it('sorts by multiple columns and exposes controlled sort state', () => {
  const value = { type:'table' as const, columns:[{key:'name',label:'Name',format:'text',align:'left'},{key:'amount',label:'Amount',format:'number',align:'right'}], rows:[
   {name:{v:'A',t:'A'},amount:{v:2,t:'2'}}, {name:{v:'B',t:'B'},amount:{v:1,t:'1'}}, {name:{v:'A',t:'A'},amount:{v:1,t:'1'}},
  ] }
  function Example() { const [sort,setSort]=useState<string>(); return <><output>{sort}</output><DataTable value={value} storageKey="test" sort={sort} onSortChange={setSort}/></> }
  render(<Example/>)
  fireEvent.click(screen.getByRole('button',{name:'Name'}))
  fireEvent.click(screen.getByRole('button',{name:'Amount'}),{shiftKey:true})
  expect(screen.getByRole('status').textContent).toBe('name,amount')
  const body=screen.getByRole('table').querySelector('tbody')!
  const rows=within(body).getAllByRole('row')
  expect(rows.map(row => row.textContent)).toEqual(['A1','A2','B1'])
 })
 it('steps leap-month ranges and preserves the selected month', () => {
  expect(presetRange('3',new Date(2024,2,31,12))).toEqual({from:'2023-12-01',to:'2024-02-29'})
  function Example() { const [values,setValues]=useState({from:'2024-02-01',to:'2024-02-29',month:'2024-02'}); return <><DateRange range month values={values} defaults={{}} onChange={value => setValues(old => ({...old,...value}))}/><output>{`${values.from}/${values.to}/${values.month}`}</output></> }
  render(<Example/>)
  fireEvent.click(screen.getByRole('button',{name:'Next date range'}))
  fireEvent.click(screen.getByRole('button',{name:'Next dashboard month'}))
  expect(screen.getByRole('status').textContent).toBe('2024-03-01/2024-03-31/2024-03')
 })
})

it('applies a date preset through the range dialog', async () => {
 const changes:unknown[]=[]
 render(<DateRange range month={false} values={{}} defaults={{}} onChange={value => changes.push(value)}/> )
 fireEvent.click(screen.getByRole('button',{name:'Dashboard default'}))
 fireEvent.click(await screen.findByRole('button',{name:'Last 3 months'},{timeout:10000}))
 expect(changes).toEqual([{...presetRange('3'),all:false}])
})
