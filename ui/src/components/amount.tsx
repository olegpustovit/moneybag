import { amountParts } from '../lib/format'

export function Amount({value,commodity}:{value:string;commodity:string}) {
 const {text,code}=amountParts({v:value,c:commodity})
 return <span className="data" title={`${value} ${commodity}`}>{text}{code && <> <span className="amount-code">{code}</span></>}</span>
}
