import { queryOptions, type QueryClient } from '@tanstack/react-query'
import type { MoneybagTools } from './tools'

export interface Session { user: { id: string; name: string; email: string; is_admin: boolean }; csrf: string; version: string; mcp_url: string }
export class ApiError extends Error { readonly status: number; constructor(message: string, status: number) { super(message); this.status = status } }
export type NavItem = { id:string; label:string; icon:string; dashboard?:string; route?:string }
export type DashboardHead = { id:string; title:string; published_version:number; draft_version:number; revision:number; home?:boolean; published_at?:string; published_by?:{id:string;kind:string} }
export type TableHead = { id:string; name:string; archived?:boolean; columns:{id:string;type:string;formula?:string}[]|null }
export type Bootstrap = { session:Session; nav:NavItem[]; dashboards:DashboardHead[]; tables:TableHead[]; counts:{imports:number;reconcile:number;feedback:number}; labels:Record<string,string> }

async function getJson<T>(path:string,failure:string):Promise<T> {
 const response=await fetch(path,{credentials:'same-origin'})
 if(!response.ok)throw new ApiError(response.status===401 ? 'Sign in to continue.' : failure,response.status)
 return response.json() as Promise<T>
}
// One round trip instead of session, discover and lists. The browser revalidates it with the ETag the server sends.
export const bootstrapQuery=queryOptions({queryKey:['bootstrap'],queryFn:() => getJson<Bootstrap>('/api/bootstrap','Could not load the app.'),staleTime:30_000})
export const refreshBootstrap=(client:QueryClient) => client.invalidateQueries({queryKey:['bootstrap']})

let requestID=0
const protocol='2026-07-28'
type Envelope={error?:{message:string};result?:{isError?:boolean;structuredContent?:unknown;content?:{type:string;text?:string}[]}}
export async function invokeTool(session:Session,name:string,args:object):Promise<unknown> {
 const response=await fetch(session.mcp_url,{
  method:'POST',credentials:'same-origin',
  headers:{'Content-Type':'application/json',Accept:'application/json, text/event-stream','X-CSRF-Token':session.csrf,'MCP-Protocol-Version':protocol,'Mcp-Method':'tools/call','Mcp-Name':name},
  body:JSON.stringify({jsonrpc:'2.0',id:++requestID,method:'tools/call',params:{name,arguments:args,_meta:{'io.modelcontextprotocol/protocolVersion':protocol,'io.modelcontextprotocol/clientInfo':{name:'moneybag-web',version:'1'},'io.modelcontextprotocol/clientCapabilities':{}}}}),
 })
 if(!response.ok)throw new ApiError(response.status===401 ? 'Your session expired. Sign in again.' : `Request failed (${response.status}).`,response.status)
 const envelope=await response.json() as Envelope
 if(envelope.error || !envelope.result)throw new Error(envelope.error?.message ?? 'The server returned an empty response.')
 const result=envelope.result
 if(result.isError)throw new Error(result.content?.filter(item => item.type==='text').map(item => item.text).join('\n') || 'The operation failed.')
 if(result.structuredContent!==undefined)return result.structuredContent
 const text=result.content?.find(item => item.type==='text')?.text
 if(!text)throw new Error('The server returned no result.')
 return JSON.parse(text) as unknown
}
export async function tool<K extends keyof MoneybagTools>(session:Session,name:K,args:MoneybagTools[K]['input']):Promise<MoneybagTools[K]['output']> {
 return await invokeTool(session,name,args) as MoneybagTools[K]['output']
}
export async function settingsPost<T>(session:Session,path:'/settings/invite'|'/settings/revoke'|'/settings/password'|'/settings/agents/stop'|'/settings/agents/resume',fields:Record<string,string>):Promise<T> {
 const response=await fetch(path,{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','Content-Type':'application/x-www-form-urlencoded','X-CSRF-Token':session.csrf},body:new URLSearchParams(fields)})
 const result=await response.json() as T&{error?:string}
 if(!response.ok)throw new ApiError(result.error || 'Request failed.',response.status)
 return result
}
async function send(session:Session,method:'POST'|'PUT'|'DELETE',path:string,body?:unknown,keepalive=false):Promise<Response> {
 const response=await fetch(path,{method,credentials:'same-origin',keepalive,headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf},body:body===undefined ? undefined : JSON.stringify(body)})
 if(!response.ok)throw new ApiError(`Request failed (${response.status}).`,response.status)
 return response
}
export type FlagTarget={dashboard?:string;component?:string;metric?:string;value?:string;params?:Record<string,string>}
export const flagNumber=(session:Session,target:FlagTarget,note:string) => send(session,'POST','/api/feedback',{target,note})
// Not audited, so it never invalidates a cached view. A failed beacon is not worth telling anyone about.
export const usageBeacon=(session:Session,path:string,dashboard?:string) => {void send(session,'POST','/api/usage',{path,dashboard},true).catch(() => {})}

export type SavedView={id:string;name:string;page:string;state?:Record<string,string>}
export const savedViewsQuery=(session:Session) => queryOptions({queryKey:['saved-views',session.user.id],queryFn:async () => (await getJson<{views:SavedView[]|null}>('/api/saved-views','Could not load saved views.')).views ?? []})
export const saveView=async (session:Session,view:Omit<SavedView,'id'>&{id?:string}) => await (await send(session,'PUT','/api/saved-views',view)).json() as SavedView
export const deleteView=(session:Session,id:string) => send(session,'DELETE',`/api/saved-views?id=${encodeURIComponent(id)}`)

export type SettingsData={is_admin?:boolean;agents_suspended?:boolean;feeds:{id:string;account:string;format:string;date_format?:string}[]|null;agents:{id:string;client_id?:string;client_name:string;scopes:string[]|null;revoked:boolean;resource:string;expires_at:string}[]|null}
export const settingsQuery=(session:Session) => queryOptions({queryKey:['settings',session.user.id],queryFn:() => getJson<SettingsData>('/api/settings','Could not load settings.')})
export type Batch={id:string;account:string;format:string;status:string;from:string;to:string;created_at:string;closing_balance?:{c:string;v:string}|null}
export const importsQuery=(session:Session) => queryOptions({queryKey:['imports',session.user.id],queryFn:async () => (await getJson<{batches:Batch[]|null}>('/api/imports','Could not load imports.')).batches ?? []})
export type ChangeEvent={id:string;seq:number;at:string;tool:string;target:{coll:string;id:string};summary:string;reason?:string;status:string;hash:string}
export type ChangeGroup={id:string;at:string;actor:{kind:string;label:string};summary:string;count:number;routine:boolean;batch_id?:string;status:string;undoable:boolean;targets:{coll:string;id:string}[]|null;events:ChangeEvent[]|null}
export type ChangesPage={groups:ChangeGroup[]|null;next?:number}
export const fetchChanges=(params:URLSearchParams) => getJson<ChangesPage>(`/api/changes?${params}`,'Could not load the changes.')
export const agentsQuery=(session:Session) => queryOptions({queryKey:['activity-agents',session.user.id],queryFn:async () => (await getJson<{agents:{client_id:string;client_name:string}[]|null}>('/api/activity/agents','Could not load agents.')).agents ?? []})
export type SharedView={title:string;version:number;role:string;mode:string;at:string;pages?:{id:string;title:string;layout:{cols:{span:number;component:string}[]}[]}[];values?:Record<string,unknown>;issues?:Record<string,string>;legacy_html?:string}
export const sharedQuery=(session:Session,id:string) => queryOptions({queryKey:['shared-view',session.user.id,id],queryFn:() => getJson<SharedView>(`/api/shared/${encodeURIComponent(id)}`,'This shared dashboard is unavailable.')})
export const receivedQuery=(session:Session) => queryOptions({queryKey:['shares-received',session.user.id],queryFn:() => tool(session,'shared_with_me_list',{})})
