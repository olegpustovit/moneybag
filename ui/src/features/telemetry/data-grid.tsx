import { useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { useWindowVirtualizer } from '@tanstack/react-virtual'
import { humanDate, moneyExact, percent } from './format'
import { Sparkline } from './marks'
import './telemetry.css'

export type ColumnType='text'|'money'|'number'|'date'|'pct'|'days'|'count'
export type GridColumn={key:string;label:string;type?:ColumnType;total?:boolean;cell?:'spark'|'bar';machine?:boolean;width?:string;sortable?:boolean;currency?:string}
/** A cell is a raw value, or the server's `{v,t,tone}` where `t` is already formatted. */
export type GridValue=string|number|null|undefined|number[]|{v:unknown;t:string;tone?:string}
export type GridRow=Record<string,GridValue>
export type GridSort={key:string;desc:boolean}|null
const NUMERIC=new Set<ColumnType>(['money','number','pct','days','count'])
const ADDITIVE=new Set<ColumnType>(['money','number','days','count'])
const isCell=(v:GridValue):v is {v:unknown;t:string;tone?:string} => typeof v==='object' && v!==null && !Array.isArray(v)
const raw=(v:GridValue):unknown => isCell(v) ? v.v : v
export function gridText(column:GridColumn,value:GridValue):string {
 if(isCell(value))return value.t
 if(value===null || value===undefined || value==='')return '—'
 if(Array.isArray(value))return value.join(', ')
 const type=column.type ?? 'text'
 if(typeof value==='number'){
  if(type==='money')return moneyExact(value,column.currency)
  if(type==='pct')return percent(value)
  if(type==='days')return `${value.toLocaleString('en-US')} ${value===1 ? 'day' : 'days'}`
  if(type==='date')return new Date(value).toISOString().slice(0,10).replace(/^(.*)$/,humanDate)
  return value.toLocaleString('en-US')
 }
 return type==='date' ? humanDate(value) : value
}
const compare=(a:unknown,b:unknown) => {if(a===b)return 0;if(a===null || a===undefined)return 1;if(b===null || b===undefined)return -1;return typeof a==='number' && typeof b==='number' ? a-b : String(a).localeCompare(String(b))}
export function sortRows<R extends GridRow>(rows:R[],sort:GridSort):R[] {if(!sort)return rows;const sign=sort.desc ? -1 : 1;return [...rows].sort((a,b) => {const x=raw(a[sort.key]),y=raw(b[sort.key]);if(x===null || x===undefined)return y===null || y===undefined ? 0 : 1;if(y===null || y===undefined)return -1;return sign*compare(x,y)})}
// A total exists only when the column asks for one and its unit can add up. Rates, runway and text never get one, whatever the spec says.
export function columnTotal(column:GridColumn,rows:GridRow[]):number|null {
 if(!column.total || !ADDITIVE.has(column.type ?? 'text'))return null
 let sum=0;for(const row of rows){const v=raw(row[column.key]);if(typeof v==='number' && Number.isFinite(v))sum+=v}
 return sum
}
const template=(columns:GridColumn[],details:boolean) => [...columns.map(c => c.width ?? (c.cell==='spark' ? 'minmax(70px,120px)' : c.cell==='bar' ? 'minmax(90px,1fr)' : (c.type==='date' ? '108px' : NUMERIC.has(c.type ?? 'text') ? 'minmax(84px,120px)' : 'minmax(0,1fr)'))),...(details ? ['72px'] : [])].join(' ')
const roleFor=(columns:GridColumn[]) => {const title=columns.find(c => (c.type ?? 'text')==='text')?.key ?? columns[0]?.key,value=[...columns].reverse().find(c => c.type==='money' || c.type==='number')?.key;return (key:string) => key===title ? 'title' : key===value ? 'value' : 'meta'}

export type DataGridProps={columns:GridColumn[];rows:GridRow[];rowKey:(row:GridRow,index:number) => string;label:string;
 sort?:GridSort;onSortChange?:(sort:GridSort) => void;initialSort?:GridSort;onDetails?:(row:GridRow) => void;onRowActivate?:(row:GridRow) => void;
 renderCell?:(row:GridRow,column:GridColumn) => ReactNode;emptyText?:string;virtualFrom?:number}
export function DataGrid({columns,rows,rowKey,label,sort:controlled,onSortChange,initialSort=null,onDetails,onRowActivate,renderCell,emptyText='No entries match.',virtualFrom=60}:DataGridProps) {
 const [local,setLocal]=useState<GridSort>(initialSort),sort=controlled===undefined ? local : controlled,setSort=(next:GridSort) => {if(onSortChange)onSortChange(next);if(controlled===undefined)setLocal(next)}
 const [active,setActive]=useState(0),[margin,setMargin]=useState(0),body=useRef<HTMLDivElement>(null),selectId=useId()
 const visible=columns.filter(c => !c.machine),sorted=sortRows(rows,sort),details=!!onDetails,cols=template(visible,details),m=roleFor(visible)
 const virtual=sorted.length>virtualFrom
 useLayoutEffect(() => {const top=(body.current?.getBoundingClientRect().top ?? 0)+scrollY;if(Math.abs(top-margin)>1)setMargin(top)})
 const virtualizer=useWindowVirtualizer({count:sorted.length,estimateSize:() => 36,overscan:14,scrollMargin:margin,enabled:virtual})
 const items=virtual ? virtualizer.getVirtualItems() : sorted.map((_,index) => ({index,start:0}))
 const barPeak=new Map(visible.filter(c => c.cell==='bar').map(c => [c.key,Math.max(1e-9,...rows.map(r => {const v=raw(r[c.key]);return typeof v==='number' ? Math.abs(v) : 0}))]))
 const totals=visible.map(c => columnTotal(c,sorted)),hasTotal=totals.some(t => t!==null)
 const toggle=(key:string) => setSort(sort?.key===key ? (sort.desc ? null : {key,desc:true}) : {key,desc:false})
 const focusRow=(index:number) => {
  const next=Math.max(0,Math.min(sorted.length-1,index));setActive(next)
  if(virtual)virtualizer.scrollToIndex(next,{align:'auto'})
  requestAnimationFrame(() => body.current?.querySelector<HTMLElement>(`[data-row="${next}"]`)?.focus({preventScroll:!virtual ? false : true}))
 }
 const onRowKey=(e:KeyboardEvent,index:number,row:GridRow) => {
  if(e.target!==e.currentTarget)return
  if(e.key==='ArrowDown'){e.preventDefault();focusRow(index+1)}else if(e.key==='ArrowUp'){e.preventDefault();focusRow(index-1)}
  else if(e.key==='Home'){e.preventDefault();focusRow(0)}else if(e.key==='End'){e.preventDefault();focusRow(sorted.length-1)}
  else if(e.key==='PageDown'){e.preventDefault();focusRow(index+12)}else if(e.key==='PageUp'){e.preventDefault();focusRow(index-12)}
  else if(e.key==='Enter'){if(onRowActivate){e.preventDefault();onRowActivate(row)}else if(onDetails){e.preventDefault();onDetails(row)}}
 }
 const cell=(row:GridRow,c:GridColumn) => {
  const value=row[c.key]
  if(renderCell){const made=renderCell(row,c);if(made!==undefined)return made}
  if(c.cell==='spark' && Array.isArray(value))return <Sparkline values={value} height={22}/>
  if(c.cell==='bar'){const v=raw(value);return <span className="tm-cell-bar"><i style={{width:`${typeof v==='number' ? Math.abs(v)/(barPeak.get(c.key) ?? 1)*100 : 0}%`}}/><span>{gridText(c,value)}</span></span>}
  return gridText(c,value)
 }
 const line=(row:GridRow,index:number) => <div key={rowKey(row,index)} role="row" aria-rowindex={index+2} data-row={index} data-index={index} className="tm-grid-row" tabIndex={index===Math.min(active,sorted.length-1) ? 0 : -1} style={{gridTemplateColumns:cols}} onFocus={e => {if(e.target===e.currentTarget)setActive(index)}} onKeyDown={e => onRowKey(e,index,row)} onClick={onRowActivate ? e => {if(!(e.target as Element).closest('button,a'))onRowActivate(row)} : undefined}>
  {visible.map(c => {const v=row[c.key],tone=isCell(v) ? v.tone : undefined;return <div key={c.key} role="cell" data-m={m(c.key)} data-label={c.label} className={`tm-cell${NUMERIC.has(c.type ?? 'text') ? ' num' : ''}${tone ? ` ${tone}` : ''}`} title={isCell(v) || typeof v==='string' ? gridText(c,v) : undefined}>{cell(row,c)}</div>})}
  {details && <div role="cell" className="tm-cell details" data-m="meta"><button type="button" className="tm-details-link" aria-label={`Details for row ${index+1}`} onClick={() => onDetails!(row)}>Details</button></div>}
 </div>
 if(!sorted.length)return <p className="tm-empty">{emptyText}</p>
 return <div className="tm-grid" role="table" aria-label={label} aria-rowcount={sorted.length+1}>
  <div className="tm-grid-sort"><label htmlFor={selectId}>Sort</label><select id={selectId} className="tm-btn tm-select" value={sort ? `${sort.key}:${sort.desc ? 'd' : 'a'}` : ''} onChange={e => {const [key,dir]=e.target.value.split(':');setSort(key ? {key,desc:dir==='d'} : null)}}><option value="">As listed</option>{visible.filter(c => c.sortable!==false && !c.cell).flatMap(c => [<option key={`${c.key}a`} value={`${c.key}:a`}>{c.label}, {NUMERIC.has(c.type ?? 'text') ? 'smallest first' : c.type==='date' ? 'oldest first' : 'A to Z'}</option>,<option key={`${c.key}d`} value={`${c.key}:d`}>{c.label}, {NUMERIC.has(c.type ?? 'text') ? 'largest first' : c.type==='date' ? 'newest first' : 'Z to A'}</option>])}</select></div>
  <div role="row" aria-rowindex={1} className="tm-grid-head" style={{gridTemplateColumns:cols}}>
   {visible.map(c => {const on=sort?.key===c.key,can=c.sortable!==false && !c.cell;return <div key={c.key} role="columnheader" aria-sort={on ? (sort!.desc ? 'descending' : 'ascending') : 'none'} className={NUMERIC.has(c.type ?? 'text') ? 'num' : undefined}>{can ? <button type="button" onClick={() => toggle(c.key)}>{c.label}{on && <span aria-hidden="true"> {sort!.desc ? '↓' : '↑'}</span>}</button> : <span>{c.label}</span>}</div>})}
   {details && <div role="columnheader"><span className="sr-only">Details</span></div>}
  </div>
  <div ref={body} role="rowgroup" className="tm-grid-body" style={virtual ? {height:virtualizer.getTotalSize(),position:'relative'} : undefined}>
   {virtual ? <div style={{position:'absolute',top:0,left:0,right:0,transform:`translateY(${(items[0]?.start ?? margin)-margin}px)`}}>{items.map(item => <div key={item.index} ref={virtualizer.measureElement} data-index={item.index}>{line(sorted[item.index],item.index)}</div>)}</div> : sorted.map((row,i) => line(row,i))}
  </div>
  {hasTotal && <div role="row" aria-rowindex={sorted.length+2} className="tm-grid-row tm-grid-total" style={{gridTemplateColumns:cols}}>
   {visible.map((c,i) => {const t=totals[i];return <div key={c.key} role="cell" data-m={m(c.key)} className={`tm-cell${NUMERIC.has(c.type ?? 'text') ? ' num' : ''}`}>{t!==null ? gridText(c,t) : m(c.key)==='title' ? `Total · ${sorted.length.toLocaleString('en-US')} rows` : ''}</div>})}
   {details && <div role="cell" className="tm-cell details" data-m="meta"/>}
  </div>}
 </div>
}
