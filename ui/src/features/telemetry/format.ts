import { chartNumber } from '../dashboard/chart-data'

// The single place amounts become text. Money arrives as integer cents; nothing downstream divides by 100.
export type Unit='eur'|'usd'|'int'|'pct'|'mo'|'days'|'rate'|'number'
export type ValueFormat={full:(value:number) => string;axis:(value:number,step:number) => string;zero:boolean}
const SYMBOL:Record<string,string>={eur:'€',EUR:'€',usd:'$',USD:'$'}
const group=(n:number) => Math.trunc(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g,',')
const sign=(n:number) => n<0 ? '−' : ''
export const currencySymbol=(code:string) => SYMBOL[code] ?? `${code} `
export function money(cents:number,code='EUR'):string {return `${sign(Math.round(cents/100))}${currencySymbol(code)}${group(Math.abs(Math.round(cents/100)))}`}
export function moneyExact(cents:number,code='EUR'):string {const a=Math.abs(Math.round(cents));return `${sign(cents)}${currencySymbol(code)}${group(Math.floor(a/100))}.${String(a%100).padStart(2,'0')}`}
export function moneySigned(cents:number,code='EUR'):string {return `${cents>0 ? '+' : ''}${money(cents,code)}`}
export function moneyCompact(cents:number,code='EUR'):string {
 const a=Math.abs(cents)/100,s=`${sign(cents)}${currencySymbol(code)}`
 if(a>=1e6)return `${s}${(a/1e6).toFixed(a>=1e7 ? 0 : 1)}M`
 if(a>=1e3)return `${s}${(a/1e3).toFixed(a>=1e4 ? 0 : 1)}k`
 return `${s}${Math.round(a)}`
}
export function compactNumber(n:number):string {
 const a=Math.abs(n),s=sign(n);if(a>=1e6)return `${s}${(a/1e6).toFixed(a>=1e7 ? 0 : 1)}M`;if(a>=1e4)return `${s}${(a/1e3).toFixed(0)}k`
 return `${s}${group(a)}${a%1 ? `.${(a%1).toFixed(2).slice(2).replace(/0+$/,'')}` : ''}`
}
export const percent=(n:number,digits=1) => `${sign(n)}${Math.abs(n).toFixed(digits)}%`
const stepDigits=(step:number) => {for(let d=0;d<6;d++){const x=step*10**d;if(Math.abs(Math.round(x)-x)<1e-6)return d}return 6}
// `minor` means the values are integer cents, which is how the cube delivers money.
export function unitFormat(unit:Unit,{minor=false,code}:{minor?:boolean;code?:string}={}):ValueFormat {
 switch(unit) {
  case 'eur': case 'usd': {const c=code ?? unit.toUpperCase(),k=minor ? 1 : 100;return {zero:true,full:v => moneyExact(v*k,c),axis:v => moneyCompact(v*k,c)}}
  case 'pct': return {zero:true,full:v => percent(v),axis:(v,step) => percent(v,stepDigits(step))}
  case 'mo': return {zero:true,full:v => `${v.toFixed(1)} mo`,axis:(v,step) => `${v.toFixed(stepDigits(step))} mo`}
  case 'days': return {zero:true,full:v => `${group(v)} ${Math.abs(v)===1 ? 'day' : 'days'}`,axis:v => `${group(v)} d`}
  case 'int': return {zero:true,full:v => `${sign(v)}${group(Math.round(Math.abs(v)))}`,axis:v => `${sign(v)}${compactNumber(Math.abs(v))}`}
  case 'rate': return {zero:false,full:v => v.toLocaleString('en-US',{maximumFractionDigits:Math.abs(v)>=100 ? 2 : 4,minimumFractionDigits:Math.abs(v)>=100 ? 2 : 4}),axis:(v,step) => v.toLocaleString('en-US',{minimumFractionDigits:stepDigits(step),maximumFractionDigits:stepDigits(step)})}
  default: return {zero:false,full:v => `${sign(v)}${compactNumber(Math.abs(v))}`,axis:(v,step) => step<1 ? v.toFixed(stepDigits(step)) : compactNumber(v)}
 }
}
// Legacy `y_format` strings keep their server-matching tooltip text and gain a unit-aware axis.
export function legacyFormat(format:string):ValueFormat {
 const base=format.startsWith('usd') ? 'usd' : format.startsWith('eur') ? 'eur' : format.startsWith('pct') ? 'pct' : format==='mo' ? 'mo' : format==='int' ? 'int' : format==='days' ? 'days' : format==='rate' ? 'rate' : 'number'
 return {...unitFormat(base),full:value => chartNumber(value,format)}
}
const SHORT_MONTH=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec']
export const humanDate=(iso:string) => {const m=iso.match(/^(\d{4})-(\d{2})(?:-(\d{2}))?/);if(!m || +m[2]<1 || +m[2]>12)return iso;return `${m[3] ? `${+m[3]} ` : ''}${SHORT_MONTH[+m[2]-1]} ${m[1]}`}
