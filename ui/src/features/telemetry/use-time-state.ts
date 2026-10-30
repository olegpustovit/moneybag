import { useEffect, useRef, useState } from 'react'
import { z } from 'zod'
import { canonicalText, compareWindow, parseRange, stepRange, zoomOutRange, type Bounds, type CompareMode, type CompareWindow, type Range } from './time-grammar'

// Routes spread this into validateSearch: a malformed URL degrades to the page default instead of erroring.
export const telemetrySearch=z.object({t:z.string().max(64).optional().catch(undefined),cmp:z.enum(['previous_period','previous_year','none']).optional().catch(undefined),f:z.string().max(2000).optional().catch(undefined)})
export type TelemetrySearch=z.infer<typeof telemetrySearch>
export type SearchWriter=(next:Partial<TelemetrySearch>,options:{push:boolean}) => void
export type TimeState={range:Range;text:string;compare:CompareMode;window:CompareWindow|null;canUndo:boolean;
 setText:(text:string) => void;setRange:(range:Range,meta?:{final?:boolean}) => void;setCompare:(mode:CompareMode) => void;step:(direction:number) => void;zoomOut:() => void;undo:() => void}
const same=(a:Range,b:Range) => a.from===b.from && a.to===b.to

// The URL owns the range. Only an in-flight drag lives here, so a brush can move at frame rate without a navigation per frame.
export function useTimeState({bounds,search,onSearch,defaultText='12mo',defaultCompare='previous_period'}:{bounds:Bounds;search:TelemetrySearch;onSearch:SearchWriter;defaultText?:string;defaultCompare?:CompareMode}):TimeState {
 const [live,setLive]=useState<Range|null>(null),[history,setHistory]=useState<Range[]>([])
 const fallback=parseRange(defaultText,bounds) ?? {from:bounds.start,to:bounds.today}
 const parsed=search.t ? parseRange(search.t,bounds) : null
 const settled=parsed ?? fallback,text=parsed ? search.t! : defaultText,range=live ?? settled,compare=search.cmp ?? defaultCompare
 const dragBase=useRef<Range|null>(null)
 const remember=(prev:Range) => setHistory(h => [...h.slice(-29),prev])
 const commit=(next:Range,push:boolean) => {dragBase.current=null;setLive(null);if(same(next,settled))return;remember(settled);onSearch({t:canonicalText(next)},{push})}
 return {range,text,compare,window:compareWindow(range,compare,bounds),canUndo:history.length>0,
  setText:value => {const next=parseRange(value,bounds);if(!next)return;if(!same(next,settled))remember(settled);setLive(null);onSearch({t:value},{push:true})},
  setRange:(next,{final=true}={}) => {
   if(!final){dragBase.current??=settled;setLive(next);return}
   const base=dragBase.current;dragBase.current=null;setLive(null)
   if(base){if(!same(next,base)){remember(base);onSearch({t:canonicalText(next)},{push:true})}return}
   commit(next,true)
  },
  setCompare:mode => onSearch({cmp:mode},{push:false}),
  step:direction => commit(stepRange(range,direction,bounds),false),
  zoomOut:() => commit(zoomOutRange(range,bounds),false),
  undo:() => {const prev=history.at(-1);if(!prev)return;setHistory(history.slice(0,-1));onSearch({t:canonicalText(prev)},{push:false})},
 }
}
// A keyboard shortcut must never fire while someone is typing or a dialog owns the keyboard.
export function ignoreShortcut(event:KeyboardEvent):boolean {
 if(event.ctrlKey || event.metaKey || event.altKey)return true
 const target=event.target
 if(target instanceof HTMLElement && (target.isContentEditable || /^(INPUT|SELECT|TEXTAREA)$/.test(target.tagName)))return true
 return !!document.querySelector('[role="dialog"]')
}
// For pages with no router in scope, such as the demo harness. Routes pass Route.useSearch and Route.useNavigate instead.
export function useBrowserSearch():[TelemetrySearch,SearchWriter] {
 const read=() => telemetrySearch.parse(Object.fromEntries(new URLSearchParams(location.search)))
 const [search,setSearch]=useState<TelemetrySearch>(read)
 useEffect(() => {const onPop=() => setSearch(read());addEventListener('popstate',onPop);return () => removeEventListener('popstate',onPop)},[])
 return [search,(next,{push}) => {
  const merged={...read(),...next},params=new URLSearchParams()
  for(const [k,v] of Object.entries(merged))if(v)params.set(k,v)
  const url=`${location.pathname}${params.size ? `?${params}` : ''}`
  if(push)history.pushState(null,'',url);else history.replaceState(null,'',url)
  setSearch(merged)
 }]
}
