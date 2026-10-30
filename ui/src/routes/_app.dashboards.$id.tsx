import { useRef, useState } from 'react'
import { useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { Link, createFileRoute, useBlocker, useRouter } from '@tanstack/react-router'
import { z } from 'zod'
import { Button } from '../components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { DashboardActions } from '../features/dashboard/actions'
import { DashboardEditor } from '../features/dashboard/editor'
import { DateRange } from '../features/dashboard/date-range'
import { componentValue } from '../features/dashboard/values'
import { Component } from '../features/dashboard/component'
import { DashboardSkeleton } from '../features/dashboard/skeleton'
import { useLoadingSkeleton } from '../components/use-loading-skeleton'
import { tableSort } from '../features/dashboard/table-sort'
import { relativeTime } from '../lib/format'
import { agentsQuery, dashboardPreviewQuery, dashboardSpecQuery, dashboardsQuery } from '../lib/queries'

const validDate=(value:string) => /^\d{4}-\d{2}-\d{2}$/.test(value) && !Number.isNaN(Date.parse(`${value}T00:00:00Z`)) && new Date(`${value}T00:00:00Z`).toISOString().slice(0,10)===value
const date = z.string().refine(value => value==='' || validDate(value)).optional().catch(undefined)
function readsParam(value:unknown,key:'from'|'to'|'month'):boolean {if(typeof value==='string')return new RegExp(`\\bparams\\s*(?:\\.\\s*${key}\\b|\\[\\s*['"]${key}['"]\\s*\\])`).test(value);if(Array.isArray(value))return value.some(item => readsParam(item,key));if(value && typeof value==='object')return Object.values(value).some(item => readsParam(item,key));return false}
const searchSchema = z.object({ sort:tableSort, sort_table:z.string().max(200).optional().catch(undefined), version: z.enum(['draft','published']).optional().catch(undefined), edit: z.union([z.literal('1'),z.literal(1)]).transform(() => '1' as const).optional().catch(undefined), page: z.string().optional().catch(undefined), from: date, to: date, all: z.boolean().optional().catch(undefined), month: z.string().regex(/^\d{4}-(0[1-9]|1[0-2])$/).optional().catch(undefined) })
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
  pendingMs: 150,
  pendingMinMs: 300,
  pendingComponent: () => <div className="dashboard-pending" aria-busy="true">Loading dashboard…</div>,
  errorComponent: DashboardError,
  component: Dashboard,
})
function DashboardError({ error, reset }: { error: unknown; reset: () => void }) {
  const router = useRouter()
  return <section className="dashboard-error"><h1>Could not load this dashboard</h1><p>{error instanceof Error ? error.message : String(error)}</p><Button onClick={() => { reset(); void router.invalidate() }}>Try again</Button></section>
}
function Dashboard() {
  const { session } = Route.useRouteContext()
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const router = useRouter()
  const [selected, setSelected] = useState<string>()
  const [dirty, setDirty] = useState(false)
  const dirtyRef = useRef(false)
  function markDirty(value:boolean) {dirtyRef.current=value;setDirty(value)}
  useBlocker({shouldBlockFn:({current,next}) => {
    if (!dirtyRef.current) return false
    const leaving=current.pathname!==next.pathname || (next.routeId==='/_app/dashboards/$id' && (next.search.edit!=='1' || next.search.version!==search.version))
    if (!leaving) return false
    if (!window.confirm('Discard unsaved dashboard changes?')) return true
    markDirty(false);return false
  },enableBeforeUnload:() => dirtyRef.current})
  const { version, head } = Route.useLoaderData()
  const { data: dashboard } = useSuspenseQuery(dashboardSpecQuery(session, id, version))
  const agents = useQuery({...agentsQuery(session),enabled:dashboard.author.kind==='agent'})
  const author = dashboard.author.kind==='agent' ? agents.data?.find(agent => agent.client_id===dashboard.author.id)?.client_name || 'Connected agent' : dashboard.author.kind==='human' || dashboard.author.kind==='user' ? dashboard.author.id===session.user.id ? 'You' : 'Collaborator' : dashboard.author.kind || 'Unknown'
  const publishedAt = dashboard.published_at && relativeTime(dashboard.published_at)
  const params: Record<string, string> = {}
  for (const key of ['from','to','month'] as const) if (search[key] !== undefined) params[key] = search[key]
  if(search.all){params.from='';params.to=''}
  const preview = useQuery({ ...dashboardPreviewQuery(session, id, version, params), placeholderData: (previous, query) => query?.queryKey[0]==='dashboard' && query.queryKey[1]===session.user.id && query.queryKey[2]===id && query.queryKey[3]===version ? previous : undefined })
  const showSkeleton = useLoadingSkeleton(preview.isPending)
  const pages = dashboard.spec.pages ?? []
  const page = pages.find((item) => item.id === search.page) ?? pages[0]
  const components = new Map((dashboard.spec.components ?? []).map((item) => [item.id, item]))
  const tableCount=(page?.layout ?? []).flatMap(row => row.cols ?? []).filter(item => {const type=components.get(item.component)?.type;return type==='table' || type==='scenario_compare' || type==='chart'}).length
  const reads={data:dashboard.spec.data,computed:dashboard.spec.computed,components:dashboard.spec.components}
  const range = readsParam(reads,'from') || readsParam(reads,'to')
  const month = readsParam(reads,'month')
  return <>
    <header className="page-header"><div><p className="breadcrumb"><Link to="/">DASHBOARDS</Link> / {id}</p><h1>{dashboard.spec.title}</h1><p className="description"><span className="version-badge">{dashboard.status === 'published' ? 'Published' : 'Draft'} v{dashboard.version}</span>{dashboard.author.id && <> · {dashboard.status==='published' ? 'Published' : 'Edited'} by {author}</>}{publishedAt && <> · <time dateTime={dashboard.published_at || undefined} title={new Date(dashboard.published_at!).toLocaleString()}>{publishedAt}</time></>}</p></div>
      <div className="dashboard-toolbar">{head && head.published_version > 0 && head.draft_version > 0 && <label>Version <select value={version} onChange={(event) => void navigate({ search: (old) => ({ ...old, version: event.target.value === 'draft' ? 'draft' : 'published' }) })}><option value="published">Published</option><option value="draft">Draft</option></select></label>}<Button size="sm" onClick={() => {setSelected(undefined);void navigate({search:(old) => ({...old,edit:'1',version:head?.draft_version ? 'draft' : 'published'})})}}>Edit</Button><DashboardActions session={session} id={id} head={head} onPublished={() => {void navigate({search:(old) => ({...old,version:'published',edit:undefined})});void router.invalidate()}} onUndoPublication={() => {void navigate({search:(old) => ({...old,version:'draft',edit:undefined})});void router.invalidate()}}/></div>
    </header>
    {(range || month) && <DateRange range={range} month={month} values={search} defaults={{from:typeof dashboard.spec.params?.from==='string' && validDate(dashboard.spec.params.from) ? dashboard.spec.params.from : undefined,to:typeof dashboard.spec.params?.to==='string' && validDate(dashboard.spec.params.to) ? dashboard.spec.params.to : undefined,month:typeof dashboard.spec.params?.month==='string' && /^\d{4}-(0[1-9]|1[0-2])$/.test(dashboard.spec.params.month) ? dashboard.spec.params.month : undefined}} onChange={value => void navigate({search:old => ({...old,from:value.from,to:value.to,month:value.month,all:value.all || undefined})})}/>}
    {pages.length > 1 && <nav className="page-tabs" aria-label="Dashboard pages">{pages.map((item) => <button type="button" key={item.id} className={item.id === page?.id ? 'active' : ''} aria-current={item.id === page?.id ? 'page' : undefined} onClick={() => void navigate({ search: (old) => ({ ...old, page: item.id }) })}>{item.title || item.id}</button>)}</nav>}
    {preview.isFetching && !preview.isPending && <div className="refresh-line" role="status" aria-label="Refreshing dashboard"/>}
    {preview.isPending || showSkeleton ? <div role="status" aria-busy="true"><span className="sr-only">Loading dashboard values…</span>{showSkeleton && <DashboardSkeleton page={page} components={components}/>}</div> : preview.isError ? <div className="dashboard-error" role="alert"><p>{preview.error.message}</p><Button onClick={() => void preview.refetch()}>Try again</Button></div> : <>{!preview.data.validation.ok && <div className="dashboard-error" role="alert"><h2>Dashboard needs attention</h2><ul>{(preview.data.validation.errors ?? []).map((issue,index) => <li key={index}>{issue.msg}</li>)}</ul></div>}<div className="dashboard-grid">{(page?.layout ?? []).flatMap((row, rowIndex) => (row.cols ?? []).map((placement, colIndex) => {
      const component = components.get(placement.component)
      const parsed = componentValue.safeParse(preview.data.values[placement.component])
      const issues=(preview.data.validation.errors ?? []).filter(issue => issue.component_id===placement.component)
      return <section className="panel" key={`${rowIndex}-${colIndex}`} style={{ gridColumn: `span ${Math.max(1,Math.min(12,placement.span))}` }}><h2>{parsed.success ? parsed.data.title || component?.title || placement.component : component?.title || placement.component}</h2>{issues.length ? <div className="component-issues" role="alert">{issues.map((issue,index) => <div key={index}><p>{issue.msg}</p>{issue.expr && <pre>{issue.expr}</pre>}{issue.hint && <p>{issue.hint}</p>}</div>)}</div> : parsed.success ? <Component value={parsed.data} drilldown tableId={`dashboard:${id}:${placement.component}`} sort={search.sort_table===placement.component || !search.sort_table && tableCount===1 ? search.sort : undefined} onSortChange={sort => void navigate({search:old => ({...old,sort,sort_table:sort ? placement.component : undefined})})}/> : <p className="muted">This component could not be displayed.</p>}{component && <Button size="xs" variant="ghost" onClick={() => {setSelected(component.id);void navigate({search:(old) => ({...old,edit:'1',version:head?.draft_version ? 'draft' : 'published'})})}}>Edit {component.title || component.id}</Button>}</section>
    }))}</div></>}
    <Sheet open={search.edit==='1'} onOpenChange={(open) => {if(!open && (!dirty || window.confirm('Discard unsaved dashboard changes?'))) {markDirty(false);void navigate({search:(old) => ({...old,edit:undefined})})}}}><SheetContent className="dashboard-editor-sheet"><SheetHeader><SheetTitle>Edit dashboard</SheetTitle><SheetDescription>Save creates a new draft. The published dashboard stays unchanged.</SheetDescription></SheetHeader>{search.edit==='1' && <DashboardEditor key={`${id}-${dashboard.version}-${selected ?? ''}`} session={session} id={id} spec={dashboard.spec} initialComponent={selected} onDirty={markDirty} done={(saved) => {if(!saved && dirty && !window.confirm('Discard unsaved dashboard changes?')) return;markDirty(false);void navigate({search:(old) => ({...old,edit:undefined,version:saved ? 'draft' : old.version})});if(saved) void router.invalidate()}}/>}</SheetContent></Sheet>
  </>
}
