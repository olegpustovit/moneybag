import { useContext } from 'react'
import { useSuspenseQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { LayoutDashboard, ArrowUpRight } from 'lucide-react'
import { dashboardsQuery } from '../lib/queries'
import { CreateWorkspaceContext } from '../features/workspace/create'
import { Button } from '../components/ui/button'

export const Route = createFileRoute('/_app/')({ component: Home })
function Home() {
  const { session } = Route.useRouteContext()
  const create=useContext(CreateWorkspaceContext)
  const { data } = useSuspenseQuery(dashboardsQuery(session))
  const dashboards = data.items ?? []
  return <><header className="page-header"><div><p className="breadcrumb">WORKSPACE / DASHBOARDS</p><h1>Dashboards</h1><p className="description">Your saved views, in one place.</p></div></header>
    <section aria-label="Dashboards" className="dashboard-list">
      {dashboards.length === 0 ? <div className="empty"><LayoutDashboard size={22} aria-hidden="true"/><h2>No dashboards yet</h2><p>Create a dashboard to start building your first view.</p><Button onClick={() => create?.('dashboard')}>Create dashboard</Button></div> : dashboards.map((dashboard) => <Link className="dashboard-row" key={dashboard.id} to="/dashboards/$id" params={{id:dashboard.id}}><LayoutDashboard size={17} aria-hidden="true"/><span className="dashboard-row-title"><strong>{dashboard.title}</strong><span>{dashboard.id}</span></span>{dashboard.draft_version > 0 && <span className="draft-badge">Draft</span>}<ArrowUpRight size={16} aria-hidden="true"/></Link>)}
    </section>
  </>
}
