export function compareDecimals(a:string,b:string):number {
 const parse=(value:string) => {
  const [mantissa,exponent='0']=value.toLowerCase().split('e'),negative=mantissa.startsWith('-'),unsigned=negative ? mantissa.slice(1) : mantissa
  const [whole,fraction='']=unsigned.split('.')
  return {value:BigInt((negative ? '-' : '')+whole+fraction),scale:fraction.length-Number(exponent)}
 }
 const x=parse(a),y=parse(b),scale=Math.max(x.scale,y.scale),left=x.value*10n**BigInt(scale-x.scale),right=y.value*10n**BigInt(scale-y.scale)
 return left<right ? -1 : left>right ? 1 : 0
}
