import { DAY, autoBucket, bucketEdges, dayFromIso, rangeLabel, type Bounds, type Bucket, type CompareWindow, type Range } from './time-grammar'
import { money, percent } from './format'

// Wire shape of GET /api/cube, from docs/telemetry.md.
export type CubeAccount={id:string;label:string;category:string;type:string;pot:string;source:string;class:string;estimated:boolean}
export type Annotation={id:string;label:string;from:string;to:string;kind?:string;tag_rule?:string}
export type CubeWire={revision:number;as_of:string;base:string;start:string;end:string;accounts:CubeAccount[];payees:string[];tag_sets:string[][];
 flows:{d:number[];a:number[];p:number[];t:number[];v:number[];n:number[]};
 balances:{d:number[];net:number[];assets:number[];liabilities:number[];estimated_until?:string};
 rates:Record<string,{d:number[];v:number[]}>;metrics:CubeMetric[];annotations:Annotation[];labels:Record<string,string>;aliases?:{pattern:string;name:string}[];unvalued?:number}
export type CubeMetric={id:string;label:string;description?:string;unit:string;kind:string;additivity:string;estimates:string;accounts?:string[]|null;exclude?:string[]|null;sign?:number;where?:string;dimensions?:string[]|null}
export type Dimension='category'|'account'|'payee'|'trip'|'type'|'pot'|'source'|'class'|'estimate'
export type Op='is'|'is not'|'contains'|'does not contain'|'over'|'under'
export type Pill={field:Dimension|'amount';op:Op;values:string[]}
export type Query={range:Range;pills?:Pill[]}
export type Measure='spending'|'income'|'net'|CubeMetric
export type EstimatePolicy='include'|'exclude'|'split'
export type DimIndex={values:string[];row:Int32Array}
export type Cube={revision:number;asOf:string;base:string;startMs:number;endMs:number;bounds:Bounds;size:number;
 d:Int32Array;a:Int32Array;p:Int32Array;t:Int32Array;v:Float64Array;n:Int32Array;
 accounts:CubeAccount[];payees:string[];tagSets:string[][];cls:Uint8Array;est:Uint8Array;
 balances:{d:Int32Array;net:Float64Array;assets:Float64Array;liabilities:Float64Array;estimatedUntil:number|null};
 rates:Record<string,{d:Int32Array;v:Float64Array}>;annotations:Annotation[];labels:Record<string,string>;metrics:CubeMetric[];aliases:{re:RegExp;name:string}[];unvalued:number;dims:Map<Dimension,DimIndex>;colors:Map<Dimension,Map<string,string>>;mask:{key:string;mask:Uint8Array|null}|null}
export type Totals={in:number;out:number;net:number;estimated:number;entries:number}
export type SeriesOut={key:string;label:string;color:string;values:Float64Array;estimated?:boolean}
export type Timeseries={bucket:Bucket;xs:number[];series:SeriesOut[];total:Float64Array}
export type Contributor={key:string;label:string;now:number;before:number;delta:number;share:number}
export type Suggestion={value:string;label:string;count:number;total:number}
export type Fact={kind:string;sentence:string;value?:number;text?:string;direction?:'up'|'down'|'flat';good?:boolean|null;contributors?:{name:string;key?:string;delta:number;text:string;share:number}[]}

const EXPENSE=1,INCOME=2
const column=<T extends Int32Array|Float64Array>(make:new (n:number) => T,src:number[],order:Int32Array|null):T => {const out=new make(src.length);if(order)for(let i=0;i<src.length;i++)out[i]=src[order[i]];else out.set(src);return out}

// Accepts untrusted JSON: a cube that does not line up is an error, never a silently wrong number.
export function decodeCube(wire:CubeWire):Cube {
 const f=wire.flows,size=f?.d?.length
 if(typeof size!=='number' || [f.a,f.p,f.t,f.v,f.n].some(c => !Array.isArray(c) || c.length!==size))throw new Error('Cube flows columns have different lengths')
 const startMs=dayFromIso(wire.start),endMs=dayFromIso(wire.end),asOf=dayFromIso(wire.as_of)
 if(!Number.isFinite(startMs+endMs+asOf))throw new Error('Cube dates are not readable')
 let sorted=true;for(let i=1;i<size;i++)if(f.d[i]<f.d[i-1]){sorted=false;break}
 const order=sorted ? null : Int32Array.from(f.d.keys()).sort((x,y) => f.d[x]-f.d[y])
 const accounts=wire.accounts,cls=new Uint8Array(accounts.length),est=new Uint8Array(accounts.length)
 // `class` is an optional field on the account, so the id prefix decides when it is empty.
 accounts.forEach((a,i) => {cls[i]=a.class==='expense' || a.id.startsWith('expenses:') ? EXPENSE : a.class==='income' || a.id.startsWith('income:') ? INCOME : 0;est[i]=a.estimated ? 1 : 0})
 const b=wire.balances
 return {revision:wire.revision,asOf:wire.as_of,base:wire.base,startMs,endMs,bounds:{today:asOf,start:startMs,end:endMs},size,
  d:column(Int32Array,f.d,order),a:column(Int32Array,f.a,order),p:column(Int32Array,f.p,order),t:column(Int32Array,f.t,order),v:column(Float64Array,f.v,order),n:column(Int32Array,f.n,order),
  accounts,payees:wire.payees,tagSets:wire.tag_sets,cls,est,
  balances:{d:Int32Array.from(b?.d ?? []),net:Float64Array.from(b?.net ?? []),assets:Float64Array.from(b?.assets ?? []),liabilities:Float64Array.from(b?.liabilities ?? []),estimatedUntil:b?.estimated_until ? dayFromIso(b.estimated_until) : null},
  rates:Object.fromEntries(Object.entries(wire.rates ?? {}).map(([k,r]) => [k,{d:Int32Array.from(r.d),v:Float64Array.from(r.v)}])),
  // A stored annotation and the trip tag it describes both arrive; one band per trip is enough.
  annotations:(wire.annotations ?? []).filter((x,i,all) => all.findIndex(y => y.label===x.label && y.from===x.from)===i),labels:wire.labels ?? {},metrics:wire.metrics ?? [],unvalued:wire.unvalued ?? 0,
  aliases:(wire.aliases ?? []).flatMap(a => {try {return [{re:new RegExp(a.pattern,'i'),name:a.name}]} catch {return []}}),dims:new Map(),colors:new Map(),mask:null}
}
export const label=(cube:Cube,value:string) => cube.labels[value] ?? value
const lower=(a:Int32Array,x:number) => {let l=0,h=a.length;while(l<h){const m=(l+h)>>>1;if(a[m]<x)l=m+1;else h=m}return l}
const span=(c:Cube,r:Range):[number,number] => [lower(c.d,Math.round((r.from-c.startMs)/DAY)),lower(c.d,Math.round((r.to-c.startMs)/DAY)+1)]

// Each dimension resolves to a dictionary and a row column once per cube; -1 means the row has no value.
export function dim(c:Cube,key:Dimension):DimIndex {
 const hit=c.dims.get(key);if(hit)return hit
 const row=new Int32Array(c.size);let values:string[]
 if(key==='payee'){values=c.payees;row.set(c.p)}
 else if(key==='trip'){
  const seen=new Map<string,number>(),of=c.tagSets.map(set => {const tag=set.find(x => x.startsWith('trip:'));if(!tag)return -1;const name=tag.slice(5);if(!seen.has(name))seen.set(name,seen.size);return seen.get(name)!})
  values=[...seen.keys()];for(let i=0;i<c.size;i++)row[i]=of[c.t[i]] ?? -1
 } else if(key==='estimate'){values=['Recorded','Estimated'];for(let i=0;i<c.size;i++)row[i]=c.est[c.a[i]]}
 else {
  const pick=(a:CubeAccount) => key==='category' ? a.category : key==='account' ? a.label : key==='type' ? a.type : key==='source' ? a.source : key==='class' ? a.class : a.pot
  const seen=new Map<string,number>(),of=c.accounts.map(a => {const v=pick(a);if(!v)return -1;if(!seen.has(v))seen.set(v,seen.size);return seen.get(v)!})
  values=[...seen.keys()];for(let i=0;i<c.size;i++)row[i]=of[c.a[i]]
 }
 const made={values,row};c.dims.set(key,made);return made
}
function buildMask(c:Cube,pills:Pill[]):Uint8Array {
 const mask=new Uint8Array(c.size).fill(1)
 for(const pill of pills) {
  if(pill.field==='amount'){
   // shortcut: rows aggregate a day/account/payee, so this tests the row's average entry; the server's metric_query is exact.
   const limit=Number(pill.values[0])*100,over=pill.op==='over'
   for(let i=0;i<c.size;i++){const avg=Math.abs(c.v[i])/Math.max(1,c.n[i]);if(over ? avg<=limit : avg>=limit)mask[i]=0}
   continue
  }
  const {values,row}=dim(c,pill.field),wanted=new Uint8Array(values.length+1),negative=pill.op==='is not' || pill.op==='does not contain'
  if(pill.op==='contains' || pill.op==='does not contain'){const needles=pill.values.map(x => x.toLowerCase());values.forEach((v,i) => {const low=v.toLowerCase();if(needles.some(n => low.includes(n)))wanted[i+1]=1})}
  else {const set=new Set(pill.values);values.forEach((v,i) => {if(set.has(v))wanted[i+1]=1})}
  for(let i=0;i<c.size;i++)if((wanted[row[i]+1]===1)===negative)mask[i]=0
 }
 return mask
}
function pillMask(c:Cube,pills:Pill[]|undefined):Uint8Array|null {
 if(!pills?.length)return null
 const key=JSON.stringify(pills);if(c.mask?.key===key)return c.mask.mask
 const mask=buildMask(c,pills);c.mask={key,mask};return mask
}
// The sign convention lives here and matches semantic/cube.go: `v` is the raw posting, so expenses are positive, income negative, refunds reduce spending.
// A measure becomes one multiplier per account, which is all the row loops need.
const matches=(id:string,prefixes:string[]|null|undefined) => !!prefixes?.some(p => id===p || id.startsWith(p))
export function weights(c:Cube,m:Measure):Float64Array {
 const w=new Float64Array(c.accounts.length)
 c.accounts.forEach((a,i) => {
  if(typeof m==='object'){if((!m.accounts?.length || matches(a.id,m.accounts)) && !matches(a.id,m.exclude))w[i]=m.sign || 1;return}
  const k=c.cls[i];w[i]=k===EXPENSE ? (m==='income' ? 0 : m==='spending' ? 1 : -1) : k===INCOME ? (m==='spending' ? 0 : -1) : 0
 })
 return w
}
// Only what the cube can reproduce: valued flows with no CEL filter, or net worth from its balance columns. The rest stays on the server's figure.
export const balanceField=(m:CubeMetric):BalanceField|null => {
 if(m.kind!=='balance' || m.where || m.exclude?.length)return null
 const a=m.accounts ?? [],assets=a.includes('assets:'),debts=a.includes('liabilities:')
 return a.length===2 && assets && debts ? 'net' : a.length===1 && assets ? 'assets' : a.length===1 && debts ? 'liabilities' : null
}
export const cubeCanCompute=(m:CubeMetric|undefined,split?:string):m is CubeMetric => !!m && !m.where && (m.unit==='eur' || m.unit==='int') && (m.kind==='flow' || m.kind==='rate' || balanceField(m)!==null) && (!split || ['category','account','payee','trip','type','pot','source','class'].includes(split))
// Polarity from the ledger's signs, as the server's lowerIsGood reads it: spending and income with sign 1 are good when they fall; assets and debts run the other way.
export function goodWhen(m:CubeMetric):'up'|'down'|'neutral' {
 const a=m.accounts ?? [];if(!a.length)return 'neutral'
 const lower=(id:string) => (id.startsWith('expenses:') || id.startsWith('income:'))===((m.sign || 1)!==-1),first=lower(a[0])
 return a.every(id => lower(id)===first) ? (first ? 'down' : 'up') : 'neutral'
}
const months=(r:Range) => Math.max(1,(new Date(r.to).getUTCFullYear()-new Date(r.from).getUTCFullYear())*12+new Date(r.to).getUTCMonth()-new Date(r.from).getUTCMonth()+1)
// The figure a metric shows for a range: flows sum, `avg` and `rate` read per month, `last` the closing month, balances the closing value.
export function metricTotal(c:Cube,q:Query,m:CubeMetric,estimates?:EstimatePolicy):{total:number;estimated:number;entries:number} {
 const field=balanceField(m)
 if(field){const v=balanceAt(c,q.range.to,field);return {total:(v ?? 0)*(m.sign || 1),estimated:0,entries:0}}
 const [lo,hi]=span(c,q.range),mask=pillMask(c,q.pills),w=weights(c,m),{a,v,n,est}=c,count=m.unit==='int',policy=estimates ?? (m.estimates as EstimatePolicy)
 let total=0,estimated=0,entries=0
 const lastMonth=m.additivity==='last' ? Date.UTC(new Date(q.range.to).getUTCFullYear(),new Date(q.range.to).getUTCMonth(),1) : -Infinity
 for(let i=lo;i<hi;i++){
  if((mask && !mask[i]) || !w[a[i]])continue
  if(policy==='exclude' && est[a[i]])continue
  if(c.startMs+c.d[i]*DAY<lastMonth)continue
  const x=count ? n[i] : w[a[i]]*v[i];total+=x;entries+=n[i];if(est[a[i]])estimated+=x
 }
 if(m.kind==='rate' || m.additivity==='avg'){const k=months(q.range);total/=k;estimated/=k}
 return {total,estimated,entries}
}

export function totals(c:Cube,q:Query):Totals {
 const [lo,hi]=span(c,q.range),m=pillMask(c,q.pills);let inn=0,out=0,estimated=0,entries=0
 const {a,v,n,cls,est}=c
 for(let i=lo;i<hi;i++){if(m && !m[i])continue;const k=cls[a[i]];if(k===EXPENSE){out+=v[i];if(est[a[i]])estimated+=v[i];entries+=n[i]}else if(k===INCOME){inn-=v[i];entries+=n[i]}}
 return {in:inn,out,net:inn-out,estimated,entries}
}
// Colour follows the entity: slots go by all-time rank across the whole cube, so a range or filter can never reshuffle them.
const SLOTS=['--c0','--c1','--c2','--c3','--c4']
export function entityColors(c:Cube,key:Dimension):Map<string,string> {
 const hit=c.colors.get(key);if(hit)return hit
 const {values,row}=dim(c,key),spend=new Float64Array(values.length),earn=new Float64Array(values.length)
 for(let i=0;i<c.size;i++){if(row[i]<0 || c.est[c.a[i]])continue;const k=c.cls[c.a[i]];if(k===EXPENSE)spend[row[i]]+=Math.abs(c.v[i]);else if(k===INCOME)earn[row[i]]+=Math.abs(c.v[i])}
 // Spending and income rank separately: they rarely share a chart, and income categories must not all land in Other.
 const map=new Map<string,string>(),assign=(sum:Float64Array) => {let rank=0;for(const idx of [...values.keys()].sort((x,y) => sum[y]-sum[x] || x-y)){if(!sum[idx] || map.has(values[idx]))continue;map.set(values[idx],rank<SLOTS.length ? SLOTS[rank] : '--series-other');rank++}}
 assign(spend);assign(earn)
 c.colors.set(key,map);return map
}
export const ESTIMATED='Estimated',OTHER='Other'
export function colorOf(c:Cube,key:Dimension,value:string):string {return value===ESTIMATED ? '--hatch' : entityColors(c,key).get(value) ?? '--series-other'}
const bucketIndex=(c:Cube,r:Range,b:Bucket):{xs:number[];loD:number;of:Int32Array} => {
 const xs=bucketEdges(r,b),loD=Math.round((r.from-c.startMs)/DAY),of=new Int32Array(Math.round((r.to-r.from)/DAY)+1)
 let k=0,next=xs.length>1 ? xs[1] : Infinity
 for(let i=0,t=r.from;i<of.length;i++,t+=DAY){while(t>=next){k++;next=k+1<xs.length ? xs[k+1] : Infinity}of[i]=k}
 return {xs,loD,of}
}
export function timeseries(c:Cube,q:Query,{measure='spending',bucket,split,estimates='include'}:{measure?:Measure;bucket?:Bucket;split?:Dimension;estimates?:EstimatePolicy}={}):Timeseries {
 const w=weights(c,measure),mInt=typeof measure==='object' && measure.unit==='int'
 const b=bucket ?? autoBucket(q.range),[lo,hi]=span(c,q.range),m=pillMask(c,q.pills),{xs,loD,of}=bucketIndex(c,q.range,b),len=xs.length,total=new Float64Array(len)
 const {a,d,v,n,est}=c
 if(!split){
  for(let i=lo;i<hi;i++){if(m && !m[i])continue;if(estimates==='exclude' && est[a[i]])continue;const k=w[a[i]];if(k)total[of[d[i]-loD]]+=mInt ? n[i] : k*v[i]}
  return {bucket:b,xs,total,series:[{key:'total',label:typeof measure==='object' ? measure.label : measure==='income' ? 'Money in' : measure==='net' ? 'Net' : 'Money out',color:typeof measure==='object' ? '--c0' : measure==='income' ? '--c2' : measure==='net' ? '--c3' : '--c4',values:total}]}
 }
 const {values,row}=dim(c,split),colors=entityColors(c,split),slot=new Int32Array(values.length+1).fill(-1),named:string[]=[]
 values.forEach((value,i) => {const color=colors.get(value);if(color && color!=='--series-other'){slot[i+1]=named.length;named.push(value)}})
 const otherIdx=named.length,estIdx=named.length+1,grid=Array.from({length:named.length+2},() => new Float64Array(len))
 for(let i=lo;i<hi;i++){
  if(m && !m[i])continue
  const isEst=est[a[i]];if(isEst && estimates==='exclude')continue
  const value=w[a[i]]*(mInt ? n[i] : v[i]);if(value===0)continue
  const s=isEst && estimates==='split' ? estIdx : slot[row[i]+1]<0 ? otherIdx : slot[row[i]+1]
  const at=of[d[i]-loD];grid[s][at]+=value;total[at]+=value
 }
 const series:SeriesOut[]=named.map((name,i) => ({key:name,label:label(c,name),color:colors.get(name)!,values:grid[i]}))
 series.push({key:OTHER,label:OTHER,color:'--series-other',values:grid[otherIdx]},{key:ESTIMATED,label:ESTIMATED,color:'--hatch',values:grid[estIdx],estimated:true})
 return {bucket:b,xs,total,series:series.filter(s => s.values.some(x => x!==0))}
}
// The comparison window bucketed like the current one, aligned by position so it overlays directly.
export function compareSeries(c:Cube,q:Query,win:CompareWindow|null,{measure='spending',bucket,estimates='include'}:{measure?:Measure;bucket?:Bucket;estimates?:EstimatePolicy}={}):(number|null)[]|null {
 if(!win?.available)return null
 const b=bucket ?? autoBucket(q.range),base=timeseries(c,{range:win,pills:q.pills},{measure,bucket:b,estimates}),xs=bucketEdges(q.range,b)
 return xs.map((_,i) => i<base.xs.length ? base.total[i] : null)
}
export function compareTotals(c:Cube,q:Query,win:CompareWindow|null):{current:Totals;previous:Totals|null} {return {current:totals(c,q),previous:win?.available ? totals(c,{range:win,pills:q.pills}) : null}}
const groupSum=(c:Cube,q:Query,row:Int32Array,size:number,measure:Measure) => {
 const [lo,hi]=span(c,q.range),m=pillMask(c,q.pills),sum=new Float64Array(size),w=weights(c,measure),{a,v}=c
 for(let i=lo;i<hi;i++){if((m && !m[i]) || row[i]<0)continue;sum[row[i]]+=w[a[i]]*v[i]}
 return sum
}
// With no window this ranks by size, so the same list still answers "where did it go".
export function whatMoved(c:Cube,q:Query,win:CompareWindow|null,{by='category',measure='spending',limit=7}:{by?:Dimension;measure?:Measure;limit?:number}={}):Contributor[] {
 const {values,row}=dim(c,by),now=groupSum(c,q,row,values.length,measure),then=win?.available ? groupSum(c,{range:win,pills:q.pills},row,values.length,measure) : null
 let moved=0;const rows:Contributor[]=[]
 values.forEach((value,i) => {const b=then ? then[i] : 0,delta=now[i]-b;if(now[i]===0 && b===0)return;moved+=Math.abs(then ? delta : now[i]);rows.push({key:value,label:label(c,value),now:now[i],before:b,delta,share:0})})
 for(const r of rows)r.share=moved ? Math.abs(then ? r.delta : r.now)/moved : 0
 return rows.sort((x,y) => then ? Math.abs(y.delta)-Math.abs(x.delta) : y.now-x.now).slice(0,limit)
}
export function suggestValues(c:Cube,q:Query,field:Dimension,prefix='',limit=10):Suggestion[] {
 const {values,row}=dim(c,field),[lo,hi]=span(c,q.range),others=q.pills?.filter(p => p.field!==field),m=pillMask(c,others),count=new Int32Array(values.length),sum=new Float64Array(values.length),{a,v,n,cls}=c
 for(let i=lo;i<hi;i++){if((m && !m[i]) || row[i]<0 || !cls[a[i]])continue;count[row[i]]+=n[i];sum[row[i]]+=Math.abs(v[i])}
 const needle=prefix.trim().toLowerCase(),out:Suggestion[]=[]
 values.forEach((value,i) => {if(!count[i])return;const text=label(c,value);if(needle && !text.toLowerCase().includes(needle))return;out.push({value,label:text,count:count[i],total:sum[i]})})
 return out.sort((x,y) => y.total-x.total).slice(0,limit)
}
export function estimateSplit(c:Cube,q:Query):{recorded:number;estimated:number;share:number} {const t=totals(c,q);return {recorded:t.out-t.estimated,estimated:t.estimated,share:t.out ? t.estimated/t.out : 0}}
export type BalanceField='net'|'assets'|'liabilities'
// Balances list only changed days, so the value at any day is the last one at or before it.
export function balanceAt(c:Cube,t:number,field:BalanceField='net'):number|null {const i=lower(c.balances.d,Math.round((t-c.startMs)/DAY)+1)-1;return i<0 ? null : c.balances[field][i]}
export function balanceSeries(c:Cube,r:Range,{field='net',maxPoints=1400}:{field?:BalanceField;maxPoints?:number}={}):{xs:number[];values:number[]} {
 const days=Math.round((r.to-r.from)/DAY)+1,step=Math.max(1,Math.ceil(days/maxPoints)),xs:number[]=[],values:number[]=[],b=c.balances,arr=b[field]
 let i=lower(b.d,Math.round((r.from-c.startMs)/DAY)+1)-1
 for(let k=0;k<days;k+=step){const t=r.from+k*DAY,dd=Math.round((t-c.startMs)/DAY);while(i+1<b.d.length && b.d[i+1]<=dd)i++;if(i<0)continue;xs.push(t);values.push(arr[i])}
 if(xs.length && xs.at(-1)!==r.to){const last=balanceAt(c,r.to,field);if(last!==null){xs.push(r.to);values.push(last)}}
 return {xs,values}
}
export function rateSeries(c:Cube,pair:string,r:Range):{xs:number[];values:number[]} {
 const s=c.rates[pair],xs:number[]=[],values:number[]=[];if(!s)return {xs,values}
 const lo=Math.round((r.from-c.startMs)/DAY),hi=Math.round((r.to-c.startMs)/DAY)
 for(let i=lower(s.d,lo);i<s.d.length && s.d[i]<=hi;i++){xs.push(c.startMs+s.d[i]*DAY);values.push(s.v[i])}
 return {xs,values}
}
export function annotationBands(c:Cube,r?:Range):{from:number;to:number;label:string}[] {
 const out=c.annotations.map(x => ({from:dayFromIso(x.from),to:dayFromIso(x.to),label:x.label}));return r ? out.filter(x => x.to>=r.from && x.from<=r.to) : out
}
// Row indices newest first, for a grid. Asset and equity postings are left out because they are not money in or out.
export function rowsIn(c:Cube,q:Query):Int32Array {
 const [lo,hi]=span(c,q.range),m=pillMask(c,q.pills),out=new Int32Array(hi-lo);let k=0
 for(let i=hi-1;i>=lo;i--)if((!m || m[i]) && c.cls[c.a[i]])out[k++]=i
 return out.subarray(0,k)
}
// Facts the browser can compute while exploring. A saved view's sentence comes from the server's `facts`.
export function localFacts(c:Cube,q:Query,win:CompareWindow|null,metric?:CubeMetric):Fact[] {
 const name=metric?.label ?? 'Spending',lowerGood=metric ? goodWhen(metric)==='down' : true,measure:Measure=metric ?? 'spending'
 const read=(range:Range) => {if(metric){const r=metricTotal(c,{range,pills:q.pills},metric);return {out:r.total,estimated:r.estimated,entries:r.entries || (r.total ? 1 : 0)}}const r=totals(c,{range,pills:q.pills});return {out:r.out,estimated:r.estimated,entries:r.entries}}
 const t=read(q.range),prev=win?.available ? read(win) : null,facts:Fact[]=[],code=c.base
 if(!t.entries)return [{kind:'period_change',sentence:'No entries match this range and filter.'}]
 const moved=metric && balanceField(metric) ? [] : whatMoved(c,q,win,{measure}),contributors=moved.map(x => ({name:x.label,key:x.key,delta:prev ? x.delta : x.now,text:prev ? `${x.delta>=0 ? '+' : ''}${money(x.delta,code)}` : money(x.now,code),share:x.share}))
 if(prev && prev.out){
  const delta=t.out-prev.out,top=moved[0],direction=delta>0 ? 'up' as const : delta<0 ? 'down' as const : 'flat' as const
  facts.push({kind:'period_change',direction,good:delta===0 ? null : lowerGood ? delta<0 : delta>0,value:delta,text:money(delta,code),contributors,
   sentence:delta===0 ? `${name} matched the ${win!.label}.` : `${name} ${delta>0 ? 'rose' : 'fell'} ${money(Math.abs(delta),code)} (${percent(Math.abs(delta)/Math.abs(prev.out)*100,0)}) compared with the ${win!.label}.${top ? ` The biggest change was ${top.label} (${top.delta>=0 ? '+' : ''}${money(top.delta,code)}).` : ''}`})
 } else {
  const top=moved[0];facts.push({kind:'period_change',direction:'flat',good:null,value:t.out,text:money(t.out,code),contributors,sentence:`${metric ? `${name} was ${money(t.out,code)}` : `You spent ${money(t.out,code)}`} from ${rangeLabel(q.range)}.${top ? ` The largest category was ${top.label}.` : ''}`})
 }
 if(t.estimated && t.out){const share=t.estimated/t.out*100;facts.push({kind:'estimate_share',value:share,text:percent(share,0),direction:'flat',good:null,sentence:`${percent(share,0)} of this is estimated, not from statements.`})}
 return facts
}
