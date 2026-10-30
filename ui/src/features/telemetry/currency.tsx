import { useMemo, useSyncExternalStore } from 'react'
import { inCurrency, type Cube } from './cube'

// The viewer's display currency. Saved in this browser only; the data and the server stay in EUR.
export type Currency='EUR'|'USD'
const KEY='moneybag.currency',subs=new Set<() => void>()
const read=():Currency => {try {return localStorage.getItem(KEY)==='USD' ? 'USD' : 'EUR'} catch {return 'EUR'}}
let current=read()
export const setCurrency=(next:Currency) => {current=next;try {localStorage.setItem(KEY,next)} catch {/* The choice still holds for this visit. */}subs.forEach(f => f())}
const subscribe=(f:() => void) => {subs.add(f);return () => {subs.delete(f)}}
export const canSwitch=(cube:Cube|null):cube is Cube => !!cube && cube.base==='EUR' && !!cube.rates['EUR/USD']?.d.length
// The cube to draw: converted when the viewer chose USD and the cube has quotes. `switchable` says whether to show the control.
export function useCurrencyCube(cube:Cube|null):{cube:Cube|null;currency:Currency;switchable:boolean} {
 const chosen=useSyncExternalStore(subscribe,() => current,() => 'EUR' as Currency),switchable=canSwitch(cube)
 const shown=useMemo(() => cube && switchable && chosen==='USD' ? inCurrency(cube,'USD') : cube,[cube,chosen,switchable])
 return {cube:shown,currency:shown?.base==='USD' ? 'USD' : 'EUR',switchable}
}
export function CurrencySwitch({value}:{value:Currency}) {
 return <div className="tm-seg" role="group" aria-label="Currency">{(['EUR','USD'] as const).map(c => <button key={c} type="button" className="tm-btn" aria-pressed={value===c} onClick={() => setCurrency(c)}>{c}</button>)}</div>
}
