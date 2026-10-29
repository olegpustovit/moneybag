import { lazy, Suspense } from 'react'
import type { Session } from '../../lib/api'

export interface FormulaProps {session:Session;label:string;value:string;onChange:(value:string) => void;role:'where'|'derive'|'agg'|'value';from?:string;sharedId?:string;derived?:string[];data?:string[];computed?:string[];params?:string[];readOnly?:boolean}
const Editor=lazy(() => import('./editor'))
export function FormulaInput(props:FormulaProps) {return <Suspense fallback={<div aria-busy="true">Loading formula editor…</div>}><Editor {...props}/></Suspense>}
export function JSONInput({label,value,onChange}:{label:string;value:string;onChange:(value:string) => void}) {return <Suspense fallback={<div aria-busy="true">Loading JSON editor…</div>}><Editor label={label} value={value} onChange={onChange} json/></Suspense>}
