import { useState, type ReactNode } from 'react'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../../components/ui/sheet'
import './telemetry.css'

export type DetailField={label:string;value:string;mono?:boolean};export type DetailHistory={at:string;actor:string;summary:string}
export type DetailsContent={title:string;value?:string;subtitle?:string;
 calculation?:{summary?:string;formula?:string;rates?:{label:string;value:string}[];entries?:ReactNode};
 sourceRows?:{columns:string[];rows:string[][]};history?:DetailHistory[];machine?:DetailField[]}
export type FlagTarget={dashboard?:string;component?:string;metric?:string;value?:string;params?:Record<string,string>}
// The one home for IDs, hashes, formulas, source rows and history, so they never have to appear on a page.
export function DetailsDrawer({open,onOpenChange,content,target,onFlag}:{open:boolean;onOpenChange:(open:boolean) => void;content:DetailsContent|null;target?:FlagTarget;onFlag?:(flag:{target:FlagTarget;note:string}) => void|Promise<void>}) {
 const [flagging,setFlagging]=useState(false),[note,setNote]=useState(''),[status,setStatus]=useState<'idle'|'sending'|'sent'|'failed'>('idle')
 const reset=() => {setFlagging(false);setNote('');setStatus('idle')}
 const send=async () => {setStatus('sending');try {await onFlag?.({target:target ?? {},note:note.trim()});setStatus('sent')} catch {setStatus('failed')}}
 const c=content
 return <Sheet open={open} onOpenChange={next => {if(!next)reset();onOpenChange(next)}}>
  <SheetContent side="right" className="tm-drawer">
   {c && <>
    <SheetHeader className="tm-drawer-head"><SheetTitle>{c.title}</SheetTitle>{c.value && <div className="tm-drawer-value data">{c.value}</div>}<SheetDescription>{c.subtitle ?? 'Where this number comes from.'}</SheetDescription></SheetHeader>
    <div className="tm-drawer-body">
     {c.calculation && <section><h3>How was this calculated?</h3>{c.calculation.summary && <p>{c.calculation.summary}</p>}{c.calculation.formula && <pre className="tm-code" tabIndex={0}>{c.calculation.formula}</pre>}{c.calculation.rates?.length ? <dl className="tm-dl">{c.calculation.rates.map(r => <div key={r.label}><dt>{r.label}</dt><dd>{r.value}</dd></div>)}</dl> : null}{c.calculation.entries}</section>}
     {c.sourceRows?.rows.length ? <section><h3>Source rows</h3><div className="tm-drawer-table" tabIndex={0} role="region" aria-label="Source rows"><table><thead><tr>{c.sourceRows.columns.map(h => <th key={h} scope="col">{h}</th>)}</tr></thead><tbody>{c.sourceRows.rows.map((row,i) => <tr key={i}>{row.map((v,k) => <td key={k}>{v}</td>)}</tr>)}</tbody></table></div></section> : null}
     {c.history?.length ? <section><h3>History</h3><ol className="tm-history">{c.history.map((h,i) => <li key={i}><time>{h.at}</time><span>{h.summary}</span><small>{h.actor}</small></li>)}</ol></section> : null}
     {c.machine?.length ? <section><h3>Machine fields</h3><dl className="tm-dl">{c.machine.map(f => <div key={f.label}><dt>{f.label}</dt><dd className={f.mono===false ? undefined : 'mono'}>{f.value}</dd></div>)}</dl></section> : null}
     {onFlag && <section>
      <h3>Does this look wrong?</h3>
      {status==='sent' ? <p role="status">Sent to an agent with this number’s context. It will show up in Changes when someone answers.</p> : !flagging ? <button type="button" className="tm-btn" onClick={() => setFlagging(true)}>Flag this</button> : <form onSubmit={e => {e.preventDefault();void send()}}>
       <label className="tm-field-label" htmlFor="tm-flag-note">What looks wrong? (optional)</label>
       <textarea id="tm-flag-note" className="tm-textarea" rows={3} value={note} onChange={e => setNote(e.target.value)} autoFocus/>
       <div className="tm-row"><button type="submit" className="tm-btn primary" disabled={status==='sending'}>{status==='sending' ? 'Sending…' : 'Send to an agent'}</button><button type="button" className="tm-btn" onClick={reset}>Cancel</button></div>
       {status==='failed' && <p role="alert" className="tm-cel-error">That did not send. Your note is still here, so try again.</p>}
      </form>}
     </section>}
    </div>
   </>}
  </SheetContent>
 </Sheet>
}
