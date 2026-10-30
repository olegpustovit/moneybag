import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { flagNumber, saveView, savedViewsQuery, tool, type Session } from '../../lib/api'
import { DetailsDrawer, type DetailsContent } from './details-drawer'
import { agentPrompt, closeAsk, closeDetails, closeSaveView, useShell, type DetailsRequest, type MetricRequest } from './shell-store'
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
function Drawer({session,request}:{session:Session;request:DetailsRequest}) {
 const history=useQuery({queryKey:['history',session.user.id,request.historyOf],enabled:!!request.historyOf,staleTime:60_000,retry:false,queryFn:() => tool(session,'history_of',{target_id:request.historyOf!})})
 const content={...request.content,history:request.content.history ?? (history.data?.events ?? []).slice(0,8).map(e => ({at:whenLabel(e.at),actor:e.actor.kind==='agent' ? 'An agent' : e.actor.kind==='system' ? 'System' : 'You',summary:e.summary || verbFor(e.tool)}))}
 return <DetailsDrawer open onOpenChange={next => {if(!next)closeDetails()}} content={content} target={request.target} onFlag={async flag => {await flagNumber(session,flag.target,flag.note)}}/>
}
function MetricDrawer({session,request,metric}:{session:Session;request:DetailsRequest;metric:MetricRequest}) {
 const content=useMetricDetails(session,request,metric)
 return <DetailsDrawer open onOpenChange={next => {if(!next)closeDetails()}} content={content} target={request.target} onFlag={async flag => {await flagNumber(session,flag.target,flag.note)}}/>
}
function AskDialog({question,page}:{question:string;page:Parameters<typeof agentPrompt>[1]}) {
 const [text,setText]=useState(question),[copied,setCopied]=useState(false),prompt=agentPrompt(text,page)
 return <Dialog open onOpenChange={next => {if(!next)closeAsk()}}>
  <DialogContent className="tm-ask">
   <DialogHeader><DialogTitle>Ask an agent</DialogTitle><DialogDescription>Moneybag doesn’t talk to agents itself. Paste this into the one you use. It already says what you’re looking at.</DialogDescription></DialogHeader>
   <label className="tm-field-label" htmlFor="tm-ask-question">Your question</label>
   <input id="tm-ask-question" className="tm-input" value={text} autoFocus onChange={e => {setText(e.target.value);setCopied(false)}}/>
   <label className="tm-field-label" htmlFor="tm-ask-prompt">What gets copied</label>
   <textarea id="tm-ask-prompt" className="tm-textarea" rows={9} readOnly value={prompt}/>
   <div className="tm-row"><button type="button" className="tm-btn primary" onClick={async () => {try {await navigator.clipboard.writeText(prompt);setCopied(true)} catch {document.getElementById('tm-ask-prompt')?.focus()}}}>{copied ? 'Copied' : 'Copy prompt'}</button><span role="status" className="tm-muted">{copied ? 'Paste it into your agent.' : ''}</span></div>
  </DialogContent>
 </Dialog>
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
 const {details,ask,saveView:saving,page}=useShell()
 return <>
  {details && (details.metric ? <MetricDrawer session={session} request={details} metric={details.metric}/> : <Drawer session={session} request={details}/>)}
  {ask && <AskDialog question={ask.question} page={page}/>}
  {saving && <SaveDialog session={session} request={saving}/>}
 </>
}
