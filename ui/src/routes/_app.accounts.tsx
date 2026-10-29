import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { createFileRoute, useRouter } from '@tanstack/react-router'
import { z } from 'zod'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { AccountLabel } from '../components/account-label'
import { Amount } from '../components/amount'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { accountsQuery, balancesQuery } from '../features/accounts/queries'
import { tool, type Session } from '../lib/api'
import { dateLabel, decimalLabel } from '../lib/format'
import type { AccountsListOutput } from '../lib/tools'

type Account = NonNullable<AccountsListOutput['items']>[number]
const schema = z.object({at:z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional().catch(undefined), currency:z.enum(['EUR','USD']).optional().catch(undefined)})
export const Route = createFileRoute('/_app/accounts')({
  validateSearch:schema,
  loaderDeps:({search}) => ({at:search.at}),
  loader:async ({context,deps}) => {await Promise.all([context.queryClient.query({...accountsQuery(context.session),staleTime:'static'}),context.queryClient.query({...balancesQuery(context.session,deps.at,undefined),staleTime:'static'})])},
  pendingComponent:() => <section aria-busy="true"><h1>Accounts</h1><p>Loading balances…</p></section>,
  errorComponent:AccountError,
  component:Accounts,
})
function AccountError({reset}:{reset:() => void}) {const router=useRouter();return <section className="dashboard-error"><h1>Could not load accounts</h1><Button onClick={() => {reset();void router.invalidate()}}>Try again</Button></section>}
// Values from balances_get are rounded to cents; BigInt keeps totals exact beyond JavaScript's safe integer range.
function total(values:(string | undefined)[]): string | undefined {
  let cents=0n
  for (const value of values) {
    if (value === undefined || !/^-?\d+\.\d{2}$/.test(value)) return undefined
    const negative=value.startsWith('-'); const digits=value.replace('-','').replace('.','')
    cents += (negative ? -1n : 1n)*BigInt(digits)
  }
  const negative=cents<0n; const digits=(negative ? -cents : cents).toString().padStart(3,'0')
  return `${negative ? '−' : ''}${digits.slice(0,-2)}.${digits.slice(-2)}`
}
function Accounts() {
  const {session}=Route.useRouteContext(); const search=Route.useSearch(); const navigate=Route.useNavigate()
  const currency=search.currency ?? 'EUR'
  const {data:accounts}=useSuspenseQuery(accountsQuery(session))
  const {data:raw}=useSuspenseQuery(balancesQuery(session,search.at,undefined))
  const valued=useQuery(balancesQuery(session,search.at,currency))
  const [dialog,setDialog]=useState<Account | 'new' | null>(null)
  const rawByID=new Map((raw.rows ?? []).map((row) => [row.key,row]))
  const valueByID=new Map((valued.data?.rows ?? []).map((row) => [row.key,row.value?.v]))
  const valueFor=(account:Account) => !rawByID.has(account.id) ? '0.00' : valueByID.get(account.id)
  const net=valued.isSuccess ? total(accounts.filter((account) => account.type==='asset' || account.type==='liability').map(valueFor)) : undefined
  const groups = new Map<string,Map<string,Account[]>>()
  for (const account of accounts) {const pots=groups.get(account.type) ?? new Map<string,Account[]>(); const pot=account.pot || 'Other'; pots.set(pot,[...(pots.get(pot) ?? []),account]); groups.set(account.type,pots)}
  return <><header className="page-header"><div><p className="breadcrumb">WORKSPACE / ACCOUNTS</p><h1>Accounts</h1><p className="description">Balances from posted journal entries.</p></div><Button onClick={() => setDialog('new')}>Add account</Button></header>
    <form className="account-controls" key={`${search.at}-${currency}`} onSubmit={(event) => {event.preventDefault();const data=new FormData(event.currentTarget);void navigate({search:{at:String(data.get('at') || '') || undefined,currency:data.get('currency')==='USD' ? 'USD' : 'EUR'}})}}><label>As of<Input type="date" name="at" defaultValue={search.at}/></label><label>Value in<select name="currency" defaultValue={currency}><option value="EUR">EUR</option><option value="USD">USD</option></select></label><Button type="submit" variant="outline" size="sm">Apply</Button></form>
    <section className="account-net"><span>Net worth</span><strong>{net === undefined ? '—' : <Amount value={net} commodity={currency}/>}</strong><small>Assets and liabilities · as of {dateLabel(raw.as_of)}</small></section>
    {valued.isPending ? <p aria-busy="true">Calculating converted values…</p> : valued.isError && <div className="account-warning" role="alert"><p>Converted values are unavailable: {valued.error.message}</p><Button size="sm" variant="outline" onClick={() => void valued.refetch()}>Retry conversion</Button></div>}
    {accounts.length===0 ? <p className="settings-empty">No accounts yet. Add an account to start tracking balances.</p> : <div className="account-groups">{[...groups].map(([type,pots]) => {const groupAccounts=[...pots.values()].flat(); const groupTotal=valued.isSuccess ? total(groupAccounts.map(valueFor)) : undefined; return <section key={type}><h2>{type[0]?.toUpperCase()+type.slice(1)} <span>{groupTotal === undefined ? '—' : <Amount value={groupTotal} commodity={currency}/>}</span></h2>{[...pots].map(([pot,items]) => {const potTotal=valued.isSuccess ? total(items.map(valueFor)) : undefined; return <div className="account-pot" key={pot}><h3>{pot}<span>{potTotal === undefined ? '—' : <Amount value={potTotal} commodity={currency}/>}</span></h3><div className="table-scroll" role="region" tabIndex={0} aria-label={`${type} ${pot} accounts`}><table><thead><tr><th>Account</th><th>Balances</th><th className="numeric">Value {currency}</th><th><span className="sr-only">Action</span></th></tr></thead><tbody>{items.map((account) => {const row=rawByID.get(account.id); const value=valued.isSuccess ? valueFor(account) : undefined; return <tr key={account.id}><td><AccountLabel id={account.id} name={account.name}/>{account.closed_at && <small className="account-id">Closed</small>}</td><td>{row?.amounts?.length ? row.amounts.map((amount,index) => <span key={`${amount.c}-${index}`}>{index>0 && ' · '}<Amount value={amount.v} commodity={amount.c}/></span>) : '—'}</td><td className="numeric">{value === undefined ? '—' : decimalLabel(value)}</td><td><Button variant="outline" size="xs" onClick={() => setDialog(account)}>Edit<span className="sr-only"> {account.name}</span></Button></td></tr>})}</tbody></table></div></div>})}</section>})}</div>}
    <Dialog open={dialog!==null} onOpenChange={(open) => {if (!open) setDialog(null)}}><DialogContent className="account-dialog"><DialogHeader><DialogTitle>{dialog==='new' ? 'Add account' : 'Edit account'}</DialogTitle><DialogDescription>Account balances come from posted entries.</DialogDescription></DialogHeader>{dialog && <AccountForm key={dialog==='new' ? 'new' : dialog.id} session={session} account={dialog==='new' ? undefined : dialog} onDone={() => setDialog(null)}/>}</DialogContent></Dialog>
  </>
}
function AccountForm({session,account,onDone}:{session:Session;account?:Account;onDone:() => void}) {
  const queryClient=useQueryClient()
  const save=useMutation({mutationFn:async (form:FormData) => {const value=(key:string) => String(form.get(key) || '').trim(); const fields={name:value('name'),pot:value('pot'),location:value('location'),class:value('class'),commodities:value('commodities').split(',').map((c)=>c.trim()).filter(Boolean)}; const close=form.has('close'); return account ? tool(session,'account_update',{id:account.id,...fields,...(close !== !!account.closed_at ? {close} : {})}) : tool(session,'account_create',{id:value('id'),type:value('type'),...fields})},onSuccess:async () => {await Promise.all([queryClient.invalidateQueries({queryKey:['accounts',session.user.id]}),queryClient.invalidateQueries({queryKey:['balances',session.user.id]}),queryClient.invalidateQueries({queryKey:['forecast',session.user.id]}),queryClient.invalidateQueries({queryKey:['scenario-comparison',session.user.id]})]);onDone()}})
  function submit(event:FormEvent<HTMLFormElement>) {event.preventDefault();save.mutate(new FormData(event.currentTarget))}
  return <form className="account-form" onSubmit={submit}>{account ? <p>{account.id} · {account.type}</p> : <><label>Account ID<Input name="id" placeholder="assets:bank" required maxLength={150}/></label><label>Type<select name="type" defaultValue="asset"><option value="asset">Asset</option><option value="liability">Liability</option><option value="expense">Expense</option><option value="income">Income</option><option value="equity">Equity</option></select></label></>}<label>Name<Input name="name" required maxLength={200} defaultValue={account?.name}/></label><label>Commodities, separated by commas<Input name="commodities" required defaultValue={account?.commodities?.join(', ') ?? 'EUR'}/></label><label>Pot<Input name="pot" defaultValue={account?.pot}/></label><label>Location<Input name="location" defaultValue={account?.location}/></label><label>Class<select name="class" defaultValue={account?.class ?? ''}><option value="">None</option>{['bank','cash','metal_holding','receivable','liability','equity','income','expense'].map((value) => <option key={value} value={value}>{value.replace('_',' ')}</option>)}</select></label>{account && <><p>Existing commodities cannot be removed.</p><label className="account-close"><input name="close" type="checkbox" defaultChecked={!!account.closed_at}/> Account closed</label></>}<Button type="submit" disabled={save.isPending}>{account ? 'Save changes' : 'Create account'}</Button>{save.error && <p role="alert">{save.error.message}</p>}</form>
}
