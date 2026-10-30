import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { useState, type ReactNode } from 'react'
import LegacyChart from '../src/features/telemetry/legacy-chart'
import { TimeChart } from '../src/features/telemetry/time-chart'
import { TimeBar, defaultPresets } from '../src/features/telemetry/time-bar'
import { FilterBar } from '../src/features/telemetry/filter-bar'
import { Stat, StatSkeleton, changeBetween } from '../src/features/telemetry/stat'
import { Answer } from '../src/features/telemetry/answer'
import { DataGrid, columnTotal, gridText, sortRows, type GridRow } from '../src/features/telemetry/data-grid'
import { Donut, RankedBars, Sankey, layoutSankey } from '../src/features/telemetry/marks'
import { DetailsDrawer } from '../src/features/telemetry/details-drawer'
import { CommandBar } from '../src/features/telemetry/command-bar'
import { useTimeState, type TelemetrySearch } from '../src/features/telemetry/use-time-state'
import { unitFormat } from '../src/features/telemetry/format'
import { dayFromIso as d } from '../src/features/telemetry/time-grammar'
import type { Pill } from '../src/features/telemetry/cube'

const navigate=vi.fn()
vi.mock('@tanstack/react-router',() => ({useNavigate:() => navigate,Link:({children}:{children:ReactNode}) => <span>{children}</span>}))
// jsdom has no canvas: the plot itself is covered by e2e/telemetry-demo-verify.mjs, everything around it is covered here.
vi.mock('../src/features/telemetry/time-chart-canvas',() => ({default:({series,title,bands}:{series:{label:string}[];title:string;bands?:unknown[]}) => <div data-testid="canvas" data-title={title} data-bands={bands?.length ?? 0}>{series.map(s => s.label).join('|')}</div>}))
HTMLElement.prototype.scrollTo=() => {}
HTMLElement.prototype.scrollIntoView=() => {}
window.scrollTo=(() => {}) as typeof window.scrollTo
globalThis.ResizeObserver=class {observe() {}unobserve() {}disconnect() {}} as unknown as typeof ResizeObserver
afterEach(() => {cleanup();localStorage.clear();navigate.mockReset()})

describe('LegacyChart keeps what tests/chart.component.test.tsx pins down',() => {
 it('shows the server-formatted chart value in its data table',() => {
  render(<LegacyChart value={{type:'chart',title:'Rounding',mark:'line',x:{field:'date',kind:'date'},y_format:'int',series:[{name:'',color_index:0}],points:[{x:'2024-01-01',series:'',y:2.5,y_text:'Server rounded value'}]}}/>)
  fireEvent.click(screen.getByRole('button',{name:'Table'}))
  expect(within(screen.getByRole('table')).getByText('Server rounded value',{exact:true})).toBeTruthy()
 })
 it('keeps series colors and filters chart-table rows when toggled',() => {
  render(<LegacyChart value={{type:'chart',title:'Test chart',mark:'line',x:{field:'date',kind:'date'},y_format:'number',series:[{name:'Alpha',color_index:0},{name:'Beta',color_index:1}],points:[{x:'2024-01-01',series:'Alpha',y:1},{x:'2024-01-01',series:'Beta',y:2}]}}/>)
  const alpha=screen.getByRole('button',{name:'Alpha'}),beta=screen.getByRole('button',{name:'Beta'}),alphaColor=alpha.querySelector('i')?.getAttribute('style'),betaColor=beta.querySelector('i')?.getAttribute('style')
  expect(alphaColor).toContain('--c0');expect(betaColor).toContain('--c1')
  fireEvent.click(alpha)
  expect(alpha.getAttribute('aria-pressed')).toBe('false');expect(beta.querySelector('i')?.getAttribute('style')).toBe(betaColor)
  fireEvent.click(screen.getByRole('button',{name:'Table'}))
  const table=screen.getByRole('table');expect(within(table).queryByText('Alpha')).toBeNull();expect(within(table).getByText('Beta')).toBeTruthy()
  fireEvent.click(screen.getByRole('button',{name:'Chart'}));fireEvent.click(screen.getByRole('button',{name:'Alpha'}))
  expect(screen.getByRole('button',{name:'Alpha'}).querySelector('i')?.getAttribute('style')).toBe(alphaColor)
 })
 const seven=Array.from({length:7},(_,i) => ({name:`s${i}`,color_index:i%5}))
 const points=seven.flatMap((s,i) => [{x:'2024-01',series:s.name,y:i+1},{x:'2024-02',series:s.name,y:(i+1)*10}])
 it('folds additive marks into Other and says so',async () => {
  render(<LegacyChart value={{type:'chart',title:'Stack',mark:'stack',x:{field:'month',kind:'month'},y_format:'int',series:seven,points}}/>)
  expect(screen.getByText('Other is the sum of the remaining series.')).toBeTruthy()
  expect((await screen.findByTestId('canvas')).textContent).toBe('s6|s5|s4|s3|s2|Other')
  expect(screen.getByRole('button',{name:'Other'}).querySelector('i')?.getAttribute('style')).toContain('--series-other')
 })
 it('never sums lines into a fake Other',async () => {
  render(<LegacyChart value={{type:'chart',title:'Forecast',mark:'line',x:{field:'month',kind:'month'},y_format:'int',series:seven,points}}/>)
  expect(screen.getByText('Showing the five largest series.')).toBeTruthy()
  expect((await screen.findByTestId('canvas')).textContent).toBe('s6|s5|s4|s3|s2')
 })
 it('renders a donut without a plot, and its legend hides slices',() => {
  const {container}=render(<LegacyChart value={{type:'chart',title:'Pots',mark:'donut',x:{field:'pot',kind:'category'},y_format:'int',series:[{name:'',color_index:0}],points:[{x:'A',series:'',y:3},{x:'B',series:'',y:1}]}}/>)
  expect(container.querySelectorAll('circle').length).toBe(2);expect(screen.queryByTestId('canvas')).toBeNull()
  fireEvent.click(screen.getByRole('button',{name:'A'}));expect(container.querySelectorAll('circle').length).toBe(1)
 })
 it('sorts a categorical bar into ranked bars and drills to the journal',() => {
  render(<LegacyChart drilldown value={{type:'chart',title:'Cats',mark:'bar',x:{field:'account',kind:'category'},y_format:'int',series:[{name:'',color_index:0}],points:[{x:'expenses:small',series:'',y:1},{x:'expenses:big',series:'',y:9}]}}/>)
  const buttons=screen.getAllByRole('button').filter(b => b.getAttribute('aria-label')?.startsWith('See entries'));expect(buttons[0].getAttribute('aria-label')).toContain('expenses:big')
  fireEvent.click(buttons[0]);expect(navigate).toHaveBeenCalledWith({to:'/ledger',search:{t:undefined,accounts:['expenses:big']}})
 })
 it('uses a category axis when x is not a date and states an empty chart',async () => {
  const {unmount}=render(<LegacyChart value={{type:'chart',title:'Plans',mark:'line',x:{field:'month',kind:'number'},y_format:'mo',series:[{name:'',color_index:0}],points:[{x:'0',series:'',y:1},{x:'1',series:'',y:2}]}}/>)
  expect(await screen.findByTestId('canvas')).toBeTruthy();unmount()
  render(<LegacyChart value={{type:'chart',title:'Empty',mark:'line',x:{field:'date',kind:'date'},y_format:'int',series:[],points:[]}}/>)
  expect(screen.getByText('No data for this chart.')).toBeTruthy()
 })
})

describe('TimeChart',() => {
 const base={title:'Spend',format:unitFormat('eur',{minor:true}),xs:[d('2026-01-01'),d('2026-02-01')],bucket:'month' as const,series:[{key:'a',label:'Alpha',color:'--c0',values:[100,200]},{key:'b',label:'Beta',color:'--c1',values:[300,400]},{key:'e',label:'Estimated',color:'--hatch',values:[5,5],estimated:true}]}
 it('isolates on click, toggles on shift-click and marks estimates with the hatch',async () => {
  render(<TimeChart {...base}/>)
  const alpha=screen.getByRole('button',{name:'Alpha'}),beta=screen.getByRole('button',{name:'Beta'})
  expect(screen.getByRole('button',{name:'Estimated'}).querySelector('i')?.className).toBe('tm-hatch')
  fireEvent.click(alpha);expect(beta.getAttribute('aria-pressed')).toBe('false');expect(alpha.getAttribute('aria-pressed')).toBe('true')
  expect((await screen.findByTestId('canvas')).textContent).toBe('Alpha')
  fireEvent.click(alpha);expect(beta.getAttribute('aria-pressed')).toBe('true')
  fireEvent.click(beta,{shiftKey:true});expect(beta.getAttribute('aria-pressed')).toBe('false');expect(alpha.getAttribute('aria-pressed')).toBe('true')
 })
 it('describes itself in words and offers a human table',() => {
  const {container}=render(<TimeChart {...base}/>)
  expect(container.querySelector('.sr-only')?.textContent).toContain('Line chart of Spend, Jan 2026 to Feb 2026. Alpha: latest €2.00')
  fireEvent.click(screen.getByRole('button',{name:'Table'}))
  const table=screen.getByRole('table');expect(within(table).getByText('Feb 2026')).toBeTruthy();expect(within(table).getByText('€3.00')).toBeTruthy()
 })
 it('keeps its height while the plot loads and handles no data',() => {
  render(<TimeChart {...base} xs={[]} height={180} emptyText="Nothing here"/>)
  expect(screen.getByText('Nothing here').getAttribute('style')).toContain('180')
 })
})

const bounds={today:d('2026-10-30'),start:d('2023-01-01')}
function Bar({initial={}}:{initial?:TelemetrySearch}) {
 const [search,setSearch]=useState<TelemetrySearch>(initial)
 const state=useTimeState({bounds,search,onSearch:next => setSearch(old => ({...old,...next}))})
 return <><TimeBar state={state} bounds={bounds} navigator={{xs:[bounds.start,bounds.today],values:[1,2]}}/><output data-testid="t">{search.t ?? ''}</output><output data-testid="cmp">{search.cmp ?? ''}</output></>
}
describe('TimeBar',() => {
 const field=() => screen.getByLabelText('Time range. Type anything') as HTMLInputElement
 it('shows a friendly name and the resolved dates',() => {render(<Bar/>);expect(field().value).toBe('Last 12 months');expect(screen.getByText(/31 Oct 2025 to 30 Oct 2026/)).toBeTruthy()})
 it('offers This month after 30 days, even when a page lists other presets',() => {
  expect(defaultPresets(bounds,['90d','30d','ytd']).map(p => p.label)).toEqual(['90D','30D','This month','YTD'])
  expect(defaultPresets(bounds,['30d','this month']).map(p => p.label)).toEqual(['30D','This month'])
  render(<Bar/>);expect(screen.getAllByRole('button').map(b => b.textContent).slice(2,6)).toEqual(['30D','This month','90D','6M'])
  fireEvent.click(screen.getByRole('button',{name:'This month'}));expect(screen.getByTestId('t').textContent).toBe('this month');expect(screen.getByText(/1 Oct 2026 to 30 Oct 2026/)).toBeTruthy()
 })
 it('parses live, reports errors through aria, and applies on Enter',() => {
  render(<Bar/>)
  fireEvent.change(field(),{target:{value:'q2 2026'}});expect(screen.getByText(/1 Apr 2026 to 30 Jun 2026 · 91 days · press Enter/)).toBeTruthy();expect(field().getAttribute('aria-invalid')).toBe('false')
  fireEvent.change(field(),{target:{value:'banana'}});expect(field().getAttribute('aria-invalid')).toBe('true');expect(document.getElementById(field().getAttribute('aria-describedby')!)?.textContent).toContain('Can’t read')
  fireEvent.keyDown(field(),{key:'Enter'});expect(screen.getByTestId('t').textContent).toBe('')
  fireEvent.keyDown(field(),{key:'Escape'});expect(field().value).toBe('Last 12 months')
  fireEvent.change(field(),{target:{value:'2025'}});fireEvent.keyDown(field(),{key:'Enter'});expect(screen.getByTestId('t').textContent).toBe('2025')
  expect(screen.getByRole('button',{name:'2025'}).getAttribute('aria-pressed')).toBe('true')
 })
 it('answers the page keys but not while typing',() => {
  render(<Bar/>)
  fireEvent.keyDown(field(),{key:'['});expect(screen.getByTestId('t').textContent).toBe('')
  fireEvent.keyDown(document.body,{key:'z'});expect(screen.getByTestId('t').textContent).toMatch(/^2024-10-31 to /)
  fireEvent.keyDown(document.body,{key:'['},);expect(screen.getByRole('button',{name:'Undo zoom'}).hasAttribute('disabled')).toBe(false)
  fireEvent.click(screen.getByRole('button',{name:'Undo zoom'}));fireEvent.keyDown(document.body,{key:'t'});expect(document.activeElement).toBe(field())
 })
 it('steps, undoes and moves the brush by keyboard with readable values',() => {
  render(<Bar initial={{t:'2026-03'}}/>)
  fireEvent.click(screen.getByRole('button',{name:'Previous period'}));expect(screen.getByTestId('t').textContent).toBe('2026-02-01 to 2026-02-28')
  fireEvent.click(screen.getByRole('button',{name:'Undo zoom'}));expect(screen.getByTestId('t').textContent).toBe('2026-03-01 to 2026-03-31')
  const end=screen.getByRole('slider',{name:'End of the time window'});expect(end.getAttribute('aria-valuetext')).toBe('31 Mar 2026')
  fireEvent.keyDown(end,{key:'ArrowRight'});expect(screen.getByTestId('t').textContent).toBe('2026-03-01 to 2026-04-07')
  fireEvent.change(screen.getByLabelText('Compare to'),{target:{value:'previous_year'}});expect(screen.getByTestId('cmp').textContent).toBe('previous_year')
 })
 it('falls back on a bad URL value instead of breaking',() => {render(<Bar initial={{t:'nonsense'}}/>);expect(screen.getByText(/31 Oct 2025 to 30 Oct 2026/)).toBeTruthy()})
})

function Filters({onSave}:{onSave?:() => void}) {
 const [pills,setPills]=useState<Pill[]>([])
 return <><FilterBar pills={pills} onChange={setPills} formatTotal={c => `€${c/100}`} onSaveView={onSave} countAmount={() => 7} suggest={(field,q) => field==='category' ? [{value:'Groceries',label:'Groceries',count:12,total:34500},{value:'Rent',label:'Rent',count:3,total:300000}].filter(s => s.label.toLowerCase().includes(q.toLowerCase())) : field==='payee' ? [{value:'Lidl',label:'Lidl',count:4,total:900}].filter(s => s.label.toLowerCase().includes(q.toLowerCase())) : []} asks={['Rent over €50']} ask={{dictionary:{category:['Rent']},bounds,onRange:() => {}}}/><output data-testid="pills">{JSON.stringify(pills)}</output></>
}
describe('FilterBar follows the ARIA combobox pattern',() => {
 const box=() => screen.getByRole('combobox',{name:'Add a filter'}) as HTMLInputElement
 it('wires expanded, controls and activedescendant',() => {
  render(<Filters/>);expect(box().getAttribute('aria-expanded')).toBe('false')
  fireEvent.focus(box());expect(box().getAttribute('aria-expanded')).toBe('true')
  const list=document.getElementById(box().getAttribute('aria-controls')!);expect(list?.getAttribute('role')).toBe('listbox')
  const first=box().getAttribute('aria-activedescendant')!;expect(document.getElementById(first)?.getAttribute('aria-selected')).toBe('true')
  fireEvent.keyDown(box(),{key:'ArrowDown'});const second=box().getAttribute('aria-activedescendant')!;expect(second).not.toBe(first);expect(document.getElementById(first)?.getAttribute('aria-selected')).toBe('false')
  fireEvent.keyDown(box(),{key:'ArrowUp'});fireEvent.keyDown(box(),{key:'ArrowUp'});expect(document.getElementById(box().getAttribute('aria-activedescendant')!)?.textContent).toContain('Source')
  fireEvent.keyDown(box(),{key:'Escape'});expect(box().getAttribute('aria-expanded')).toBe('false');expect(box().getAttribute('aria-activedescendant')).toBeNull()
 })
 it('builds a pill through field, operator and value, with counts and totals',() => {
  render(<Filters/>);fireEvent.focus(box());fireEvent.keyDown(box(),{key:'Enter'})
  fireEvent.keyDown(box(),{key:'Enter'})
  const option=screen.getAllByRole('option')[0];expect(option.textContent).toContain('12 entries · €345')
  fireEvent.keyDown(box(),{key:'Enter'})
  expect(screen.getByTestId('pills').textContent).toBe('[{"field":"category","op":"is","values":["Groceries"]}]')
  fireEvent.click(screen.getByRole('button',{name:/Change to is not/}));expect(screen.getByTestId('pills').textContent).toContain('"is not"')
  fireEvent.keyDown(box(),{key:'Backspace'});expect(screen.getByTestId('pills').textContent).toBe('[]')
 })
 it('finds values by typing, offers a payee text match, and / focuses',() => {
  render(<Filters/>);fireEvent.keyDown(document.body,{key:'/'});expect(document.activeElement).toBe(box())
  fireEvent.change(box(),{target:{value:'lid'}});expect(screen.getByRole('option',{name:/Payee contains/})).toBeTruthy()
 })
 it('turns plain words into pills, and Advanced round-trips CEL',() => {
  const save=vi.fn();render(<Filters onSave={save}/>)
  fireEvent.click(screen.getByRole('button',{name:'Rent over €50'}));expect(JSON.parse(screen.getByTestId('pills').textContent!).length).toBe(2)
  fireEvent.click(screen.getByRole('button',{name:'Advanced'}));const cel=screen.getByLabelText(/Same filter as CEL/) as HTMLTextAreaElement;expect(cel.value).toBe('row.amount > 50 && row.category == "Rent"')
  fireEvent.change(cel,{target:{value:'size(x) > 1'}});fireEvent.click(screen.getByRole('button',{name:'Apply'}));expect(screen.getByRole('alert').textContent).toContain('needs an agent')
  fireEvent.click(screen.getByRole('button',{name:'Save view'}));expect(save).toHaveBeenCalledOnce()
 })
})

describe('Stat',() => {
 const eur=(v:number) => `€${v}`
 it('tone follows meaning, not direction',() => {
  expect(changeBetween(90,100,'down',eur)).toEqual({direction:'down',good:true,text:'−10.0%',detail:'−€10'})
  expect(changeBetween(110,100,'down',eur)?.good).toBe(false);expect(changeBetween(110,100,'up',eur)?.good).toBe(true);expect(changeBetween(110,100,'neutral',eur)?.good).toBe(null)
  expect(changeBetween(100,100,'up',eur)).toMatchObject({direction:'flat',good:null});expect(changeBetween(5,0,'up',eur)).toBe(null);expect(changeBetween(5,null,'up',eur)).toBe(null)
  const {container}=render(<Stat label="Money out" value="€90" change={changeBetween(90,100,'down',eur)} comparison="previous period"/>);expect(container.querySelector('.good')?.textContent).toContain('Down');expect(container.querySelector('.bad')).toBeNull()
 })
 it('shows estimates, links to entries, and admits when there is no comparison',() => {
  const open=vi.fn();render(<Stat label="Out" value="€9" comparison="previous period" estimatedShare={.21} onOpen={open} onDetails={open}/>)
  expect(screen.getByText('21% estimated')).toBeTruthy();expect(screen.getByText(/No earlier data/)).toBeTruthy()
  fireEvent.click(screen.getByRole('link'));fireEvent.click(screen.getByRole('button',{name:'How was Out calculated?'}));expect(open).toHaveBeenCalledTimes(2)
 })
 it('skeleton has the same structure as a loaded stat',() => {
  const {container}=render(<><StatSkeleton label="x"/><Stat label="x" value="1"/></>),stats=container.querySelectorAll('.tm-stat');expect([...stats[0].children].map(c => c.getAttribute('class')?.split(' ')[0])).toEqual([...stats[1].children].map(c => c.getAttribute('class')?.split(' ')[0]))
 })
})

it('Answer renders the server sentence and filters from What moved',() => {
 const pick=vi.fn(),{container}=render(<Answer onContributor={pick} facts={[{kind:'period_change',sentence:'Spending fell €10.',direction:'down',good:true,contributors:[{name:'Rent',delta:-20,text:'−€20',share:.7},{name:'Food',delta:10,text:'+€10',share:.3}]},{kind:'estimate_share',sentence:'5% is estimated.'}]}/>)
 expect(screen.getByText('Spending fell €10.')).toBeTruthy();expect(screen.getByText('5% is estimated.')).toBeTruthy()
 expect(container.querySelector('.t.good')?.textContent).toBe('−€20');expect(container.querySelector('.t.bad')?.textContent).toBe('+€10')
 fireEvent.click(screen.getByRole('button',{name:'Filter to Rent, −€20'}));expect(pick).toHaveBeenCalledWith('Rent','Rent')
})

describe('marks',() => {
 it('RankedBars sorts, collapses near-zero rows and filters on click',() => {
  const pick=vi.fn(),rows=[{key:'a',label:'Small',value:1,text:'€1'},{key:'b',label:'Big',value:100,text:'€100',compare:50,compareText:'€50'},{key:'c',label:'Tiny',value:.5,text:'€0.5'},{key:'d',label:'Wee',value:.2,text:'€0.2'},{key:'e',label:'Mini',value:.1,text:'€0.1'},{key:'f',label:'Bit',value:.1,text:'€0.1'}]
  const {container}=render(<RankedBars rows={rows} onSelect={pick} noun="accounts" selectLabel="Filter by"/>)
  expect(container.querySelectorAll('li').length).toBe(3);expect(screen.getByRole('button',{name:/Filter by Big: €100, was €50/})).toBeTruthy();expect(screen.getByText('4 small accounts')).toBeTruthy()
  expect(container.querySelector('li .n')?.textContent).toBe('Big')
  fireEvent.click(screen.getByText('4 small accounts'));expect(container.querySelectorAll('li').length).toBe(6)
  fireEvent.click(screen.getByRole('button',{name:/Filter by Big/}));expect(pick).toHaveBeenCalledWith('b')
 })
 it('Donut slices are proportional and keyboard selectable',() => {
  const pick=vi.fn(),{container}=render(<Donut size={100} onSelect={pick} slices={[{key:'a',label:'A',value:3,text:'€3',color:'red'},{key:'b',label:'B',value:1,text:'€1',color:'blue'},{key:'z',label:'Zero',value:0,text:'€0',color:'green'}]} center="€4"/>)
  const c=container.querySelectorAll('circle');expect(c.length).toBe(2);expect(parseFloat(c[0].getAttribute('stroke-dasharray')!)/parseFloat(c[1].getAttribute('stroke-dasharray')!)).toBeGreaterThan(2.5)
  fireEvent.keyDown(screen.getByRole('button',{name:'A: €3'}),{key:'Enter'});expect(pick).toHaveBeenCalledWith('a')
 })
 it('Sankey keeps flow totals on both sides of a node and stays in bounds',() => {
  const nodes=[{key:'a',label:'A'},{key:'b',label:'B'},{key:'p',label:'P'},{key:'x',label:'X'},{key:'y',label:'Y'}],links=[{from:'a',to:'p',value:80},{from:'b',to:'p',value:20},{from:'p',to:'x',value:70},{from:'p',to:'y',value:30}]
  const l=layoutSankey(nodes,links,600,240),p=l.nodes.find(n => n.key==='p')!
  expect(l.columns).toBe(3);expect(p.col).toBe(1);expect(l.ribbons.length).toBe(4)
  for(const n of l.nodes){expect(n.y).toBeGreaterThanOrEqual(-.01);expect(n.y+n.h).toBeLessThanOrEqual(240.01)}
  expect(p.in).toBeCloseTo(p.out,5);expect(p.in).toBeCloseTo(p.h,5)
  expect(layoutSankey(nodes,[{from:'a',to:'p',value:0}],600,240).ribbons.length).toBe(0)
  expect(render(<Sankey nodes={nodes} links={links} formatValue={String}/>).container.querySelector('.tm-sankey')).toBeTruthy()
 })
})

describe('DataGrid',() => {
 const columns=[{key:'date',label:'Date',type:'date' as const},{key:'name',label:'Name',type:'text' as const},{key:'amount',label:'Amount',type:'money' as const,total:true},{key:'rate',label:'Rate',type:'number' as const},{key:'runway',label:'Runway',type:'days' as const},{key:'hash',label:'Hash',machine:true}]
 const rows:GridRow[]=[{date:'2026-03-01',name:'Rent',amount:125000,rate:1.08,runway:30,hash:'abc'},{date:'2026-03-02',name:'Lidl',amount:-4550,rate:1.1,runway:1,hash:'def'}]
 it('formats by type, hides machine fields, sorts and totals only declared columns',() => {
  render(<DataGrid label="T" columns={columns} rows={rows} rowKey={r => String(r.hash)}/>)
  expect(screen.getByText('1 Mar 2026')).toBeTruthy();expect(screen.getByText('€1,250.00')).toBeTruthy();expect(screen.getByText('−€45.50')).toBeTruthy();expect(screen.getByText('1 day')).toBeTruthy();expect(screen.queryByText('abc')).toBeNull();expect(screen.queryByText('Hash')).toBeNull()
  const total=document.querySelector('.tm-grid-total')!;expect(total.textContent).toContain('€1,204.50');expect(total.textContent).not.toContain('2.18');expect(total.textContent).toContain('Total · 2 rows')
  const amount=screen.getByRole('button',{name:'Amount'});fireEvent.click(amount);expect(document.querySelector('[data-row="0"]')?.textContent).toContain('Lidl')
  expect(screen.getAllByRole('columnheader')[2].getAttribute('aria-sort')).toBe('ascending')
  fireEvent.click(amount);expect(screen.getAllByRole('columnheader')[2].getAttribute('aria-sort')).toBe('descending');fireEvent.click(amount);expect(screen.getAllByRole('columnheader')[2].getAttribute('aria-sort')).toBe('none')
 })
 it('never totals a non-additive column even if asked, and keeps server text',() => {
  expect(columnTotal({key:'r',label:'r',type:'pct',total:true},[{r:1}])).toBe(null);expect(columnTotal({key:'r',label:'r',type:'text',total:true},[{r:1}])).toBe(null);expect(columnTotal({key:'r',label:'r',type:'number'},[{r:1}])).toBe(null)
  expect(gridText({key:'m',label:'m',type:'money'},{v:1,t:'Server text'})).toBe('Server text');expect(gridText({key:'m',label:'m',type:'money'},null)).toBe('—')
  expect(sortRows([{a:null},{a:2},{a:1}],{key:'a',desc:true}).map(r => r.a)).toEqual([2,1,null])
 })
 it('opens Details from the button and Enter, and arrows move the roving row',() => {
  const details=vi.fn();render(<DataGrid label="T" columns={columns} rows={rows} rowKey={r => String(r.hash)} onDetails={details}/>)
  fireEvent.click(screen.getByRole('button',{name:'Details for row 2'}));expect(details).toHaveBeenCalledWith(rows[1])
  const first=document.querySelector<HTMLElement>('[data-row="0"]')!;expect(first.tabIndex).toBe(0);expect(document.querySelector<HTMLElement>('[data-row="1"]')!.tabIndex).toBe(-1)
  fireEvent.keyDown(first,{key:'Enter'});expect(details).toHaveBeenCalledWith(rows[0])
 })
 it('virtualizes long tables against the page instead of a fixed viewport',() => {
  const many:GridRow[]=Array.from({length:5000},(_,i) => ({date:'2026-03-01',name:`Row ${i}`,amount:i,rate:1,runway:1,hash:String(i)}))
  render(<DataGrid label="T" columns={columns} rows={many} rowKey={r => String(r.hash)}/>)
  expect(document.querySelectorAll('.tm-grid-body [role=row]').length).toBeLessThan(100);expect(screen.getByRole('table').getAttribute('aria-rowcount')).toBe('5001')
  expect((document.querySelector('.tm-grid-body') as HTMLElement).style.maxHeight).toBe('');expect(document.querySelector('.tm-grid-total')?.textContent).toContain('€124,975.00')
 })
 it('states an empty result',() => {render(<DataGrid label="T" columns={columns} rows={[]} rowKey={() => ''} emptyText="Nothing"/>);expect(screen.getByText('Nothing')).toBeTruthy()})
})

it('DetailsDrawer shows calculation, machine fields and sends a flag',async () => {
 const flag=vi.fn(async () => {})
 render(<DetailsDrawer open onOpenChange={() => {}} onFlag={flag} target={{metric:'burn',value:'€9'}} content={{title:'Burn',value:'€9',calculation:{summary:'Added up.',formula:'row.a == 1',rates:[{label:'EUR/USD',value:'1.08'}]},sourceRows:{columns:['F'],rows:[['v']]},history:[{at:'Today',actor:'Codex',summary:'Rebuilt'}],machine:[{label:'ID',value:'01XYZ'}]}}/>)
 const dialog=await screen.findByRole('dialog');for(const text of ['How was this calculated?','row.a == 1','1.08','Source rows','Rebuilt','01XYZ'])expect(within(dialog).getByText(text)).toBeTruthy()
 fireEvent.click(within(dialog).getByRole('button',{name:'Flag this'}));fireEvent.change(within(dialog).getByLabelText(/What looks wrong/),{target:{value:'Too high'}})
 await act(async () => {fireEvent.click(within(dialog).getByRole('button',{name:'Send to an agent'}))})
 expect(flag).toHaveBeenCalledWith({target:{metric:'burn',value:'€9'},note:'Too high'});expect(within(dialog).getByRole('status').textContent).toContain('Sent to an agent')
})

it('CommandBar opens on the shortcut, jumps, and says so when nothing matches',async () => {
 const select=vi.fn()
 render(<CommandBar groups={[{heading:'Pages',items:[{id:'now',label:'Now'}]}]} onSelect={select}/>)
 expect(screen.queryByRole('dialog')).toBeNull()
 await act(async () => {fireEvent.keyDown(document,{key:'k',ctrlKey:true})})
 const input=await screen.findByLabelText('Search');expect(screen.getByText('Now')).toBeTruthy()
 await act(async () => {fireEvent.change(input,{target:{value:'why is rent up'}})})
 expect(await screen.findByText('No matches')).toBeTruthy();expect(screen.queryByText(/Ask/)).toBeNull();expect(select).not.toHaveBeenCalled()
})
