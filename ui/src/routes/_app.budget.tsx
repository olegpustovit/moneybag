import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { Link, createFileRoute } from '@tanstack/react-router'
import { tool } from '../lib/api'
import { budgetSearch } from '../features/telemetry/search'
import { dateLabel } from '../lib/format'
import { Stat } from '../features/telemetry/stat'
import { openDetails } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton } from '../features/telemetry/page-bits'
import { money } from '../features/telemetry/format'

export const Route=createFileRoute('/_app/budget')({validateSearch:budgetSearch,errorComponent:p => <RouteError {...p}/>,component:Budget})
const shift=(month:string,by:number) => {const [y,m]=month.split('-').map(Number),d=new Date(Date.UTC(y,m-1+by,1));return d.toISOString().slice(0,7)}
const eur=(v:number) => money(Math.round(v*100))
function Budget() {
 const {session}=Route.useRouteContext(),{month:param}=Route.useSearch(),now=new Date().toISOString().slice(0,7),month=param ?? now
 const data=useQuery({queryKey:['budget',session.user.id,month],staleTime:30_000,placeholderData:keepPreviousData,queryFn:() => tool(session,'budget_get',{month})})
 const b=data.data,windowLabel=b?.window?.label ?? (b?.window?.months ? `${b.window.months}-month average` : 'recent average'),over=(b?.lines ?? []).filter(l => l.status==='over').length
 const nav=<div className="tm-row tm-month"><Link className="tm-btn square" to="/budget" search={{month:shift(month,-1)}} aria-label="Previous month">←</Link><strong>{dateLabel(month)}</strong>{month<now ? <Link className="tm-btn square" to="/budget" search={{month:shift(month,1)}} aria-label="Next month">→</Link> : <span className="tm-btn square" aria-hidden="true" style={{opacity:.35}}>→</span>}</div>
 const head=<PageHead title="Budget" description={b ? `Am I on pace this month? Averages on this page all use one window: ${windowLabel}.` : 'Am I on pace this month?'}>{nav}</PageHead>
 if(data.error)return <>{head}<RouteError error={data.error} reset={() => void data.refetch()} title="The budget didn’t load"/></>
 if(!b)return <>{head}<RowsSkeleton rows={8}/></>
 const lines=b.lines ?? [],peak=Math.max(1,...lines.flatMap(l => [l.actual,l.limit,l.avg6])),t=b.total,left=t.limit-t.actual
 if(!lines.length)return <>{head}<Empty title="No budget yet">An agent sets one up from what you’ve been spending.</Empty></>
 return <>{head}
  <div className="tm-stats tm-sheet-stats">
   <Stat label="Spent" value={eur(t.actual)} hideEmptyCompare note={`of ${eur(t.limit)} budgeted`}/>
   <Stat label={left>=0 ? 'Left this month' : 'Over budget'} value={eur(Math.abs(left))} hideEmptyCompare note={over ? `${over} ${over===1 ? 'line is' : 'lines are'} over` : 'Every line is within its limit'}/>
   <Stat label={`Average, ${windowLabel}`} value={eur(t.avg6)} hideEmptyCompare note={t.avg6>t.limit ? 'Above this month’s limit' : 'Within this month’s limit'}/>
  </div>
  <div className="tm-budget" role="table" aria-label={`Budget for ${dateLabel(month)}`}>
   <div className="tm-budget-head" role="row"><span role="columnheader">Line</span><span role="columnheader" className="bar">Spent against limit</span><span role="columnheader" className="num">Spent</span><span role="columnheader" className="num">Limit</span><span role="columnheader" className="num">Average</span></div>
   {lines.map(l => {const isOver=l.status==='over';return <div key={l.id} className="tm-budget-row" role="row">
    <span role="cell" className="n"><Link to="/ledger" search={{t:month,accounts:l.accounts?.length ? l.accounts : undefined}} title={`See entries for ${l.name}`}>{l.name}</Link></span>
    <span role="cell" className="bar"><span className="budget-track"><i className={isOver ? 'over' : ''} style={{width:`${l.actual/peak*100}%`}}/><b style={{left:`${l.limit/peak*100}%`}}/></span></span>
    <span role="cell" className={`num${isOver ? ' bad' : ''}`}>{eur(l.actual)}<span className="sr-only">{isOver ? ', over' : ''}</span></span>
    <span role="cell" className="num">{eur(l.limit)}</span>
    <span role="cell" className="num muted"><span className="tm-phone-only">Average </span><button type="button" className="tm-plain" onClick={() => openDetails({historyOf:l.id,target:{metric:`budget:${l.name}`,value:eur(l.actual),params:{month}},content:{title:l.name,value:eur(l.actual),subtitle:dateLabel(month),calculation:{summary:`Spent ${eur(l.actual)} against a limit of ${eur(l.limit)}. The average for ${windowLabel} is ${eur(l.avg6)}.`,entries:<Link to="/ledger" search={{t:month,accounts:l.accounts?.length ? l.accounts : undefined}}>See the entries</Link>},machine:(l.accounts ?? []).map(a => ({label:'Account ID',value:a}))}})}>{eur(l.avg6)}</button></span>
   </div>})}
  </div>
  {(b.unmapped?.length ?? 0)>0 && <p className="tm-lede">{b.unmapped!.length} {b.unmapped!.length===1 ? 'spending account isn’t' : 'spending accounts aren’t'} in any budget line yet. An agent can add {b.unmapped!.length===1 ? 'it' : 'them'}.</p>}
 </>
}
