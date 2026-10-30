import { useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { Link, createFileRoute } from '@tanstack/react-router'
import { tool } from '../lib/api'
import { DataGrid, type ColumnType, type GridColumn, type GridRow } from '../features/telemetry/data-grid'
import { describePage, openDetails } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton, humanKey } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/data/$id')({errorComponent:p => <RouteError {...p}/>,component:Dataset})
// Bookkeeping columns stay in Details: sort order, free-form tags and anything an agent marked internal with a leading underscore.
const INTERNAL=new Set(['o','tag','tags','id','row_id','rowid','seq','version'])
const TYPES:Record<string,ColumnType>={number:'number',decimal:'number',int:'number',integer:'number',money:'money',date:'date',datetime:'date',percent:'pct',pct:'pct'}
function Dataset() {
 const {session,bootstrap}=Route.useRouteContext(),{id}=Route.useParams(),table=bootstrap.tables.find(t => t.id===id)
 const data=useQuery({queryKey:['dataset',session.user.id,id],staleTime:30_000,queryFn:() => tool(session,'db_query',{db:id,limit:5000})})
 const name=table?.name ?? 'Dataset'
 describePage({title:`Dataset: ${name}`,lines:[]})
 const head=<><p className="breadcrumb"><Link to="/data" className="tm-crumb">← Data</Link></p><PageHead title={name}/></>
 if(data.error)return <>{head}<RouteError error={data.error} reset={() => void data.refetch()} title="This dataset didn’t load"/></>
 if(!data.data)return <>{head}<RowsSkeleton rows={12}/></>
 const raw=data.data.columns ?? [],columns:GridColumn[]=raw.filter(c => !INTERNAL.has(c.id.toLowerCase()) && !c.id.startsWith('_')).map(c => ({key:c.id,label:humanKey(c.id),type:TYPES[c.type] ?? 'text',format:c.type}))
 const cell=(v:unknown) => v===null || v===undefined ? '' : typeof v==='number' || typeof v==='string' ? v : JSON.stringify(v)
 const rows:GridRow[]=(data.data.rows ?? []).filter(r => !r.archived).map(r => ({__id:r.id,__version:String(r.version),...Object.fromEntries(columns.map(c => {const v=r.cells[c.key];return [c.key,c.type==='money' && typeof v==='number' ? {v,t:v.toLocaleString('en-US',{minimumFractionDigits:2,maximumFractionDigits:2})} : cell(v)]}))}))
 return <>{head}{rows.length ? <DataGrid label={name} columns={columns} rows={rows} rowKey={r => String(r.__id)} onDetails={row => openDetails({historyOf:String(row.__id),content:{title:name,subtitle:'One row of this dataset.',sourceRows:{columns:['Field','Value'],rows:raw.map(c => [humanKey(c.id),String(cell((data.data.rows ?? []).find(x => x.id===row.__id)?.cells[c.id]))])},machine:[{label:'Row',value:String(row.__id)},{label:'Version',value:String(row.__version)}]}})}/> : <Empty title="This dataset is empty">An agent fills it in as it works.</Empty>}</>
}
