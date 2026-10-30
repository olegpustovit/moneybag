import { amountParts } from '../lib/format'

export function Amount({value,commodity,minimumFractionDigits=0}:{value:string;commodity:string;minimumFractionDigits?:number}) {
 const {text,code}=amountParts({v:value,c:commodity},minimumFractionDigits)
 return <span className="data" title={`${value} ${commodity}`}>{text}{code && <> <span className="amount-code">{code}</span></>}</span>
}
