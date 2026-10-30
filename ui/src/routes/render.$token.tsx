import { useEffect, useRef, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { DashboardBody } from '../features/telemetry/dashboard-view'
import { PageSkeleton } from '../features/telemetry/page-skeleton'
import { fetchRenderView, type ViewBody } from '../features/telemetry/view-data'
import { decodeCube, type Cube, type CubeWire } from '../features/telemetry/cube'
import type { TelemetrySearch } from '../features/telemetry/use-time-state'
import { collectReport, emptyReport, sendReport } from '../features/telemetry/render-report'

// No session and no chrome: the agent's only preview is the real page, reading through its token.
export const Route=createFileRoute('/render/$token')({component:RenderRoute})
const SETTLE_MS=4500
function RenderRoute() {
 const {token}=Route.useParams(),started=useRef(performance.now()),sent=useRef(false),[view,setView]=useState<ViewBody|null>(null),[cube,setCube]=useState<Cube|null>(null),[cubeDone,setCubeDone]=useState(false),[failed,setFailed]=useState(''),[search,setSearch]=useState<TelemetrySearch>({})
 const report=(body:ReturnType<typeof emptyReport>) => {if(sent.current)return;sent.current=true;void sendReport(token,{...body,timing_ms:Math.round(performance.now()-started.current)})}
 useEffect(() => {
  let alive=true
  void fetchRenderView(token).then(body => {
   if(!alive)return
   // The token fixes the range, so the bar shows it instead of guessing from the page default.
   const value=Object.values(body.values ?? {}).find(v => typeof (v as {from?:unknown})?.from==='string' && !(v as {fixed?:unknown}).fixed) as {from:string;to:string}|undefined
   if(value)setSearch({t:`${value.from} to ${value.to}`})
   setView(body)
  },(error:unknown) => {if(!alive)return;setFailed(error instanceof Error ? error.message : 'Render failed');report(emptyReport(['page']))})
  void fetch(`/api/render/${encodeURIComponent(token)}/cube`).then(async response => {if(response.ok && alive)setCube(decodeCube(await response.json() as CubeWire))}).catch(() => {}).finally(() => {if(alive)setCubeDone(true)})
  // Whatever happens below, publish is gated on this report, so it always goes out. Virtual time runs this timer fast and waits for fetches first.
  const failsafe=window.setTimeout(() => report(collectReport(viewRef.current)),SETTLE_MS)
  const onError=() => {window.setTimeout(() => report(collectReport(viewRef.current)),100)}
  window.addEventListener('error',onError);window.addEventListener('unhandledrejection',onError)
  return () => {alive=false;clearTimeout(failsafe);window.removeEventListener('error',onError);window.removeEventListener('unhandledrejection',onError)}
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[token])
 const viewRef=useRef<ViewBody|null>(null);viewRef.current=view
 useEffect(() => {
  if(!view || !cubeDone)return
  let tries=0,timer=0
  const settled=() => !document.querySelector('.tm-plot-skeleton')
  const check=() => {tries++;if(settled() || tries>30)void (document.fonts?.ready ?? Promise.resolve()).then(() => {window.setTimeout(() => report(collectReport(view)),120)});else timer=window.setTimeout(check,60)}
  timer=window.setTimeout(check,60)
  return () => clearTimeout(timer)
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[view,cubeDone])
 if(failed)return <main className="tm-render" data-panel="page"><p className="tm-empty">This preview could not load: {failed}</p></main>
 if(!view)return <main className="tm-render"><PageSkeleton/></main>
 return <main className="tm-render"><DashboardBody view={view} cube={cube} search={search} onSearch={next => setSearch(old => ({...old,...next}))} dashboardId={view.head.id} render/></main>
}
