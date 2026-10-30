import { useSyncExternalStore } from 'react'
import type { DetailsContent } from './details-drawer'
import type { FlagTarget } from '../../lib/api'

// One drawer and one dialog for the whole app, opened from anywhere without threading props through every panel.
export type MetricRequest={id:string;usd?:boolean;from?:string;to?:string;compare?:string;split?:string;filters?:{field:string;op:string;values:string[]}[]}
export type DetailsRequest={content:DetailsContent;target?:FlagTarget;metric?:MetricRequest;historyOf?:string}
export type ShellState={details:DetailsRequest|null;saveView:{page:string;state:Record<string,string>;suggested:string}|null}
let state:ShellState={details:null,saveView:null}
const listeners=new Set<() => void>()
const set=(next:Partial<ShellState>) => {state={...state,...next};for(const listener of listeners)listener()}
const subscribe=(listener:() => void) => {listeners.add(listener);return () => {listeners.delete(listener)}}
export const useShell=() => useSyncExternalStore(subscribe,() => state)
export const openDetails=(details:DetailsRequest) => set({details})
export const closeDetails=() => set({details:null})
export const promptSaveView=(page:string,viewState:Record<string,string>,suggested:string) => set({saveView:{page,state:viewState,suggested}})
export const closeSaveView=() => set({saveView:null})
/** The search shortcut as this platform names it. */
export const searchKey=typeof navigator!=='undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K'
