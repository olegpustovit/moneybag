import { useEffect, useState } from 'react'
import { RouteError } from '../features/telemetry/route-error'
import { useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { Link, Outlet, createFileRoute, redirect, useLocation, useNavigate, useRouter } from '@tanstack/react-router'
import { Banknote, BookOpen, Database, Gauge, History, LayoutDashboard, Menu, Plane, Receipt, Settings, Share2, TrendingUp, Wallet, type LucideIcon } from 'lucide-react'
import { ApiError, bootstrapQuery, savedViewsQuery, usageBeacon, type Bootstrap, type DashboardHead, type NavItem } from '../lib/api'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from '../components/ui/sheet'
import { Button } from '../components/ui/button'
import { CommandBar, type CommandGroup, type CommandItem } from '../features/telemetry/command-bar'
import { ShellOverlays } from '../features/telemetry/shell-overlays'
import { searchKey } from '../features/telemetry/shell-store'
import { useCube } from '../features/telemetry/cube-store'
import { homeDashboard, normalizeRoute } from '../features/telemetry/nav'

export const Route=createFileRoute('/_app')({
 beforeLoad:async ({context,location}) => {
  try {const bootstrap=await context.queryClient.fetchQuery(bootstrapQuery);return {bootstrap,session:bootstrap.session}}
  catch(error){if(error instanceof ApiError && error.status===401)throw redirect({href:`/login?next=${encodeURIComponent(location.href)}`});throw error}
 },
 // The cube is for exploring: fetch it behind the page and never hold a page for it.
 loader:({context}) => {void import('../features/telemetry/cube-store').then(store => context.queryClient.prefetchQuery(store.cubeQuery(context.session.user.id,context.queryClient)))},
 shouldReload:false,pendingMs:Infinity,errorComponent:p => <main className="loading"><RouteError {...p} title="Could not load your workspace"/></main>,component:Workspace,
})
const ICONS:Record<string,LucideIcon>={gauge:Gauge,plane:Plane,receipt:Receipt,banknote:Banknote,wallet:Wallet,'trending-up':TrendingUp,'book-open':BookOpen,database:Database,history:History}
function usableNav(data:Bootstrap):(NavItem&{to:string})[] {
 const known=new Set(data.dashboards.map(d => d.id))
 // An agent's nav may name a dashboard that isn't built yet; a dead link is worse than a shorter list.
 return data.nav.flatMap(item => item.dashboard ? (known.has(item.dashboard) ? [{...item,to:`/dashboards/${item.dashboard}`}] : []) : [{...item,to:normalizeRoute(item.route ?? '/')}])
}
const count=(item:NavItem,data:Bootstrap) => item.id==='data' ? data.counts.imports+data.counts.reconcile : item.id==='changes' ? data.counts.feedback : 0
const countHint=(item:NavItem,n:number) => item.id==='data' ? `${n} waiting for review` : `${n} open ${n===1 ? 'flag' : 'flags'}`
function isActive(to:string,path:string,home:DashboardHead|undefined) {
 if(to==='/')return path==='/' || (!!home && path===`/dashboards/${home.id}`)
 return path===to || path.startsWith(`${to}/`)
}
function Navigation({data,onNavigate,onSearch}:{data:Bootstrap;onNavigate?:() => void;onSearch:() => void}) {
 const router=useRouter(),path=useLocation({select:l => l.pathname}),items=usableNav(data),home=homeDashboard(data.dashboards)
 const preload=(to:string) => {void router.preloadRoute({to:to as never}).catch(() => {})}
 const inNav=new Set(items.flatMap(i => i.dashboard ? [i.dashboard] : [])),others=data.dashboards.filter(d => !inNav.has(d.id) && d.id!==home?.id)
 return <div className="navigation">
  <Link className="workspace" to="/" onClick={onNavigate}><span className="workspace-mark" aria-hidden="true">M</span><span className="nav-label workspace-name">{data.session.user.name}’s money</span></Link>
  <nav aria-label="Workspace" className="nav-main">
   {items.map(item => {const Icon=ICONS[item.icon] ?? LayoutDashboard,active=isActive(item.to,path,home),n=count(item,data);return <a key={item.id} href={item.to} className={active ? 'nav-link active' : 'nav-link'} aria-current={active ? 'page' : undefined} title={item.label} onClick={event => {if(!event.metaKey && !event.ctrlKey && !event.shiftKey){event.preventDefault();onNavigate?.();void router.navigate({href:item.to})}}} onMouseEnter={() => preload(item.to)} onFocus={() => preload(item.to)}><Icon aria-hidden="true" size={16}/><span className="nav-label">{item.label}</span>{n>0 && <span className="nav-count" title={countHint(item,n)}><span className="sr-only">, {countHint(item,n)}</span>{n}</span>}</a>})}
  </nav>
  {others.length>0 && <nav aria-label="More dashboards" className="nav-more"><span className="nav-group">More</span>{others.map(d => {const to=`/dashboards/${d.id}`,active=path===to;return <Link key={d.id} to="/dashboards/$id" params={{id:d.id}} onClick={onNavigate} title={d.title} className={active ? 'nav-link active' : 'nav-link'} aria-current={active ? 'page' : undefined}><LayoutDashboard aria-hidden="true" size={16}/><span className="nav-label nav-title">{d.title}</span></Link>})}</nav>}
  <div className="nav-footer">
   <Link to="/shared" onClick={onNavigate} className={path.startsWith('/shared') ? 'nav-link active' : 'nav-link'} title="Shared with me"><Share2 aria-hidden="true" size={16}/><span className="nav-label">Shared with me</span></Link>
   <Link to="/settings" onClick={onNavigate} className={path==='/settings' ? 'nav-link active' : 'nav-link'} title="Settings"><Settings aria-hidden="true" size={16}/><span className="nav-label">Settings</span></Link>
   <button type="button" className="nav-link nav-action" title="Search and jump (Ctrl K)" onClick={() => {onNavigate?.();onSearch()}}><span className="nav-kbd" aria-hidden="true">{searchKey}</span><span className="nav-label">Search</span></button>
  </div>
 </div>
}
function Workspace() {
 const {data}=useSuspenseQuery(bootstrapQuery),session=data.session,router=useRouter(),navigate=useNavigate(),cube=useCube(session.user.id),[mobileOpen,setMobileOpen]=useState(false),[commandOpen,setCommandOpen]=useState(false)
 const views=useQuery({...savedViewsQuery(session),staleTime:60_000,enabled:commandOpen})
 useEffect(() => {
  // A headless capture only looks: no usage beacons, and the report goes out once the page settles.
  if(data.render_token){
   const token=data.render_token
   document.documentElement.classList.toggle('dark',new URLSearchParams(location.search).get('theme')==='dark')
   let stop=() => {},alive=true
   void import('../features/telemetry/render-page').then(m => {if(alive)stop=m.startPageReport(token)})
   return () => {alive=false;stop()}
  }
  const send=() => {const {pathname}=router.state.location,dashboard=pathname.match(/^\/dashboards\/([^/]+)/)?.[1];usageBeacon(session,pathname,dashboard ? decodeURIComponent(dashboard) : undefined)}
  send();return router.subscribe('onResolved',event => {if(event.pathChanged)send()})
 },[router,session,data.render_token])
 const groups:CommandGroup[]=[
  {heading:'Pages',items:[...usableNav(data).map(i => ({id:i.to,label:i.label})),{id:'/ledger/accounts',label:'Accounts'},{id:'/data/rates',label:'Rates'},{id:'/data/imports',label:'Imports'},{id:'/data/reconcile',label:'Reconcile'},{id:'/settings',label:'Settings'},...data.dashboards.map(d => ({id:`/dashboards/${d.id}`,label:d.title,hint:'Dashboard'}))].filter((item,i,all) => all.findIndex(x => x.id===item.id)===i)},
  {heading:'Saved views',items:(views.data ?? []).map(v => ({id:v.id,label:v.name,hint:'Saved view'}))},
  {heading:'Accounts',items:(cube?.accounts ?? []).filter(a => a.class!=='equity').map(a => ({id:a.id,label:a.label,hint:a.category!==a.label ? a.category : undefined}))},
  {heading:'Payees',items:(cube?.payees ?? []).map(p => ({id:p,label:p}))},
 ]
 const select=(item:CommandItem,group:string) => {
  if(group==='Pages')void router.navigate({href:item.id})
  else if(group==='Saved views'){const v=views.data?.find(x => x.id===item.id);if(v)void router.navigate({href:`${v.page}?${new URLSearchParams(v.state ?? {})}`})}
  else if(group==='Accounts')void navigate({to:'/ledger',search:{accounts:[item.id]}})
  else void navigate({to:'/ledger',search:{q:item.label}})
 }
 return <div className="workspace-shell">
  <aside className="desktop-sidebar"><Navigation data={data} onSearch={() => setCommandOpen(true)}/></aside>
  <div className="workspace-content">
   <a className="skip" href="#main-content">Skip to content</a>
   <header className="mobile-header"><Sheet open={mobileOpen} onOpenChange={setMobileOpen}><SheetTrigger render={<Button variant="ghost" size="icon" aria-label="Open navigation"/>}><Menu/></SheetTrigger><SheetContent side="left" className="mobile-sheet"><SheetHeader className="sr-only"><SheetTitle>Navigation</SheetTitle></SheetHeader><Navigation data={data} onNavigate={() => setMobileOpen(false)} onSearch={() => setCommandOpen(true)}/></SheetContent></Sheet><Link to="/" className="mobile-brand"><span className="workspace-mark" aria-hidden="true">M</span>Moneybag</Link><Button variant="ghost" size="sm" onClick={() => setCommandOpen(true)}>Search</Button></header>
   <main id="main-content" className="main-content"><Outlet/></main>
  </div>
  <CommandBar groups={groups} onSelect={select} open={commandOpen} onOpenChange={setCommandOpen}/>
  <ShellOverlays session={session}/>
 </div>
}
