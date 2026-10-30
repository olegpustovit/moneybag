import { keepPreviousData, queryOptions, type QueryClient } from '@tanstack/react-query'
import { ApiError } from '../../lib/api'
import { readCachedView, writeCachedView } from './cube-store'
import { isoDay, parseRange, type Bounds } from './time-grammar'

export type SpecColumn={label?:string;value?:string;format?:string;type?:string;total?:boolean}
export type SpecComponent={id:string;type:string;title?:string;metric?:string;split?:string;mark?:string;x?:{type:string};estimates?:string;annotate?:string;compare?:string;fixed?:{reason:string;range?:string};drill?:{to:string;filter?:string};value?:string;rows?:string;format?:string;columns?:SpecColumn[]}
export type ViewPage={id:string;title:string;layout:{cols:{span:number;component:string}[]}[]}
export type ViewSpec={id?:string;title?:string;question?:string;scope?:{time?:{default?:string;presets?:string[]};compare?:'previous_period'|'previous_year'|'none'};explore?:{dimensions?:string[]};answer?:{metric:string;facts?:string[]};components?:SpecComponent[];pages?:ViewPage[]}
export type ViewBody={head:{id:string;title:string};spec:ViewSpec;version:number;status:string;pages:ViewPage[]|null;values:Record<string,unknown>|null;validation?:{ok:boolean;errors?:{path?:string;msg?:string}[]|null};as_of:string;stale:boolean}
export type ViewParams={from?:string;to?:string}

export function viewParams(text:string|undefined,bounds:Bounds):ViewParams {
 const range=text ? parseRange(text,bounds) : null
 return range ? {from:isoDay(range.from),to:isoDay(range.to)} : {}
}
const keyOf=(user:string,id:string,params:ViewParams) => ['dashboard-view',user,id,params.from ?? '',params.to ?? ''] as const
async function fetchView(user:string,id:string,params:ViewParams):Promise<ViewBody> {
 const query=new URLSearchParams(Object.entries(params).filter(([,v]) => !!v) as [string,string][])
 const response=await fetch(`/api/dashboards/${encodeURIComponent(id)}/view${query.size ? `?${query}` : ''}`,{credentials:'same-origin'})
 if(!response.ok)throw new ApiError(response.status===404 ? 'This dashboard is not available.' : response.status===401 ? 'Sign in to continue.' : 'Could not load this dashboard.',response.status)
 const body=await response.json() as ViewBody
 if(!query.size)writeCachedView(user,id,body)
 return body
}
export const viewQuery=(user:string,id:string,params:ViewParams) => queryOptions({queryKey:keyOf(user,id,params),staleTime:15_000,gcTime:600_000,placeholderData:keepPreviousData,queryFn:() => fetchView(user,id,params),
 // A rate-only write serves the last render with stale:true while the server refreshes it.
 refetchInterval:query => query.state.data?.stale ? 5_000 : false})
// Last good view first: a remembered page paints at once and useQuery revalidates it behind the numbers.
export async function paintLastGood(client:QueryClient,user:string,id:string):Promise<boolean> {
 const key=keyOf(user,id,{})
 if(client.getQueryData(key))return true
 const body=await readCachedView<ViewBody>(user,id)
 if(!body)return false
 client.setQueryData(key,body,{updatedAt:1})
 return true
}
export async function prepareView(client:QueryClient,user:string,id:string,params:ViewParams):Promise<void> {
 if(!Object.keys(params).length && await paintLastGood(client,user,id)){void client.prefetchQuery(viewQuery(user,id,params));return}
 await client.fetchQuery(viewQuery(user,id,params))
}
export async function fetchRenderView(token:string):Promise<ViewBody> {
 const response=await fetch(`/api/render/${encodeURIComponent(token)}/view`)
 if(!response.ok)throw new Error(`Render view failed (${response.status})`)
 return response.json() as Promise<ViewBody>
}
