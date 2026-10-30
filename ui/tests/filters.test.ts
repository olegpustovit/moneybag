import { strict as assert } from 'node:assert'
import { addPill, celToPills, decodePills, encodePills, nextOp, opLabel, parseAsk, pillsToCel } from '../src/features/telemetry/filters'
import { cubeKey, clearCached, fetchCube, loadCube, readCached, writeCached } from '../src/features/telemetry/cube-store'
import type { CubeWire, Pill } from '../src/features/telemetry/cube'
import { dayFromIso as d, isoDay } from '../src/features/telemetry/time-grammar'
import { syntheticCube } from './fixtures/synthetic-cube'

const samples:Pill[][]=[
 [{field:'category',op:'is',values:['Rent']}],
 [{field:'category',op:'is',values:['Rent','Travel']},{field:'amount',op:'over',values:['50']}],
 [{field:'category',op:'is not',values:['Rent']},{field:'estimate',op:'is',values:['Recorded']}],
 [{field:'trip',op:'is not',values:['Lisbon','Vienna']}],
 [{field:'payee',op:'contains',values:['lidl']}],
 [{field:'payee',op:'contains',values:['lidl','spar']},{field:'account',op:'is',values:['A "quoted" & name']}],
 [{field:'payee',op:'does not contain',values:['uber','bolt']}],
 [{field:'amount',op:'under',values:['12.5']},{field:'estimate',op:'is',values:['Estimated']}],
 [],
]
for(const pills of samples){
 const cel=pillsToCel(pills),back=celToPills(cel)
 assert.ok(back.ok,cel);assert.deepEqual(back.ok && back.pills,pills,cel)
 assert.deepEqual(decodePills(encodePills(pills)),pills)
}
assert.equal(pillsToCel(samples[1]),'row.category in ["Rent", "Travel"] && row.amount > 50')
assert.equal(pillsToCel(samples[2]),'row.category != "Rent" && row.estimated == false')
const refused=celToPills('row.amount > 5 && size(row.tags) > 2');assert.equal(refused.ok,false);assert.match(refused.ok ? '' : refused.message,/size\(row\.tags\)/)
assert.equal(celToPills('row.nope == "x"').ok,false)
assert.equal(celToPills('row.category > 5').ok,false)
assert.deepEqual(celToPills('row.estimated != false'),{ok:true,pills:[{field:'estimate',op:'is',values:['Estimated']}]})
// A && inside a string is not a clause boundary.
assert.deepEqual(celToPills('row.payee.contains("a && b")'),{ok:true,pills:[{field:'payee',op:'contains',values:['a && b']}]})

// A hand-edited URL degrades instead of breaking the page.
assert.deepEqual(decodePills('category:is:Rent;nope:is:x;category:over:5;payee:contains:%E0%A4%A;amount:over:9'),[{field:'category',op:'is',values:['Rent']},{field:'amount',op:'over',values:['9']}])
assert.deepEqual(decodePills(undefined),[])
assert.deepEqual(decodePills(encodePills([{field:'payee',op:'contains',values:['a:b;c|d']}])),[{field:'payee',op:'contains',values:['a:b;c|d']}])

assert.equal(nextOp({field:'category',op:'is',values:['x']}),'is not')
assert.equal(nextOp({field:'category',op:'is not',values:['x']}),'is')
assert.equal(nextOp({field:'amount',op:'over',values:['1']}),'under')
assert.equal(nextOp({field:'estimate',op:'is',values:['Recorded']}),'is')
assert.equal(opLabel({field:'category',op:'is',values:['a','b']}),'is either of')
assert.equal(opLabel({field:'category',op:'is not',values:['a','b']}),'is none of')
assert.deepEqual(addPill([{field:'category',op:'is',values:['a']}],{field:'category',op:'is',values:['b']}),[{field:'category',op:'is',values:['a','b']}])
assert.deepEqual(addPill([{field:'category',op:'is',values:['a']}],{field:'category',op:'is',values:['a']}),[{field:'category',op:'is',values:['a']}])
assert.deepEqual(addPill([{field:'amount',op:'over',values:['1']}],{field:'amount',op:'over',values:['9']}),[{field:'amount',op:'over',values:['9']}])

// The ask chips from the proposal, plus a few honest misses.
const bounds={today:d('2026-10-30'),start:d('2023-01-01')},dict={category:['Rent','Travel','Groceries','Restaurants','Estimated'],trip:['Lisbon'],account:['Rent','Everyday card'],payee:['Lidl','Corner Bistro']}
const ask=(s:string) => parseAsk(s,dict,bounds)
const q1=ask('Groceries over €50 last 3 months');assert.deepEqual(q1.pills,[{field:'amount',op:'over',values:['50']},{field:'category',op:'is',values:['Groceries']}]);assert.equal(isoDay(q1.range!.from),'2026-07-31')
const q2=ask('Travel this year');assert.deepEqual(q2.pills,[{field:'category',op:'is',values:['Travel']}]);assert.equal(isoDay(q2.range!.from),'2026-01-01')
const q3=ask('Everything except rent, no estimates');assert.deepEqual(q3.pills,[{field:'estimate',op:'is',values:['Recorded']},{field:'category',op:'is not',values:['Rent']}]);assert.equal(q3.range,undefined)
const q4=ask('Charges above €500 since march');assert.deepEqual(q4.pills,[{field:'amount',op:'over',values:['500']}]);assert.equal(isoDay(q4.range!.from),'2026-03-01')
const q5=ask('spending on the Lisbon trip in 2024');assert.deepEqual(q5.pills,[{field:'trip',op:'is',values:['Lisbon']}]);assert.equal(isoDay(q5.range!.to),'2024-12-31')
assert.deepEqual(ask('at lidl').pills,[{field:'payee',op:'contains',values:['lidl']}])
assert.equal(ask('how long will my money last').understood,false)
assert.equal(ask('groceries under 20').pills[0].op,'under')

// The store degrades to the network and keeps stale data when the refresh fails. Bun has no IndexedDB, which is the case under test.
assert.equal(await readCached('u'),null)
assert.equal(await writeCached({scope:'u',revision:1,etag:null,wire:{} as CubeWire}),false)
await clearCached('u')
assert.deepEqual(cubeKey('u'),['telemetry-cube','u'])
const realFetch=globalThis.fetch,wire=syntheticCube({start:'2026-10-01'})
let seen:string|undefined
globalThis.fetch=(async (_url:string,init?:RequestInit) => {seen=(init?.headers as Record<string,string>|undefined)?.['If-None-Match'];return new Response(JSON.stringify(wire),{status:200,headers:{ETag:'"r1"'}})}) as typeof fetch
assert.equal((await fetchCube('"r0"'))?.etag,'"r1"');assert.equal(seen,'"r0"')
assert.equal((await loadCube('u',() => {})).size,wire.flows.d.length)
globalThis.fetch=(async () => new Response(null,{status:304})) as unknown as typeof fetch
assert.equal(await fetchCube('"r1"'),null)
await assert.rejects(loadCube('u',() => {}),/no cube/)
globalThis.fetch=(async () => new Response('',{status:500})) as unknown as typeof fetch
await assert.rejects(fetchCube(null),/500/)
globalThis.fetch=realFetch
console.log('filters ok')
