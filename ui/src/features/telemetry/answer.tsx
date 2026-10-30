import { useState, type ReactNode } from 'react'
import type { Fact } from './cube'
import './telemetry.css'

// The "What moved" list on its own, for pages that place it beside a chart.
// Rows are keyed by place, not name, so a new range changes their text in place; the list keeps the height of the fullest range seen, so nothing below it moves.
export function WhatMoved({facts,onContributor,label='What moved',reserve}:{facts:Fact[];onContributor?:(key:string,name:string) => void;label?:string;reserve?:boolean}) {
 const [fullest,setFullest]=useState(0)
 const moved=facts.find(f => f.contributors?.length)
 const goodUp=moved?.direction==='flat' || moved?.good===null || moved?.good===undefined ? null : (moved.direction==='up')===moved.good
 const rows=moved?.contributors ?? [],peak=Math.max(1,...rows.map(r => Math.abs(r.delta))),diverging=rows.some(r => r.delta<0)
 if(rows.length>fullest)setFullest(rows.length)
 if(!rows.length && !reserve && !fullest)return null
 return <div className="tm-moved"><h3>{label}{onContributor && <small>Click a row to filter</small>}</h3><ol style={{minHeight:Math.max(rows.length,fullest)*29}}>
   {rows.map((row,i) => {
    const good=goodUp===null ? null : (row.delta>=0)===goodUp,tone=good===null ? 'var(--c0)' : good ? 'var(--ok)' : 'var(--neg)',size=Math.abs(row.delta)/peak*(diverging ? 50 : 100)
    const content=<><span className="n">{row.name}</span><span className={`bar${diverging ? ' diverging' : ''}`}><i style={{left:0,width:`${size}%`,background:tone,transform:diverging && size ? `translateX(${(row.delta>=0 ? 50 : 50-size)/size*100}%)` : undefined}}/></span><span className={`t${good===true ? ' good' : good===false ? ' bad' : ''}`}>{row.text}</span></>
    return <li key={i}>{onContributor ? <button type="button" aria-label={`Filter to ${row.name}, ${row.text}`} onClick={() => onContributor(row.key ?? row.name,row.name)}>{content}</button> : <div>{content}</div>}</li>
   })}
 </ol></div>
}

// Renders the server's deterministic `facts`. Nothing here composes a number or a sentence of its own.
export function Answer({facts,status,note,onContributor,whatMovedLabel='What moved',hideMoved,loading,reserve}:{facts:Fact[];status?:ReactNode;note?:ReactNode;onContributor?:(key:string,name:string) => void;whatMovedLabel?:string;hideMoved?:boolean;loading?:boolean;reserve?:boolean}) {
 if(loading)return <div className="tm-answer" aria-busy="true"><div className="tm-answer-say"><p className="tm-answer-lead"><i className="tm-skel" style={{width:'60vw',maxWidth:'100%',height:18}}/></p></div></div>
 const lead=facts.find(f => f.kind==='period_change') ?? facts[0],rest=facts.filter(f => f!==lead)
 return <div className="tm-answer">
  <div className="tm-answer-say"><div><p className="tm-answer-lead">{lead?.sentence}</p>{rest.length>0 && <p className="tm-answer-rest">{rest.map(f => f.sentence).join(' ')}</p>}{note && <p className="tm-answer-rest">{note}</p>}</div>{status && <div className="tm-answer-status">{status}</div>}</div>
  {!hideMoved && <WhatMoved facts={facts} onContributor={onContributor} label={whatMovedLabel} reserve={reserve}/>}
 </div>
}
