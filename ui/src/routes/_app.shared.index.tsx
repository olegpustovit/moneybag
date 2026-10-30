import { useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { Link, createFileRoute } from '@tanstack/react-router'
import { receivedQuery, sharedQuery, type Session } from '../lib/api'
import { Empty, PageHead, RowsSkeleton } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/shared/')({errorComponent:p => <RouteError {...p}/>,component:Shared})
function SharedRow({session,id,until}:{session:Session;id:string;until?:string|null}) {
 const view=useQuery(sharedQuery(session,id))
 return <li><Link to="/shared/$id" params={{id}}><span className="n">{view.data?.title ?? 'Shared dashboard'}</span><span className="t">{until ? `Until ${new Date(until).toLocaleDateString()}` : 'No end date'}</span></Link></li>
}
function Shared() {
 const {session}=Route.useRouteContext(),received=useQuery(receivedQuery(session))
 const head=<PageHead title="Shared with me" description="Dashboards other people have shared with you."/>
 if(received.error)return <>{head}<RouteError error={received.error} reset={() => void received.refetch()} title="Shared dashboards didn’t load"/></>
 if(!received.data)return <>{head}<RowsSkeleton rows={4}/></>
 const items=received.data.items ?? []
 return <>{head}{items.length ? <ul className="tm-list">{items.map(item => <SharedRow key={item.id} session={session} id={item.id} until={item.expires_at}/>)}</ul> : <Empty title="Nothing shared with you yet">When someone shares a dashboard, it appears here.</Empty>}</>
}
