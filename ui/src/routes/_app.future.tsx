import { useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { tool } from '../lib/api'
import { TimeChart } from '../features/telemetry/time-chart'
import { DataGrid } from '../features/telemetry/data-grid'
import { unitFormat } from '../features/telemetry/format'
import { describePage } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/future')({errorComponent:p => <RouteError {...p}/>,component:Future})
const HORIZON=120
const runwayText=(months:number|null|undefined) => months===null || months===undefined ? `Over ${Math.round(HORIZON/12)} years` : months>=24 ? `${Math.round(months/12)} years` : `${Math.round(months)} months`
function Future() {
 const {session}=Route.useRouteContext()
 const data=useQuery({queryKey:['future',session.user.id],staleTime:60_000,queryFn:async () => {const model=await tool(session,'model_get',{}),ids=(model.scenarios ?? []).filter(s => !s.archived).map(s => s.id);if(!ids.length)return [];const out=[];for(let i=0;i<ids.length;i+=20)out.push(...((await tool(session,'forecast_run',{scenario_ids:ids.slice(i,i+20),horizon_months:HORIZON,value_in:'EUR'})).series ?? []));return out}})
 describePage({title:'Future',lines:[]})
 const head=<PageHead title="Future" description="How long does this last under each plan?"/>
 if(data.error)return <>{head}<RouteError error={data.error} reset={() => void data.refetch()} title="The forecast didn’t load"/></>
 if(!data.data)return <>{head}<RowsSkeleton rows={10}/></>
 if(!data.data.length)return <>{head}<Empty title="No plans yet">Ask an agent to model a plan and compare it with what you’re doing now.</Empty></>
 // The plan that spends most comes first: the cheapest plan always looks best, and shouldn't lead.
 const series=[...data.data].sort((a,b) => (b.spend ?? 0)-(a.spend ?? 0)),start=Date.parse(`${(series[0].start_as_of || new Date().toISOString()).slice(0,7)}-01T00:00:00Z`),months=Math.max(...series.map(s => s.months?.length ?? 0)),xs=Array.from({length:months},(_,i) => {const d=new Date(start);d.setUTCMonth(d.getUTCMonth()+i);return d.getTime()})
 const lines=series.slice(0,6).map((s,i) => ({key:s.scenario,label:s.name,color:i<5 ? `--c${i}` : '--series-other',values:xs.map((_,k) => s.months?.[k]?.net ?? null)}))
 const rows=series.map(s => ({id:s.scenario,name:s.name,runway:{v:s.runway ?? 1e9,t:runwayText(s.runway)},exGold:{v:s.runway_ex_gold ?? 1e9,t:runwayText(s.runway_ex_gold)},end:{v:s.end_net,t:unitFormat('eur').full(s.end_net)},spend:{v:s.spend,t:`${unitFormat('eur').axis(s.spend*100/100,1)} a month`}}))
 return <>{head}
  <div className="dashboard-grid"><section className="panel" style={{gridColumn:'span 12'}}><TimeChart title="Net worth under each plan" note={`Next ${Math.round(HORIZON/12)} years, by month.${series.length>lines.length ? ` The ${lines.length} costliest of ${series.length} plans; the table has them all.` : ''}`} mark="line" format={unitFormat('eur')} bucket="month" xs={xs} series={lines} direct height={300} legend="isolate"/></section></div>
  <h2 className="tm-section">The plans side by side</h2>
  <DataGrid label="Plans" rowKey={r => String(r.id)} rows={rows} columns={[{key:'name',label:'Plan',type:'text'},{key:'runway',label:'Lasts',type:'number',phoneLabel:true},{key:'exGold',label:'Without gold',type:'number',phoneLabel:true},{key:'end',label:`After ${Math.round(HORIZON/12)} years`,type:'money'},...(series.some(s => s.spend) ? [{key:'spend',label:'Spending',type:'number' as const,phoneLabel:true}] : [])]}/>
 </>
}
