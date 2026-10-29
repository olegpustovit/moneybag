import { FormulaInput } from '../../components/formula/input'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import type { Session } from '../../lib/api'
import type { DashboardGetOutput } from '../../lib/tools'

type Spec=DashboardGetOutput['spec']
type Component=NonNullable<Spec['components']>[number]
type Column=NonNullable<Component['columns']>[number]
export function ColumnsEditor({session,spec,component,onChange,sharedId}:{session:Session;sharedId?:string;spec:Spec;component:Component;onChange:(columns:Column[]) => void}) {
 const columns=component.columns ?? [],source=spec.data?.[component.rows ?? ''],common={session,sharedId,from:source?.from,derived:Object.keys(source?.derive ?? {}),data:Object.keys(spec.data ?? {}),computed:Object.keys(spec.computed ?? {}),params:Object.keys(spec.params ?? {})}
 function change(index:number,updates:Partial<Column>) {onChange(columns.map((column,i) => i===index ? {...column,...updates} : column))}
 return <fieldset className="query-map"><legend>Table columns</legend>{columns.map((column,index) => <fieldset className="query-map" key={index}><legend>Column {index+1}</legend><label>Label<Input aria-label={`Column ${index+1} label`} value={column.label} onChange={event => change(index,{label:event.target.value})}/></label><div><span>Value</span><FormulaInput {...common} label={`Column ${index+1} value`} role="value" value={column.value} onChange={value => change(index,{value})}/></div><label>Format<Input aria-label={`Column ${index+1} format`} value={column.format ?? ''} onChange={event => change(index,{format:event.target.value})}/></label><div><span>Tone</span><FormulaInput {...common} label={`Column ${index+1} tone`} role="value" value={column.tone ?? ''} onChange={tone => change(index,{tone})}/></div><Button type="button" size="xs" variant="ghost" onClick={() => onChange(columns.filter((_,i) => i!==index))}>Remove column {index+1}</Button></fieldset>)}<Button type="button" size="sm" variant="outline" disabled={columns.length>=30} onClick={() => onChange([...columns,{label:'',value:''}])}>Add table column</Button></fieldset>
}
