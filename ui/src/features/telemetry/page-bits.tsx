import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { askAgent } from './shell-store'
import './telemetry.css'

export function PageHead({title,description,children}:{title:string;description?:ReactNode;children?:ReactNode}) {
 return <header className="page-header"><div><h1>{title}</h1>{description && <p className="description">{description}</p>}</div><div className="tm-dash-tools">{children}<button type="button" className="tm-btn small" onClick={() => askAgent()}>Ask an agent</button></div></header>
}
const LEDGER=[{to:'/ledger',label:'Entries'},{to:'/ledger/accounts',label:'Accounts'}] as const
const DATA=[{to:'/data',label:'Datasets'},{to:'/data/rates',label:'Rates'},{to:'/data/imports',label:'Imports'},{to:'/data/reconcile',label:'Reconcile'}] as const
export function SubNav({of,counts}:{of:'ledger'|'data';counts?:Record<string,number>}) {
 const items=of==='ledger' ? LEDGER : DATA
 return <nav className="page-tabs" aria-label={of==='ledger' ? 'Ledger' : 'Data'}>{items.map(i => <Link key={i.to} to={i.to} activeOptions={{exact:true,includeSearch:false}} activeProps={{className:'active','aria-current':'page'}}>{i.label}{counts?.[i.to] ? <span className="nav-count">{counts[i.to]}</span> : null}</Link>)}</nav>
}
export function RowsSkeleton({rows=8}:{rows?:number}) {
 return <div className="tm-rows-skeleton" role="status" aria-busy="true"><span className="sr-only">Loading…</span>{Array.from({length:rows},(_,i) => <i key={i} className="tm-skel" style={{width:`${92-(i%3)*14}%`}}/>)}</div>
}
export function Empty({title,children}:{title:string;children?:ReactNode}) {return <div className="empty"><h2>{title}</h2>{children && <p>{children}</p>}</div>}
const fmtAgo=new Intl.RelativeTimeFormat('en',{numeric:'auto'})
export function ago(iso:string):string {
 const ms=Date.parse(iso);if(Number.isNaN(ms))return ''
 const seconds=(ms-Date.now())/1000,steps:[number,Intl.RelativeTimeFormatUnit][]=[[60,'second'],[3600,'minute'],[86400,'hour'],[86400*30,'day'],[86400*365,'month'],[Infinity,'year']]
 let unit=1;for(const [limit,name] of steps){if(Math.abs(seconds)<limit)return fmtAgo.format(Math.round(seconds/unit),name);unit=limit}
 return ''
}
// snake_case keys never reach the screen; an agent's label wins and otherwise the key is at least words.
export const humanKey=(key:string,labels?:Record<string,string>,kind?:string) => (kind && labels?.[`${kind}:${key}`]) || key.replace(/[_-]+/g,' ').replace(/^./,c => c.toUpperCase())
