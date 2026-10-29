import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type KeyboardEvent, type RefObject } from 'react'
import { columnResizingFeature, columnSizingFeature, columnVisibilityFeature, createSortedRowModel, rowSortingFeature, sortFns, tableFeatures, useTable, type ColumnDef, type ColumnSizingState, type ColumnVisibilityState, type SortingState } from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { ComponentValue } from './values'
import { tableTotal } from './table-total'

type Value = Extract<ComponentValue, { type: 'table' | 'scenario_compare' }>
const features=tableFeatures({rowSortingFeature,sortedRowModel:createSortedRowModel(),sortFns,columnVisibilityFeature,columnSizingFeature,columnResizingFeature})
function preferences(key:string):{visibility:ColumnVisibilityState;sizing:ColumnSizingState} {
 try {const data=JSON.parse(localStorage.getItem(key) || '{}');return {visibility:data.visibility && typeof data.visibility==='object' ? data.visibility : {},sizing:data.sizing && typeof data.sizing==='object' ? data.sizing : {}}} catch {return {visibility:{},sizing:{}}}
}
export function DataTable({ value, storageKey, sort, onSortChange, regionLabel, renderCell, sorters, requiredColumns, unsortable, onFilterChange, initialSizing, externalFilterRef, onRowActivate, onScroll, footer, forceVirtual, resetKey }: { value:Value; storageKey:string; sort?:string; onSortChange?:(sort:string|undefined)=>void; regionLabel?:string; renderCell?:(row:Value['rows'][number],column:string)=>ReactNode; sorters?:Record<string,(a:Value['rows'][number],b:Value['rows'][number])=>number>; requiredColumns?:string[]; unsortable?:string[]; onFilterChange?:()=>void; initialSizing?:ColumnSizingState; externalFilterRef?:RefObject<HTMLInputElement|null>; onRowActivate?:(row:Value['rows'][number])=>void; onScroll?:(element:HTMLDivElement)=>void; footer?:ReactNode; forceVirtual?:boolean; resetKey?:string }) {
 const key=`moneybag:table:${storageKey}`
 const [initial]=useState(() => preferences(key))
 const [localSorting,setLocalSorting]=useState<SortingState>([])
 const [filter,setFilter]=useState('')
 const [focus,setFocus]=useState({row:0,col:0})
 const filterRef=useRef<HTMLInputElement>(null)
 const sorting=onSortChange ? (sort?.split(',').flatMap(item => {const desc=item.startsWith('-'),id=desc ? item.slice(1) : item;return value.columns.some(column => column.key===id) && !unsortable?.includes(id) ? [{id,desc}] : []}) ?? []) : localSorting
 const setSorting=(updater:SortingState | ((old:SortingState)=>SortingState)) => {const next=typeof updater==='function' ? updater(sorting) : updater;if(onSortChange)onSortChange(next.map(item => `${item.desc ? '-' : ''}${item.id}`).join(',') || undefined);else setLocalSorting(next);scroll.current?.scrollTo(0,0)}
 const [visibility,setVisibility]=useState<ColumnVisibilityState>(() => ({...initial.visibility,...Object.fromEntries([value.columns[0]?.key,...(requiredColumns ?? [])].filter(Boolean).map(id => [id,true]))}))
 const [sizing,setSizing]=useState<ColumnSizingState>(() => ({...initialSizing,...initial.sizing}))
 const scroll=useRef<HTMLDivElement>(null)
 useLayoutEffect(() => {if(resetKey!==undefined)scroll.current?.scrollTo(0,0)},[resetKey])
 useEffect(() => {try {localStorage.setItem(key,JSON.stringify({visibility,sizing}))} catch { /* Storage can be unavailable. */ }},[key,visibility,sizing])
 const columns=useMemo<Array<ColumnDef<typeof features,Value['rows'][number]>>>(() => value.columns.map((column,index) => ({
  id:column.key,header:column.label,accessorFn:row => {const v=row[column.key]?.v;return typeof v==='number' || typeof v==='string' ? v : undefined},
  enableHiding:index!==0 && !requiredColumns?.includes(column.key),enableSorting:!unsortable?.includes(column.key),sortDescFirst:false,
  ...(sorters?.[column.key] ? {sortFn:(a,b) => sorters[column.key](a.original,b.original)} : {}),
  cell:({row}) => renderCell ? renderCell(row.original,column.key) : row.original[column.key]?.t ?? '—',
 })),[value.columns,renderCell,sorters,requiredColumns,unsortable])
 const filtered=useMemo(() => {const query=filter.trim().toLocaleLowerCase();return query ? value.rows.filter(row => Object.values(row).some(cell => cell.t.toLocaleLowerCase().includes(query))) : value.rows},[value.rows,filter])
 const totals=useMemo(() => new Map(value.columns.map(column => {const values=filtered.map(row => row[column.key]?.v);return [column.key,{total:tableTotal(values,column.format),numeric:values.some(value => typeof value==='number')}] })),[filtered,value.columns])
 const table=useTable({features,columns,data:filtered,state:{sorting,columnVisibility:visibility,columnSizing:sizing},onSortingChange:setSorting,onColumnVisibilityChange:setVisibility,onColumnSizingChange:setSizing,columnResizeMode:'onEnd'})
 const rows=table.getRowModel().rows
 const virtualized=forceVirtual || rows.length>200
 const virtual=useVirtualizer({count:rows.length,getScrollElement:() => scroll.current,estimateSize:() => 32,overscan:12,enabled:virtualized})
 const visible=virtualized ? virtual.getVirtualItems() : []
 const top=visible[0]?.start ?? 0,bottom=visible.length ? virtual.getTotalSize()-visible[visible.length-1].end : 0
 const rendered=virtualized ? visible.map(item => ({row:rows[item.index],index:item.index})) : rows.map((row,index) => ({row,index}))
 const activeRow=rendered.some(item => item.index===focus.row) ? focus.row : rendered[0]?.index
 function tableKey(event:KeyboardEvent<HTMLTableElement>) {
  if(event.key==='/' && !event.ctrlKey && !event.metaKey && !event.altKey && !(event.target instanceof HTMLInputElement)) {event.preventDefault();(externalFilterRef ?? filterRef).current?.focus();return}
  if(!(event.target instanceof HTMLTableCellElement))return
  if(event.key==='Enter') {const links=event.target.parentElement?.querySelectorAll<HTMLAnchorElement>('a[href]');const action=event.target.querySelector<HTMLElement>('a[href],button:not([disabled])') ?? (links?.length===1 ? links[0] : undefined);if(action){event.preventDefault();action.click()}else if(onRowActivate){event.preventDefault();const row=rows[Number(event.target.parentElement?.dataset.row)];if(row)onRowActivate(row.original)}return}
  const moves:Record<string,[number,number]>={ArrowUp:[-1,0],ArrowDown:[1,0],ArrowLeft:[0,-1],ArrowRight:[0,1]}
  const move=moves[event.key];if(!move)return
  event.preventDefault();const source=event.target,row=Number(source.parentElement?.dataset.row)+move[0],col=source.cellIndex+move[1]
  if(row<0 || row>=rows.length || col<0 || col>=table.getVisibleLeafColumns().length)return
  const focusCell=() => {const target=scroll.current?.querySelector<HTMLTableCellElement>(`tbody tr[data-row="${row}"] td:nth-child(${col+1})`);if(target){target.focus({preventScroll:true});target.scrollIntoView({block:'nearest',inline:'nearest'});return true}return false}
  if(!focusCell() && virtualized){virtual.scrollToIndex(row,{align:'auto'});requestAnimationFrame(() => {if(document.activeElement===source)focusCell()})}
 }
 return <>
  {!externalFilterRef && <label className="data-table-filter">Filter <input ref={filterRef} type="search" value={filter} onChange={event => {setFilter(event.target.value);onFilterChange?.();scroll.current?.scrollTo(0,0)}}/></label>}
  <details className="data-table-columns"><summary>Columns</summary>{table.getAllLeafColumns().map(column => <label key={column.id}><input type="checkbox" checked={column.getIsVisible()} disabled={!column.getCanHide()} onChange={column.getToggleVisibilityHandler()}/>{value.columns.find(item => item.key===column.id)?.label}</label>)}</details>
  <div ref={scroll} className={`table-scroll data-table-scroll${virtualized ? ' virtual' : ''}`} tabIndex={0} data-scroll-restoration-id={resetKey===undefined ? undefined : `table-${encodeURIComponent(resetKey)}`} onScroll={event => onScroll?.(event.currentTarget)} role="region" aria-label={regionLabel || `${value.title || 'Dashboard'} table`}><table className="shared-data-table" onKeyDown={tableKey}><thead>{table.getHeaderGroups().map(group => <tr key={group.id}>{group.headers.map(header => {const sorted=header.column.getIsSorted(),column=value.columns.find(item => item.key===header.column.id);return <th key={header.id} className={column?.align==='right' ? 'numeric' : ''} aria-sort={sorted==='asc' ? 'ascending' : sorted==='desc' ? 'descending' : 'none'} style={{width:header.getSize()}}><button type="button" disabled={!header.column.getCanSort()} onClick={header.column.getToggleSortingHandler()}><table.FlexRender header={header}/>{sorted && (sorted==='asc' ? ' ↑' : ' ↓')}</button><span className="column-resize" role="separator" tabIndex={0} aria-label={`Resize ${column?.label || header.column.id} column`} aria-orientation="vertical" aria-valuemin={40} aria-valuemax={1200} aria-valuenow={header.getSize()} onKeyDown={event => {if(event.key==='ArrowLeft' || event.key==='ArrowRight'){event.preventDefault();table.setColumnSizing(old => ({...old,[header.column.id]:Math.max(40,Math.min(1200,header.getSize()+(event.key==='ArrowRight' ? 16 : -16)))}))}}} onMouseDown={header.getResizeHandler()} onTouchStart={header.getResizeHandler()}/></th>})}</tr>)}</thead><tbody>
   {top>0 && <tr aria-hidden="true"><td colSpan={table.getVisibleLeafColumns().length} style={{height:top,padding:0,border:0}}/></tr>}
   {rendered.map(({row,index}) => <tr key={row.id} data-row={index} data-index={index} ref={virtualized ? virtual.measureElement : undefined} style={virtualized ? {height:32} : undefined} onClick={onRowActivate ? event => {if(!(event.target instanceof Element) || !event.target.closest('button,a,input,select,textarea'))onRowActivate(row.original)} : undefined}>{row.getVisibleCells().map((cell,col) => {const column=value.columns.find(item => item.key===cell.column.id);return <td key={cell.id} tabIndex={index===activeRow && col===Math.min(focus.col,table.getVisibleLeafColumns().length-1) ? 0 : -1} onFocus={event => {if(event.target===event.currentTarget)setFocus({row:index,col})}} title={row.original[cell.column.id]?.t} className={`${column?.align==='right' ? 'numeric ' : ''}${row.original[cell.column.id]?.tone || ''}`}><table.FlexRender cell={cell}/></td>})}</tr>)}
   {bottom>0 && <tr aria-hidden="true"><td colSpan={table.getVisibleLeafColumns().length} style={{height:bottom,padding:0,border:0}}/></tr>}
  </tbody>{rows.length>0 && <tfoot><tr>{table.getVisibleLeafColumns().map((column,index) => {const def=value.columns.find(item => item.key===column.id),result=totals.get(column.id),total=result?.total ?? (result?.numeric ? '—' : undefined);return <td key={column.id} title={result?.numeric && result.total===undefined ? 'Total unavailable for mixed or unsafe numeric values.' : undefined} className={def?.align==='right' ? 'numeric' : ''}>{index===0 && <strong>Total · {filtered.length} {filtered.length===1 ? 'row' : 'rows'}{total!==undefined ? ' · ' : ''}</strong>}{total}</td>})}</tr></tfoot>}</table>{rows.length===0 && <p className="muted">No rows match this filter.</p>}{footer}</div>
 </>
}
