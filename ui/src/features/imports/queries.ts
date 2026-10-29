import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { ApiError, tool, type Session } from '../../lib/api'
export type Batch = {id:string;account:string;format:string;status:string;from:string;to:string;created_at:string;closing_balance?:{c:string;v:string}|null}
// shortcut: the API returns only the latest 100 batches; add pagination when it is available.
export const importsQuery=(session:Session) => queryOptions({queryKey:['imports',session.user.id],queryFn:async ():Promise<Batch[]> => {const response=await fetch('/api/imports',{credentials:'same-origin'});if(!response.ok) throw new ApiError('Could not load imports.',response.status);const data=await response.json() as {batches:Batch[]|null};return data.batches ?? []}})
export const previewQuery=(session:Session,id:string) => infiniteQueryOptions({queryKey:['import-preview',session.user.id,id],queryFn:({pageParam}) => tool(session,'import_preview',{batch_id:id,cursor:pageParam || undefined}),initialPageParam:0,getNextPageParam:(page) => page.next_cursor || undefined})
