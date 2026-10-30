import { useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { DashboardBody } from '../features/telemetry/dashboard-view'
import { PageSkeleton } from '../features/telemetry/page-skeleton'
import { defaultBounds } from '../features/telemetry/time-grammar'
import { dashboardSearch, type DashboardSearch } from '../features/telemetry/search'
import { viewParams, viewQuery } from '../features/telemetry/view-data'
import { useCube } from '../features/telemetry/cube-store'

export const Route=createFileRoute('/_app/dashboards/$id')({
 validateSearch:dashboardSearch,
 // Runs on hover too: the view is ready, or its last good copy is, before the click lands.
 loader:async ({context,params,location}) => {const view=await import('../features/telemetry/view-data'),{defaultBounds:now}=await import('../features/telemetry/time-grammar');await view.prepareView(context.queryClient,context.session.user.id,params.id,view.viewParams((location.search as {t?:string}).t,now()))},
 shouldReload:false,pendingMs:0,pendingMinMs:0,pendingComponent:PageSkeleton,errorComponent:p => <RouteError {...p}/>,component:DashboardRoute,
})
function DashboardRoute() {
 const {session}=Route.useRouteContext(),{id}=Route.useParams(),params=Route.useSearch(),navigate=Route.useNavigate(),cube=useCube(session.user.id)
 const bounds=cube?.bounds ?? defaultBounds(),view=useQuery(viewQuery(session.user.id,id,viewParams(params.t,bounds)))
 const onSearch=(next:Partial<DashboardSearch>,{push}:{push:boolean}) => {void navigate({search:old => ({...old,...next}),replace:!push,resetScroll:false})}
 if(view.error && !view.data)return <RouteError error={view.error} reset={() => void view.refetch()}/>
 if(!view.data)return <PageSkeleton/>
 return <DashboardBody key={id} view={view.data} cube={cube} search={params} onSearch={onSearch} dashboardId={id}/>
}
