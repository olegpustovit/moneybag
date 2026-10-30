import type { ChartValue } from './values'
export type ChartRow=Record<string,string|number>
export function journalFilter(axis:ChartValue['x'],x:string):{from?:string;to?:string;account?:string}|undefined {
 if(axis.kind==='month' && /^\d{4}-(0[1-9]|1[0-2])$/.test(x)) {
  const [year,month]=x.split('-').map(Number), days=month===2 ? (year%4===0 && (year%100!==0 || year%400===0) ? 29 : 28) : [4,6,9,11].includes(month) ? 30 : 31
  return {from:`${x}-01`,to:`${x}-${String(days).padStart(2,'0')}`}
 }
 if(axis.kind==='date' && /^\d{4}-\d{2}-\d{2}$/.test(x)) {
  const date=new Date(`${x}T00:00:00Z`)
  if(!Number.isNaN(date.getTime()) && date.toISOString().slice(0,10)===x)return {from:x,to:x}
 }
 if(axis.kind==='category' && (axis.field==='account' || axis.field==='row.account') && /^(assets|liabilities|income|expenses|equity):[a-z0-9_:-]{1,150}$/.test(x))return {account:x}
 return undefined
}
export function chartRows(points:ChartValue['points'],series:ChartValue['series']):ChartRow[] {
 const rows:ChartRow[]=[],byX=new Map<string,ChartRow[]>(),indices=new Map(series.map((item,index) => [item.name,index]))
 for(const point of points){const index=indices.get(point.series) ?? 0,key=`s${index}`;let bucket=byX.get(point.x);if(!bucket){bucket=[];byX.set(point.x,bucket)}let row=bucket.findLast(item => item[key]===undefined);if(!row){row={x:point.x};bucket.push(row);rows.push(row)}row[key]=point.y;if(point.y_text!==undefined)row[`t${index}`]=point.y_text}
 return rows
}
export function previousValue(rows:ChartRow[],index:number,key:string):number|undefined {for(let i=index-1;i>=0;i--){const value=rows[i][key];if(typeof value==='number')return value}return undefined}
export function chartNumber(value:number,format:string):string {
 const digits=format==='usd0' || format==='eur0' || format==='int' || format==='pct0' ? 0 : format==='pct' || format==='mo' ? 1 : format==='usd' || format==='eur' ? 2 : Number.isInteger(value) ? 0 : 2
 if(!Number.isFinite(value))return String(value)
 // Match Go's binary float rounding for derived totals and deltas.
 const view=new DataView(new ArrayBuffer(8));view.setFloat64(0,Math.abs(value))
 const high=view.getUint32(0),exponent=(high>>>20)&0x7ff,fraction=(BigInt(high&0xfffff)<<32n)|BigInt(view.getUint32(4))
 const mantissa=exponent===0 ? fraction : (1n<<52n)|fraction,shift=exponent===0 ? -1074 : exponent-1023-52
 let scaled=mantissa*10n**BigInt(digits)
 if(shift>=0)scaled<<=BigInt(shift)
 else {const divisor=1n<<BigInt(-shift),quotient=scaled/divisor,remainder=scaled%divisor;scaled=quotient+(remainder*2n>divisor || remainder*2n===divisor && quotient%2n===1n ? 1n : 0n)}
 const raw=scaled.toString().padStart(digits+1,'0'),whole=digits ? raw.slice(0,-digits) : raw,decimal=digits ? `.${raw.slice(-digits)}` : ''
 const number=`${value<0 ? '−' : Object.is(value,-0) ? '-' : ''}${whole.replace(/\B(?=(\d{3})+(?!\d))/g,',')}${decimal}`
 return `${format==='usd' || format==='usd0' ? '$' : format==='eur' || format==='eur0' ? '€' : ''}${number}${format==='pct' || format==='pct0' ? '%' : format==='mo' ? ' mo' : ''}`
}

// Keep the five largest series/categories. Additive marks fold the rest into Other; line/area drop overflow.
export function foldChart(value:ChartValue):ChartValue {
 const donut=value.mark==='donut', additive=donut || value.mark==='bar' || value.mark==='stack'
 const names=donut ? [...new Set(value.points.map(point => point.x))] : value.series.map(item => item.name)
 if(names.length<=5)return value
 const weight=new Map<string,number>()
 for(const point of value.points){const name=donut ? point.x : point.series;if(name==='Other')continue;weight.set(name,(weight.get(name) ?? 0)+Math.abs(point.y))}
 const ranked=[...names].filter(name => name!=='Other').sort((a,b) => (weight.get(b) ?? 0)-(weight.get(a) ?? 0) || names.indexOf(a)-names.indexOf(b))
 const kept=new Set(ranked.slice(0,5))
 if(!additive) {
  const points=value.points.filter(point => kept.has(donut ? point.x : point.series))
  const keptSeries=ranked.slice(0,5).map(name => value.series.find(item => item.name===name)).filter((item):item is ChartValue['series'][number] => !!item)
  return {...value,points,series:keptSeries}
 }
 const points:ChartValue['points']=[],groups=new Map<string,ChartValue['points']>(),counts=new Map<string,Map<string,number>>()
 for(const point of value.points) {
  const name=donut ? point.x : point.series
  if(kept.has(name)){points.push({...point});continue}
  const x=donut ? 'Other' : point.x
  let index=0
  if(!donut){let byX=counts.get(name);if(!byX){byX=new Map();counts.set(name,byX)}index=byX.get(x) ?? 0;byX.set(x,index+1)}
  let group=groups.get(x);if(!group){group=[];groups.set(x,group)}
  if(!group[index]){group[index]={...point,x,series:donut ? point.series : 'Other',y:0,y_text:undefined};points.push(group[index])}
  group[index].y+=point.y
  // shortcut: unrepresentable sums keep the original chart; use decimal chart values if extreme values matter.
  if(!Number.isFinite(group[index].y))return value
 }
 const keptSeries=ranked.slice(0,5).map(name => value.series.find(item => item.name===name)).filter((item):item is ChartValue['series'][number] => !!item)
 return {...value,points,series:donut ? value.series : [...keptSeries,{name:'Other',color_index:5}]}
}
