import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { bootstrapQuery, flagNumber, saveView, savedViewsQuery, tool, type Session } from '../../lib/api'
import { humanKey } from './page-bits'
import { DetailsDrawer, type DetailsContent } from './details-drawer'
import { closeDetails, closeSaveView, useShell, type DetailsRequest, type MetricRequest } from './shell-store'
import { humanDate } from './format'
import { verbFor } from './verbs'
import './telemetry.css'

const whenLabel=(at:string) => {const d=new Date(at);return Number.isNaN(d.getTime()) ? at : humanDate(d.toISOString())}
function useMetricDetails(session:Session,request:DetailsRequest,metric:MetricRequest):DetailsContent {
 const exact=useQuery({queryKey:['metric-exact',session.user.id,metric],staleTime:30_000,queryFn:() => tool(session,'metric_query',{metric:metric.id,from:metric.from,to:metric.to,compare:metric.compare,filters:metric.filters})})
 const history=useQuery({queryKey:['metric-history',session.user.id,metric.id],staleTime:60_000,retry:false,queryFn:() => tool(session,'history_of',{target_id:metric.id})})
 const data=exact.data as undefined|{text?:string;formula?:string;estimates?:{text?:string;share_pct?:number};entries?:{from?:string;to?:string;accounts?:string[]|null;exclude?:string[]|null};compare?:{text?:string;from?:string;to?:string}|null;unit?:string}
 const accounts=data?.entries?.accounts ?? []
 return {...request.content,value:data?.text ?? request.content.value,
  calculation:{
   summary:exact.isPending ? 'Asking the ledger for the exact figure…' : exact.error ? 'The exact figure could not be read just now. The number on the page comes from the same entries.' : `Exact figure from the ledger${metric.from && metric.to ? `, ${humanDate(metric.from)} to ${humanDate(metric.to)}` : ''}.${metric.usd ? ' Exact figure in EUR; the USD shown uses the EUR/USD rate on each entry’s date.' : ''}${data?.compare?.text ? ` Before that it was ${data.compare.text}.` : ''}${data?.estimates?.share_pct ? ` ${Math.round(data.estimates.share_pct)}% of it is estimated.` : ''}`,
   formula:data?.formula,
   rates:request.content.calculation?.rates,
   entries:data?.entries ? <Link to="/ledger" search={{t:data.entries.from && data.entries.to ? `${data.entries.from} to ${data.entries.to}` : undefined,accounts:accounts.length ? accounts : undefined}} onClick={closeDetails}>See the entries behind this</Link> : undefined,
  },
  history:(history.data?.events ?? []).slice(0,8).map(e => ({at:whenLabel(e.at),actor:e.actor.kind==='agent' ? 'An agent' : e.actor.kind==='system' ? 'System' : 'You',summary:e.summary || verbFor(e.tool)})),
  machine:[...(request.content.machine ?? []),{label:'Metric',value:metric.id},...(accounts.length ? [{label:'Accounts',value:accounts.join(', ')}] : []),...(data?.entries?.exclude?.length ? [{label:'Left out',value:data.entries.exclude.join(', ')}] : [])],
 }
}
const NAME_FIELDS=['label','name','legal_name','brand','raw_name','_row_id']
const cellText=(v:unknown):string => {
 if(v===null || v===undefined)return ''
 if(typeof v!=='object')return String(v)
 // An expanded reference reads as its name, not its id.
 const named=NAME_FIELDS.map(k => (v as Record<string,unknown>)[k]).find(x => typeof x==='string' && x)
 return typeof named==='string' ? named : JSON.stringify(v)
}
// Rows in data tables that explain an entry, such as receipt line items, one table each.
async function explainedRows(session:Session,entry:string,heads:Map<string,{name:string;refs:string[]}>) {
 const details=await tool(session,'entry_get',{id:entry})
 const tables=await Promise.all((details.explained_by ?? []).map(async ({db}) => {
  // join_entry makes row.entry the linked entry column whatever it is called.
  const data=await tool(session,'db_query',{db,where:`row.entry == ${JSON.stringify(entry)}`,join_entry:true,expand:heads.get(db)?.refs,limit:500}).catch(() => null)
  if(!data?.rows?.length)return null
  // Hide columns nobody filled in, so the amounts fit the drawer.
  const columns=(data.columns ?? []).filter(c => c.type!=='entry' && c.type!=='json' && !c.id.startsWith('_') && data.rows!.some(r => cellText(r.cells[c.id])!==''))
  const rows=[...data.rows].sort((a,b) => Number(a.cells.line ?? 0)-Number(b.cells.line ?? 0)).map(r => columns.map(c => cellText(r.cells[c.id])))
  return {title:heads.get(db)?.name ?? humanKey(db),columns:columns.map(c => humanKey(c.id)),rows}
 }))
 return tables.filter(t => t!==null)
}
function Drawer({session,request}:{session:Session;request:DetailsRequest}) {
 const history=useQuery({queryKey:['history',session.user.id,request.historyOf],enabled:!!request.historyOf,staleTime:60_000,retry:false,queryFn:() => tool(session,'history_of',{target_id:request.historyOf!})})
 const tables=new Map((useQuery(bootstrapQuery).data?.tables ?? []).map(t => [t.id,{name:t.name,refs:(t.columns ?? []).filter(c => c.type==='ref').map(c => c.id)}]))
 const explained=useQuery({queryKey:['explained',session.user.id,request.entry],enabled:!!request.entry,staleTime:30_000,retry:false,queryFn:() => explainedRows(session,request.entry!,tables)})
 const content={...request.content,explained:request.content.explained ?? explained.data,history:request.content.history ?? (history.data?.events ?? []).slice(0,8).map(e => ({at:whenLabel(e.at),actor:e.actor.kind==='agent' ? 'An agent' : e.actor.kind==='system' ? 'System' : 'You',summary:e.summary || verbFor(e.tool)}))}
 return <DetailsDrawer open onOpenChange={next => {if(!next)closeDetails()}} content={content} target={request.target} onFlag={async flag => {await flagNumber(session,flag.target,flag.note)}}/>
}
function MetricDrawer({session,request,metric}:{session:Session;request:DetailsRequest;metric:MetricRequest}) {
 const content=useMetricDetails(session,request,metric)
 return <DetailsDrawer open onOpenChange={next => {if(!next)closeDetails()}} content={content} target={request.target} onFlag={async flag => {await flagNumber(session,flag.target,flag.note)}}/>
}
function SaveDialog({session,request}:{session:Session;request:NonNullable<ReturnType<typeof useShell>['saveView']>}) {
 const [name,setName]=useState(request.suggested),[error,setError]=useState(''),[busy,setBusy]=useState(false),client=useQueryClient()
 return <Dialog open onOpenChange={next => {if(!next)closeSaveView()}}>
  <DialogContent className="tm-ask">
   <DialogHeader><DialogTitle>Save this view</DialogTitle><DialogDescription>Keeps this range and these filters for you. Nobody else sees it.</DialogDescription></DialogHeader>
   <form className="tm-form" onSubmit={async e => {e.preventDefault();setBusy(true);setError('');try {await saveView(session,{name:name.trim(),page:request.page,state:request.state});await client.invalidateQueries({queryKey:savedViewsQuery(session).queryKey});closeSaveView()} catch {setError('That did not save. Try again.');setBusy(false)}}}>
    <label className="tm-field-label" htmlFor="tm-view-name">Name</label>
    <input id="tm-view-name" className="tm-input" value={name} required maxLength={120} autoFocus onChange={e => setName(e.target.value)}/>
    <div className="tm-row"><button type="submit" className="tm-btn primary" disabled={busy || !name.trim()}>Save view</button>{error && <span role="alert" className="tm-cel-error">{error}</span>}</div>
   </form>
  </DialogContent>
 </Dialog>
}
export function ShellOverlays({session}:{session:Session}) {
 const {details,saveView:saving}=useShell()
 return <>
  {details && (details.metric ? <MetricDrawer session={session} request={details} metric={details.metric}/> : <Drawer session={session} request={details}/>)}
  {saving && <SaveDialog session={session} request={saving}/>}
 </>
}
