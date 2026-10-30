import { useSyncExternalStore } from 'react'
import type { DetailsContent } from './details-drawer'
import type { FlagTarget } from '../../lib/api'

// One drawer and one dialog for the whole app, opened from anywhere without threading props through every panel.
export type MetricRequest={id:string;usd?:boolean;from?:string;to?:string;compare?:string;split?:string;filters?:{field:string;op:string;values:string[]}[]}
export type DetailsRequest={content:DetailsContent;target?:FlagTarget;metric?:MetricRequest;historyOf?:string}
export type PageContext={title:string;lines:string[]}
export type ShellState={details:DetailsRequest|null;ask:{question:string}|null;saveView:{page:string;state:Record<string,string>;suggested:string}|null;page:PageContext}
let state:ShellState={details:null,ask:null,saveView:null,page:{title:'',lines:[]}}
const listeners=new Set<() => void>()
const set=(next:Partial<ShellState>) => {state={...state,...next};for(const listener of listeners)listener()}
const subscribe=(listener:() => void) => {listeners.add(listener);return () => {listeners.delete(listener)}}
export const useShell=() => useSyncExternalStore(subscribe,() => state)
export const openDetails=(details:DetailsRequest) => set({details})
export const closeDetails=() => set({details:null})
export const askAgent=(question='') => set({ask:{question}})
export const closeAsk=() => set({ask:null})
export const promptSaveView=(page:string,viewState:Record<string,string>,suggested:string) => set({saveView:{page,state:viewState,suggested}})
export const closeSaveView=() => set({saveView:null})
// Pages describe themselves here so a question to an agent carries what the person is looking at.
export const describePage=(page:PageContext) => {if(page.title!==state.page.title || page.lines.join('\n')!==state.page.lines.join('\n'))state={...state,page}}
/** The search shortcut as this platform names it. */
export const searchKey=typeof navigator!=='undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K'
export function agentPrompt(question:string,page:PageContext):string {
 return [`I'm looking at ${page.title ? `"${page.title}"` : 'a page'} in Moneybag: ${location.href}`,...page.lines,'',`My question: ${question || '(write your question here)'}`,'','Use the Moneybag MCP tools. Quote exact figures from metric_query, and tell me which entries they rest on.'].join('\n')
}
