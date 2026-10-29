import { useState, type FormEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { tool, type Session } from '../../lib/api'
import type { EntryGetOutput, EntryPostInput } from '../../lib/tools'

type Posting = NonNullable<EntryPostInput['postings']>[number]
const blank = (): Posting => ({account:'', amount:{v:'',c:''}})
function balances(postings: Posting[]): string | null {
  const sums = new Map<string, { integer:bigint; scale:number }>()
  for (const { amount } of postings) {
    if (!amount.c || amount.v.length > 80 || !/^-?\d+(\.\d+)?$/.test(amount.v)) return null
    const [whole, fraction = ''] = amount.v.split('.')
    const number = BigInt(whole + fraction)
    const old = sums.get(amount.c) ?? {integer:0n,scale:0}
    const scale = Math.max(old.scale,fraction.length)
    sums.set(amount.c,{integer:old.integer*10n**BigInt(scale-old.scale)+number*10n**BigInt(scale-fraction.length),scale})
  }
  return [...sums].map(([commodity,{integer,scale}]) => {
    const negative=integer<0n; const digits=(negative ? -integer : integer).toString().padStart(scale+1,'0')
    return `${negative ? '−' : ''}${scale ? `${digits.slice(0,-scale)}.${digits.slice(-scale)}` : digits} ${commodity}`
  }).join(' · ')
}
export function EntryForm({session, entry, accounts, onDone, onCancel}: {session:Session; entry?:EntryGetOutput['entry']; accounts:{id:string;name:string}[]; onDone:() => void; onCancel:() => void}) {
  const queryClient=useQueryClient()
  const [postings,setPostings]=useState<Posting[]>(() => entry?.postings?.map((p) => ({account:p.account,amount:{v:p.amount.v,c:p.amount.c},memo:p.memo,cost:p.cost ? {v:p.cost.v,c:p.cost.c} : undefined,price:p.price ? {v:p.price.v,c:p.price.c} : undefined})) ?? [blank(),blank()])
  const result=balances(postings)
  const save=useMutation({mutationFn:(input:EntryPostInput) => tool(session,'entry_post',input),onSuccess:async () => {await Promise.all([queryClient.invalidateQueries({queryKey:['journal',session.user.id]}),queryClient.invalidateQueries({queryKey:['balances',session.user.id]}),queryClient.invalidateQueries({queryKey:['dashboard',session.user.id]}),queryClient.invalidateQueries({queryKey:['entry',session.user.id]}),queryClient.invalidateQueries({queryKey:['history',session.user.id]})]);onDone()}})
  function submit(event:FormEvent<HTMLFormElement>) {
    event.preventDefault(); const fields=new FormData(event.currentTarget)
    save.mutate({id:entry?.id,date:String(fields.get('date')),payee:String(fields.get('payee')),tags:String(fields.get('tags') || '').split(',').map((tag)=>tag.trim()).filter(Boolean),postings,commit:!entry && fields.has('commit')})
  }
  function update(index:number, change:Partial<Posting>) {setPostings((old) => old.map((posting,i) => i===index ? {...posting,...change} : posting))}
  return <form className="entry-form" onSubmit={submit}><label>Date<Input type="date" name="date" required defaultValue={entry?.date}/></label><label>Payee<Input name="payee" maxLength={300} defaultValue={entry?.payee}/></label><label>Tags, separated by commas<Input name="tags" defaultValue={entry?.tags?.join(', ')}/></label><datalist id="entry-accounts">{accounts.map((account) => <option key={account.id} value={account.id}>{account.name}</option>)}</datalist><h3>Postings</h3>{postings.map((posting,index) => <fieldset key={index}><legend>Posting {index+1}</legend><label>Account<Input list="entry-accounts" required value={posting.account} onChange={(event) => update(index,{account:event.target.value})}/></label><label>Amount<Input inputMode="decimal" required pattern="-?[0-9]+([.][0-9]+)?" value={posting.amount.v} onChange={(event) => update(index,{amount:{...posting.amount,v:event.target.value}})}/></label><label>Commodity<Input required maxLength={20} value={posting.amount.c} onChange={(event) => update(index,{amount:{...posting.amount,c:event.target.value}})}/></label><label>Memo<Input maxLength={1000} value={posting.memo ?? ''} onChange={(event) => update(index,{memo:event.target.value})}/></label>{postings.length>2 && <Button type="button" variant="ghost" size="sm" onClick={() => setPostings((old) => old.filter((_,i) => i!==index))}>Remove posting {index+1}</Button>}</fieldset>)}<Button type="button" variant="outline" size="sm" disabled={postings.length>=100} onClick={() => setPostings((old) => [...old,blank()])}>Add posting</Button><p aria-live="polite">{result === null ? 'Enter decimal amounts and commodities to check the balance.' : `Balance: ${result}`}</p>{!entry && <label className="journal-check"><input type="checkbox" name="commit"/> Post now</label>}<div className="setting-actions"><Button type="submit" disabled={save.isPending}>Save {entry ? 'draft' : 'entry'}</Button><Button type="button" variant="outline" onClick={onCancel}>Cancel</Button></div>{save.error && <p role="alert">{save.error.message}</p>}</form>
}
