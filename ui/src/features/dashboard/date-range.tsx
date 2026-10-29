import { lazy, Suspense, useState } from 'react'
import { Button } from '../../components/ui/button'

type Values={from?:string;to?:string;month?:string;all?:boolean}
const RangeDialog=lazy(() => import('./range-dialog'))
const iso=(date:Date) => `${date.getUTCFullYear().toString().padStart(4,'0')}-${String(date.getUTCMonth()+1).padStart(2,'0')}-${String(date.getUTCDate()).padStart(2,'0')}`
const parse=(text:string) => {const [y,m,d]=text.split('-').map(Number);const date=new Date(0);date.setUTCFullYear(y,m-1,d);date.setUTCHours(0,0,0,0);return date}
const day=(y:number,m:number,d:number) => {const date=new Date(0);date.setUTCFullYear(y,m,d);date.setUTCHours(0,0,0,0);return date}
export function presetRange(preset:string,today=new Date()):Values {
 const y=today.getFullYear(),m=today.getMonth(),now=day(y,m,today.getDate())
 if(preset==='all')return {from:undefined,to:undefined,all:true}
 if(preset==='this')return {from:iso(day(y,m,1)),to:iso(day(y,m+1,0))}
 if(preset==='ytd')return {from:iso(day(y,0,1)),to:iso(now)}
 if(preset==='year')return {from:iso(day(y-1,0,1)),to:iso(day(y-1,12,0))}
 const n=Number(preset);return {from:iso(day(y,m-n,1)),to:iso(day(y,m,0))}
}
export function stepRange(from:string,to:string,direction:number):Values {
 const start=parse(from),end=parse(to),first=start.getUTCDate()===1,last=end.getUTCDate()===day(end.getUTCFullYear(),end.getUTCMonth()+1,0).getUTCDate()
 if(first && last){const n=(end.getUTCFullYear()-start.getUTCFullYear())*12+end.getUTCMonth()-start.getUTCMonth()+1;return {from:iso(day(start.getUTCFullYear(),start.getUTCMonth()+direction*n,1)),to:iso(day(end.getUTCFullYear(),end.getUTCMonth()+direction*n+1,0))}}
 const days=Math.round((end.getTime()-start.getTime())/86400000)+1;return {from:iso(day(start.getUTCFullYear(),start.getUTCMonth(),start.getUTCDate()+direction*days)),to:iso(day(end.getUTCFullYear(),end.getUTCMonth(),end.getUTCDate()+direction*days))}
}
function human(from?:string,to?:string,all?:boolean) {if(all)return 'All time';if(!from && !to)return 'Dashboard default';const format=(value:string) => new Intl.DateTimeFormat('en-GB',{day:'numeric',month:'short',year:'numeric',timeZone:'UTC'}).format(parse(value));if(!from)return `Until ${format(to!)}`;if(!to)return `From ${format(from)}`;const start=parse(from),end=parse(to),months=(end.getUTCFullYear()-start.getUTCFullYear())*12+end.getUTCMonth()-start.getUTCMonth()+1;const whole=start.getUTCDate()===1 && end.getUTCDate()===day(end.getUTCFullYear(),end.getUTCMonth()+1,0).getUTCDate();return `${format(from)} – ${format(to)}${whole ? ` · ${months} ${months===1 ? 'month' : 'months'}` : ''}`}
export function DateRange({range,month,values,defaults,onChange}:{range:boolean;month:boolean;values:Values;defaults:Values;onChange:(value:Values) => void}) {
 const [open,setOpen]=useState(false)
 const from=values.all ? undefined : values.from ?? defaults.from,to=values.all ? undefined : values.to ?? defaults.to,currentMonth=values.month ?? defaults.month ?? iso(day(new Date().getFullYear(),new Date().getMonth(),1)).slice(0,7)
 function shiftMonth(n:number){const [year,part]=currentMonth.split('-').map(Number);onChange({...values,month:iso(day(year,part-1+n,1)).slice(0,7)})}
 return <div className="range-bar">{range && <><Button type="button" variant="outline" size="sm" aria-label="Previous date range" disabled={!from || !to || from>to} onClick={() => from && to && onChange({...values,...stepRange(from,to,-1),all:false})}>←</Button><Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)}>{human(from,to,values.all)}</Button><Button type="button" variant="outline" size="sm" aria-label="Next date range" disabled={!from || !to || from>to} onClick={() => from && to && onChange({...values,...stepRange(from,to,1),all:false})}>→</Button>{open && <Suspense fallback={<span>Loading calendar…</span>}><RangeDialog from={from} to={to} onClose={() => setOpen(false)} onApply={value => {onChange({...values,...value});setOpen(false)}}/></Suspense>}</>}{month && <div className="month-step"><Button type="button" variant="outline" size="sm" aria-label="Previous dashboard month" onClick={() => shiftMonth(-1)}>←</Button><label>Month <input type="month" aria-label="Dashboard month" value={currentMonth} onChange={event => {if(event.target.value)onChange({...values,month:event.target.value})}}/></label><Button type="button" variant="outline" size="sm" aria-label="Next dashboard month" onClick={() => shiftMonth(1)}>→</Button></div>}</div>
}
