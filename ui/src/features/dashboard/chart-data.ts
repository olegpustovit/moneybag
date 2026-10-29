import type { ChartValue } from './values'
export type ChartRow=Record<string,string|number>
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
