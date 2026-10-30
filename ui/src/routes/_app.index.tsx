import { createFileRoute, redirect } from '@tanstack/react-router'
import { homeDashboard } from '../features/telemetry/nav'

export const Route=createFileRoute('/_app/')({
 beforeLoad:({context}) => {const home=homeDashboard(context.bootstrap.dashboards);if(home)throw redirect({to:'/dashboards/$id',params:{id:home.id},replace:true})},
 component:() => <div className="empty"><h1>Nothing here yet</h1><p>Agents build your pages. Connect one from Settings and the dashboards it sets up appear here.</p></div>,
})
