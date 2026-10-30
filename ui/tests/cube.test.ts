import { strict as assert } from 'node:assert'
import { months as monthCount, alignCompare, annotationBands, balanceAt, balanceSeries, colorOf, compareSeries, compareTotals, decodeCube, entityColors, estimateSplit, localFacts, rateSeries, rowsIn, suggestValues, timeseries, totals, whatMoved, type Pill } from '../src/features/telemetry/cube'
import { compareWindow, dayFromIso as d, parseRange, type Range } from '../src/features/telemetry/time-grammar'
import { money, moneyCompact, moneyExact, moneySigned, unitFormat } from '../src/features/telemetry/format'
import { syntheticCube } from './fixtures/synthetic-cube'

// A tiny hand-written cube, so every expected figure can be checked by eye.
const wire={revision:3,as_of:'2024-03-31',base:'EUR',start:'2024-01-01',end:'2024-03-31',
 accounts:[{id:'expenses:food',label:'Groceries',category:'Food',type:'Everyday',pot:'',source:'',class:'expense',estimated:false},{id:'expenses:fun',label:'Cinema',category:'Fun',type:'Everyday',pot:'',source:'',class:'expense',estimated:false},{id:'expenses:est',label:'Cash',category:'Estimated',type:'Everyday',pot:'',source:'',class:'expense',estimated:true},{id:'income:pay',label:'Pay',category:'Salary',type:'Income',pot:'',source:'',class:'income',estimated:false},{id:'assets:bank',label:'Bank',category:'',type:'Bank',pot:'cash',source:'',class:'asset',estimated:false}],
 payees:['Lidl','Spar','Odeon','Employer'],tag_sets:[[],['trip:Lisbon']],
 flows:{d:[0,31,0,60,60,31,10,75],a:[0,0,1,0,2,3,4,1],p:[0,1,2,0,0,3,0,2],t:[0,0,0,1,0,0,0,1],v:[1000,2500,400,-300,5000,-100000,7777,1500],n:[1,2,1,1,1,1,1,3]},
 balances:{d:[0,31,60],net:[100,200,300],assets:[150,250,350],liabilities:[-50,-50,-50],estimated_until:'2024-02-15'},
 rates:{'EUR/USD':{d:[0,31],v:[1.1,1.2]}},metrics:[],annotations:[{id:'a',label:'Lisbon',from:'2024-03-01',to:'2024-03-05',kind:'trip'}],labels:{Food:'Food and drink'}}
const c=decodeCube(wire),all:Range={from:d('2024-01-01'),to:d('2024-03-31')},jan:Range={from:d('2024-01-01'),to:d('2024-01-31')},feb:Range={from:d('2024-02-01'),to:d('2024-02-29')}

// Decode sorts unsorted input and keeps columns aligned.
assert.deepEqual([...c.d],[0,0,10,31,31,60,60,75])
assert.equal(c.v[3]+c.v[4],2500-100000)
assert.throws(() => decodeCube({...wire,flows:{...wire.flows,v:[1]}}),/different lengths/)
assert.throws(() => decodeCube({...wire,start:'nope'}),/not readable/)

// Totals: asset postings are not money in or out, refunds reduce spending, income flips sign.
assert.deepEqual(totals(c,{range:all}),{in:100000,out:1000+2500+400-300+5000+1500,net:100000-10100,estimated:5000,entries:1+2+1+1+1+1+3})
assert.equal(totals(c,{range:jan}).out,1400)
assert.equal(totals(c,{range:{from:d('2024-01-02'),to:d('2024-01-31')}}).out,0)
assert.equal(totals(c,{range:{from:d('2024-02-01'),to:d('2024-02-01')}}).out,2500)

// Pills.
const pills=(p:Pill[]) => totals(c,{range:all,pills:p}).out
assert.equal(pills([{field:'category',op:'is',values:['Food']}]),1000+2500-300)
assert.equal(pills([{field:'category',op:'is not',values:['Food','Fun']}]),5000)
assert.equal(pills([{field:'payee',op:'contains',values:['LID']}]),1000-300+5000)
assert.equal(pills([{field:'payee',op:'does not contain',values:['lid']}]),2500+400+1500)
assert.equal(pills([{field:'trip',op:'is',values:['Lisbon']}]),-300+1500)
assert.equal(pills([{field:'trip',op:'is not',values:['Lisbon']}]),1000+2500+400+5000)
assert.equal(pills([{field:'estimate',op:'is',values:['Recorded']}]),10100-5000)
assert.equal(pills([{field:'amount',op:'over',values:['10']}]),2500+5000)
assert.equal(pills([{field:'amount',op:'under',values:['10']}]),400-300+1500)
assert.equal(pills([{field:'category',op:'is',values:['Food']},{field:'payee',op:'is',values:['Spar']}]),2500)
assert.equal(pills([{field:'category',op:'is',values:['Nope']}]),0)

// Bucketing and split.
const months=timeseries(c,{range:all},{bucket:'month'})
assert.deepEqual([...months.total],[1400,2500,6200])
assert.equal(months.xs.length,3);assert.equal(months.bucket,'month')
assert.equal(timeseries(c,{range:jan}).bucket,'day');assert.equal(timeseries(c,{range:jan}).xs.length,31)
const inc=timeseries(c,{range:all},{bucket:'month',measure:'income'});assert.deepEqual([...inc.total],[0,100000,0])
assert.deepEqual([...timeseries(c,{range:all},{bucket:'month',measure:'net'}).total],[-1400,100000-2500,-6200])
const split=timeseries(c,{range:all},{bucket:'month',split:'category',estimates:'split'})
assert.deepEqual(split.series.map(s => s.key),['Food','Fun','Estimated'])
assert.equal(split.series[0].label,'Food and drink')
assert.equal(split.series.at(-1)?.estimated,true);assert.equal(split.series.at(-1)?.color,'--hatch')
for(let i=0;i<3;i++)assert.equal(split.series.reduce((s,x) => s+x.values[i],0),split.total[i])
assert.deepEqual([...timeseries(c,{range:all},{bucket:'month',estimates:'exclude'}).total],[1400,2500,1200])

// Colour follows the entity, never the rank in a filtered range.
const big=decodeCube(syntheticCube())
const r12=parseRange('12mo',big.bounds)!,colorsA=timeseries(big,{range:r12},{split:'category',estimates:'split'}).series.map(s => [s.key,s.color]),colorsB=timeseries(big,{range:parseRange('2023-05',big.bounds)!,pills:[{field:'category',op:'is not',values:['Rent']}]},{split:'category',estimates:'split'}).series.map(s => [s.key,s.color])
for(const [k,color] of colorsB){const other=colorsA.find(x => x[0]===k);if(other)assert.equal(other[1],color,k)}
assert.equal(colorOf(big,'category','Estimated'),'--hatch');assert.equal(entityColors(big,'category').get('Rent'),'--c0')

// Compare and what moved.
const win=compareWindow(feb,'previous_period',c.bounds)!
assert.equal(compareTotals(c,{range:feb},win).previous?.out,1400)
assert.equal(compareTotals(c,{range:jan},compareWindow(jan,'previous_period',c.bounds)).previous,null)
assert.deepEqual(compareSeries(c,{range:feb},win,{bucket:'month'}),[1400])
assert.equal(compareSeries(c,{range:jan},compareWindow(jan,'previous_period',c.bounds)),null)
const moved=whatMoved(c,{range:feb},win)
assert.deepEqual(moved.map(x => [x.key,x.now,x.before,x.delta]),[['Food',2500,1000,1500],['Fun',0,400,-400],['Salary',0,0,0]].filter(x => x[0]!=='Salary'))
assert.equal(moved[0].share,1500/1900)
assert.deepEqual(whatMoved(c,{range:all},null,{limit:1}).map(x => x.key),['Estimated'])
assert.deepEqual(whatMoved(c,{range:all},null,{by:'payee',limit:1}).map(x => [x.key,x.now]),[['Lidl',5700]])

// Suggestions: scoped to range and other filters, ignore their own field's filter, keep counts and totals.
assert.deepEqual(suggestValues(c,{range:all},'payee','',2).map(x => [x.value,x.count,x.total]),[['Employer',1,100000],['Lidl',3,6300]])
assert.deepEqual(suggestValues(c,{range:jan},'payee').map(x => x.value),['Lidl','Odeon'])
assert.deepEqual(suggestValues(c,{range:all},'payee','SPA').map(x => x.value),['Spar'])
assert.deepEqual(suggestValues(c,{range:all,pills:[{field:'payee',op:'is',values:['Lidl']}]},'payee').length,4)
assert.deepEqual(suggestValues(c,{range:all,pills:[{field:'category',op:'is',values:['Fun']}]},'payee').map(x => x.value),['Odeon'])
assert.deepEqual(suggestValues(c,{range:all},'trip').map(x => [x.value,x.count]),[['Lisbon',4]])
assert.equal(suggestValues(c,{range:all},'category','food and')[0].label,'Food and drink')

// Estimates, balances, rates, annotations.
assert.deepEqual(estimateSplit(c,{range:all}),{recorded:5100,estimated:5000,share:5000/10100})
assert.equal(balanceAt(c,d('2024-01-15')),100);assert.equal(balanceAt(c,d('2024-02-01'),'assets'),250);assert.equal(balanceAt(c,d('2024-03-31')),300)
assert.equal(balanceAt(decodeCube({...wire,balances:{...wire.balances,d:[5,31,60]}}),d('2024-01-01')),null)
const bs=balanceSeries(c,jan);assert.equal(bs.xs.length,31);assert.equal(bs.values[0],100);assert.equal(bs.values[30],100)
const thin=balanceSeries(c,all,{maxPoints:10});assert.ok(thin.xs.length<=12);assert.equal(thin.xs.at(-1),all.to);assert.equal(thin.values.at(-1),300)
assert.deepEqual(rateSeries(c,'EUR/USD',{from:d('2024-02-01'),to:d('2024-03-01')}).values,[1.2]);assert.equal(rateSeries(c,'NOPE',all).xs.length,0)
assert.equal(annotationBands(c,jan).length,0);assert.equal(annotationBands(c,all)[0].label,'Lisbon')
assert.deepEqual([...rowsIn(c,{range:jan})].map(i => c.v[i]).sort((x,y) => x-y),[400,1000])

// Facts.
assert.match(localFacts(c,{range:feb},win)[0].sentence,/^Spending rose €11 \(79%\) compared with the previous period\. The biggest change was Food and drink \(\+€15\)\.$/)
assert.equal(localFacts(c,{range:feb},win)[0].good,false)
assert.match(localFacts(c,{range:jan},null)[0].sentence,/^You spent €14 from 1 Jan 2024 to 31 Jan 2024\./)
assert.equal(localFacts(c,{range:all,pills:[{field:'payee',op:'is',values:['Nope']}]},null)[0].sentence,'No entries match this range and filter.')
assert.equal(localFacts(c,{range:{from:d('2024-03-01'),to:d('2024-03-31')}},null)[1].kind,'estimate_share')

// Money stays integer cents until it is formatted.
const fx=decodeCube({...wire,flows:{d:[0,0,0],a:[0,0,0],p:[0,0,0],t:[0,0,0],v:[10,20,30],n:[1,1,1]}});const sum=totals(fx,{range:all}).out;assert.equal(sum,60);assert.equal(moneyExact(sum),'€0.60');assert.ok(Number.isInteger(sum))
assert.equal(money(123456),'€1,235');assert.equal(money(-123456),'−€1,235');assert.equal(moneyExact(-123456),'−€1,234.56');assert.equal(moneyExact(5),'€0.05');assert.equal(moneyExact(123456,'USD'),'$1,234.56');assert.equal(moneySigned(100),'+€1');assert.equal(moneyCompact(1500000),'€15k');assert.equal(moneyCompact(120000),'€1.2k');assert.equal(moneyCompact(250000000),'€2.5M');assert.equal(moneyCompact(-5000),'−€50')
assert.equal(unitFormat('eur',{minor:true}).axis(500000,100000),'€5.0k');assert.equal(unitFormat('eur').full(12.5),'€12.50');assert.equal(unitFormat('rate').axis(1.085,.005),'1.085');assert.equal(unitFormat('rate').zero,false);assert.equal(unitFormat('days').full(1),'1 day');assert.equal(unitFormat('pct').axis(12.5,2.5),'12.5%');assert.equal(unitFormat('mo').axis(3,1),'3 mo');assert.equal(unitFormat('int').full(1234.4),'1,234')

// Benchmark: a full page recalculation on 20k rows is well inside one frame. Median of many runs avoids a GC pause deciding the result.
const heavy=decodeCube(syntheticCube({density:15}))
assert.ok(heavy.size>=20000,`fixture has ${heavy.size} rows`)
const payload=syntheticCube({density:7}),gz=Bun.gzipSync(new TextEncoder().encode(JSON.stringify(payload)));console.log(`cube ${payload.flows.d.length} rows gzip ${(gz.length/1024).toFixed(0)} KB`)
const range=parseRange('all',heavy.bounds)!,pillset:Pill[]=[{field:'category',op:'is not',values:['Rent']},{field:'payee',op:'contains',values:['a']}],times:number[]=[]
for(let run=0;run<40;run++){
 const q={range,pills:run%2 ? pillset : undefined},w=compareWindow(range,'previous_year',heavy.bounds),t0=performance.now()
 heavy.mask=null
 totals(heavy,q);compareTotals(heavy,q,w);timeseries(heavy,q,{split:'category',estimates:'split'});timeseries(heavy,q,{measure:'income'});compareSeries(heavy,q,w);whatMoved(heavy,q,w);suggestValues(heavy,q,'payee');estimateSplit(heavy,q);balanceSeries(heavy,range)
 times.push(performance.now()-t0)
}
times.sort((a,b) => a-b);const median=times[times.length>>1]
console.log(`cube bench: ${heavy.size} rows, median ${median.toFixed(2)} ms, worst ${times.at(-1)!.toFixed(2)} ms`)
assert.ok(median<16,`recalculation took ${median} ms`)
// Same fixture as TestMonthsBetweenCountsDaysOverMonthLength in the Go semantic package.
const mo=(a:string,b:string)=>monthCount({from:d(a),to:d(b)} as Range)
assert.ok(Math.abs(mo('2026-08-02','2026-10-30')-(1+60/31))<1e-9)
assert.ok(Math.abs(mo('2026-05-04','2026-08-01')-(1+60/31))<1e-9)
assert.equal(mo('2026-07-01','2026-09-30'),3)
console.log('cube ok')
assert.deepEqual(alignCompare([0,5,6],2),[5,6])
assert.deepEqual(alignCompare([5,6],3),[null,5,6])
