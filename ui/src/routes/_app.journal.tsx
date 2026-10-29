import { useRef, useState, type FormEvent } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { createFileRoute, useRouter } from '@tanstack/react-router'
import { useVirtualizer } from '@tanstack/react-virtual'
import { z } from 'zod'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { EntryForm } from '../features/journal/entry-form'
import { accountsQuery } from '../features/accounts/queries'
import { journalQuery } from '../features/journal/queries'
import { tool, type Session } from '../lib/api'
import type { EntriesListInput } from '../lib/tools'

const date = z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional().catch(undefined)
const searchSchema = z.object({ text:z.string().max(200).optional().catch(undefined), account:z.string().optional().catch(undefined), tag:z.string().optional().catch(undefined), from:date, to:date, status:z.enum(['draft','posted']).optional().catch(undefined), effective:z.boolean().optional().catch(undefined), entry:z.string().optional().catch(undefined), new:z.boolean().optional().catch(undefined) })
export const Route = createFileRoute('/_app/journal')({
  validateSearch: searchSchema,
  loaderDeps: ({ search }) => ({ text:search.text, account:search.account, tag:search.tag, from:search.from, to:search.to, status:search.status, effective:search.effective }),
  loader: async ({ context, deps }) => { await Promise.all([context.queryClient.infiniteQuery({ ...journalQuery(context.session, deps), staleTime:'static' }), context.queryClient.query({ ...accountsQuery(context.session), staleTime:'static' })]) },
  pendingComponent: () => <section aria-busy="true"><h1>Journal</h1><p>Loading entries…</p></section>,
  errorComponent: JournalError,
  component: Journal,
})
function JournalError({ reset }: { reset: () => void }) { const router = useRouter(); return <section className="dashboard-error"><h1>Could not load the journal</h1><Button onClick={() => { reset(); void router.invalidate() }}>Try again</Button></section> }
function Journal() {
  const { session } = Route.useRouteContext()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const filters: EntriesListInput = { text:search.text, account:search.account, tag:search.tag, from:search.from, to:search.to, status:search.status, effective:search.effective }
  const entries = useInfiniteQuery(journalQuery(session, filters))
  const { data: accounts } = useSuspenseQuery(accountsQuery(session))
  const names = new Map(accounts.map((account) => [account.id, account.name]))
  // shortcut: entries remain in sequence order until the API supports stable sorted cursors.
  const rows = entries.data?.pages.flatMap((page) => page.items ?? []) ?? []
  const scrollRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({ count:rows.length, getScrollElement:() => scrollRef.current, estimateSize:() => 32, overscan:12 })
  const virtual = virtualizer.getVirtualItems()
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const data = new FormData(event.currentTarget)
    const value = (key:string) => String(data.get(key) || '') || undefined
    void navigate({ search: { text:value('text'), account:value('account'), tag:value('tag'), from:value('from'), to:value('to'), status: data.get('status') === 'draft' ? 'draft' : data.get('status') === 'posted' ? 'posted' : undefined, effective:data.has('effective') || undefined, entry:undefined } })
    scrollRef.current?.scrollTo(0,0)
  }
  return <><header className="page-header"><div><p className="breadcrumb">WORKSPACE / JOURNAL</p><h1>Journal</h1><p className="description">Entries and their postings.</p></div><Button onClick={() => void navigate({search:(old) => ({...old,new:true,entry:undefined})})}>New entry</Button></header>
    <form className="journal-filters" key={JSON.stringify(filters)} onSubmit={submit}>
      <label>Search<Input name="text" type="search" defaultValue={search.text} maxLength={200}/></label><label>Account<Input name="account" list="journal-accounts" defaultValue={search.account}/><datalist id="journal-accounts">{accounts.map((account) => <option value={account.id} key={account.id}>{account.name}</option>)}</datalist></label><label>Tag<Input name="tag" defaultValue={search.tag}/></label><label>From<Input name="from" type="date" defaultValue={search.from}/></label><label>To<Input name="to" type="date" defaultValue={search.to}/></label><label>Status<select name="status" defaultValue={search.status ?? ''}><option value="">All</option><option value="draft">Draft</option><option value="posted">Posted</option></select></label><label className="journal-check"><input name="effective" type="checkbox" defaultChecked={search.effective}/> Effective only</label><Button type="submit" variant="outline" size="sm">Apply filters</Button>
    </form>
    {entries.isPending ? <p aria-busy="true">Loading entries…</p> : entries.isError && rows.length === 0 ? <div role="alert"><p>{entries.error.message}</p><Button onClick={() => void entries.refetch()}>Retry</Button></div> : rows.length === 0 ? <p className="settings-empty">No entries match these filters.</p> : <div ref={scrollRef} className="journal-scroll" role="region" aria-label="Journal entries" tabIndex={0} onScroll={() => { const element=scrollRef.current; if (element && element.scrollHeight-element.scrollTop-element.clientHeight < 320 && entries.hasNextPage && !entries.isFetching) void entries.fetchNextPage() }}>
      <table className="journal-table"><thead><tr><th>Date</th><th>Payee</th><th>Accounts</th><th className="numeric">Posting amount</th><th>Status</th><th>Tags</th></tr></thead><tbody>
        {virtual.length > 0 && virtual[0].start > 0 && <tr aria-hidden="true"><td colSpan={6} style={{height:virtual[0].start,padding:0,border:0}}/></tr>}
        {/* shortcut: amounts use one posting until main-commodity selection is defined. */}
        {virtual.map((item) => { const entry=rows[item.index]; const amount=(entry.postings?.find((posting) => search.account && posting.account === search.account) ?? entry.postings?.[0])?.amount; return <tr key={entry.id} onClick={() => void navigate({ search:(old) => ({...old,entry:entry.id}) })}><td><button className="journal-entry-link" onClick={(event) => { event.stopPropagation(); void navigate({ search:(old) => ({...old,entry:entry.id}) }) }}>{entry.date}</button></td><td>{entry.payee || '—'}</td><td>{(entry.postings ?? []).map((posting) => names.get(posting.account) || posting.account).join(', ')}</td><td className="numeric">{amount ? `${amount.v.replace(/^-/, '−')} ${amount.c}` : '—'}</td><td>{entry.status}</td><td>{entry.tags?.join(', ') || '—'}</td></tr> })}
        {virtual.length > 0 && <tr aria-hidden="true"><td colSpan={6} style={{height:Math.max(0,virtualizer.getTotalSize()-virtual[virtual.length-1].end),padding:0,border:0}}/></tr>}
      </tbody></table>{entries.isFetchingNextPage && <p>Loading more entries…</p>}{entries.isFetchNextPageError && <div role="alert"><p>{entries.error.message}</p><Button onClick={() => void entries.fetchNextPage()}>Retry</Button></div>}{entries.hasNextPage && <Button variant="outline" size="sm" disabled={entries.isFetching} onClick={() => void entries.fetchNextPage()}>Load more</Button>}
    </div>}
    <Sheet open={!!search.entry || !!search.new} onOpenChange={(open) => { if (!open) void navigate({search:(old) => ({...old,entry:undefined,new:undefined})}) }}><SheetContent className="journal-sheet"><SheetHeader><SheetTitle>{search.new ? "New entry" : "Entry"}</SheetTitle><SheetDescription>Postings, links and history</SheetDescription></SheetHeader>{search.new ? <div className="journal-detail"><EntryForm session={session} accounts={accounts} onDone={() => void navigate({search:(old) => ({...old,new:undefined})})} onCancel={() => void navigate({search:(old) => ({...old,new:undefined})})}/></div> : search.entry && <EntryDetail key={search.entry} session={session} id={search.entry} names={names} accounts={accounts}/>}</SheetContent></Sheet>
  </>
}
function EntryDetail({ session, id, names, accounts }: { session:Session; id:string; names:Map<string,string>; accounts:{id:string;name:string}[] }) {
  const queryClient = useQueryClient()
  const [reason,setReason] = useState('')
  const [confirm,setConfirm] = useState(false)
  const [editing,setEditing] = useState(false)
  const detail = useQuery({queryKey:['entry',session.user.id,id],queryFn:() => tool(session,'entry_get',{id})})
  const history = useQuery({queryKey:['history',session.user.id,id],queryFn:() => tool(session,'history_of',{target_id:id})})
  const post = useMutation({mutationFn:() => tool(session,'entry_post',{id,commit:true}),onSuccess:async () => {await Promise.all([queryClient.invalidateQueries({queryKey:['journal',session.user.id]}),queryClient.invalidateQueries({queryKey:['balances',session.user.id]}),queryClient.invalidateQueries({queryKey:['budget',session.user.id]}),queryClient.invalidateQueries({queryKey:['reconcile',session.user.id]}),queryClient.invalidateQueries({queryKey:['reconcile-suggestions',session.user.id]}),queryClient.invalidateQueries({queryKey:['dashboard',session.user.id]}),queryClient.invalidateQueries({queryKey:['entry',session.user.id,id]}),queryClient.invalidateQueries({queryKey:['history',session.user.id,id]})])}})
  const reverse = useMutation({mutationFn:() => tool(session,'entry_reverse',{id,reason}),onSuccess:async () => {setConfirm(false);setReason('');await Promise.all([queryClient.invalidateQueries({queryKey:['journal',session.user.id]}),queryClient.invalidateQueries({queryKey:['balances',session.user.id]}),queryClient.invalidateQueries({queryKey:['budget',session.user.id]}),queryClient.invalidateQueries({queryKey:['reconcile',session.user.id]}),queryClient.invalidateQueries({queryKey:['reconcile-suggestions',session.user.id]}),queryClient.invalidateQueries({queryKey:['dashboard',session.user.id]}),queryClient.invalidateQueries({queryKey:['entry',session.user.id,id]}),queryClient.invalidateQueries({queryKey:['history',session.user.id,id]})])}})
  if (detail.isPending) return <p>Loading entry…</p>
  if (detail.isError) return <div role="alert"><p>{detail.error.message}</p><Button onClick={() => void detail.refetch()}>Retry</Button></div>
  const entry=detail.data.entry
  if (editing) return <div className="journal-detail"><EntryForm session={session} entry={entry} accounts={accounts} onDone={() => setEditing(false)} onCancel={() => setEditing(false)}/></div>
  return <div className="journal-detail">{entry.status === 'draft' && <><div className="setting-actions"><Button variant="outline" onClick={() => setEditing(true)}>Edit draft</Button><Button disabled={post.isPending} onClick={() => post.mutate()}>Post draft</Button></div>{post.error && <p role="alert">{post.error.message}</p>}</>}<h2>{entry.payee || 'Entry'}</h2><p>{entry.date} · {entry.status} · {entry.kind}</p><p className="muted">{entry.id}</p><h3>Postings</h3><ul>{(entry.postings ?? []).map((posting,index) => <li key={index}><span>{names.get(posting.account) || posting.account}</span><span>{posting.amount.v.replace(/^-/, '−')} {posting.amount.c}</span>{posting.memo && <small>{posting.memo}</small>}</li>)}</ul><h3>Links</h3>{!entry.links?.length ? <p>None</p> : <ul>{entry.links.map((link,index) => <li key={index}>{link.rel}: {link.id}</li>)}</ul>}<h3>History</h3>{history.isPending ? <p>Loading history…</p> : history.isError ? <div role="alert"><p>{history.error.message}</p><Button onClick={() => void history.refetch()}>Retry history</Button></div> : !history.data.events?.length ? <p>No history.</p> : <ul>{history.data.events.map((event) => <li key={event.id}>{event.tool} · <time dateTime={event.at}>{new Date(event.at).toLocaleString()}</time></li>)}</ul>}{entry.status === 'posted' && (confirm ? <form onSubmit={(event) => {event.preventDefault();reverse.mutate()}}><label>Reason<Input required value={reason} onChange={(event) => setReason(event.target.value)}/></label><div className="setting-actions"><Button type="submit" variant="destructive" disabled={reverse.isPending}>Confirm reversal</Button><Button type="button" variant="outline" onClick={() => setConfirm(false)}>Cancel</Button></div>{reverse.error && <p role="alert">{reverse.error.message}</p>}</form> : <Button variant="outline" onClick={() => setConfirm(true)}>Reverse entry</Button>)}</div>
}
