import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { Link, createFileRoute } from '@tanstack/react-router'
import { tool, type Session } from '../lib/api'
import type { AccountsListOutput } from '../lib/tools'
import { amountLabel } from '../lib/format'
import { Stat } from '../features/telemetry/stat'
import { money } from '../features/telemetry/format'
import { useCube } from '../features/telemetry/cube-store'
import { balanceAt } from '../features/telemetry/cube'
import { describePage, openDetails } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton, SubNav, humanKey } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/ledger/accounts')({errorComponent:p => <RouteError {...p}/>,component:Accounts})
type Account=NonNullable<AccountsListOutput['items']>[number]
async function allAccounts(session:Session):Promise<Account[]> {
 const items:Account[]=[];let cursor:string|undefined
 const seen=new Set<string>()
 do {const page=await tool(session,'accounts_list',{include_closed:false,limit:500,cursor});items.push(...(page.items ?? []));cursor=page.next_cursor;if(cursor && seen.has(cursor))break;if(cursor)seen.add(cursor)} while(cursor)
 return items
}
const nameOf=(a:Account) => a.display_name || a.name
type Row={account:Account;value:number;text:string;native:string}
function Group({title,rows,currency}:{title:string;rows:Row[];currency:string}) {
 const [open,setOpen]=useState(false),big=rows.filter(r => Math.abs(r.value)>=.005).sort((a,b) => Math.abs(b.value)-Math.abs(a.value)),small=rows.filter(r => Math.abs(r.value)<.005),shown=open ? [...big,...small] : big,total=rows.reduce((n,r) => n+r.value,0)
 const details=(r:Row) => openDetails({historyOf:r.account.id,target:{value:r.text,params:{account:r.account.id}},content:{title:nameOf(r.account),value:r.text,subtitle:r.account.note || humanKey(r.account.class ?? r.account.type),calculation:{summary:r.native ? `Holds ${r.native}, valued in ${currency} at today’s rate.` : 'Nothing is held here right now.',entries:<Link to="/ledger" search={{accounts:[r.account.id],t:'all'}}>See its entries</Link>},machine:[{label:'Account ID',value:r.account.id},...(r.account.pot ? [{label:'Pot',value:r.account.pot}] : []),...(r.account.note ? [{label:'Note',value:r.account.note,mono:false}] : []),...(r.account.location ? [{label:'Where',value:r.account.location,mono:false}] : [])]}})
 return <section className="tm-account-group"><header><h2>{title}</h2><span className="data">{money(total*100,currency)}</span></header>
  <ul>{shown.map(r => <li key={r.account.id}><button type="button" onClick={() => details(r)}><span className="n">{nameOf(r.account)}{r.account.note && <small>{r.account.note}</small>}</span><span className="t data">{r.text}{r.native && r.native!==r.text && <small>{r.native}</small>}</span></button></li>)}
  {!open && small.length>0 && <li className="more"><button type="button" onClick={() => setOpen(true)}><span className="n">{small.length} small {small.length===1 ? 'account' : 'accounts'}</span><span className="t">Show</span></button></li>}</ul>
 </section>
}
function Accounts() {
 const {session,bootstrap}=Route.useRouteContext(),cube=useCube(session.user.id),currency=cube?.base ?? 'EUR'
 const accounts=useQuery({queryKey:['accounts',session.user.id],staleTime:60_000,queryFn:() => allAccounts(session)})
 const balances=useQuery({queryKey:['balances',session.user.id,currency],staleTime:60_000,queryFn:() => tool(session,'balances_get',{value_in:currency,group_by:'account'})})
 describePage({title:'Accounts',lines:[]})
 if(accounts.error || balances.error)return <><PageHead title="Ledger"/><SubNav of="ledger"/><RouteError error={accounts.error ?? balances.error} reset={() => {void accounts.refetch();void balances.refetch()}} title="Accounts didn’t load"/></>
 if(!accounts.data || !balances.data)return <><PageHead title="Ledger" description="Show me the entries and the accounts."/><SubNav of="ledger"/><RowsSkeleton rows={12}/></>
 const byKey=new Map((balances.data.rows ?? []).map(r => [r.key,r])),rows:Row[]=accounts.data.map(account => {const b=byKey.get(account.id),value=Number(b?.value?.v ?? 0),amounts=(b?.amounts ?? []).filter(x => Number(x.v)!==0);return {account,value,text:b?.value ? amountLabel(b.value,2) : money(0,currency),native:amounts.map(x => amountLabel(x,2)).join(' · ')}})
 const holds=rows.filter(r => r.account.type==='asset' || r.account.type==='liability'),groups=new Map<string,Row[]>()
 for(const r of holds){const key=r.account.type==='liability' ? 'Debts' : r.account.pot ? humanKey(r.account.pot,bootstrap.labels,'pot') : 'Other accounts';groups.set(key,[...(groups.get(key) ?? []),r])}
 const net=cube ? balanceAt(cube,cube.bounds.today) : null,sorted=[...groups.entries()].sort((a,b) => Math.abs(b[1].reduce((n,r) => n+r.value,0))-Math.abs(a[1].reduce((n,r) => n+r.value,0)))
 return <>
  <PageHead title="Ledger" description="Show me the entries and the accounts."/>
  <SubNav of="ledger"/>
  {net!==null && <div className="tm-stats tm-account-net"><Stat label="Net worth today" value={money(net,currency)} hideEmptyCompare/></div>}
  {sorted.length ? <div className="tm-account-groups">{sorted.map(([title,items]) => <Group key={title} title={title} rows={items} currency={currency}/>)}</div> : <Empty title="No accounts yet">An agent sets these up when it imports your first statement.</Empty>}
 </>
}
