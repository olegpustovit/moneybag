import { createFileRoute, redirect } from '@tanstack/react-router'
import { DashboardList } from '../features/dashboard/dashboard-list'
import { dashboardsQuery } from '../lib/queries'

export const Route = createFileRoute('/_app/')({
  beforeLoad: async ({context}) => {
    const dashboards=await context.queryClient.query({...dashboardsQuery(context.session),staleTime:'static'})
    const first=dashboards.items?.[0]
    if(first)throw redirect({to:'/dashboards/$id',params:{id:first.id},replace:true})
  },
  component: DashboardList,
})
