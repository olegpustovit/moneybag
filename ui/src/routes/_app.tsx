import { useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { createFileRoute, Outlet, redirect, useLocation, useRouter } from '@tanstack/react-router'
import { Activity, BookOpen, Boxes, ChartNoAxesCombined, ChevronRight, CircleDollarSign, ClipboardCheck, Database, FileInput, LayoutDashboard, Menu, Settings, Share2, Wallet } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { Toaster } from 'sonner'
import { ApiError, sessionQuery } from '../lib/api'
import { dashboardsQuery, tablesQuery } from '../lib/queries'
import { Button } from '../components/ui/button'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from '../components/ui/sheet'

export const Route = createFileRoute('/_app')({
  beforeLoad: async ({ context, location }) => {
    try { return { session: await context.queryClient.query({ ...sessionQuery, staleTime: 'static' }) } }
    catch (error) { if (error instanceof ApiError && error.status === 401) throw redirect({ href: `/login?next=${encodeURIComponent(location.href)}` }); throw error }
  },
  loader: async ({ context }) => { await Promise.all([context.queryClient.query({ ...dashboardsQuery(context.session), staleTime: 'static' }), context.queryClient.query({ ...tablesQuery(context.session), staleTime: 'static' })]) },
  pendingComponent: () => <main className="loading" aria-busy="true">Loading workspace…</main>,
  errorComponent: WorkspaceError,
  component: Workspace,
})

function WorkspaceError({ error, reset }: { error: unknown; reset: () => void }) {
  const router = useRouter()
  return <main className="loading"><h1>Could not load your workspace</h1><p>{error instanceof Error ? error.message : String(error)}</p><Button onClick={() => { reset(); void router.invalidate() }}>Try again</Button></main>
}

type NavLink = { href: string; label: string; icon: LucideIcon }
const records: NavLink[] = [
  { href: '/accounts', label: 'Accounts', icon: Wallet }, { href: '/journal', label: 'Journal', icon: BookOpen },
  { href: '/budgets', label: 'Budgets', icon: CircleDollarSign }, { href: '/scenarios', label: 'Scenarios', icon: ChartNoAxesCombined },
]
const workflows: NavLink[] = [
  { href: '/imports', label: 'Imports', icon: FileInput }, { href: '/reconcile', label: 'Reconcile', icon: ClipboardCheck },
  { href: '/shared', label: 'Shared with me', icon: Share2 }, { href: '/activity', label: 'Activity', icon: Activity },
]
async function navCount(path:string):Promise<number> {
  const response=await fetch(path,{credentials:'same-origin'})
  if(!response.ok) throw new ApiError('Could not load navigation count.',response.status)
  const data=await response.json() as {count:unknown}
  if(typeof data.count!=='number' || !Number.isSafeInteger(data.count) || data.count<0) throw new Error('Invalid navigation count.')
  return data.count
}
function Navigation({ compact = false }: { compact?: boolean }) {
  const { session } = Route.useRouteContext()
  const { data: dashboards } = useSuspenseQuery(dashboardsQuery(session))
  const { data: tables } = useSuspenseQuery(tablesQuery(session))
  const path = useLocation({ select: (location) => location.pathname })
  const imports = useQuery({queryKey:['imports',session.user.id,'count'],queryFn:() => navCount('/api/imports/count'),staleTime:30_000,refetchInterval:60_000})
  const discrepancies = useQuery({queryKey:['reconcile',session.user.id,'count'],queryFn:() => navCount('/api/reconcile/count'),staleTime:30_000,refetchInterval:60_000})
  const link = ({ href, label, icon: Icon }: NavLink) => {const count=href==='/imports' ? imports.data : href==='/reconcile' ? discrepancies.data : undefined,description=href==='/imports' ? 'staged imports' : 'open discrepancies';return <a key={href} href={href} title={compact ? count ? `${label}: ${count} ${description}` : label : undefined} className={path === href || path.startsWith(`${href}/`) ? 'nav-link active' : 'nav-link'} aria-current={path === href ? 'page' : undefined}><Icon aria-hidden="true" size={16} /><span className="nav-label">{label}</span>{!!count && <span className="nav-count" aria-label={`${count} ${description}`}>{count}</span>}</a>}
  return <div className="navigation">
    <a className="workspace" href="/"><span className="workspace-mark" aria-hidden="true">M</span><span className="nav-label workspace-name">{session.user.name}’s money</span></a>
    <nav aria-label="Workspace">
      <div className="nav-group">Dashboards</div>
      {(dashboards.items ?? []).map((dashboard) => <a key={dashboard.id} href={`/dashboards/${encodeURIComponent(dashboard.id)}`} title={dashboard.title} className={path === `/dashboards/${dashboard.id}` ? 'nav-link active' : 'nav-link'} aria-current={path === `/dashboards/${dashboard.id}` ? 'page' : undefined}><LayoutDashboard aria-hidden="true" size={16}/><span className="nav-label nav-title">{dashboard.title}</span>{dashboard.draft_version > 0 && <span className="draft-dot" title="Has a draft" aria-label="Has a draft" />}</a>)}
      {!(dashboards.items ?? []).length && <span className="nav-empty">No dashboards yet</span>}
      <div className="nav-group">Workspace</div>{records.map(link)}
      <div className="nav-group">Databases</div>
      {(tables.items ?? []).map((table) => link({ href: `/databases/${encodeURIComponent(table.id)}`, label: table.name, icon: Database }))}
      {!(tables.items ?? []).length && <span className="nav-empty">No databases yet</span>}
      <div className="nav-group">Manage</div>{workflows.map(link)}
    </nav>
    <div className="nav-footer">{link({ href: '/settings', label: 'Settings', icon: Settings })}<span className="nav-label version">{session.version}</span></div>
  </div>
}
function Workspace() {
  return <><a href="#main" className="skip">Skip to content</a><div className="workspace-shell">
    <aside className="desktop-sidebar"><Navigation compact /></aside>
    <div className="workspace-content"><header className="mobile-header"><Sheet><SheetTrigger render={<Button variant="ghost" size="icon" aria-label="Open navigation" />}><Menu /></SheetTrigger><SheetContent side="left" className="mobile-sheet"><SheetHeader><SheetTitle>Workspace navigation</SheetTitle></SheetHeader><Navigation /></SheetContent></Sheet><span className="mobile-brand"><Boxes size={17} aria-hidden="true" /> Moneybag</span><ChevronRight size={14} aria-hidden="true"/></header><main id="main" className="main-content"><Outlet /></main></div>
  </div><Toaster position="bottom-right" closeButton toastOptions={{style:{background:'var(--paper)',color:'var(--text)',border:'1px solid var(--bd)',borderRadius:0}}}/></>
}
