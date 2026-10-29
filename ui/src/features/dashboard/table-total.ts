// Totals use the decimal representations delivered to the browser, not binary floating-point addition.
export function tableTotal(values:unknown[],format:string):string|undefined {
 const numbers=values.filter(value => value!==null && value!==undefined)
 if(!numbers.length || numbers.some(value => typeof value!=='number' || !Number.isFinite(value) || Math.abs(value)>Number.MAX_SAFE_INTEGER))return undefined
 const parts=(numbers as number[]).map(value => {
  const [mantissa,exponent='0']=String(value).split('e'),negative=mantissa.startsWith('-'),unsigned=negative ? mantissa.slice(1) : mantissa
  const [whole,fraction='']=unsigned.split('.')
  return {integer:BigInt((negative ? '-' : '')+whole+fraction),scale:fraction.length-Number(exponent)}
 })
 let scale=parts.reduce((maximum,part) => Math.max(maximum,part.scale),0)
 let total=parts.reduce((sum,part) => sum+part.integer*10n**BigInt(scale-part.scale),0n)
 const digits=format==='usd0' || format==='eur0' || format==='int' || format==='pct0' ? 0 : format==='usd' || format==='eur' ? 2 : format==='pct' || format==='mo' ? 1 : undefined
 if(digits!==undefined && scale>digits) {const divisor=10n**BigInt(scale-digits),negative=total<0n,absolute=negative ? -total : total;total=(absolute/divisor+(absolute%divisor*2n>=divisor ? 1n : 0n))*(negative ? -1n : 1n);scale=digits}
 if(digits!==undefined && scale<digits){total*=10n**BigInt(digits-scale);scale=digits}
 const negative=total<0n,absolute=(negative ? -total : total).toString().padStart(scale+1,'0')
 let whole=scale ? absolute.slice(0,-scale) : absolute, fraction=scale ? absolute.slice(-scale) : ''
 if(digits===undefined)fraction=fraction.replace(/0+$/,'')
 whole=whole.replace(/\B(?=(\d{3})+(?!\d))/g,',')
 const prefix=format.startsWith('usd') || format==='USD' ? '$' : format.startsWith('eur') || format==='EUR' ? '€' : ''
 return `${prefix}${negative ? '−' : ''}${whole}${fraction ? `.${fraction}` : ''}${format.startsWith('pct') ? '%' : format==='mo' ? ' mo' : ''}`
}
