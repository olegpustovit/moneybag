import { queryOptions } from '@tanstack/react-query'
import { ApiError, tool, type Session } from '../../lib/api'

export const receivedQuery=(session:Session) => queryOptions({queryKey:['shares-received',session.user.id],queryFn:() => tool(session,'shared_with_me_list',{})})
export const ownedQuery=(session:Session) => queryOptions({queryKey:['shares-owned',session.user.id],queryFn:() => tool(session,'dashboard_shares_list',{})})
export interface SharedView {title:string;version:number;role:string;mode:string;at:string;pages?:{id:string;title:string;layout:{cols:{span:number;component:string}[]}[]}[];values?:Record<string,unknown>;legacy_html?:string}
export const sharedQuery=(session:Session,id:string) => queryOptions({queryKey:['shared-view',session.user.id,id],staleTime:0,gcTime:0,queryFn:async ():Promise<SharedView> => {const response=await fetch(`/api/shared/${encodeURIComponent(id)}`,{credentials:'same-origin',cache:'no-store'});if(!response.ok) throw new ApiError('This shared dashboard is unavailable.',response.status);return response.json() as Promise<SharedView>}})
