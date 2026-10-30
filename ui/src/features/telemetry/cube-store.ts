import { queryOptions, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { decodeCube, type Cube, type CubeWire } from './cube'

// Persisted per user: browsers are shared, and one person's ledger must never paint for another.
export type CachedCube={scope:string;revision:number;etag:string|null;wire:CubeWire}
const DB='moneybag-telemetry',STORE='cubes',VIEWS='views'
let opening:Promise<IDBDatabase>|undefined
function database():Promise<IDBDatabase> {
 opening??=new Promise<IDBDatabase>((resolve,reject) => {
  const request=indexedDB.open(DB,2)
  request.onupgradeneeded=() => {for(const name of [STORE,VIEWS])if(!request.result.objectStoreNames.contains(name))request.result.createObjectStore(name,{keyPath:'scope'})}
  request.onsuccess=() => resolve(request.result)
  request.onerror=() => reject(request.error)
  request.onblocked=() => reject(new Error('Cube cache is blocked'))
 }).catch((error:unknown) => {opening=undefined;throw error})
 return opening
}
const run=<T,>(store:string,mode:IDBTransactionMode,act:(store:IDBObjectStore) => IDBRequest<T>):Promise<T> => database().then(db => new Promise<T>((resolve,reject) => {const request=act(db.transaction(store,mode).objectStore(store));request.onsuccess=() => resolve(request.result);request.onerror=() => reject(request.error)}))
// Every cache failure degrades to the network: private windows, blocked storage and full disks are all normal.
export async function readCached(scope:string):Promise<CachedCube|null> {try {return await run<CachedCube|undefined>(STORE,'readonly',store => store.get(scope)) ?? null} catch {return null}}
export async function writeCached(entry:CachedCube):Promise<boolean> {
 try {const current=await readCached(entry.scope);if(current && current.revision>entry.revision)return false;await run(STORE,'readwrite',store => store.put(entry));return true} catch {return false}
}
// With no scope this wipes everything: the sign-out path, which must leave no ledger behind.
export async function clearCached(scope?:string):Promise<void> {
 for(const store of [STORE,VIEWS])try {await run(store,'readwrite',s => scope===undefined ? s.clear() : s.delete(scope))} catch {/* Nothing cached is the wanted end state. */}
}
// The last good dashboard view, so a reload paints numbers before the network answers.
export async function readCachedView<T>(user:string,id:string):Promise<T|null> {try {return (await run<{body:T}|undefined>(VIEWS,'readonly',s => s.get(`${user}|${id}`)))?.body ?? null} catch {return null}}
export function writeCachedView(user:string,id:string,body:unknown):void {void run(VIEWS,'readwrite',s => s.put({scope:`${user}|${id}`,body})).catch(() => {})}

// Returns null on 304. The contract's ETag derives from the revision and params.
export async function fetchCube(etag:string|null,signal?:AbortSignal):Promise<{wire:CubeWire;etag:string|null}|null> {
 const response=await fetch('/api/cube',{credentials:'same-origin',signal,headers:etag ? {'If-None-Match':etag} : undefined})
 if(response.status===304)return null
 if(!response.ok)throw new Error(`Could not load the ledger cube (${response.status}).`)
 return {wire:await response.json() as CubeWire,etag:response.headers.get('ETag')}
}
const store=async (scope:string,got:{wire:CubeWire;etag:string|null}) => {await writeCached({scope,revision:got.wire.revision,etag:got.etag,wire:got.wire});return decodeCube(got.wire)}
export const cubeKey=(scope:string) => ['telemetry-cube',scope] as const
// Stale-while-revalidate: paint the persisted cube now, then swap in the fresh one if the server has a newer revision.
export async function loadCube(scope:string,onFresh:(cube:Cube) => void,signal?:AbortSignal):Promise<Cube> {
 const cached=await readCached(scope)
 let local:Cube|null=null
 if(cached)try {local=decodeCube(cached.wire)} catch {await clearCached(scope)}
 if(!local){const got=await fetchCube(null,signal);if(!got)throw new Error('The server had no cube to send.');return store(scope,got)}
 const refresh=fetchCube(cached!.etag).then(async got => {if(got)onFresh(await store(scope,got))})
 // A failed refresh keeps the stale cube on screen; the next mount tries again.
 void refresh.catch(() => {})
 return local
}
export const cubeQuery=(scope:string,client:QueryClient) => queryOptions({queryKey:cubeKey(scope),staleTime:60_000,gcTime:Infinity,retry:1,queryFn:({signal}) => loadCube(scope,cube => client.setQueryData(cubeKey(scope),cube),signal)})
// The cube powers exploring only, so a page keeps working on server values when it is missing.
export function useCube(scope:string):Cube|null {const client=useQueryClient();return useQuery(cubeQuery(scope,client)).data ?? null}
