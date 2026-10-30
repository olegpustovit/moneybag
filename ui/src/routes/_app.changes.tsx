import { useState } from 'react'
import { keepPreviousData, useInfiniteQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { fetchChanges, type ChangeGroup } from '../lib/api'
import { changesSearch } from '../features/telemetry/search'
import { askAgent, describePage, openDetails } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton } from '../features/telemetry/page-bits'
import { dayLabel, DAY } from '../features/telemetry/time-grammar'
import { verbFor } from '../features/telemetry/verbs'

export const Route=createFileRoute('/_app/changes')({validateSearch:changesSearch,errorComponent:p => <RouteError {...p}/>,component:Changes})
const dayOf=(at:string) => Math.floor(Date.parse(at)/DAY)*DAY
function dayHead(day:number):string {const today=Math.floor(Date.now()/DAY)*DAY;return day===today ? 'Today' : day===today-DAY ? 'Yesterday' : dayLabel(day)}
const time=(at:string) => new Date(at).toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'})
const summaryOf=(g:ChangeGroup) => g.summary || verbFor(g.events?.[0]?.tool ?? '')
const undoQuestion=(g:ChangeGroup) => `Please undo this change: "${summaryOf(g)}" (${new Date(g.at).toLocaleString()}, by ${g.actor.label}). Tell me what it will reverse before you do it.`
function open(g:ChangeGroup) {
 const events=g.events ?? []
 openDetails({historyOf:events[0]?.target?.id,target:{value:summaryOf(g),params:{change:g.id}},content:{title:summaryOf(g),subtitle:`${g.actor.label} · ${new Date(g.at).toLocaleString()}`,
  sourceRows:events.length ? {columns:['When','What'],rows:events.slice(0,50).map(e => [time(e.at),e.summary || verbFor(e.tool)])} : undefined,
  machine:[...(g.batch_id ? [{label:'Batch',value:g.batch_id}] : []),...events.slice(0,12).flatMap(e => [{label:'Tool',value:e.tool},{label:'Target',value:`${e.target?.coll ?? ''} ${e.target?.id ?? ''}`.trim()},{label:'Hash',value:e.hash},...(e.reason ? [{label:'Reason',value:e.reason,mono:false}] : [])])]}})
}
function Row({g}:{g:ChangeGroup}) {
 const [more,setMore]=useState(false),events=g.events ?? []
 return <li onClick={() => open(g)}>
  <time dateTime={g.at}>{time(g.at)}</time>
  <div className="what"><strong>{summaryOf(g)}</strong><small>{g.actor.label}{g.count>1 ? ` · ${g.count.toLocaleString('en-US')} changes together` : ''}{g.routine ? ' · Routine' : ''}{g.status==='failed' ? ' · Failed' : ''}</small>
   {more && <ul className="evs">{events.slice(0,50).map(e => <li key={e.id}><time dateTime={e.at}>{time(e.at)}</time> {e.summary || verbFor(e.tool)}</li>)}{events.length>50 && <li>and {(events.length-50).toLocaleString('en-US')} more</li>}</ul>}</div>
  <div className="acts" onClick={e => e.stopPropagation()}>{g.count>1 && <button type="button" className="tm-details-link" aria-expanded={more} onClick={() => setMore(!more)}>{more ? 'Hide' : 'Show'} each change</button>}<button type="button" className="tm-details-link" onClick={() => open(g)}>See what changed</button>{g.undoable && <button type="button" className="tm-details-link" onClick={() => askAgent(undoQuestion(g))}>Ask to undo</button>}</div>
 </li>
}
function Changes() {
 const params=Route.useSearch(),navigate=Route.useNavigate(),set=(next:Partial<typeof params>) => {void navigate({search:old => ({...old,...next}),replace:true,resetScroll:false})}
 const from=params.since ? new Date(Date.now()-Number(params.since)*DAY).toISOString().slice(0,10) : undefined,q=[params.what,params.item].filter(Boolean).join(' ')
 const feed=useInfiniteQuery({queryKey:['changes',q,params.routine,from],staleTime:15_000,initialPageParam:0,placeholderData:keepPreviousData,
  queryFn:({pageParam}) => {const u=new URLSearchParams();if(q)u.set('q',q);if(params.routine)u.set('routine','true');if(from)u.set('from',from);if(pageParam)u.set('cursor',String(pageParam));return fetchChanges(u)},
  getNextPageParam:page => page.next || undefined})
 const all=(feed.data?.pages ?? []).flatMap(p => p.groups ?? []),people=[...new Set(all.map(g => g.actor.label))].sort(),groups=params.who ? all.filter(g => g.actor.label===params.who) : all,days=new Map<number,ChangeGroup[]>()
 for(const g of groups){const d=dayOf(g.at);days.set(d,[...(days.get(d) ?? []),g])}
 describePage({title:'Changes',lines:[...(params.who ? [`Who: ${params.who}`] : []),...(q ? [`Looking for: ${q}`] : [])]})
 return <>
  <PageHead title="Changes" description="Who changed what?"/>
  <form className="tm-change-filters" role="search" onSubmit={e => e.preventDefault()}>
   <label>Who<select className="tm-btn tm-select" value={params.who ?? ''} onChange={e => set({who:e.target.value || undefined})}><option value="">Anyone</option>{[...new Set([...(params.who ? [params.who] : []),...people])].map(p => <option key={p} value={p}>{p}</option>)}</select></label>
   <label>What changed<input className="tm-input" type="search" defaultValue={params.what} placeholder="Rent, budget, import…" onChange={e => set({what:e.target.value || undefined})}/></label>
   <label>Which item<input className="tm-input" type="search" defaultValue={params.item} placeholder="An account, a dashboard…" onChange={e => set({item:e.target.value || undefined})}/></label>
   <label>When<select className="tm-btn tm-select" value={params.since ?? ''} onChange={e => set({since:(e.target.value || undefined) as typeof params.since})}><option value="">Any time</option><option value="7">Last 7 days</option><option value="30">Last 30 days</option><option value="90">Last 90 days</option><option value="365">Last year</option></select></label>
   <label className="tm-check"><input type="checkbox" checked={!!params.routine} onChange={e => set({routine:e.target.checked || undefined})}/>Show routine updates</label>
  </form>
  {feed.isPending ? <RowsSkeleton rows={10}/> : feed.error ? <RouteError error={feed.error} reset={() => void feed.refetch()} title="Changes didn’t load"/> : !groups.length ? <Empty title="No changes here">{q || params.who ? 'Nothing matches these filters.' : 'When an agent or a person changes something, it appears here.'}</Empty>
  : <div className="tm-feed">{[...days.entries()].map(([day,items]) => <section key={day}><h2>{dayHead(day)}</h2><ol>{items.map(g => <Row key={g.id} g={g}/>)}</ol></section>)}{feed.hasNextPage && <div className="tm-more"><button type="button" className="tm-btn" disabled={feed.isFetchingNextPage} onClick={() => void feed.fetchNextPage()}>{feed.isFetchingNextPage ? 'Loading…' : 'Show earlier changes'}</button></div>}</div>}
 </>
}
