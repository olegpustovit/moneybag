import { amountLabel } from '../lib/format'

export function Amount({value,commodity}:{value:string;commodity:string}) {
 return <span className="data" title={`${value} ${commodity}`}>{amountLabel({v:value,c:commodity})}</span>
}
