import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { DashboardBody } from '../features/telemetry/dashboard-view'
import { PageSkeleton } from '../features/telemetry/page-skeleton'
import { defaultBounds, isoDay } from '../features/telemetry/time-grammar'
import { dashboardSearch, type DashboardSearch } from '../features/telemetry/search'
import { hasView, viewParams, viewQuery } from '../features/telemetry/view-data'
import { useCube } from '../features/telemetry/cube-store'

export const Route=createFileRoute('/_app/dashboards/$id')({
 validateSearch:dashboardSearch,
 // Runs on hover too: the view is ready, or its last good copy is, before the click lands.
 loader:async ({context,params,location}) => {const view=await import('../features/telemetry/view-data'),{defaultBounds:now}=await import('../features/telemetry/time-grammar');await view.prepareView(context.queryClient,context.session.user.id,params.id,view.viewParams((location.search as {t?:string}).t,now()))},
 shouldReload:false,pendingMs:0,pendingMinMs:0,pendingComponent:PageSkeleton,errorComponent:p => <RouteError {...p}/>,component:DashboardRoute,
})
function DashboardRoute() {
 const {session}=Route.useRouteContext(),{id}=Route.useParams(),params=Route.useSearch(),navigate=Route.useNavigate(),cube=useCube(session.user.id),client=useQueryClient()
 const bounds=cube?.bounds ?? defaultBounds(),wanted=viewParams(params.t,bounds),key=`${wanted.from}|${wanted.to}`
 // Stepping quickly sends one request: the server view follows the range 250 ms after it stops moving (at once when that range is cached). The cube answers in between.
 const [settled,setSettled]=useState({key,params:wanted}),cached=hasView(client,session.user.id,id,wanted)
 useEffect(() => {if(key===settled.key)return;if(cached){setSettled({key,params:wanted});return}const h=setTimeout(() => setSettled({key,params:wanted}),250);return () => clearTimeout(h)})
 const view=useQuery(viewQuery(session.user.id,id,settled.params))
 const prefetch=(r:{from:number;to:number}) => {void client.prefetchQuery(viewQuery(session.user.id,id,{from:isoDay(r.from),to:isoDay(r.to)}))}
 const onSearch=(next:Partial<DashboardSearch>,{push}:{push:boolean}) => {void navigate({search:old => ({...old,...next}),replace:!push,resetScroll:false})}
 if(view.error && !view.data)return <RouteError error={view.error} reset={() => void view.refetch()}/>
 if(!view.data)return <PageSkeleton/>
 return <DashboardBody key={id} view={view.data} cube={cube} search={params} onSearch={onSearch} dashboardId={id} prefetch={prefetch} sync={{fetching:view.isFetching || key!==settled.key,failed:view.isError,retry:() => void view.refetch(),at:view.dataUpdatedAt}}/>
}
