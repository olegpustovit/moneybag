import { useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { importsQuery } from '../lib/api'
import { amountLabel } from '../lib/format'
import { useCube } from '../features/telemetry/cube-store'
import { DataGrid } from '../features/telemetry/data-grid'
import { openDetails } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton, SubNav, humanKey } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/data/imports')({errorComponent:p => <RouteError {...p}/>,component:Imports})
const STATUS:Record<string,string>={staged:'Waiting for review',committed:'Added to the ledger',undone:'Taken back out',discarded:'Discarded'}
const FORMATS:Record<string,string>={wise_csv:'Wise statement',generic_csv:'Bank statement',plaid:'Bank feed',privat_xlsx:'PrivatBank statement'}
function Imports() {
 const {session,bootstrap}=Route.useRouteContext(),cube=useCube(session.user.id),batches=useQuery(importsQuery(session))
 const head=<><PageHead title="Data" description="What feeds this, and can I trust it?"/><SubNav of="data" counts={{'/data/imports':bootstrap.counts.imports,'/data/reconcile':bootstrap.counts.reconcile}}/></>
 if(batches.error)return <>{head}<RouteError error={batches.error} reset={() => void batches.refetch()} title="Imports didn’t load"/></>
 if(!batches.data)return <>{head}<RowsSkeleton/></>
 const rows=batches.data.map(b => ({id:b.id,added:b.created_at.slice(0,10),source:FORMATS[b.format] ?? humanKey(b.format),account:cube?.accounts.find(a => a.id===b.account)?.label ?? humanKey(b.account.split(':').at(-1) ?? b.account),from:b.from,to:b.to,status:STATUS[b.status] ?? humanKey(b.status),closing:b.closing_balance ? {v:Number(b.closing_balance.v),t:amountLabel(b.closing_balance,2)} : null}))
 return <>{head}<p className="tm-lede">Agents import statements and people look them over here. An agent approves or undoes them.</p>{rows.length ? <DataGrid label="Imports" rowKey={r => String(r.id)} rows={rows} columns={[{key:'added',label:'Added',type:'date'},{key:'source',label:'Source',type:'text'},{key:'account',label:'Account',type:'text'},{key:'from',label:'From',type:'date'},{key:'to',label:'To',type:'date'},{key:'status',label:'Status',type:'text'},{key:'closing',label:'Closing balance',type:'money'}]} onDetails={row => openDetails({historyOf:String(row.id),content:{title:String(row.source),subtitle:String(row.status),machine:[{label:'Import',value:String(row.id)}]}})}/> : <Empty title="No imports yet">When an agent brings in a statement it shows up here.</Empty>}</>
}
