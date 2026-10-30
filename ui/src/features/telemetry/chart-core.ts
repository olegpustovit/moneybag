import { DAY, MONTH_NAMES, bucketLabel, bucketNext, bucketStart, dayLabel, type Bucket } from './time-grammar'
import type { ValueFormat } from './format'

export type Mark='line'|'area'|'stack'|'bar'
export type ChartSeries={key:string;label:string;color:string;values:ArrayLike<number|null>;text?:(string|undefined)[];estimated?:boolean;dashed?:boolean}
export type Band={from:number;to:number;label:string}

const HOUR=36e5
const STEPS:[number,'hour'|'day'|'month'|'year',number][]=[[HOUR,'hour',1],[3*HOUR,'hour',3],[6*HOUR,'hour',6],[12*HOUR,'hour',12],[DAY,'day',1],[2*DAY,'day',2],[7*DAY,'day',7],[14*DAY,'day',14],[30.4*DAY,'month',1],[61*DAY,'month',2],[91*DAY,'month',3],[182*DAY,'month',6],[365*DAY,'year',1],[730*DAY,'year',2],[1826*DAY,'year',5],[3652*DAY,'year',10]]
export type Ticks={ticks:number[];labels:string[]}
// A real UTC time axis. Labels are always human; a raw timestamp can never reach the screen.
export function timeTicks(min:number,rawMax:number,width:number,gap=64):Ticks {
 // A label centred on the far edge would be cut in half, so the last 14px carry no tick.
 const max=rawMax-(rawMax-min)/Math.max(1,width)*14,span=Math.max(1,rawMax-min),[,unit,n]=STEPS.find(([ms,step]) => ms/span*width>=gap && (span<=120*DAY || (step!=='day' && step!=='hour'))) ?? STEPS[STEPS.length-1],ticks:number[]=[],labels:string[]=[]
 let lastYear=NaN
 const push=(t:number,short:string,day:Date) => {const y=day.getUTCFullYear();ticks.push(t);labels.push(lastYear!==y ? `${short}\n${y}` : short);lastYear=y}
 if(unit==='year'){for(let y=Math.ceil(new Date(min).getUTCFullYear()/n)*n;;y+=n){const t=Date.UTC(y,0,1);if(t>max)break;if(t>=min){ticks.push(t);labels.push(String(y))}}return {ticks,labels}}
 if(unit==='month'){const s=new Date(min);for(let i=s.getUTCFullYear()*12+s.getUTCMonth();;i++){const y=Math.floor(i/12),m=i%12,t=Date.UTC(y,m,1);if(t>max)break;if(t<min || m%n)continue;const d=new Date(t);push(t,MONTH_NAMES[m],d)}return {ticks,labels}}
 if(unit==='day'){
  // Week ticks fall on Mondays, so a fortnight of ticks matches the week buckets underneath.
  const size=n*DAY,monday=4*DAY,first=n>=7 ? Math.ceil((min-monday)/size)*size+monday : Math.ceil(min/size)*size
  for(let t=first;t<=max;t+=size){const d=new Date(t);push(t,`${d.getUTCDate()} ${MONTH_NAMES[d.getUTCMonth()]}`,d)}
  return {ticks,labels}
 }
 for(let t=Math.ceil(min/(n*HOUR))*n*HOUR;t<=max;t+=n*HOUR){const d=new Date(t),hh=String(d.getUTCHours()).padStart(2,'0');if(d.getUTCHours()===0 || !ticks.length)push(t,`${d.getUTCDate()} ${MONTH_NAMES[d.getUTCMonth()]}`,d);else {ticks.push(t);labels.push(`${hh}:00`)}}
 return {ticks,labels}
}
export function categoryTicks(categories:string[],width:number,charPx=6.4,maxChars=14):Ticks {
 const longest=Math.min(maxChars,Math.max(1,...categories.map(c => c.length))),every=Math.max(1,Math.ceil(categories.length/Math.max(1,Math.floor(width/(longest*charPx+10))))),ticks:number[]=[],labels:string[]=[]
 for(let i=0;i<categories.length;i+=every){ticks.push(i);labels.push(categories[i].length>maxChars ? `${categories[i].slice(0,maxChars-1)}…` : categories[i])}
 return {ticks,labels}
}
// Bars own the whole bucket, so a March bar spans March; the cursor snaps to the bucket's middle.
// A window clips the outer buckets, so every chart on a page shares one x range and a partial month draws as partial.
export function bucketGeometry(xs:number[],bucket?:Bucket,window?:{from:number;to:number}):{edges:number[];mids:number[]} {
 const n=xs.length;if(!n)return {edges:[],mids:[]}
 const edges=xs.slice()
 edges.push(bucket ? bucketNext(xs[n-1],bucket) : n>1 ? xs[n-1]+(xs[n-1]-xs[n-2]) : xs[0]+DAY)
 if(window){edges[0]=Math.max(edges[0],window.from);edges[n]=Math.min(edges[n],window.to+DAY)}
 return {edges,mids:xs.map((x,i) => (x+edges[i+1])/2)}
}
export type Stack={lo:Float64Array[];hi:Float64Array[];min:number;max:number}
// Positive and negative values stack away from zero on their own sides, so a refund never hides a bar.
export function stackSeries(series:{values:ArrayLike<number|null>}[],length:number):Stack {
 const pos=new Float64Array(length),neg=new Float64Array(length),lo:Float64Array[]=[],hi:Float64Array[]=[];let min=0,max=0
 for(const s of series){
  const a=new Float64Array(length),b=new Float64Array(length)
  for(let i=0;i<length;i++){const v=s.values[i] ?? 0;if(v>=0){a[i]=pos[i];pos[i]+=v;b[i]=pos[i];if(pos[i]>max)max=pos[i]}else {b[i]=neg[i];neg[i]+=v;a[i]=neg[i];if(neg[i]<min)min=neg[i]}}
  lo.push(a);hi.push(b)
 }
 return {lo,hi,min,max}
}
export function yExtent(series:{values:ArrayLike<number|null>}[],from=0,to=Infinity):[number,number] {
 let min=Infinity,max=-Infinity
 for(const s of series){const end=Math.min(to,s.values.length);for(let i=from;i<end;i++){const v=s.values[i];if(v===null || v===undefined || !Number.isFinite(v))continue;if(v<min)min=v;if(v>max)max=v}}
 return min===Infinity ? [0,1] : [min,max]
}
// Flows and stacks keep zero so bar lengths stay honest. Balances and rates hug the data so small moves stay visible.
export function yRange(min:number,max:number,zero:boolean):[number,number] {
 if(zero){const lo=Math.min(0,min),hi=Math.max(0,max),pad=(hi-lo)*.08 || 1;return [lo<0 ? lo-pad : 0,hi>0 ? hi+pad : 0]}
 const pad=(max-min)*.12 || Math.abs(max)*.05 || 1;return [min-pad,max+pad]
}
// Re-bucket raw points for the visible span. Flows add up, balances keep the last value, rates average.
export function rebucket(xs:number[],values:ArrayLike<number|null>,bucket:Bucket,how:'sum'|'last'|'avg'):{xs:number[];values:(number|null)[]} {
 const outX:number[]=[],out:(number|null)[]=[];let cur=NaN,sum=0,count=0,last:number|null=null
 const flush=() => {if(Number.isNaN(cur))return;outX.push(cur);out.push(count ? (how==='sum' ? sum : how==='avg' ? sum/count : last) : null)}
 for(let i=0;i<xs.length;i++){const b=bucketStart(Math.floor(xs[i]/DAY)*DAY,bucket);if(b!==cur){flush();cur=b;sum=0;count=0;last=null}const v=values[i];if(v!==null && v!==undefined){sum+=v;count++;last=v}}
 flush();return {xs:outX,values:out}
}
export function pointLabel(x:number,bucket:Bucket|undefined,intraday:boolean):string {
 if(bucket)return bucketLabel(x,bucket)
 if(intraday){const d=new Date(x);return `${dayLabel(Math.floor(x/DAY)*DAY)} ${String(d.getUTCHours()).padStart(2,'0')}:${String(d.getUTCMinutes()).padStart(2,'0')}`}
 return dayLabel(Math.floor(x/DAY)*DAY)
}
// The same picture in words, for people who cannot see the canvas.
export function summarize(title:string,mark:Mark,labels:string[],series:ChartSeries[],format:ValueFormat):string {
 const kind=mark==='bar' ? 'Bar chart' : mark==='stack' ? 'Stacked bar chart' : mark==='area' ? 'Area chart' : 'Line chart'
 if(!labels.length || !series.length)return `${kind} of ${title}. No data.`
 const parts=series.slice(0,6).map(s => {
  let hi=-Infinity,lo=Infinity,hiAt=0,loAt=0,last:number|null=null
  for(let i=0;i<labels.length;i++){const v=s.values[i];if(v===null || v===undefined)continue;if(v>hi){hi=v;hiAt=i}if(v<lo){lo=v;loAt=i}last=v}
  return last===null ? `${s.label}: no data` : `${s.label}: latest ${format.full(last)}, highest ${format.full(hi)} in ${labels[hiAt]}, lowest ${format.full(lo)} in ${labels[loAt]}`
 })
 return `${kind} of ${title}, ${labels[0]} to ${labels[labels.length-1]}. ${parts.join('. ')}.${series.length>6 ? ` ${series.length-6} more series in the table view.` : ''}`
}
