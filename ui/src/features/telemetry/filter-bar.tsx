import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import type { Dimension, Op, Pill, Suggestion } from './cube'
import { FIELDS, addPill, celToPills, nextOp, opLabel, parseAsk, pillsToCel, type FieldDef } from './filters'
import { ignoreShortcut } from './use-time-state'
import type { Bounds, Range } from './time-grammar'
import './telemetry.css'

export type FilterBarProps={pills:Pill[];onChange:(pills:Pill[]) => void;suggest:(field:Dimension,query:string) => Suggestion[];
 formatTotal:(cents:number) => string;countAmount?:(op:'over'|'under',amount:number) => number;fields?:FieldDef[];
 asks?:string[];ask?:{dictionary:Partial<Record<Dimension,string[]>>;bounds:Bounds;onRange:(text:string,range:Range) => void};
 summary?:ReactNode;onSaveView?:() => void;placeholder?:string}
type Item={head:string;label:string;hint?:string;run:() => void}
const entries=(n:number) => `${n.toLocaleString('en-US')} ${n===1 ? 'entry' : 'entries'}`
const AMOUNTS=[50,100,250,500,1000]

export function FilterBar({pills,onChange,suggest,formatTotal,countAmount,fields=FIELDS,asks,ask,summary,onSaveView,placeholder='Filter by category, payee, account, trip, amount…'}:FilterBarProps) {
 const [query,setQuery]=useState(''),[open,setOpen]=useState(false),[active,setActive]=useState(0),[field,setField]=useState<FieldDef|null>(null),[op,setOp]=useState<Op|null>(null),[advanced,setAdvanced]=useState(false),[cel,setCel]=useState<string|null>(null),[celError,setCelError]=useState('')
 const input=useRef<HTMLInputElement>(null),list=useRef<HTMLDivElement>(null),base=useId(),listId=`${base}-list`,optionId=(i:number) => `${base}-o${i}`
 const clearDraft=() => {setField(null);setOp(null);setQuery('')}
 const commit=(f:Pill['field'],o:Op,value:string) => {onChange(addPill(pills,{field:f,op:o,values:[value]}));clearDraft();setActive(0);setOpen(false)}
 const choose=(f:FieldDef) => {setField(f);setOp(f.ops.length===1 ? f.ops[0] : null);setQuery('');setActive(0);setOpen(true);input.current?.focus()}
 const runAsk=(text:string) => {
  if(!ask)return false
  const got=parseAsk(text,ask.dictionary,ask.bounds);if(!got.understood)return false
  onChange(got.pills);if(got.range && got.rangeText)ask.onRange(got.rangeText,got.range);clearDraft();setOpen(false);return true
 }
 const q=query.trim(),ql=q.toLowerCase(),items:Item[]=[]
 const valueItems=(f:FieldDef,o:Op,head='In this time range') => {
  if(f.key==='amount')return
  for(const s of suggest(f.key,q))items.push({head,label:s.label,hint:`${entries(s.count)} · ${formatTotal(s.total)}`,run:() => commit(f.key,o,s.value)})
 }
 if(!field){
  if(ql.length>=2){
   for(const key of ['category','account','trip'] as const){const f=fields.find(x => x.key===key);if(!f)continue;for(const s of suggest(key,q).slice(0,3))items.push({head:'Matching values',label:`${f.label} is ${s.label}`,hint:`${entries(s.count)} · ${formatTotal(s.total)}`,run:() => commit(key,'is',s.value)})}
   const payee=fields.find(x => x.key==='payee'),hits=payee ? suggest('payee',q) : []
   if(hits.length)items.push({head:'Matching values',label:`Payee contains “${q}”`,hint:entries(hits.reduce((n,s) => n+s.count,0)),run:() => commit('payee','contains',q)})
  }
  for(const f of fields)if(!ql || f.label.toLowerCase().startsWith(ql))items.push({head:'Filter by',label:f.label,hint:f.ops.join(' / '),run:() => choose(f)})
  if(ask && ql.length>=4 && parseAsk(q,ask.dictionary,ask.bounds).understood)items.push({head:'Ask in plain words',label:`“${q}”`,hint:'Make filters from this',run:() => {runAsk(q)}})
 } else if(!op){
  for(const o of field.ops)if(!ql || o.includes(ql))items.push({head:field.label,label:o,run:() => {setOp(o);setQuery('');setActive(0);setOpen(true)}})
 } else if(field.key==='amount'){
  const n=parseFloat(q),amount=op==='under' ? 'under' : 'over'
  for(const v of Number.isNaN(n) ? AMOUNTS : [n])items.push({head:`Amount ${op}`,label:formatTotal(v*100),hint:countAmount ? entries(countAmount(amount,v)) : undefined,run:() => commit('amount',op,String(v))})
 } else {
  if(q && field.kind==='text')items.push({head:'In this time range',label:`“${q}”`,hint:'Text match',run:() => commit(field.key,op,q)})
  valueItems(field,op)
 }
 const on=Math.min(active,Math.max(0,items.length-1)),expanded=open && (items.length>0 || !!q)
 useEffect(() => {if(expanded)list.current?.querySelector(`[id="${optionId(on)}"]`)?.scrollIntoView({block:'nearest'})},[on,expanded,optionId])
 useEffect(() => {
  const onKey=(e:globalThis.KeyboardEvent) => {if(e.key==='/' && !ignoreShortcut(e)){e.preventDefault();input.current?.focus()}}
  document.addEventListener('keydown',onKey);return () => document.removeEventListener('keydown',onKey)
 },[])
 const onKey=(e:KeyboardEvent<HTMLInputElement>) => {
  if(e.key==='ArrowDown' || e.key==='ArrowUp'){
   e.preventDefault()
   if(!open){setOpen(true);setActive(e.key==='ArrowDown' ? 0 : Math.max(0,items.length-1));return}
   if(e.altKey && e.key==='ArrowUp'){setOpen(false);return}
   if(items.length)setActive((on+(e.key==='ArrowDown' ? 1 : -1)+items.length)%items.length)
  } else if(e.key==='Enter'){e.preventDefault();if(expanded && items[on])items[on].run();else if(q && runAsk(q))return;else if(!open)setOpen(true)}
  else if(e.key==='Escape'){if(expanded){setOpen(false);e.stopPropagation()}else if(field || query)clearDraft();else e.currentTarget.blur()}
  else if(e.key==='Backspace' && !e.currentTarget.value){if(op && field && field.ops.length>1){setOp(null);setOpen(true)}else if(field){clearDraft();setOpen(true)}else if(pills.length)onChange(pills.slice(0,-1))}
  else if(e.key==='Tab')setOpen(false)
 }
 const celText=cel ?? pillsToCel(pills,fields)
 const applyCel=() => {const got=celToPills(celText,fields);if(got.ok){onChange(got.pills);setCel(null);setCelError('')}else setCelError(got.message)}
 const heads:string[]=[];for(const it of items)if(!heads.includes(it.head))heads.push(it.head)
 let index=-1
 const activeId=expanded && items.length ? optionId(on) : undefined
 return <section className="tm-filter" aria-label="Filters">
  <div className="tm-filter-box" onClick={e => {if(e.target===e.currentTarget)input.current?.focus()}}>
   {pills.map((pill,i) => {
    const def=fields.find(f => f.key===pill.field),name=def?.label ?? pill.field,value=pill.field==='amount' ? formatTotal(Number(pill.values[0])*100) : pill.values.join(', '),ops=def?.ops.length ?? 1
    return <span key={`${pill.field}-${pill.op}-${i}`} className="tm-pill">
     <span className="f">{name}</span>
     {ops>1 ? <button type="button" className="o" title="Click to change" aria-label={`${name} ${opLabel(pill)}. Change to ${nextOp(pill,fields)}.`} onClick={() => onChange(pills.map((p,k) => k===i ? {...p,op:nextOp(p,fields)} : p))}>{opLabel(pill)}</button> : <span className="o static">{opLabel(pill)}</span>}
     <span className="v" title={value}>{value}</span>
     <button type="button" className="x" aria-label={`Remove filter ${name} ${opLabel(pill)} ${value}`} onClick={() => {onChange(pills.filter((_,k) => k!==i));input.current?.focus()}}>×</button>
    </span>
   })}
   {field && <span className="tm-draft">{field.label}{op ? ` ${op}` : ''}</span>}
   <input ref={input} role="combobox" aria-expanded={expanded} aria-controls={listId} aria-autocomplete="list" aria-activedescendant={activeId} aria-label="Add a filter" autoComplete="off" spellCheck={false} value={query} placeholder={pills.length || field ? '' : placeholder}
    onChange={e => {setQuery(e.target.value);setActive(0);setOpen(true)}} onFocus={() => setOpen(true)} onBlur={() => setOpen(false)} onKeyDown={onKey}/>
  </div>
  <div ref={list} id={listId} role="listbox" aria-label="Filter suggestions" className="tm-suggest" hidden={!expanded} onMouseDown={e => e.preventDefault()}>
   {items.length===0 && <div className="none" role="presentation">No matches. Press Esc to cancel.</div>}
   {heads.map(head => {const headId=`${base}-h${heads.indexOf(head)}`;return <div key={head} role="group" aria-labelledby={headId}><div className="head" id={headId} role="presentation">{head}</div>{items.filter(it => it.head===head).map(it => {index=items.indexOf(it);const i=index;return <div key={`${it.label}${i}`} id={optionId(i)} role="option" aria-selected={i===on} className={i===on ? 'on' : undefined} onMouseEnter={() => setActive(i)} onClick={it.run}><span>{it.label}</span>{it.hint && <small>{it.hint}</small>}</div>})}</div>})}
  </div>
  <div className="tm-filter-meta">
   <div className="tm-chips">{asks?.length && ask ? <><span className="tm-chips-label">Ask in plain words:</span>{asks.map(text => <button key={text} type="button" className="tm-chip" onClick={() => runAsk(text)}>{text}</button>)}</> : null}</div>
   <div className="tm-filter-actions">
    {summary && <span className="tm-filter-summary" role="status">{summary}</span>}
    <button type="button" className="tm-btn small" aria-expanded={advanced} onClick={() => {setAdvanced(!advanced);setCel(null);setCelError('')}}>Advanced</button>
    {onSaveView && <button type="button" className="tm-btn small" onClick={onSaveView}>Save view</button>}
   </div>
  </div>
  {advanced && <div className="tm-cel">
   <label htmlFor={`${base}-cel`}>Same filter as CEL, the language agents use</label>
   <textarea id={`${base}-cel`} rows={2} spellCheck={false} value={celText} placeholder="No filter yet" aria-invalid={!!celError} onChange={e => {setCel(e.target.value);setCelError('')}} onKeyDown={e => {if(e.key==='Enter' && (e.ctrlKey || e.metaKey)){e.preventDefault();applyCel()}}}/>
   <div className="tm-row"><button type="button" className="tm-btn small" disabled={cel===null} onClick={applyCel}>Apply</button>{celError && <span className="tm-cel-error" role="alert">{celError}</span>}</div>
  </div>}
 </section>
}
