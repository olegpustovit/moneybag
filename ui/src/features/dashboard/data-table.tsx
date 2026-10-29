import { useEffect, useMemo, useRef, useState } from 'react'
import { columnResizingFeature, columnSizingFeature, columnVisibilityFeature, createSortedRowModel, rowSortingFeature, sortFns, tableFeatures, useTable, type ColumnDef, type ColumnSizingState, type ColumnVisibilityState, type SortingState } from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { ComponentValue } from './values'

type Value = Extract<ComponentValue, { type: 'table' | 'scenario_compare' }>
const features=tableFeatures({rowSortingFeature,sortedRowModel:createSortedRowModel(),sortFns,columnVisibilityFeature,columnSizingFeature,columnResizingFeature})
function preferences(key:string):{visibility:ColumnVisibilityState;sizing:ColumnSizingState} {
 try {const data=JSON.parse(localStorage.getItem(key) || '{}');return {visibility:data.visibility && typeof data.visibility==='object' ? data.visibility : {},sizing:data.sizing && typeof data.sizing==='object' ? data.sizing : {}}} catch {return {visibility:{},sizing:{}}}
}
export function DataTable({ value, storageKey }: { value:Value; storageKey:string }) {
 const key=`moneybag:table:${storageKey}`
 const [initial]=useState(() => preferences(key))
 // shortcut: sort state is local; move it to validated URL search when table routes share a sort contract.
 const [sorting,setSorting]=useState<SortingState>([])
 const [visibility,setVisibility]=useState<ColumnVisibilityState>(initial.visibility)
 const [sizing,setSizing]=useState<ColumnSizingState>(initial.sizing)
 const scroll=useRef<HTMLDivElement>(null)
 useEffect(() => {try {localStorage.setItem(key,JSON.stringify({visibility,sizing}))} catch { /* Storage can be unavailable. */ }},[key,visibility,sizing])
 const columns=useMemo<Array<ColumnDef<typeof features,Value['rows'][number]>>>(() => value.columns.map((column,index) => ({
  id:column.key,header:column.label,accessorFn:row => {const v=row[column.key]?.v;return typeof v==='number' || typeof v==='string' ? v : undefined},
  enableHiding:index!==0,sortDescFirst:false,
  cell:({row}) => row.original[column.key]?.t ?? '—',
 })),[value.columns])
 const table=useTable({features,columns,data:value.rows,state:{sorting,columnVisibility:visibility,columnSizing:sizing},onSortingChange:setSorting,onColumnVisibilityChange:setVisibility,onColumnSizingChange:setSizing,columnResizeMode:'onEnd'})
 const rows=table.getRowModel().rows
 const virtual=useVirtualizer({count:rows.length,getScrollElement:() => scroll.current,estimateSize:() => 32,overscan:12,enabled:rows.length>200})
 const visible=rows.length>200 ? virtual.getVirtualItems() : []
 const top=visible[0]?.start ?? 0,bottom=visible.length ? virtual.getTotalSize()-visible[visible.length-1].end : 0
 const rendered=rows.length>200 ? visible.map(item => rows[item.index]) : rows
 return <>
  <details className="data-table-columns"><summary>Columns</summary>{table.getAllLeafColumns().map(column => <label key={column.id}><input type="checkbox" checked={column.getIsVisible()} disabled={!column.getCanHide()} onChange={column.getToggleVisibilityHandler()}/>{value.columns.find(item => item.key===column.id)?.label}</label>)}</details>
  <div ref={scroll} className={`table-scroll data-table-scroll${rows.length>200 ? ' virtual' : ''}`} tabIndex={0} role="region" aria-label={`${value.title || 'Dashboard'} table`}><table className="shared-data-table"><thead>{table.getHeaderGroups().map(group => <tr key={group.id}>{group.headers.map(header => {const sorted=header.column.getIsSorted(),column=value.columns.find(item => item.key===header.column.id);return <th key={header.id} className={column?.align==='right' ? 'numeric' : ''} aria-sort={sorted==='asc' ? 'ascending' : sorted==='desc' ? 'descending' : 'none'} style={{width:header.getSize()}}><button type="button" onClick={header.column.getToggleSortingHandler()}><table.FlexRender header={header}/>{sorted && (sorted==='asc' ? ' ↑' : ' ↓')}</button><span className="column-resize" role="separator" tabIndex={0} aria-label={`Resize ${column?.label || header.column.id} column`} aria-orientation="vertical" aria-valuemin={40} aria-valuemax={1200} aria-valuenow={header.getSize()} onKeyDown={event => {if(event.key==='ArrowLeft' || event.key==='ArrowRight'){event.preventDefault();table.setColumnSizing(old => ({...old,[header.column.id]:Math.max(40,Math.min(1200,header.getSize()+(event.key==='ArrowRight' ? 16 : -16)))}))}}} onMouseDown={header.getResizeHandler()} onTouchStart={header.getResizeHandler()}/></th>})}</tr>)}</thead><tbody>
   {top>0 && <tr aria-hidden="true"><td colSpan={table.getVisibleLeafColumns().length} style={{height:top,padding:0,border:0}}/></tr>}
   {rendered.map(row => <tr key={row.id} style={rows.length>200 ? {height:32} : undefined}>{row.getVisibleCells().map(cell => {const column=value.columns.find(item => item.key===cell.column.id);return <td key={cell.id} title={row.original[cell.column.id]?.t} className={`${column?.align==='right' ? 'numeric ' : ''}${row.original[cell.column.id]?.tone || ''}`}><table.FlexRender cell={cell}/></td>})}</tr>)}
   {bottom>0 && <tr aria-hidden="true"><td colSpan={table.getVisibleLeafColumns().length} style={{height:bottom,padding:0,border:0}}/></tr>}
  </tbody></table></div>
 </>
}
