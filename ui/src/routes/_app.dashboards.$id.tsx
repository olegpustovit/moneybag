import { useState } from 'react'
import { keepPreviousData, useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { createFileRoute, useRouter } from '@tanstack/react-router'
import { z } from 'zod'
import { Button } from '../components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { DashboardActions } from '../features/dashboard/actions'
import { DashboardEditor } from '../features/dashboard/editor'
import { componentValue } from '../features/dashboard/values'
import { Component } from '../features/dashboard/component'
import { dashboardPreviewQuery, dashboardSpecQuery, dashboardsQuery } from '../lib/queries'

const date = z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional().catch(undefined)
const searchSchema = z.object({ version: z.enum(['draft','published']).optional().catch(undefined), edit: z.union([z.literal('1'),z.literal(1)]).transform(() => '1' as const).optional().catch(undefined), page: z.string().optional().catch(undefined), from: date, to: date, month: z.string().regex(/^\d{4}-\d{2}$/).optional().catch(undefined) })
export const Route = createFileRoute('/_app/dashboards/$id')({
  validateSearch: searchSchema,
  loaderDeps: ({ search }) => ({ version: search.version }),
  loader: async ({ context, params, deps }) => {
    const heads = await context.queryClient.query({ ...dashboardsQuery(context.session), staleTime: 'static' })
    const head = heads.items?.find((item) => item.id === params.id)
    const version = deps.version ?? (head?.published_version ? 'published' : 'draft')
    await context.queryClient.query({ ...dashboardSpecQuery(context.session, params.id, version), staleTime: 'static' })
    return { version, head }
  },
  pendingComponent: () => <div className="dashboard-pending" aria-busy="true">Loading dashboard…</div>,
  errorComponent: DashboardError,
  component: Dashboard,
})
function DashboardError({ reset }: { reset: () => void }) {
  const router = useRouter()
  return <section className="dashboard-error"><h1>Could not load this dashboard</h1><p>It may have been removed or you may not have access.</p><Button onClick={() => { reset(); void router.invalidate() }}>Try again</Button></section>
}
function Dashboard() {
  const { session } = Route.useRouteContext()
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const router = useRouter()
  const [selected, setSelected] = useState<string>()
  const [dirty, setDirty] = useState(false)
  const { version, head } = Route.useLoaderData()
  const { data: dashboard } = useSuspenseQuery(dashboardSpecQuery(session, id, version))
  const params: Record<string, string> = {}
  for (const key of ['from','to','month'] as const) if (search[key]) params[key] = search[key]
  const preview = useQuery({ ...dashboardPreviewQuery(session, id, version, params), placeholderData: keepPreviousData })
  const pages = dashboard.spec.pages ?? []
  const page = pages.find((item) => item.id === search.page) ?? pages[0]
  const components = new Map((dashboard.spec.components ?? []).map((item) => [item.id, item]))
  const reads = JSON.stringify({data:dashboard.spec.data, computed:dashboard.spec.computed, components:dashboard.spec.components})
  const range = reads.includes('params.from') || reads.includes('params.to')
  const month = reads.includes('params.month')
  return <>
    <header className="page-header"><div><p className="breadcrumb"><a href="/">DASHBOARDS</a> / {id}</p><h1>{dashboard.spec.title}</h1><p className="description"><span className="version-badge">{dashboard.status === 'published' ? 'Published' : 'Draft'} v{dashboard.version}</span>{dashboard.author.id && <> · {dashboard.author.kind === 'user' ? 'You' : dashboard.author.id}</>}</p></div>
      <div className="dashboard-toolbar">{head && head.published_version > 0 && head.draft_version > 0 && <label>Version <select value={version} onChange={(event) => void navigate({ search: (old) => ({ ...old, version: event.target.value === 'draft' ? 'draft' : 'published' }) })}><option value="published">Published</option><option value="draft">Draft</option></select></label>}<Button size="sm" onClick={() => {setSelected(undefined);void navigate({search:(old) => ({...old,edit:'1',version:head?.draft_version ? 'draft' : 'published'})})}}>Edit</Button><DashboardActions session={session} id={id} head={head} onPublished={() => {void navigate({search:(old) => ({...old,version:'published',edit:undefined})});void router.invalidate()}}/></div>
    </header>
    {(range || month) && <form key={`${search.from}-${search.to}-${search.month}`} className="range-bar" onSubmit={(event) => { event.preventDefault(); const data = new FormData(event.currentTarget); void navigate({ search: (old) => ({ ...old, from: String(data.get('from') || '') || undefined, to: String(data.get('to') || '') || undefined, month: String(data.get('month') || '') || undefined }) }) }}>
      {range && <><label>From <input name="from" type="date" defaultValue={search.from} /></label><label>To <input name="to" type="date" defaultValue={search.to} /></label></>}{month && <label>Month <input name="month" type="month" defaultValue={search.month} /></label>}<Button type="submit" variant="outline" size="sm">Apply</Button>
    </form>}
    {pages.length > 1 && <nav className="page-tabs" aria-label="Dashboard pages">{pages.map((item) => <button type="button" key={item.id} className={item.id === page?.id ? 'active' : ''} aria-current={item.id === page?.id ? 'page' : undefined} onClick={() => void navigate({ search: (old) => ({ ...old, page: item.id }) })}>{item.title || item.id}</button>)}</nav>}
    {preview.isFetching && !preview.isPending && <div className="refresh-line" role="status" aria-label="Refreshing dashboard"/>}
    {preview.isPending ? <div className="dashboard-pending" aria-busy="true">Loading dashboard values…</div> : preview.isError ? <div className="dashboard-error" role="alert"><p>Could not load dashboard values.</p><Button onClick={() => void preview.refetch()}>Try again</Button></div> : !preview.data.validation.ok ? <div className="dashboard-error" role="alert"><h2>Dashboard needs attention</h2><ul>{(preview.data.validation.errors ?? []).map((issue, index) => <li key={index}>{issue.msg}</li>)}</ul></div> : <div className="dashboard-grid">{(page?.layout ?? []).flatMap((row, rowIndex) => (row.cols ?? []).map((placement, colIndex) => {
      const component = components.get(placement.component)
      const parsed = componentValue.safeParse(preview.data.values[placement.component])
      if (!component || !parsed.success) return <section className="panel" key={`${rowIndex}-${colIndex}`} style={{ gridColumn: `span ${placement.span}` }}><h2>{component?.title || placement.component}</h2><p className="muted">This component could not be displayed.</p></section>
      return <section className="panel" key={`${rowIndex}-${colIndex}`} style={{ gridColumn: `span ${placement.span}` }}><h2>{parsed.data.title || component.title || component.id}</h2><Component value={parsed.data} /><Button size="xs" variant="ghost" onClick={() => {setSelected(component.id);void navigate({search:(old) => ({...old,edit:'1',version:head?.draft_version ? 'draft' : 'published'})})}}>Edit {component.title || component.id}</Button></section>
    }))}</div>}
    <Dialog open={search.edit==='1'} onOpenChange={(open) => {if(!open && (!dirty || window.confirm('Discard unsaved dashboard changes?'))) {setDirty(false);void navigate({search:(old) => ({...old,edit:undefined})})}}}><DialogContent className="dashboard-editor-dialog"><DialogHeader><DialogTitle>Edit dashboard</DialogTitle><DialogDescription>Save creates a new draft. The published dashboard stays unchanged.</DialogDescription></DialogHeader>{search.edit==='1' && <DashboardEditor key={`${id}-${dashboard.version}-${selected ?? ''}`} session={session} id={id} spec={dashboard.spec} initialComponent={selected} onDirty={setDirty} done={(saved) => {if(!saved && dirty && !window.confirm('Discard unsaved dashboard changes?')) return;setDirty(false);void navigate({search:(old) => ({...old,edit:undefined,version:saved ? 'draft' : old.version})});if(saved) void router.invalidate()}}/>}</DialogContent></Dialog>
  </>
}
