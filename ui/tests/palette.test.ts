import { strict as assert } from 'node:assert'
import { readFileSync } from 'node:fs'

// Reads the shipped CSS, so a token change cannot slip past this check. Non-text marks need 3:1 on every surface they sit on (WCAG 1.4.11).
const css=['../src/foundation.css','../src/index.css'].map(path => readFileSync(new URL(path,import.meta.url),'utf8')).join('\n')
const block=(selector:string) => [...css.matchAll(new RegExp(`(?:^|\\n)\\s*${selector.replace('.','\\.')}\\s*\\{([^}]*)\\}`,'g'))].flatMap(m => [...m[1].matchAll(/--([\w-]+):\s*(#[0-9a-fA-F]{3,6})/g)]).reduce<Record<string,string>>((all,m) => ({...all,[m[1]]:m[2]}),{})
const light=block(':root'),dark={...light,...block('.dark')}
const long=(hex:string) => hex.length===4 ? `#${hex[1]}${hex[1]}${hex[2]}${hex[2]}${hex[3]}${hex[3]}` : hex
const luminance=(short:string) => {const hex=long(short),[r,g,b]=[1,3,5].map(i => parseInt(hex.slice(i,i+2),16)/255).map(c => c<=.03928 ? c/12.92 : ((c+.055)/1.055)**2.4);return .2126*r+.7152*g+.0722*b}
const contrast=(a:string,b:string) => {const [x,y]=[luminance(a),luminance(b)].sort((p,q) => q-p);return (x+.05)/(y+.05)}
assert.equal(light.c1,'#a87700','the proposal moves light amber here because #c48a00 fails 3:1 on white')
assert.ok(light.hatch && dark.hatch,'the estimate hatch has a token in both themes')
assert.ok(light['series-other'] && dark['series-other'],'Other has a chart token in both themes')
const report:string[]=[]
for(const [name,theme] of [['light',light],['dark',dark]] as const)for(const token of ['c0','c1','c2','c3','c4','series-other','hatch'])for(const surface of ['bg','sunk']){
 const ratio=contrast(theme[token],theme[surface]);report.push(`${name} ${token} on ${surface} ${ratio.toFixed(2)}`)
 assert.ok(ratio>=3,`${name} --${token} ${theme[token]} on --${surface} ${theme[surface]} is ${ratio.toFixed(2)}:1`)
}
// Neighbours in a stack must stay apart too: no two series may read as one colour.
const lab=(short:string) => {const hex=long(short),[r,g,b]=[1,3,5].map(i => parseInt(hex.slice(i,i+2),16)/255).map(c => c<=.04045 ? c/12.92 : ((c+.055)/1.055)**2.4),f=(t:number) => t>.008856 ? Math.cbrt(t) : 7.787*t+16/116,x=f((.4124*r+.3576*g+.1805*b)/.95047),y=f(.2126*r+.7152*g+.0722*b),z=f((.0193*r+.1192*g+.9505*b)/1.08883);return [116*y-16,500*(x-y),200*(y-z)]}
const deltaE=(a:string,b:string) => {const p=lab(a),q=lab(b);return Math.hypot(p[0]-q[0],p[1]-q[1],p[2]-q[2])}
for(const theme of [light,dark]){const slots=['c0','c1','c2','c3','c4','series-other'];for(let i=0;i<slots.length;i++)for(let k=i+1;k<slots.length;k++)assert.ok(deltaE(theme[slots[i]],theme[slots[k]])>=20,`${slots[i]} and ${slots[k]} are too close`)}
console.log(`palette ok, worst ${report.map(r => [r,parseFloat(r.split(' ').at(-1)!)] as const).sort((a,b) => a[1]-b[1])[0][0]}`)
