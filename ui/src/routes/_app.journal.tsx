import { useRef, useState, type FormEvent } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { createFileRoute, useRouter } from '@tanstack/react-router'
import { z } from 'zod'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { Skeleton } from '../components/ui/skeleton'
import { AccountLabel } from '../components/account-label'
import type { Account } from '../components/account-picker'
import { Amount } from '../components/amount'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { EntryForm } from '../features/journal/entry-form'
import { accountsQuery } from '../features/accounts/queries'
import { journalQuery } from '../features/journal/queries'
import { postingTotals } from '../features/journal/totals'
import { postedNotice } from '../features/journal/post-notice'
import { DataTable } from '../features/dashboard/data-table'
import { compareDecimals } from '../features/dashboard/decimal-sort'
import { tableSort } from '../features/dashboard/table-sort'
import { tool, type Session } from '../lib/api'
import { accountLabel, amountLabel, dateLabel } from '../lib/format'
import type { EntriesListInput } from '../lib/tools'

const date = z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional().catch(undefined)
const searchSchema = z.object({ text:z.string().max(200).optional().catch(undefined), account:z.string().optional().catch(undefined), accounts:z.array(z.string().regex(/^(assets|liabilities|income|expenses|equity):[a-z0-9_:-]{1,150}$/)).max(1000).optional().catch(undefined), tag:z.string().optional().catch(undefined), from:date, to:date, status:z.enum(['draft','posted']).optional().catch(undefined), effective:z.boolean().optional().catch(undefined), sort:tableSort, entry:z.string().optional().catch(undefined), new:z.boolean().optional().catch(undefined) })
const serverSort=(sort:string|undefined) => sort?.split(',').find(item => ['date','-date','amount','-amount'].includes(item))
export const Route = createFileRoute('/_app/journal')({
  validateSearch: searchSchema,
  loaderDeps: ({ search }) => ({ text:search.text, account:search.account, accounts:search.account ? undefined : search.accounts, tag:search.tag, from:search.from, to:search.to, status:search.status, effective:search.effective, sort:serverSort(search.sort) }),
  loader: async ({ context, deps }) => { await Promise.all([context.queryClient.infiniteQuery({ ...journalQuery(context.session, deps), staleTime:'static' }), context.queryClient.query({ ...accountsQuery(context.session), staleTime:'static' })]) },
  pendingMs: 150,
  pendingMinMs: 300,
  pendingComponent: JournalPending,
  errorComponent: JournalError,
  component: Journal,
})
function JournalPending() {
  const columns = ['Date','Payee','Accounts','Posting amount','Status','Tags']
  return <section role="status" aria-busy="true"><span className="sr-only">Loading journal…</span><header className="page-header"><div><p className="breadcrumb">WORKSPACE / JOURNAL</p><h1>Journal</h1><p className="description">Entries and their postings.</p></div><Skeleton className="skeleton-bar journal-loading-action"/></header><div aria-hidden="true"><div className="journal-filters journal-loading-filters">{['Search','Account or accounts','Tag','From','To','Status'].map(label => <div key={label}><span>{label}</span><Skeleton className="skeleton-bar"/></div>)}</div><div className="table-scroll skeleton-table journal-loading-table"><table><thead><tr>{columns.map(label => <th key={label}>{label}</th>)}</tr></thead><tbody>{Array.from({length:8},(_,row) => <tr key={row}>{columns.map((label,index) => <td key={label}><Skeleton className={`skeleton-bar ${index===3 ? 'numeric' : ''}`}/></td>)}</tr>)}</tbody></table></div></div></section>
}
function JournalError({ error, reset }: { error: unknown; reset: () => void }) { const router = useRouter(); return <section className="dashboard-error" role="alert"><h1>Could not load the journal</h1><p>{error instanceof Error ? error.message : String(error)}</p><Button onClick={() => { reset(); void router.invalidate() }}>Try again</Button></section> }
function Journal() {
  const { session } = Route.useRouteContext()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const filters: EntriesListInput = { text:search.text, account:search.account, accounts:search.account ? undefined : search.accounts, tag:search.tag, from:search.from, to:search.to, status:search.status, effective:search.effective, sort:serverSort(search.sort) }
  const entries = useInfiniteQuery(journalQuery(session, filters))
  const { data: accounts } = useSuspenseQuery(accountsQuery(session))
  const names = new Map(accounts.map((account) => [account.id, account.name]))
  const rows = entries.data?.pages.flatMap((page) => page.items ?? []) ?? []
  const amounts=rows.map(entry => (entry.postings?.find(posting => search.account ? posting.account===search.account : search.accounts?.includes(posting.account)) ?? entry.postings?.[0])?.amount)
  const total=postingTotals(amounts)
  const searchRef = useRef<HTMLInputElement>(null)
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const data = new FormData(event.currentTarget)
    const value = (key:string) => String(data.get(key) || '') || undefined
    const selected=(value('account') ?? '').split(',').map(item => item.trim()).filter(Boolean)
    void navigate({ search: { text:value('text'), account:selected.length===1 ? selected[0] : undefined, accounts:selected.length>1 ? selected : undefined, tag:value('tag'), from:value('from'), to:value('to'), status: data.get('status') === 'draft' ? 'draft' : data.get('status') === 'posted' ? 'posted' : undefined, effective:data.has('effective') || undefined, sort:search.sort, entry:undefined } })
  }
  return <><header className="page-header"><div><p className="breadcrumb">WORKSPACE / JOURNAL</p><h1>Journal</h1><p className="description">Entries and their postings.</p></div><Button onClick={() => void navigate({search:(old) => ({...old,new:true,entry:undefined})})}>New entry</Button></header>
    <form className="journal-filters" key={JSON.stringify(filters)} onSubmit={submit}>
      <label>Search<Input ref={searchRef} name="text" type="search" defaultValue={search.text} maxLength={200}/></label><label>Account or accounts<Input name="account" aria-label="Account" pattern="(assets|liabilities|income|expenses|equity):[a-z0-9_:-]{1,150}(,\s*(assets|liabilities|income|expenses|equity):[a-z0-9_:-]{1,150})*" title="Enter account IDs separated by commas." list="journal-accounts" defaultValue={search.account ?? search.accounts?.join(', ')}/><datalist id="journal-accounts">{accounts.map((account) => <option value={account.id} key={account.id}>{account.name}</option>)}</datalist></label><label>Tag<Input name="tag" defaultValue={search.tag}/></label><label>From<Input name="from" type="date" defaultValue={search.from}/></label><label>To<Input name="to" type="date" defaultValue={search.to}/></label><label>Status<select name="status" defaultValue={search.status ?? ''}><option value="">All</option><option value="draft">Draft</option><option value="posted">Posted</option></select></label><label className="journal-check"><input name="effective" type="checkbox" defaultChecked={search.effective}/> Effective only</label><Button type="submit" variant="outline" size="sm">Apply filters</Button>
    </form>
    {entries.isPending ? <p aria-busy="true">Loading entries…</p> : entries.isError && rows.length === 0 ? <div role="alert"><p>{entries.error.message}</p><Button onClick={() => void entries.refetch()}>Retry</Button></div> : rows.length === 0 ? <p className="settings-empty">No entries match these filters.</p> : <DataTable key={`table:${JSON.stringify(filters)}`} storageKey={`journal:${session.user.id}`} regionLabel="Journal entries" resetKey={JSON.stringify(filters)} externalFilterRef={searchRef} forceVirtual totalLabel={entries.hasNextPage ? 'Loaded total' : 'Total'} totalOverrides={{amount:{value:total ?? '—',title:total ? `Displayed posting amounts by commodity: ${total}` : 'No total available for the displayed posting amounts.'}}} sort={search.sort} onSortChange={sort => void navigate({search:old => ({...old,sort})})} onRowActivate={row => void navigate({search:old => ({...old,entry:String(row._id.v)})})} onScroll={element => {if(element.scrollHeight-element.scrollTop-element.clientHeight<320 && entries.hasNextPage && !entries.isFetching)void entries.fetchNextPage()}} footer={<>{entries.isFetchingNextPage && <p>Loading more entries…</p>}{entries.isFetchNextPageError && <div role="alert"><p>{entries.error.message}</p><Button onClick={() => void entries.fetchNextPage()}>Retry</Button></div>}{entries.hasNextPage && <Button variant="outline" size="sm" disabled={entries.isFetching} onClick={() => void entries.fetchNextPage()}>Load more</Button>}</>} sorters={{amount:(a,b) => {const left=String(a.amount.v),right=String(b.amount.v);return left==='' ? right==='' ? 0 : -1 : right==='' ? 1 : compareDecimals(left,right)}}} value={{type:'table',columns:[{key:'date',label:'Date',format:'date',align:'left'},{key:'payee',label:'Payee',format:'text',align:'left'},{key:'accounts',label:'Accounts',format:'account',align:'left'},{key:'amount',label:'Posting amount',format:'money',align:'right'},{key:'status',label:'Status',format:'status',align:'left'},{key:'tags',label:'Tags',format:'text',align:'left'}],rows:rows.map((entry,index) => {const amount=amounts[index],accounts=(entry.postings ?? []).map(posting => {const label=accountLabel(posting.account,names.get(posting.account));return label.name+(label.trail ? ` · ${label.trail}` : '')}).join(', '),tags=entry.tags?.join(', ') || '—';return {_id:{v:entry.id,t:''},_index:{v:index,t:''},date:{v:entry.date,t:dateLabel(entry.date)},payee:{v:entry.payee || '',t:entry.payee || '—'},accounts:{v:accounts,t:accounts},amount:{v:amount?.v ?? '',t:amount ? amountLabel(amount) : '—'},status:{v:entry.status,t:entry.status},tags:{v:tags,t:tags}}})}} renderCell={(row,column) => {if(column==='date')return <button className="journal-entry-link" onClick={event => {event.stopPropagation();void navigate({search:old => ({...old,entry:String(row._id.v)})})}}>{row.date.t}</button>;if(column==='accounts')return (rows[Number(row._index.v)].postings ?? []).map((posting,index) => <span key={index}>{index>0 && ', '}<AccountLabel id={posting.account} name={names.get(posting.account)}/></span>);if(column==='amount'){const amount=amounts[Number(row._index.v)];return amount ? <Amount value={amount.v} commodity={amount.c}/> : '—'}return row[column]?.t ?? '—'}}/>}
    <Sheet open={!!search.entry || !!search.new} onOpenChange={(open) => { if (!open) void navigate({search:(old) => ({...old,entry:undefined,new:undefined})}) }}><SheetContent className="journal-sheet"><SheetHeader><SheetTitle>{search.new ? "New entry" : "Entry"}</SheetTitle><SheetDescription>Postings, links and history</SheetDescription></SheetHeader>{search.new ? <div className="journal-detail"><EntryForm session={session} accounts={accounts} onDone={() => void navigate({search:(old) => ({...old,new:undefined})})} onCancel={() => void navigate({search:(old) => ({...old,new:undefined})})}/></div> : search.entry && <EntryDetail key={search.entry} session={session} id={search.entry} names={names} accounts={accounts}/>}</SheetContent></Sheet>
  </>
}
function EntryDetail({ session, id, names, accounts }: { session:Session; id:string; names:Map<string,string>; accounts:Account[] }) {
  const queryClient = useQueryClient()
  const [reason,setReason] = useState('')
  const [confirm,setConfirm] = useState(false)
  const [editing,setEditing] = useState(false)
  const detail = useQuery({queryKey:['entry',session.user.id,id],queryFn:() => tool(session,'entry_get',{id})})
  const history = useQuery({queryKey:['history',session.user.id,id],queryFn:() => tool(session,'history_of',{target_id:id})})
  const post = useMutation({mutationFn:() => tool(session,'entry_post',{id,commit:true}),onSuccess:async (saved) => {await Promise.all([queryClient.invalidateQueries({queryKey:['journal',session.user.id]}),queryClient.invalidateQueries({queryKey:['balances',session.user.id]}),queryClient.invalidateQueries({queryKey:['budget',session.user.id]}),queryClient.invalidateQueries({queryKey:['reconcile',session.user.id]}),queryClient.invalidateQueries({queryKey:['reconcile-suggestions',session.user.id]}),queryClient.invalidateQueries({queryKey:['dashboard',session.user.id]}),queryClient.invalidateQueries({queryKey:['forecast',session.user.id]}),queryClient.invalidateQueries({queryKey:['scenario-comparison',session.user.id]}),queryClient.invalidateQueries({queryKey:['entry',session.user.id,id]}),queryClient.invalidateQueries({queryKey:['history',session.user.id,id]})]);if(saved.event_id && saved.entry.status==='posted')postedNotice(session,queryClient,saved.event_id)}})
  const reverse = useMutation({mutationFn:() => tool(session,'entry_reverse',{id,reason}),onSuccess:async () => {setConfirm(false);setReason('');await Promise.all([queryClient.invalidateQueries({queryKey:['journal',session.user.id]}),queryClient.invalidateQueries({queryKey:['balances',session.user.id]}),queryClient.invalidateQueries({queryKey:['budget',session.user.id]}),queryClient.invalidateQueries({queryKey:['reconcile',session.user.id]}),queryClient.invalidateQueries({queryKey:['reconcile-suggestions',session.user.id]}),queryClient.invalidateQueries({queryKey:['dashboard',session.user.id]}),queryClient.invalidateQueries({queryKey:['forecast',session.user.id]}),queryClient.invalidateQueries({queryKey:['scenario-comparison',session.user.id]}),queryClient.invalidateQueries({queryKey:['entry',session.user.id,id]}),queryClient.invalidateQueries({queryKey:['history',session.user.id,id]})])}})
  if (detail.isPending) return <p>Loading entry…</p>
  if (detail.isError) return <div role="alert"><p>{detail.error.message}</p><Button onClick={() => void detail.refetch()}>Retry</Button></div>
  const entry=detail.data.entry
  if (editing) return <div className="journal-detail"><EntryForm session={session} entry={entry} accounts={accounts} onDone={() => setEditing(false)} onCancel={() => setEditing(false)}/></div>
  return <div className="journal-detail">{entry.status === 'draft' && <><div className="setting-actions"><Button variant="outline" onClick={() => setEditing(true)}>Edit draft</Button><Button disabled={post.isPending} onClick={() => post.mutate()}>Post draft</Button></div>{post.error && <p role="alert">{post.error.message}</p>}</>}<h2>{entry.payee || 'Entry'}</h2><p><time dateTime={entry.date}>{dateLabel(entry.date)}</time> · {entry.status} · {entry.kind}</p><p className="muted">{entry.id}</p><h3>Postings</h3><ul>{(entry.postings ?? []).map((posting,index) => <li key={index}><AccountLabel id={posting.account} name={names.get(posting.account)}/><Amount value={posting.amount.v} commodity={posting.amount.c}/>{posting.memo && <small>{posting.memo}</small>}</li>)}</ul><h3>Links</h3>{!entry.links?.length ? <p>None</p> : <ul>{entry.links.map((link,index) => <li key={index}>{link.rel}: {link.id}</li>)}</ul>}<h3>History</h3>{history.isPending ? <p>Loading history…</p> : history.isError ? <div role="alert"><p>{history.error.message}</p><Button onClick={() => void history.refetch()}>Retry history</Button></div> : !history.data.events?.length ? <p>No history.</p> : <ul>{history.data.events.map((event) => <li key={event.id}>{event.tool} · <time dateTime={event.at}>{new Date(event.at).toLocaleString()}</time></li>)}</ul>}{entry.status === 'posted' && (confirm ? <form onSubmit={(event) => {event.preventDefault();reverse.mutate()}}><label>Reason<Input required value={reason} onChange={(event) => setReason(event.target.value)}/></label><div className="setting-actions"><Button type="submit" variant="destructive" disabled={reverse.isPending}>Confirm reversal</Button><Button type="button" variant="outline" onClick={() => setConfirm(false)}>Cancel</Button></div>{reverse.error && <p role="alert">{reverse.error.message}</p>}</form> : <Button variant="outline" onClick={() => setConfirm(true)}>Reverse entry</Button>)}</div>
}
