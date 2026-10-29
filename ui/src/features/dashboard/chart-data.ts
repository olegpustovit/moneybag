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
 for(const point of points){const key=`s${indices.get(point.series) ?? 0}`;let bucket=byX.get(point.x);if(!bucket){bucket=[];byX.set(point.x,bucket)}let row=bucket.findLast(item => item[key]===undefined);if(!row){row={x:point.x};bucket.push(row);rows.push(row)}row[key]=point.y}
 return rows
}
export function previousValue(rows:ChartRow[],index:number,key:string):number|undefined {for(let i=index-1;i>=0;i--){const value=rows[i][key];if(typeof value==='number')return value}return undefined}
export function chartNumber(value:number,format:string):string {
 const digits=format==='usd0' || format==='eur0' || format==='int' || format==='pct0' ? 0 : format==='pct' || format==='mo' ? 1 : format==='usd' || format==='eur' ? 2 : Number.isInteger(value) ? 0 : 2
 const number=new Intl.NumberFormat('en-US',{minimumFractionDigits:digits,maximumFractionDigits:digits}).format(value).replace('-', '−')
 return `${format.startsWith('usd') || format==='USD' ? '$' : format.startsWith('eur') || format==='EUR' ? '€' : ''}${number}${format.startsWith('pct') ? '%' : format==='mo' ? ' mo' : ''}`
}
