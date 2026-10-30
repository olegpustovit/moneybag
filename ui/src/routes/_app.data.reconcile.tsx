import { useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { tool } from '../lib/api'
import { amountLabel, dateLabel } from '../lib/format'
import { useCube } from '../features/telemetry/cube-store'
import { describePage, openDetails } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton, SubNav, ago, humanKey } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/data/reconcile')({errorComponent:p => <RouteError {...p}/>,component:Reconcile})
function Reconcile() {
 const {session,bootstrap}=Route.useRouteContext(),cube=useCube(session.user.id),status=useQuery({queryKey:['reconcile',session.user.id],staleTime:30_000,queryFn:() => tool(session,'reconcile_status',{})})
 describePage({title:'Reconcile',lines:[`${bootstrap.counts.reconcile} open differences`]})
 const head=<><PageHead title="Data" description="What feeds this, and can I trust it?"/><SubNav of="data" counts={{'/data/imports':bootstrap.counts.imports,'/data/reconcile':bootstrap.counts.reconcile}}/></>
 if(status.error)return <>{head}<RouteError error={status.error} reset={() => void status.refetch()} title="Reconcile didn’t load"/></>
 if(!status.data)return <>{head}<RowsSkeleton/></>
 const name=(id:string) => cube?.accounts.find(a => a.id===id)?.label ?? humanKey(id.split(':').at(-1) ?? id),accounts=[...(status.data.accounts ?? [])].sort((a,b) => (b.open_discrepancies?.length ?? 0)-(a.open_discrepancies?.length ?? 0))
 return <>{head}<p className="tm-lede">Each account’s balance against its statement. To resolve a difference, ask an agent.</p>
  {accounts.length ? <ul className="tm-list static">{accounts.map(a => {const open=a.open_discrepancies ?? [];return <li key={a.account}><div><span className="n">{name(a.account)}</span><span className={`t${open.length ? ' bad' : ' good'}`}>{open.length ? `${open.length} open ${open.length===1 ? 'difference' : 'differences'}` : a.last_ok ? `Reconciled ${a.last_ok.includes('T') ? ago(a.last_ok) : dateLabel(a.last_ok)}` : 'Not checked yet'}</span></div>
   {open.map(d => <button key={d.id} type="button" className="tm-difference" onClick={() => openDetails({historyOf:d.id,target:{value:amountLabel(d.diff,2),params:{account:a.account}},content:{title:name(a.account),value:amountLabel(d.diff,2),subtitle:'Difference between the ledger and the statement.',machine:[{label:'Account ID',value:d.account},{label:'Difference',value:d.id},{label:'Check',value:d.assertion_id},...(d.reason ? [{label:'Reason',value:d.reason,mono:false}] : [])]}})}><span>Off by {amountLabel(d.diff,2)}</span><span className="tm-details-link">Details</span></button>)}
  </li>})}</ul> : <Empty title="Nothing to reconcile yet">Agents check balances against statements as they import them.</Empty>}
 </>
}
