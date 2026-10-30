import { createContext } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRouter } from '@tanstack/react-router'
import { z } from 'zod'
import { toast } from 'sonner'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { tool, type Session } from '../../lib/api'

type Kind = 'dashboard' | 'database'
export const CreateWorkspaceContext=createContext<((kind:Kind)=>void)|null>(null)
const columnsSchema=z.array(z.object({id:z.string().regex(/^[A-Za-z_][A-Za-z0-9_]{0,99}$/),type:z.enum(['string','number','bool','date','formula']),formula:z.string().optional()}).strict()).min(1).max(50)
const defaultColumns='[{"id":"name","type":"string"},{"id":"value","type":"number"}]'

export function CreateWorkspaceItem({kind,session,close}:{kind:Kind;session:Session;close:()=>void}) {
 const client=useQueryClient(),router=useRouter()
 const create=useMutation({mutationFn:async (data:FormData) => {
  if(kind==='dashboard') {const id=String(data.get('id'));await tool(session,'dashboard_create',{id,title:String(data.get('title')),from_template:String(data.get('template'))});return {kind,id} as const}
  let parsed:unknown
  try {parsed=JSON.parse(String(data.get('columns')))} catch {throw new Error('Columns must be a valid JSON array.')}
  const columns=columnsSchema.safeParse(parsed)
  if(!columns.success)throw new Error('Enter 1–50 columns with valid IDs and types. Formula columns also need a formula.')
  const result=await tool(session,'db_create',{name:String(data.get('name')),columns:columns.data})
  return {kind,id:result.id} as const
 },onSuccess:async result => {
  if(result.kind==='database')for(const key of ['expression-catalog','model','dashboard','forecast','scenario-comparison'])void client.invalidateQueries({queryKey:[key,session.user.id]})
  await client.invalidateQueries({queryKey:[result.kind==='dashboard' ? 'dashboards' : 'tables',session.user.id]})
  close()
  toast.success(result.kind==='dashboard' ? 'Dashboard draft created' : 'Database created')
  if(result.kind==='dashboard')void router.navigate({to:'/dashboards/$id',params:{id:result.id},search:{version:'draft'}})
  else void router.navigate({to:'/databases/$id',params:{id:result.id}})
 }})
 function submit(event:React.FormEvent<HTMLFormElement>) {event.preventDefault();if(!create.isPending)create.mutate(new FormData(event.currentTarget))}
 return <Dialog open onOpenChange={open=>{if(!open && !create.isPending)close()}}><DialogContent className="account-dialog"><DialogHeader><DialogTitle>{kind==='dashboard' ? 'New dashboard' : 'New database'}</DialogTitle><DialogDescription>{kind==='dashboard' ? 'Create a draft from a template or start with an empty dashboard.' : 'Create a database with its initial columns.'}</DialogDescription></DialogHeader><form className="account-form" onSubmit={submit}>{kind==='dashboard' ? <><label>Name<Input name="title" required maxLength={200} placeholder="Monthly overview"/></label><label>ID<Input name="id" required maxLength={100} pattern="[A-Za-z0-9_-]+" placeholder="monthly-overview"/></label><label>Template<select name="template" defaultValue="budget-vs-actual"><option value="budget-vs-actual">Budget vs actual</option><option value="forecast">Forecast &amp; scenarios</option><option value="">Empty dashboard</option></select></label></> : <><label>Name<Input name="name" required maxLength={150}/></label><label>Columns (JSON)<textarea name="columns" className="workspace-columns" rows={5} required defaultValue={defaultColumns} spellCheck={false}/></label><p className="muted">Types: string, number, bool, date or formula. Formula columns also need a CEL formula.</p></>}<div className="setting-actions"><Button type="submit" disabled={create.isPending}>{create.isPending ? 'Creating…' : kind==='dashboard' ? 'Create draft' : 'Create database'}</Button><Button type="button" variant="outline" disabled={create.isPending} onClick={close}>Cancel</Button></div>{create.error && <p role="alert">{create.error.message}</p>}</form></DialogContent></Dialog>
}
