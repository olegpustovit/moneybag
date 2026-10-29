const commodities:Record<string,string>={EUR:'€',USD:'$',UAH:'₴',XAU_g:'Gold (g)'}
const labels:Record<string,string>={avg6:'6-month average',eur:'EUR',window_label:'Window'}
const dayFormat=new Intl.DateTimeFormat('en-GB',{timeZone:'UTC',year:'numeric',month:'short',day:'numeric'})
const monthFormat=new Intl.DateTimeFormat('en-GB',{timeZone:'UTC',year:'numeric',month:'short'})

export const commodityLabel=(code:string) => commodities[code] ?? code

export function decimalLabel(value:string):string {
 const normalized=value.replace(/^−/, '-')
 if(!/^-?\d+(\.\d+)?$/.test(normalized))return value.replace(/^-/, '−')
 const negative=normalized.startsWith('-'),[whole,fraction]=normalized.replace(/^-/, '').split('.')
 return `${negative ? '−' : ''}${whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')}${fraction===undefined ? '' : `.${fraction}`}`
}

export const amountLabel=(amount:{v:string;c:string}) => `${decimalLabel(amount.v)} ${commodityLabel(amount.c)}`

export function dateLabel(value:string):string {
 if(!/^\d{4}-\d{2}(-\d{2})?$/.test(value))return value
 const day=value.length===7 ? `${value}-01` : value,parsed=new Date(`${day}T00:00:00Z`)
 if(!Number.isFinite(parsed.getTime()) || parsed.toISOString().slice(0,10)!==day)return value
 return (value.length===10 ? dayFormat : monthFormat).format(parsed)
}

export function accountLabel(id:string,name?:string):{name:string;trail:string} {
 if(name?.trim())return {name,trail:''}
 const parts=id.split(':'),title=(part:string) => part.replace(/[_-]+/g,' ').replace(/(^|\s)(\p{L})/gu,(_,space:string,letter:string) => space+letter.toLocaleUpperCase())
 return {name:title(parts.at(-1) || id),trail:parts.slice(1,-1).reverse().map(title).join(' / ')}
}

export function fieldLabel(key:string):string {
 return labels[key] ?? key.replaceAll('_',' ').replace(/^./,letter => letter.toLocaleUpperCase())
}
