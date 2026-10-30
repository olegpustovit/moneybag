import { strict as assert } from 'node:assert'
import { bucketGeometry, categoryTicks, pointLabel, rebucket, stackSeries, summarize, timeTicks, yExtent, yRange } from '../src/features/telemetry/chart-core'
import { dayFromIso as d, isoDay } from '../src/features/telemetry/time-grammar'
import { unitFormat } from '../src/features/telemetry/format'

// No label may ever read like a machine timestamp, at any zoom.
const machine=/\d{4}-\d{2}|T\d{2}:|Z$/
for(const [a,b,w] of [['2023-01-01','2026-10-30',1200],['2023-01-01','2026-10-30',320],['2026-08-01','2026-10-30',600],['2026-10-01','2026-10-30',600],['2026-10-20','2026-10-23',800],['2000-01-01','2026-10-30',600]] as const){
 const t=timeTicks(d(a),d(b),w)
 assert.ok(t.ticks.length>=2,`${a}..${b} has ${t.ticks.length} ticks`)
 assert.ok(t.ticks.length<=w/40,`${a}..${b} at ${w}px has ${t.ticks.length} ticks`)
 assert.equal(t.ticks.length,t.labels.length)
 for(const l of t.labels)assert.ok(!machine.test(l),l)
 for(const x of t.ticks)assert.ok(x>=d(a) && x<=d(b))
}
const months=timeTicks(d('2025-11-01'),d('2026-10-30'),1000)
assert.equal(isoDay(months.ticks[0]),'2025-11-01');assert.equal(months.labels[0],'Nov\n2025');assert.equal(months.labels[1],'Dec');assert.equal(months.labels[2],'Jan\n2026')
const weeks=timeTicks(d('2026-08-01'),d('2026-10-30'),800)
assert.equal(new Date(weeks.ticks[0]).getUTCDay(),1,'week ticks are Mondays')
assert.deepEqual(timeTicks(d('2020-01-01'),d('2026-01-01'),300).labels.slice(0,2),['2020','2022'])
const hours=timeTicks(Date.UTC(2022,1,11,0),Date.UTC(2022,1,12,0),1000);assert.ok(hours.labels.includes('15:00'),hours.labels.join());assert.equal(hours.labels[0],'11 Feb\n2022')

const cats=categoryTicks(['Groceries','Restaurants','A very long category name indeed','Rent'],1000);assert.deepEqual(cats.ticks,[0,1,2,3]);assert.equal(cats.labels[2],'A very long c…')
assert.ok(categoryTicks(Array.from({length:50},(_,i) => `Category ${i}`),320).ticks.length<10)

const g=bucketGeometry([d('2024-01-01'),d('2024-02-01'),d('2024-03-01')],'month')
assert.equal(isoDay(g.edges[3]),'2024-04-01');assert.equal(g.mids[1],(d('2024-02-01')+d('2024-03-01'))/2);assert.equal(bucketGeometry([]).edges.length,0)

const s=stackSeries([{values:[5,-1]},{values:[null,-2]},{values:[3,4]}],2)
assert.deepEqual([...s.lo[0]],[0,-1]);assert.deepEqual([...s.hi[0]],[5,0]);assert.deepEqual([...s.lo[1]],[5,-3]);assert.deepEqual([...s.lo[2]],[5,0]);assert.deepEqual([...s.hi[2]],[8,4]);assert.equal(s.max,8);assert.equal(s.min,-3)

assert.deepEqual(yExtent([{values:[null,2,NaN,5]},{values:[1]}]),[1,5]);assert.deepEqual(yExtent([{values:[null]}]),[0,1])
assert.equal(yRange(10,20,true)[0],0);assert.ok(yRange(-5,20,true)[0]<-5);assert.equal(yRange(-20,-5,true)[1],0)
const rate=yRange(1.05,1.15,false);assert.ok(rate[0]>1 && rate[0]<1.05,'a rate axis does not start at zero');assert.ok(rate[1]>1.15)
assert.ok(yRange(5,5,false)[1]>5)

const xs=[d('2024-01-30'),d('2024-01-31'),d('2024-02-01'),d('2024-02-29')]
assert.deepEqual(rebucket(xs,[1,2,4,null],'month','sum').values,[3,4]);assert.deepEqual(rebucket(xs,[1,2,4,null],'month','last').values,[2,4]);assert.deepEqual(rebucket(xs,[1,3,4,null],'month','avg').values,[2,4]);assert.deepEqual(rebucket([xs[3]],[null],'month','sum').values,[null])
assert.equal(pointLabel(Date.UTC(2022,1,11,16),undefined,true),'11 Feb 2022 16:00');assert.equal(pointLabel(d('2026-03-01'),'month',false),'Mar 2026');assert.equal(pointLabel(d('2026-03-01'),undefined,false),'1 Mar 2026')

const text=summarize('Net worth','line',['Jan 2026','Feb 2026'],[{key:'a',label:'Net worth',color:'--c0',values:[100,300]}],unitFormat('eur',{minor:true}))
assert.equal(text,'Line chart of Net worth, Jan 2026 to Feb 2026. Net worth: latest €3.00, highest €3.00 in Feb 2026, lowest €1.00 in Jan 2026.')
assert.match(summarize('x','bar',[],[],unitFormat('int')),/No data/)
console.log('chart-core ok')
