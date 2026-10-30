import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { FormulaInput } from '../../components/formula/input'
import { catalogQuery } from '../../components/formula/catalog'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { sharedPost } from '../sharing/queries'
import { tool, type Session } from '../../lib/api'
import type { DashboardGetOutput, ExprEvalOutput } from '../../lib/tools'

type Spec=DashboardGetOutput['spec']
type Query=Spec['data'][string]
export function QueryEditor({session,spec,onChange,sharedId}:{session:Session;sharedId?:string;spec:Spec;onChange:(spec:Spec) => void}) {
 const names=Object.keys(spec.data ?? {}),[selected,setSelected]=useState(names[0] ?? ''),[newName,setNewName]=useState(''),[message,setMessage]=useState(''),[confirm,setConfirm]=useState(false),query=spec.data?.[selected]
 const catalog=useQuery(catalogQuery(session,sharedId))
 const preview=useMutation({mutationFn:() => {if(!query)throw Error('Choose a query.');const limit=query.limit && query.limit<5 ? query.limit : 5;const expr=`data[${JSON.stringify(selected)}]`,data={...spec.data,[selected]:{...query,limit}},params=spec.params ?? {};return sharedId ? sharedPost<ExprEvalOutput>(session,sharedId,'evaluate',{expr,data,params}) : tool(session,'expr_eval',{expr,vars:{data,params}})}})
 function change(value:Partial<Query>) {if(!query)return;preview.reset();onChange({...spec,data:{...spec.data,[selected]:{...query,...value}}})}
 function add() {const name=newName.trim();if(!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || names.includes(name)) {setMessage('Use a new query name beginning with a letter or underscore.');return}if(names.length>=50){setMessage('A dashboard can have at most 50 queries.');return}onChange({...spec,data:{...spec.data,[name]:{from:'journal'}}});setSelected(name);setNewName('');setMessage('');setConfirm(false);preview.reset()}
 function remove() {const data={...spec.data};delete data[selected];onChange({...spec,data});setSelected(Object.keys(data)[0] ?? '');setConfirm(false);preview.reset()}
 const common={session,sharedId,from:query?.from,derived:Object.keys(query?.derive ?? {}),data:names,computed:Object.keys(spec.computed ?? {}),params:Object.keys(spec.params ?? {})}
 return <section className="query-editor" onKeyDownCapture={(event) => {if((event.ctrlKey || event.metaKey) && !event.shiftKey && !event.altKey && event.key==='Enter' && query){event.preventDefault();event.stopPropagation();if(!preview.isPending)preview.mutate()}}}>
 <div className="query-create"><label>New query name<Input value={newName} onChange={event => setNewName(event.target.value)}/></label><Button type="button" size="sm" variant="outline" onClick={add}>Add query</Button></div>{message && <p role="alert">{message}</p>}
 {names.length===0 ? <p>No queries yet.</p> : <><label>Query<select aria-label="Query" value={selected} onChange={event => {setSelected(event.target.value);setConfirm(false);preview.reset()}}>{names.map(name => <option key={name} value={name}>{name}</option>)}</select></label>{query && <><label>Source<Input list="dashboard-sources" value={query.from} onChange={event => change({from:event.target.value})}/></label><datalist id="dashboard-sources">{(catalog.data?.sources ?? []).map(source => <option key={source.name} value={source.name}>{source.label}</option>)}{names.filter(name => name!==selected).map(name => <option key={name} value={`query:${name}`}/>)}</datalist>{catalog.isError && <p className="muted">Source suggestions are unavailable.</p>}
 {query.from==='range' && <div><span>Range length</span><FormulaInput {...common} label="Range length" role="value" value={query.n ?? ''} onChange={value => change({n:value})}/></div>}
 <label>Output shape<select aria-label="Output shape" value={query.as ?? ''} onChange={event => change({as:event.target.value})}><option value="">Rows</option><option value="map">Map</option></select></label>{query.as==='map' && <><label>Map key field<Input value={query.key ?? ''} onChange={event => change({key:event.target.value})}/></label><label>Map value field<Input value={query.value ?? ''} onChange={event => change({value:event.target.value})}/></label></>}
 <div><span>Where</span><FormulaInput {...common} label="Query where" role="where" value={query.where ?? ''} onChange={value => change({where:value})}/></div>
 <ExpressionMap {...common} title="Derived fields" role="derive" values={query.derive ?? {}} onChange={derive => change({derive})}/>
 <label>Group fields (comma-separated)<Input value={(query.group ?? []).join(',')} onChange={event => change({group:event.target.value.split(',')})} onBlur={event => change({group:event.target.value.split(',').map(x => x.trim()).filter(Boolean)})}/></label>
 <ExpressionMap {...common} title="Aggregates" role="agg" values={query.agg ?? {}} onChange={agg => change({agg})}/>
 <label>Sort fields (comma-separated)<Input value={(query.sort ?? []).join(',')} onChange={event => change({sort:event.target.value.split(',')})} onBlur={event => change({sort:event.target.value.split(',').map(x => x.trim()).filter(Boolean)})}/></label>
 <label>Limit<Input type="number" min={0} max={5000} value={query.limit ?? 0} onChange={event => change({limit:Number(event.target.value)})}/></label><label><input type="checkbox" checked={!!query.hide_zero} onChange={event => change({hide_zero:event.target.checked})}/> Hide zero rows</label>
 <div className="setting-actions"><Button type="button" variant="outline" size="sm" disabled={preview.isPending} onClick={() => preview.mutate()}>{preview.isPending ? 'Loading…' : 'Preview first 5 rows'}</Button>{confirm ? <><Button type="button" variant="destructive" size="sm" onClick={remove}>Confirm remove query</Button><Button type="button" variant="outline" size="sm" onClick={() => setConfirm(false)}>Cancel</Button></> : <Button type="button" variant="ghost" size="sm" onClick={() => setConfirm(true)}>Remove query</Button>}</div><p className="muted">Preview runs on demand and does not save the dashboard. Ctrl/⌘-Enter also runs it.</p>{preview.error && <p role="alert">{preview.error.message}</p>}{preview.data && <pre className="query-preview" aria-label="Query preview">{JSON.stringify(Array.isArray(preview.data.value) ? preview.data.value.slice(0,5) : preview.data.value,null,2)}</pre>}</>}</>}
 </section>
}
function ExpressionMap({title,values,onChange,role,...common}:{title:string;values:Record<string,string>;onChange:(value:Record<string,string>) => void;role:'derive'|'agg';session:Session;sharedId?:string;from?:string;derived:string[];data:string[];computed:string[];params:string[]}) {
 const [name,setName]=useState(''),[error,setError]=useState('')
 function add() {const key=name.trim();if(!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key) || key in values){setError('Use a new field name beginning with a letter or underscore.');return}onChange({...values,[key]:''});setName('');setError('')}
 return <fieldset className="query-map"><legend>{title}</legend>{Object.entries(values).map(([key,value]) => <div key={key}><span>{key}</span><FormulaInput {...common} label={`${title} ${key}`} role={role} value={value} onChange={next => onChange({...values,[key]:next})}/><Button type="button" size="xs" variant="ghost" onClick={() => {const next={...values};delete next[key];onChange(next)}}>Remove {key}</Button></div>)}<div className="query-create"><label>New {title.toLowerCase()} name<Input value={name} onChange={event => setName(event.target.value)}/></label><Button type="button" size="sm" variant="outline" onClick={add}>Add {title.toLowerCase()}</Button></div>{error && <p role="alert">{error}</p>}</fieldset>
}
