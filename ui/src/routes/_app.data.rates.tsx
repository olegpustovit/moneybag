import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { commodityLabel } from '../lib/format'
import { useCube } from '../features/telemetry/cube-store'
import { rateSeries } from '../features/telemetry/cube'
import { TimeBar } from '../features/telemetry/time-bar'
import { TimeChart } from '../features/telemetry/time-chart'
import { percent, unitFormat } from '../features/telemetry/format'
import { telemetrySearch } from '../features/telemetry/search'
import { useTimeState, type SearchWriter } from '../features/telemetry/use-time-state'
import { defaultBounds } from '../features/telemetry/time-grammar'
import { describePage } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton, SubNav } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/data/rates')({validateSearch:telemetrySearch,errorComponent:p => <RouteError {...p}/>,component:Rates})
const unit=(code:string) => code.length===3 ? code : commodityLabel(code)
const pairName=(pair:string) => {const [a,b]=pair.split('/');return `${unit(a)} in ${unit(b)}`}
const COLORS=['--c0','--c1','--c2','--c3','--c4']
function Rates() {
 const {session,bootstrap}=Route.useRouteContext(),params=Route.useSearch(),navigate=Route.useNavigate(),cube=useCube(session.user.id),bounds=cube?.bounds ?? defaultBounds()
 const onSearch:SearchWriter=(next,{push}) => {void navigate({search:old => ({...old,...next}),replace:!push,resetScroll:false})}
 const time=useTimeState({bounds,search:params,onSearch,defaultText:'12mo',defaultCompare:'none'}),pairs=Object.keys(cube?.rates ?? {}).sort()
 describePage({title:'Rates',lines:[]})
 return <>
  <PageHead title="Data" description="What feeds this, and can I trust it?"/>
  <SubNav of="data" counts={{'/data/imports':bootstrap.counts.imports,'/data/reconcile':bootstrap.counts.reconcile}}/>
  {!cube ? <RowsSkeleton/> : !pairs.length ? <Empty title="No rates yet">Quotes appear here once an agent or a feed records them.</Empty> : <>
   <div className="tm-sheet"><TimeBar state={time} bounds={bounds} hideCompare/></div>
   <div className="dashboard-grid">{pairs.map((pair,i) => {
    const s=rateSeries(cube,pair,time.range),first=s.values[0],last=s.values.at(-1),change=first && last ? (last-first)/first*100 : null
    if(s.values.length<2)return <section className="panel" key={pair} style={{gridColumn:'span 6'}}><h2>{pairName(pair)}</h2><p className="tm-stat-note">{last===undefined ? 'No quotes in this range.' : `One quote in this range: ${unitFormat('rate').full(last)}. A line needs two.`}</p></section>
    return <section className="panel" key={pair} style={{gridColumn:`span ${pairs.length===1 ? 12 : 6}`}}><TimeChart title={pairName(pair)} note={last!==undefined ? `Latest ${unitFormat('rate').full(last)}${change===null ? '' : `, ${change>=0 ? '+' : ''}${percent(change)} in this range`}` : 'No quotes in this range.'} mark="line" baseline="auto" format={unitFormat('rate')} xs={s.xs} series={[{key:pair,label:pairName(pair),color:COLORS[i%5],values:s.values}]} range={time.range} bounds={bounds} onRangeChange={(r,{final}) => time.setRange(r,{final})} onStepOut={time.zoomOut} syncKey="rates" height={220}/></section>
   })}</div>
  </>}
 </>
}
