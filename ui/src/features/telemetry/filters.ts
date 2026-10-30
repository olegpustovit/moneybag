import type { Dimension, Op, Pill } from './cube'
import { parseRange, type Bounds, type Range } from './time-grammar'

export type FieldDef={key:Pill['field'];label:string;ops:Op[];path:string;kind:'value'|'text'|'amount'|'flag'}
export const FIELDS:FieldDef[]=[
 {key:'category',label:'Category',ops:['is','is not'],path:'row.category',kind:'value'},
 {key:'payee',label:'Payee',ops:['contains','does not contain','is','is not'],path:'row.payee',kind:'text'},
 {key:'account',label:'Account',ops:['is','is not'],path:'row.account',kind:'value'},
 {key:'trip',label:'Trip',ops:['is','is not'],path:'row.trip',kind:'value'},
 {key:'amount',label:'Amount',ops:['over','under'],path:'row.amount',kind:'amount'},
 {key:'estimate',label:'Source',ops:['is'],path:'row.estimated',kind:'flag'},
]
const fieldOf=(fields:FieldDef[],key:Pill['field']) => fields.find(f => f.key===key)
export const opLabel=(pill:Pill) => pill.values.length>1 && pill.op==='is' ? 'is either of' : pill.values.length>1 && pill.op==='is not' ? 'is none of' : pill.op
export const nextOp=(pill:Pill,fields=FIELDS):Op => {const ops=fieldOf(fields,pill.field)?.ops ?? [pill.op];return ops[(ops.indexOf(pill.op)+1)%ops.length]}
export function addPill(pills:Pill[],pill:Pill):Pill[] {
 if(pill.field==='amount' || pill.field==='estimate')return [...pills.filter(p => !(p.field===pill.field && p.op===pill.op)),pill]
 const i=pills.findIndex(p => p.field===pill.field && p.op===pill.op)
 if(i<0)return [...pills,pill]
 return pills.map((p,k) => k===i ? {...p,values:[...new Set([...p.values,...pill.values])]} : p)
}

// Pills compile to the same CEL that agents and Advanced mode use.
const L=(x:string) => JSON.stringify(x)
export function pillsToCel(pills:Pill[],fields=FIELDS):string {
 return pills.map(k => {
  const path=fieldOf(fields,k.field)?.path ?? `row.${k.field}`
  if(k.field==='estimate')return `${path} == ${k.values[0]==='Estimated'}`
  if(k.op==='is')return k.values.length>1 ? `${path} in [${k.values.map(L).join(', ')}]` : `${path} == ${L(k.values[0])}`
  if(k.op==='is not')return k.values.length>1 ? `!(${path} in [${k.values.map(L).join(', ')}])` : `${path} != ${L(k.values[0])}`
  if(k.op==='contains')return k.values.length>1 ? `(${k.values.map(x => `${path}.contains(${L(x)})`).join(' || ')})` : `${path}.contains(${L(k.values[0])})`
  if(k.op==='does not contain')return k.values.map(x => `!${path}.contains(${L(x)})`).join(' && ')
  return `${path} ${k.op==='over' ? '>' : '<'} ${k.values[0]}`
 }).join(' && ')
}
// The inverse covers exactly the CEL that pills can show. Anything richer is refused with a reason, never half applied.
export function celToPills(cel:string,fields=FIELDS):{ok:true;pills:Pill[]}|{ok:false;message:string} {
 const text=cel.trim();if(!text)return {ok:true,pills:[]}
 const byPath=new Map(fields.map(f => [f.path,f])),parts:string[]=[];let depth=0,quote=false,cur=''
 for(let i=0;i<text.length;i++){
  const ch=text[i]
  if(ch==='"' && text[i-1]!=='\\')quote=!quote
  if(!quote){if('([{'.includes(ch))depth++;if(')]}'.includes(ch))depth--;if(ch==='&' && text[i+1]==='&' && !depth){parts.push(cur.trim());cur='';i++;continue}}
  cur+=ch
 }
 parts.push(cur.trim())
 const out:Pill[]=[],bad=(part:string) => ({ok:false as const,message:`Only filters the pills can show work here. This part needs an agent: ${part}`})
 const list=(s:string) => {try {const v:unknown=JSON.parse(s);return Array.isArray(v) && v.every(x => typeof x==='string') ? v as string[] : null} catch {return null}}
 const str=(s:string) => {try {const v:unknown=JSON.parse(s);return typeof v==='string' ? v : null} catch {return null}}
 for(const part of parts) {
  let m:RegExpMatchArray|null,pill:Pill|null=null
  if((m=part.match(/^!\(\s*([\w.]+) in (\[.*\])\s*\)$/s))){const f=byPath.get(m[1]),v=list(m[2]);if(f && v)pill={field:f.key,op:'is not',values:v}}
  else if((m=part.match(/^([\w.]+) in (\[.*\])$/s))){const f=byPath.get(m[1]),v=list(m[2]);if(f && v)pill={field:f.key,op:'is',values:v}}
  else if((m=part.match(/^([\w.]+) (==|!=) (true|false)$/))){const f=byPath.get(m[1]);if(f?.kind==='flag')pill={field:f.key,op:'is',values:[(m[3]==='true')===(m[2]==='==') ? 'Estimated' : 'Recorded']}}
  else if((m=part.match(/^([\w.]+) (==|!=) (".*")$/s))){const f=byPath.get(m[1]),v=str(m[3]);if(f && v!==null)pill={field:f.key,op:m[2]==='==' ? 'is' : 'is not',values:[v]}}
  else if((m=part.match(/^(!?)([\w.]+)\.contains\((".*")\)$/s))){const f=byPath.get(m[2]),v=str(m[3]);if(f && v!==null)pill={field:f.key,op:m[1] ? 'does not contain' : 'contains',values:[v]}}
  else if((m=part.match(/^\((.*)\)$/s)) && m[1].includes('||')){const alts=m[1].split(' || ').map(x => x.trim().match(/^([\w.]+)\.contains\((".*")\)$/s));if(alts.every(Boolean) && new Set(alts.map(x => x![1])).size===1){const f=byPath.get(alts[0]![1]),v=alts.map(x => str(x![2]));if(f && v.every(x => x!==null))pill={field:f.key,op:'contains',values:v as string[]}}}
  else if((m=part.match(/^([\w.]+) (>|<) (\d+(?:\.\d+)?)$/))){const f=byPath.get(m[1]);if(f?.kind==='amount')pill={field:f.key,op:m[2]==='>' ? 'over' : 'under',values:[m[3]]}}
  if(!pill)return bad(part)
  out.push(pill)
 }
 return {ok:true,pills:out.reduce<Pill[]>((all,p) => p.op==='does not contain' ? addPill(all,p) : [...all,p],[])}
}

// The URL form: `category:is:Rent|Travel;amount:over:50`. Every part is encoded, so values may hold any character.
export function encodePills(pills:Pill[]):string {return pills.map(p => [p.field,p.op,p.values.map(encodeURIComponent).join('|')].join(':')).join(';')}
const OPS:Op[]=['is','is not','contains','does not contain','over','under']
export function decodePills(text:string|undefined,fields=FIELDS):Pill[] {
 if(!text)return []
 const out:Pill[]=[]
 for(const part of text.split(';')){
  const [field,op,values]=part.split(':')
  const def=fields.find(f => f.key===field)
  if(!def || !OPS.includes(op as Op) || !def.ops.includes(op as Op) || !values)continue
  try {out.push({field:def.key,op:op as Op,values:values.split('|').map(decodeURIComponent)})} catch {/* A hand-edited URL drops the broken pill and keeps the rest. */}
 }
 return out
}

// "Ask in plain words": a small rule parser that produces the same editable pills an agent would.
export type AskResult={pills:Pill[];rangeText?:string;range?:Range;understood:boolean}
const TIME=/(last \d+ \w+|past \d+ \w+|last (?:month|quarter|year|week)|this (?:month|quarter|year|week)|since [\w ]+?(?= with| without| no |,|$)|before [\w ]+?(?= with| without| no |,|$)|(?:from )?[a-z]{3,9} to [a-z]{3,9}|\d{4}-q[1-4]|q[1-4] \d{4}|\bin \d{4}\b|\bytd\b)/
export function parseAsk(raw:string,dictionary:Partial<Record<Dimension,string[]>>,bounds:Bounds):AskResult {
 const s=raw.toLowerCase().replace(/[.?!]+$/,'');let m:RegExpMatchArray|null,rest=s
 const pills:Pill[]=[],time=s.match(TIME);let rangeText:string|undefined,range:Range|undefined
 if(time){const text=time[1].replace(/^in /,'');const got=parseRange(text,bounds);if(got){rangeText=text;range=got;rest=rest.replace(time[1],' ')}}
 if((m=rest.match(/(?:over|above|more than|at least) [€$]?(\d+(?:\.\d+)?)/)))pills.push({field:'amount',op:'over',values:[m[1]]})
 if((m=rest.match(/(?:under|below|less than) [€$]?(\d+(?:\.\d+)?)/)))pills.push({field:'amount',op:'under',values:[m[1]]})
 if(/(no|without|exclude|excluding) estimates?/.test(rest))pills.push({field:'estimate',op:'is',values:['Recorded']})
 if(/(only )?estimates? only|only estimates?/.test(rest))pills.push({field:'estimate',op:'is',values:['Estimated']})
 for(const field of ['category','trip','account'] as const) {
  const hits:string[]=[],negative:string[]=[]
  for(const value of dictionary[field] ?? []){
   const low=value.toLowerCase();if(field==='category' && low==='estimated')continue
   const at=rest.search(new RegExp(`\\b${low.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')}\\b`));if(at<0)continue
   if(pills.some(p => p.values.includes(value)))continue
   ;(/(except|not|without|excluding|other than)\s+(?:\w+\s+)?$/.test(rest.slice(0,at)) ? negative : hits).push(value)
  }
  if(hits.length)pills.push({field,op:'is',values:hits})
  if(negative.length)pills.push({field,op:'is not',values:negative})
 }
 if((m=rest.match(/\b(?:at|from|to|payee) ([a-z][\w'& ]{1,30})$/)) && !pills.some(p => p.field==='category')){const hit=(dictionary.payee ?? []).find(p => p.toLowerCase().includes(m![1].trim()));if(hit)pills.push({field:'payee',op:'contains',values:[m[1].trim()]})}
 return {pills,rangeText,range,understood:pills.length>0 || !!range}
}
