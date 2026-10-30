import { createContext, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { FormulaInput } from '../../components/formula/input'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { tool, type Session } from '../../lib/api'

type Kind = 'dashboard' | 'database'
export const CreateWorkspaceContext=createContext<((kind:Kind)=>void)|null>(null)
type Column = { id:string; type:'string'|'number'|'bool'|'date'|'formula'; formula:string }

export function CreateWorkspaceItem({kind,session,close}:{kind:Kind;session:Session;close:()=>void}) {
 const client=useQueryClient(),router=useRouter()
 const [columns,setColumns]=useState<Column[]>([{id:'name',type:'string',formula:''},{id:'value',type:'number',formula:''}])
 const updateColumn=(index:number,change:Partial<Column>) => setColumns(old => old.map((column,i) => i===index ? {...column,...change} : column))
 const create=useMutation({mutationFn:async (data:FormData) => {
  if(kind==='dashboard') {const id=String(data.get('id'));await tool(session,'dashboard_create',{id,title:String(data.get('title')),from_template:String(data.get('template'))});return {kind,id} as const}
  if(new Set(columns.map(column => column.id)).size!==columns.length)throw new Error('Column IDs must be unique.')
  if(columns.some(column => column.type==='formula' && !column.formula.trim()))throw new Error('Formula columns need an expression.')
  const result=await tool(session,'db_create',{name:String(data.get('name')),columns:columns.map(({id,type,formula}) => ({id,type,...(type==='formula' ? {formula} : {})}))})
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
 return <Dialog open onOpenChange={open=>{if(!open && !create.isPending)close()}}><DialogContent className="account-dialog"><DialogHeader><DialogTitle>{kind==='dashboard' ? 'New dashboard' : 'New database'}</DialogTitle><DialogDescription>{kind==='dashboard' ? 'Create a draft from a template or start with an empty dashboard.' : 'Create a database with its initial columns.'}</DialogDescription></DialogHeader><form className="account-form" onSubmit={submit}>{kind==='dashboard' ? <><label>Name<Input name="title" required maxLength={200} placeholder="Monthly overview"/></label><label>ID<Input name="id" required maxLength={100} pattern="[A-Za-z0-9_-]+" placeholder="monthly-overview"/></label><label>Template<select name="template" defaultValue="budget-vs-actual"><option value="budget-vs-actual">Budget vs actual</option><option value="forecast">Forecast &amp; scenarios</option><option value="">Empty dashboard</option></select></label></> : <><label>Name<Input name="name" required maxLength={150}/></label><div className="workspace-column-list"><span>Columns</span>{columns.map((column,index) => <fieldset className="workspace-column" key={index}><legend>Column {index+1}</legend><label>ID<Input aria-label={`Column ${index+1} ID`} value={column.id} required pattern="[A-Za-z_][A-Za-z0-9_]{0,99}" onChange={event => updateColumn(index,{id:event.target.value})}/></label><label>Type<select aria-label={`Column ${index+1} type`} value={column.type} onChange={event => updateColumn(index,{type:event.target.value as Column['type']})}>{['string','number','bool','date','formula'].map(type => <option key={type} value={type}>{type}</option>)}</select></label>{column.type==='formula' && <div className="workspace-column-formula"><span>Formula</span><FormulaInput session={session} label={`Column ${index+1} formula`} role="value" derived={columns.filter((_,i) => i!==index).map(item => item.id).filter(Boolean)} value={column.formula} onChange={formula => updateColumn(index,{formula})}/></div>}<Button type="button" variant="ghost" size="sm" disabled={columns.length===1} onClick={() => setColumns(old => old.filter((_,i) => i!==index))}>Remove column {index+1}</Button></fieldset>)}<Button type="button" variant="outline" size="sm" disabled={columns.length>=50} onClick={() => setColumns(old => [...old,{id:'',type:'string',formula:''}])}>Add column</Button></div></>}<div className="setting-actions"><Button type="submit" disabled={create.isPending}>{create.isPending ? 'Creating…' : kind==='dashboard' ? 'Create draft' : 'Create database'}</Button><Button type="button" variant="outline" disabled={create.isPending} onClick={close}>Cancel</Button></div>{create.error && <p role="alert">{create.error.message}</p>}</form></DialogContent></Dialog>
}
