// Every timestamp is a UTC midnight in ms and every range is inclusive of both days.
export const DAY=864e5
export type Range={from:number;to:number}
export type Bounds={today:number;start:number;end?:number}
export type CompareMode='previous_period'|'previous_year'|'none'
export type Bucket='day'|'week'|'month'|'quarter'
export type ParseResult={ok:true;range:Range;clipped:boolean}|{ok:false;reason:'empty'|'unreadable'|'outside'|'order';message:string}
export type CompareWindow=Range&{mode:Exclude<CompareMode,'none'>;label:string;partial:boolean;available:boolean}

const MONTHS=['january','february','march','april','may','june','july','august','september','october','november','december']
export const MONTH_NAMES=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec']
const utc=Date.UTC
// Before the cube arrives the page still needs a clock; the real start of history replaces this.
export const defaultBounds=():Bounds => {const today=Math.floor(Date.now()/DAY)*DAY;return {today,start:today-40*365*DAY}}
const eom=(y:number,m:number) => utc(y,m+1,0)
const fields=(t:number) => {const d=new Date(t);return {y:d.getUTCFullYear(),m:d.getUTCMonth(),d:d.getUTCDate(),w:(d.getUTCDay()+6)%7}}
const monthIndex=(s:string) => s.length<3 ? -1 : MONTHS.findIndex(name => name.startsWith(s))
const validDay=(y:number,m:number,d:number) => m>=0 && m<12 && d>=1 && d<=new Date(utc(y,m+1,0)).getUTCDate()
const hi=(b:Bounds) => b.end===undefined ? b.today : Math.min(b.end,b.today)

export const isoDay=(t:number) => new Date(t).toISOString().slice(0,10)
export const dayFromIso=(s:string) => utc(+s.slice(0,4),+s.slice(5,7)-1,+s.slice(8,10))
export const dayLabel=(t:number) => {const f=fields(t);return `${f.d} ${MONTH_NAMES[f.m]} ${f.y}`}
export const rangeLabel=(r:Range) => r.from===r.to ? dayLabel(r.from) : `${dayLabel(r.from)} to ${dayLabel(r.to)}`
export const rangeDays=(r:Range) => Math.round((r.to-r.from)/DAY)+1
export const canonicalText=(r:Range) => `${isoDay(r.from)} to ${isoDay(r.to)}`

// Month arithmetic clamps to the target month's length so 31 Mar minus a month is 28 Feb, never 3 Mar.
export function addMonths(t:number,n:number):number {const f=fields(t),last=new Date(utc(f.y,f.m+n+1,0)).getUTCDate();return utc(f.y,f.m+n,Math.min(f.d,last))}

// A date whose year was not typed resolves against a year chosen later, which is what `aug to oct` needs.
type Point={at:(y:number) => number|null;begin?:(y:number) => number;year?:number;fixed?:number}
function latest(p:Point,before:number):number|null {
 if(p.fixed!==undefined)return p.fixed
 if(p.year!==undefined)return p.at(p.year)
 // Feb 29 can sit up to eight years back across a skipped century leap year.
 for(let y=fields(before).y,i=0;i<9;i++,y--){const t=p.at(y),begin=p.begin ? p.begin(y) : t;if(t!==null && begin!==null && begin<=before)return t}
 return null
}
function point(raw:string,end:boolean,today:number):Point|null {
 const s=raw.trim();let m:RegExpMatchArray|null
 const fixed=(t:number|null):Point|null => t===null ? null : {at:() => t,fixed:t}
 if(s==='today' || s==='now')return fixed(today)
 if(s==='yesterday')return fixed(today-DAY)
 if((m=s.match(/^(\d{4})-(\d{1,2})-(\d{1,2})$/)))return fixed(validDay(+m[1],+m[2]-1,+m[3]) ? utc(+m[1],+m[2]-1,+m[3]) : null)
 if((m=s.match(/^(\d{4})-(\d{1,2})$/))){const y=+m[1],i=+m[2]-1;return i<0 || i>11 ? null : fixed(end ? eom(y,i) : utc(y,i,1))}
 if((m=s.match(/^(\d{4})[- ]?q([1-4])$/))){const y=+m[1],i=(+m[2]-1)*3;return fixed(end ? eom(y,i+2) : utc(y,i,1))}
 if((m=s.match(/^q([1-4])[- ](\d{4})$/))){const y=+m[2],i=(+m[1]-1)*3;return fixed(end ? eom(y,i+2) : utc(y,i,1))}
 if((m=s.match(/^(\d{4})$/)))return fixed(end ? utc(+m[1],11,31) : utc(+m[1],0,1))
 const dayMonth=s.match(/^(\d{1,2})\s+([a-z]+)(?:,?\s+(\d{4}))?$/),monthDay=s.match(/^([a-z]+)\s+(\d{1,2})(?:,?\s+(\d{4}))?$/)
 if(dayMonth || monthDay){const day=+(dayMonth ? dayMonth[1] : monthDay![2]),i=monthIndex(dayMonth ? dayMonth[2] : monthDay![1]),y=dayMonth ? dayMonth[3] : monthDay![3];if(i<0)return null
  // shortcut: `feb 2026` parses as a month below, so a year must follow a day to be a day.
  return {at:yr => validDay(yr,i,day) ? utc(yr,i,day) : null,year:y ? +y : undefined}}
 if((m=s.match(/^([a-z]+)(?:,?\s+(\d{4}))?$/))){const i=monthIndex(m[1]);if(i<0)return null;return {at:yr => end ? eom(yr,i) : utc(yr,i,1),begin:yr => utc(yr,i,1),year:m[2] ? +m[2] : undefined}}
 return null
}
function between(a:Point,b:Point,today:number):[number|null,number|null] {
 if(a.fixed!==undefined || a.year!==undefined){const from=latest(a,today);if(from===null)return [null,null];if(b.fixed!==undefined || b.year!==undefined)return [from,latest(b,today)];const y=fields(from).y,to=b.at(y);return [from,to!==null && to>=from ? to : b.at(y+1)]}
 if(b.fixed!==undefined || b.year!==undefined){const to=latest(b,today);return to===null ? [null,null] : [latest(a,to),to]}
 const to=latest(b,today);return to===null ? [null,null] : [latest(a,to),to]
}

const HINT='Try 90d, 2026-Q2 or since march.'
function resolve(raw:string,today:number):[number|null,number|null]|null {
 const s=raw.trim().toLowerCase().replace(/\s+/g,' ');let m:RegExpMatchArray|null
 const t=fields(today),q=Math.floor(t.m/3)*3,monday=today-t.w*DAY
 const named:Record<string,[number,number]>={
  'this month':[utc(t.y,t.m,1),today],mtd:[utc(t.y,t.m,1),today],'last month':[utc(t.y,t.m-1,1),eom(t.y,t.m-1)],
  'this year':[utc(t.y,0,1),today],ytd:[utc(t.y,0,1),today],'last year':[utc(t.y-1,0,1),utc(t.y-1,11,31)],
  'this quarter':[utc(t.y,q,1),today],qtd:[utc(t.y,q,1),today],'last quarter':[utc(t.y,q-3,1),eom(t.y,q-1)],
  'this week':[monday,today],'last week':[monday-7*DAY,monday-DAY],today:[today,today],yesterday:[today-DAY,today-DAY],
 }
 if(named[s])return named[s]
 if((m=s.match(/^(?:last |past |previous )?(\d+) ?(d|days?|w|wks?|weeks?|m|mos?|mths?|months?|q|qtrs?|quarters?|y|yrs?|years?)$/))){
  const n=+m[1];if(!n)return null
  const u=m[2][0];if(u==='d')return [today-(n-1)*DAY,today];if(u==='w')return [today-(n*7-1)*DAY,today]
  return [addMonths(today,-(u==='m' ? n : u==='q' ? n*3 : n*12))+DAY,today]
 }
 if((m=s.match(/^q([1-4])$/))){const i=(+m[1]-1)*3,y=i>t.m ? t.y-1 : t.y;return [utc(y,i,1),eom(y,i+2)]}
 if((m=s.match(/^(?:from |between )?(.+?) (?:to|until|till|through|thru|and|-|–|—) (.+)$/))){const a=point(m[1],false,today),b=point(m[2],true,today);return a && b ? between(a,b,today) : null}
 if((m=s.match(/^(?:since|from|after) (.+)$/))){const after=s.startsWith('after'),p=point(m[1],after,today),a=p && latest(p,today);return a===null || a===undefined ? null : [after ? a+DAY : a,today]}
 if((m=s.match(/^before (.+)$/))){const p=point(m[1],false,today),a=p && latest(p,today);return a===null || a===undefined ? null : [Number.NEGATIVE_INFINITY,a-DAY]}
 if((m=s.match(/^(?:until|till|through|thru|up to) (.+)$/))){const p=point(m[1],true,today),a=p && latest(p,today);return a===null || a===undefined ? null : [Number.NEGATIVE_INFINITY,a]}
 const a=point(s,false,today),b=point(s,true,today);if(!a || !b)return null
 const from=latest(a,today);if(from===null)return null
 return [from,b.fixed!==undefined ? b.fixed : b.at(fields(from).y)]
}
export function parseTime(raw:string,bounds:Bounds):ParseResult {
 const s=raw.trim().toLowerCase();if(!s)return {ok:false,reason:'empty',message:'Try: 90d, 2026-Q2, since march, aug to oct, last year'}
 const top=hi(bounds)
 if(s==='all' || s==='all time' || s==='everything' || s==='max')return {ok:true,range:{from:bounds.start,to:top},clipped:false}
 const got=resolve(s,bounds.today)
 if(!got || got[0]===null || got[1]===null)return {ok:false,reason:'unreadable',message:`Can’t read that yet. ${HINT}`}
 if(got[0]>got[1])return {ok:false,reason:'order',message:'That range ends before it starts.'}
 const from=Math.max(bounds.start,got[0]),to=Math.min(top,got[1])
 if(from>to)return {ok:false,reason:'outside',message:got[1]<bounds.start ? `History starts on ${dayLabel(bounds.start)}.` : `History ends on ${dayLabel(top)}.`}
 return {ok:true,range:{from,to},clipped:from!==got[0] || to!==got[1]}
}
export const parseRange=(raw:string,bounds:Bounds):Range|null => {const r=parseTime(raw,bounds);return r.ok ? r.range : null}

const wholeMonths=(r:Range):number => {const a=fields(r.from),b=fields(r.to);return a.d===1 && r.to===eom(b.y,b.m) ? (b.y-a.y)*12+b.m-a.m+1 : 0}
const clampLength=(from:number,to:number,bounds:Bounds):Range => {
 const top=hi(bounds),len=to-from
 if(to>top){const a=Math.max(bounds.start,top-len);return {from:a,to:top}}
 if(from<bounds.start)return {from:bounds.start,to:Math.min(top,bounds.start+len)}
 return {from,to}
}
// Whole months step as months, so March steps back to February with the right length.
export function stepRange(r:Range,direction:number,bounds:Bounds):Range {
 const n=wholeMonths(r)
 if(n){const a=addMonths(r.from,direction*n),f=fields(a),b=eom(f.y,f.m+n-1),top=hi(bounds);if(a>top || b<bounds.start)return r;return {from:Math.max(bounds.start,a),to:Math.min(top,b)}}
 const len=r.to-r.from+DAY;return clampLength(r.from+direction*len,r.to+direction*len,bounds)
}
export function zoomOutRange(r:Range,bounds:Bounds):Range {
 const len=r.to-r.from+DAY,top=hi(bounds);let from=r.from-Math.floor(len/DAY/2)*DAY,to=r.to+Math.ceil(len/DAY/2)*DAY
 if(from<bounds.start){to+=bounds.start-from;from=bounds.start}
 if(to>top){from-=to-top;to=top}
 return {from:Math.max(bounds.start,from),to:Math.min(top,to)}
}
export const MIN_ZOOM_DAYS=3
// `factor` above 1 widens, below 1 narrows. The day under `center` stays where it is.
export function zoomAround(r:Range,center:number,factor:number,bounds:Bounds):Range {
 const snap=(t:number) => Math.round(t/DAY)*DAY,c=Math.max(r.from,Math.min(r.to,center));let from=snap(c-(c-r.from)*factor),to=snap(c+(r.to-c)*factor)
 if((to-from)/DAY+1<MIN_ZOOM_DAYS){if(r.to-r.from<=(MIN_ZOOM_DAYS-1)*DAY)return r;from=Math.max(bounds.start,c-DAY);to=from+(MIN_ZOOM_DAYS-1)*DAY}
 return {from:Math.max(bounds.start,from),to:Math.min(hi(bounds),to)}
}
// Same dates a year earlier. A month-end stays a month-end, so 28 Feb 2025 maps to 29 Feb 2024.
const yearEarlier=(t:number,edge:boolean) => {const f=fields(t);if(edge && t===eom(f.y,f.m))return eom(f.y-1,f.m);return addMonths(t,-12)}
export function compareWindow(r:Range,mode:CompareMode,bounds:Bounds):CompareWindow|null {
 if(mode==='none')return null
 let from:number,to:number
 if(mode==='previous_year'){from=yearEarlier(r.from,false);to=yearEarlier(r.to,true)}
 else {const n=wholeMonths(r);if(n){const f=fields(r.from);from=addMonths(r.from,-n);to=eom(f.y,f.m-1)}else {to=r.from-DAY;from=to-(r.to-r.from)}}
 const available=to>=bounds.start
 return {mode,from:available ? Math.max(bounds.start,from) : from,to,partial:available && from<bounds.start,available,label:mode==='previous_period' ? 'previous period' : 'same period last year'}
}

export function autoBucket(r:Range):Bucket {const d=rangeDays(r);return d<=45 ? 'day' : d<=120 ? 'week' : d<=1830 ? 'month' : 'quarter'}
export function bucketStart(t:number,b:Bucket):number {
 if(b==='day')return t
 const f=fields(t);if(b==='week')return t-f.w*DAY
 return b==='month' ? utc(f.y,f.m,1) : utc(f.y,Math.floor(f.m/3)*3,1)
}
export function bucketNext(t:number,b:Bucket):number {
 if(b==='day')return t+DAY
 if(b==='week')return t+7*DAY
 const f=fields(t);return utc(f.y,f.m+(b==='month' ? 1 : 3),1)
}
export function bucketLabel(t:number,b:Bucket):string {
 if(b==='day')return dayLabel(t)
 if(b==='week')return `Week of ${dayLabel(t)}`
 const f=fields(t);return b==='month' ? `${MONTH_NAMES[f.m]} ${f.y}` : `Q${Math.floor(f.m/3)+1} ${f.y}`
}
export function bucketEdges(r:Range,b:Bucket):number[] {const xs:number[]=[];for(let t=bucketStart(r.from,b);t<=r.to;t=bucketNext(t,b))xs.push(t);return xs}
