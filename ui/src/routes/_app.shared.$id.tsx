import { useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { Link, createFileRoute } from '@tanstack/react-router'
import { sharedQuery } from '../lib/api'
import { Panel, type PanelCtx } from '../features/telemetry/panels'
import { defaultBounds } from '../features/telemetry/time-grammar'
import { RowsSkeleton } from '../features/telemetry/page-bits'
import { parseRange } from '../features/telemetry/time-grammar'

export const Route=createFileRoute('/_app/shared/$id')({errorComponent:p => <RouteError {...p}/>,component:SharedDashboard})
function SharedDashboard() {
 const {session}=Route.useRouteContext(),{id}=Route.useParams(),data=useQuery(sharedQuery(session,id)),bounds=defaultBounds()
 const head=(title:string,sub?:string) => <header className="page-header"><div><p className="breadcrumb"><Link to="/shared" className="tm-crumb">← Shared with me</Link></p><h1>{title}</h1>{sub && <p className="description">{sub}</p>}</div></header>
 if(data.error)return <>{head('Shared dashboard')}<RouteError error={data.error} reset={() => void data.refetch()} title="This share is unavailable"/></>
 if(!data.data)return <>{head('Shared dashboard')}<RowsSkeleton rows={8}/></>
 const view=data.data,range=parseRange('12mo',bounds)!
 // A share has its own values and no cube, so every panel draws what the server sent.
 const ctx:PanelCtx={cube:null,range,win:null,compareMode:'none',defaultCompare:'none',pills:[],filtering:false,dashboard:id,syncKey:`share-${id}`,bounds,explore:[],readOnly:true,setRange:() => {},stepOut:() => {},addPill:() => {}}
 if(view.legacy_html)return <>{head(view.title,`Shared ${view.at ? new Date(view.at).toLocaleDateString() : ''}`)}<div className="shared-legacy" dangerouslySetInnerHTML={{__html:view.legacy_html}}/></>
 return <>{head(view.title,view.mode==='live' ? 'Live' : view.at ? `As of ${new Date(view.at).toLocaleDateString()}` : undefined)}{(view.pages ?? []).map(page => <div key={page.id} className="dashboard-grid">{page.layout.flatMap((row,ri) => (row.cols ?? []).map((col,ci) => <Panel key={`${ri}-${ci}`} c={{id:col.component,type:'unknown'}} value={view.values?.[col.component]} ctx={ctx} span={col.span}/>))}</div>)}</>
}
