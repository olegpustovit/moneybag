import { queryOptions, type QueryClient } from '@tanstack/react-query'
import { decodeCube, type Cube, type CubeWire } from './cube'

// Persisted per user: browsers are shared, and one person's ledger must never paint for another.
export type CachedCube={scope:string;revision:number;etag:string|null;wire:CubeWire}
const DB='moneybag-telemetry',STORE='cubes'
let opening:Promise<IDBDatabase>|undefined
function database():Promise<IDBDatabase> {
 opening??=new Promise<IDBDatabase>((resolve,reject) => {
  const request=indexedDB.open(DB,1)
  request.onupgradeneeded=() => {request.result.createObjectStore(STORE,{keyPath:'scope'})}
  request.onsuccess=() => resolve(request.result)
  request.onerror=() => reject(request.error)
  request.onblocked=() => reject(new Error('Cube cache is blocked'))
 }).catch((error:unknown) => {opening=undefined;throw error})
 return opening
}
const run=<T,>(mode:IDBTransactionMode,act:(store:IDBObjectStore) => IDBRequest<T>):Promise<T> => database().then(db => new Promise<T>((resolve,reject) => {const request=act(db.transaction(STORE,mode).objectStore(STORE));request.onsuccess=() => resolve(request.result);request.onerror=() => reject(request.error)}))
// Every cache failure degrades to the network: private windows, blocked storage and full disks are all normal.
export async function readCached(scope:string):Promise<CachedCube|null> {try {return await run<CachedCube|undefined>('readonly',store => store.get(scope)) ?? null} catch {return null}}
export async function writeCached(entry:CachedCube):Promise<boolean> {
 try {const current=await readCached(entry.scope);if(current && current.revision>entry.revision)return false;await run('readwrite',store => store.put(entry));return true} catch {return false}
}
export async function clearCached(scope?:string):Promise<void> {try {await run('readwrite',store => scope===undefined ? store.clear() : store.delete(scope))} catch {/* Nothing cached is the wanted end state. */}}

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
 const refresh=fetchCube(cached!.etag,signal).then(async got => {if(got)onFresh(await store(scope,got))})
 // A failed refresh keeps the stale cube on screen; the next mount tries again.
 void refresh.catch(() => {})
 return local
}
export const cubeQuery=(scope:string,client:QueryClient) => queryOptions({queryKey:cubeKey(scope),staleTime:60_000,queryFn:({signal}) => loadCube(scope,cube => client.setQueryData(cubeKey(scope),cube),signal)})
