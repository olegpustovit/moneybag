// Port of the proposal's synthetic ledger into the /api/cube wire shape. Every value is invented; no real data belongs here.
import { isoDay, MONTH_NAMES } from '../../src/features/telemetry/time-grammar'
import type { CubeAccount, CubeWire } from '../../src/features/telemetry/cube'

const DAY=864e5,utc=Date.UTC
function rng(seed:number){let a=seed;return () => {a|=0;a=a+0x6D2B79F5|0;let t=Math.imul(a^a>>>15,1|a);t=t+Math.imul(t^t>>>7,61|t)^t;return ((t^t>>>14)>>>0)/4294967296}}
const PAYEES:Record<string,string[]>={Groceries:['Konzum','Lidl','Spar','Green Market'],Restaurants:['Corner Bistro','Noodle Bar','Wolt delivery','Cafe Central'],Transport:['City Transit','Bolt','Uber','Fuel Stop'],Health:['City Pharmacy','Dental Studio','Gym Pass'],Shopping:['IKEA','Decathlon','Amazon','Zara','Apple']}
const TRIPS:[string,number,number,number,number][]=[['Lisbon',2023,4,12,8],['Vienna',2023,9,6,5],['Canada',2024,6,18,14],['Krakow',2024,10,2,5],['Tokyo',2025,3,10,12],['Athens',2025,8,20,7],['Lisbon',2026,4,1,9],['Toronto',2026,6,16,13]]
const ACCOUNTS:[string,string,string,string,boolean?][]=[['expenses:home:rent','Rent','Rent','expense'],['expenses:travel','Travel','Travel','expense'],['expenses:food:groceries','Groceries','Groceries','expense'],['expenses:food:restaurants','Restaurants','Restaurants','expense'],['expenses:shopping','Shopping','Shopping','expense'],['expenses:transport','Transport','Transport','expense'],['expenses:health','Health','Health','expense'],['expenses:subscriptions:streaming','Streaming','Subscriptions','expense'],['expenses:subscriptions:mobile','Mobile plan','Subscriptions','expense'],['expenses:subscriptions:cloud','Cloud and software','Subscriptions','expense'],['expenses:utilities:energy','Electric and gas','Utilities','expense'],['expenses:utilities:internet','Internet','Utilities','expense'],['expenses:estimated','Cash spending','Estimated','expense',true],['income:salary','Salary','Salary','income'],['income:side','Client work','Side income','income']]

// `density` repeats daily activity with extra payees, which is how the benchmark reaches 20k+ rows on the same shape.
export function syntheticCube({start='2023-01-01',end='2026-10-30',seed=20261030,density=1}:{start?:string;end?:string;seed?:number;density?:number}={}):CubeWire {
 const S=Date.parse(`${start}T00:00:00Z`),E=Date.parse(`${end}T00:00:00Z`),R=rng(seed),pick=<T,>(a:T[]) => a[Math.floor(R()*a.length)]
 const accounts:CubeAccount[]=ACCOUNTS.map(([id,label,category,cls,estimated]) => ({id,label,category,type:cls==='income' ? 'Income' : category==='Rent' ? 'Fixed' : 'Everyday',pot:'',source:estimated ? 'Worked out from cash withdrawals' : '',class:cls,estimated:!!estimated}))
 const aIdx=new Map(ACCOUNTS.map((x,i) => [x[0],i])),payees:string[]=[],pIdx=new Map<string,number>(),tagSets:string[][]=[[]],tIdx=new Map<string,number>([['',0]])
 const trips=TRIPS.map(t => ({name:t[0],from:utc(t[1],t[2],t[3]),to:utc(t[1],t[2],t[3]+t[4])})).filter(t => t.from<=E && t.to>=S)
 const rows=new Map<string,[number,number,number,number,number,number]>()
 const add=(t:number,account:string,payee:string,amount:number,trip='') => {
  if(!pIdx.has(payee)){pIdx.set(payee,payees.length);payees.push(payee)}
  if(!tIdx.has(trip)){tIdx.set(trip,tagSets.length);tagSets.push([trip])}
  const d=Math.round((t-S)/DAY),a=aIdx.get(account)!,p=pIdx.get(payee)!,tag=tIdx.get(trip)!,k=`${d}/${a}/${p}/${tag}`,cents=Math.round(amount*100)*(ACCOUNTS[a][3]==='income' ? -1 : 1),hit=rows.get(k)
  if(hit){hit[4]+=cents;hit[5]+=1}else rows.set(k,[d,a,p,tag,cents,1])
 }
 for(let t=S;t<=E;t+=DAY){
  const date=new Date(t),dom=date.getUTCDate(),m=date.getUTCMonth(),y=date.getUTCFullYear(),drift=1+(t-S)/Math.max(1,E-S)*.25,trip=trips.find(x => t>=x.from && t<=x.to)
  if(dom===1)add(t,'expenses:home:rent','Landlord',y>=2025 ? 1250 : 980)
  if(dom===5)add(t,'expenses:subscriptions:streaming','Streaming bundle',28+R()*4)
  if(dom===9)add(t,'expenses:subscriptions:mobile','Mobile plan',19)
  if(dom===14)add(t,'expenses:subscriptions:cloud','Cloud and software',64+R()*18)
  if(dom===20)add(t,'expenses:utilities:energy','Electric and gas',70+R()*90)
  if(dom===24)add(t,'expenses:utilities:internet','Internet provider',40)
  for(let k=0;k<density;k++){
   const tag=k ? ` ${k}` : ''
   if(!trip){
    if(R()<.38)add(t,'expenses:food:groceries',pick(PAYEES.Groceries)+tag,(14+R()*72)*drift)
    if(R()<.3)add(t,'expenses:food:restaurants',pick(PAYEES.Restaurants)+tag,(8+R()*58)*drift)
    if(R()<.2)add(t,'expenses:transport',pick(PAYEES.Transport)+tag,3+R()*32)
    if(R()<.05)add(t,'expenses:health',pick(PAYEES.Health)+tag,15+R()*120)
    if(R()<.07)add(t,'expenses:shopping',pick(PAYEES.Shopping)+tag,18+R()*240)
   } else {
    const label=`trip:${trip.name}`
    if(t===trip.from && !k)add(t,'expenses:travel','Airline tickets',340+R()*900,label)
    if(R()<.9)add(t,'expenses:travel',pick(['Hotel','Airbnb','Local tours','Train'])+tag,60+R()*320,label)
    if(R()<.7)add(t,'expenses:food:restaurants',pick(['Harbor Grill','Old Town Cafe','Street food'])+tag,18+R()*90,label)
   }
  }
  if(R()<.003)add(t,'expenses:shopping','Laptop repair',500+R()*700)
  if(dom===28 && t<utc(2025,9,1))add(t,'expenses:estimated',`Estimated cash spending, ${MONTH_NAMES[m]} ${y}`,500+R()*1500)
  if(date.getUTCDay()===5 && Math.floor((t-S)/DAY/7)%2===0)add(t,'income:salary','Employer',3650+(y-2023)*180)
  if(dom===16 && y>=2024)add(t,'income:side','Client',420+R()*380)
 }
 const sorted=[...rows.values()].sort((a,b) => a[0]-b[0]),flows={d:sorted.map(r => r[0]),a:sorted.map(r => r[1]),p:sorted.map(r => r[2]),t:sorted.map(r => r[3]),v:sorted.map(r => r[4]),n:sorted.map(r => r[5])}
 // Net worth: cash moved by the flows plus a walking gold holding, in cents.
 const net:number[]=[],assets:number[]=[],liabilities:number[]=[],days:number[]=[],gold:{d:number[];v:number[]}={d:[],v:[]},eurusd:{d:number[];v:number[]}={d:[],v:[]}
 let cash=3800000,price=78,fx=1.08,ri=0
 for(let d=0;d*DAY+S<=E;d++){
  price=Math.max(50,price*(1+(R()-.482)*.012));fx=Math.max(.95,Math.min(1.25,fx*(1+(R()-.5)*.006)))
  while(ri<sorted.length && sorted[ri][0]===d)cash-=sorted[ri++][4]
  const value=Math.round(cash+price*220*100);days.push(d);net.push(value);assets.push(value);liabilities.push(0)
  gold.d.push(d);gold.v.push(Math.round(price*100)/100);eurusd.d.push(d);eurusd.v.push(Math.round(fx*1e4)/1e4)
 }
 return {revision:1,as_of:end,base:'EUR',start,end,accounts,payees,tag_sets:tagSets,flows,
  balances:{d:days,net,assets,liabilities,estimated_until:'2025-09-30'},rates:{'EUR/USD':eurusd,'XAU/EUR':gold},metrics:[],
  annotations:trips.map((t,i) => ({id:`trip-${i}`,label:t.name,from:isoDay(t.from),to:isoDay(t.to),kind:'trip'})),labels:{}}
}
