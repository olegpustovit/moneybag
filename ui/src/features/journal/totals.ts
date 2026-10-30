import { amountLabel } from '../../lib/format'

type Amount={v:string;c:string}

// Sum the displayed postings exactly, keeping commodities separate.
export function postingTotals(amounts:(Amount|undefined)[],minimumFractionDigits=0):string|undefined {
 const totals=new Map<string,{value:bigint;scale:number}>()
 for(const amount of amounts) {
  if(!amount)continue
  if(!amount.c || amount.v.replace(/^-/, '').length>80 || !/^-?(0|[1-9]\d*)(\.\d+)?$/.test(amount.v))return undefined
  const negative=amount.v.startsWith('-'),[whole,fraction='']=(negative ? amount.v.slice(1) : amount.v).split('.')
  const value=BigInt((negative ? '-' : '')+whole+fraction),old=totals.get(amount.c),scale=Math.max(old?.scale ?? 0,fraction.length)
  totals.set(amount.c,{value:(old?.value ?? 0n)*10n**BigInt(scale-(old?.scale ?? 0))+value*10n**BigInt(scale-fraction.length),scale})
 }
 if(!totals.size)return undefined
 return [...totals].sort(([a],[b]) => a.localeCompare(b)).map(([currency,{value,scale}]) => {
  const negative=value<0n,digits=(negative ? -value : value).toString().padStart(scale+1,'0'),whole=scale ? digits.slice(0,-scale) : digits,fraction=scale ? digits.slice(-scale).replace(/0+$/,'') : ''
  return amountLabel({v:`${negative ? '-' : ''}${whole}${fraction ? `.${fraction}` : ''}`,c:currency},minimumFractionDigits)
 }).join(' · ')
}
