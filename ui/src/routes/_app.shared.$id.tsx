import { useSuspenseQuery } from '@tanstack/react-query'
import { createFileRoute, useRouter } from '@tanstack/react-router'
import { z } from 'zod'
import { Button } from '../components/ui/button'
import { Component } from '../features/dashboard/component'
import { componentValue } from '../features/dashboard/values'
import { sharedQuery } from '../features/sharing/queries'

export const Route=createFileRoute('/_app/shared/$id')({validateSearch:z.object({page:z.string().optional().catch(undefined)}),loader:async ({context,params}) => {await context.queryClient.fetchQuery(sharedQuery(context.session,params.id))},pendingComponent:() => <section aria-busy="true"><h1>Shared dashboard</h1><p>Loading dashboard…</p></section>,errorComponent:SharedError,component:SharedDashboard})
function SharedError({reset}:{reset:() => void}) {const router=useRouter();return <section className="dashboard-error"><h1>Shared dashboard unavailable</h1><p>It may have expired or access may have been revoked.</p><Button onClick={() => {reset();void router.invalidate()}}>Try again</Button> <a href="/shared">Back to shares</a></section>}
function SharedDashboard() {
 const {session}=Route.useRouteContext(),{id}=Route.useParams(),search=Route.useSearch(),navigate=Route.useNavigate(),{data}=useSuspenseQuery(sharedQuery(session,id)),pages=data.pages ?? [],page=pages.find(item => item.id===search.page) ?? pages[0]
 return <><header className="page-header"><div><p className="breadcrumb"><a href="/shared">SHARED DASHBOARDS</a></p><h1>{data.title}</h1><p className="description">Shared {data.mode} · {data.role} · v{data.version}</p></div></header>{pages.length>1 && <nav className="page-tabs" aria-label="Dashboard pages">{pages.map(item => <button type="button" key={item.id} className={item.id===page?.id ? 'active' : ''} aria-current={item.id===page?.id ? 'page' : undefined} onClick={() => void navigate({search:{page:item.id}})}>{item.title || item.id}</button>)}</nav>}{data.legacy_html !== undefined ? <div className="shared-legacy" dangerouslySetInnerHTML={{__html:data.legacy_html}}/> : <div className="dashboard-grid">{(page?.layout ?? []).flatMap((row,ri) => (row.cols ?? []).map((placement,ci) => {const parsed=componentValue.safeParse(data.values?.[placement.component]);return <section className="panel" key={`${ri}-${ci}`} style={{gridColumn:`span ${placement.span}`}}>{parsed.success ? <><h2>{parsed.data.title || placement.component}</h2><Component value={parsed.data}/></> : <><h2>{placement.component}</h2><p className="muted">This component could not be displayed.</p></>}</section>}))}</div>}</>
}
