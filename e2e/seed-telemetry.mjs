// Seeds a synthetic ledger through MCP, the way an agent would. Every figure is invented; the generator is the prototype's.
// Usage: node seed-telemetry.mjs [http://127.0.0.1:16602]. Refuses anything but a loopback QA server.
import assert from 'node:assert/strict'

const base=process.argv[2] ?? process.env.MONEYBAG_E2E_URL ?? 'http://127.0.0.1:16602',TOKEN=process.env.MONEYBAG_SETUP_TOKEN ?? 'synthetic-preview-token'
assert.match(base,/^http:\/\/127\.0\.0\.1:\d+$/,'Seed only a dedicated local QA server with synthetic data.')
export const EMAIL='demo@example.test',PASSWORD='synthetic-preview-password'
const DAY=864e5,utc=Date.UTC,iso=t => new Date(t).toISOString().slice(0,10)
let cookie='',csrf='',id=0
async function form(path,fields){
 const response=await fetch(base+path,{method:'POST',redirect:'manual',headers:{'Content-Type':'application/x-www-form-urlencoded',Origin:base},body:new URLSearchParams(fields)})
 const set=response.headers.getSetCookie?.() ?? [];for(const c of set)if(c.startsWith('moneybag_session='))cookie=c.split(';')[0]
 return response
}
async function signIn(){
 let r=await form('/setup',{token:TOKEN,name:'Demo',email:EMAIL,password:PASSWORD})
 if(!cookie){r=await form('/login',{email:EMAIL,password:PASSWORD});assert.ok(cookie,`login failed: ${r.status}`)}
 const session=await (await fetch(base+'/api/session',{headers:{cookie}})).json();csrf=session.csrf;assert.ok(csrf,'no session')
}
async function call(name,args={},attempt=0){
 const response=await fetch(base+'/api/mcp',{method:'POST',headers:{cookie,Origin:base,'X-CSRF-Token':csrf,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2026-07-28','Mcp-Method':'tools/call','Mcp-Name':name},body:JSON.stringify({jsonrpc:'2.0',id:++id,method:'tools/call',params:{name,arguments:args,_meta:{'io.modelcontextprotocol/protocolVersion':'2026-07-28','io.modelcontextprotocol/clientInfo':{name:'seed',version:'1'},'io.modelcontextprotocol/clientCapabilities':{}}}})})
 if(response.status===429 && attempt<8){await new Promise(r => setTimeout(r,500*(attempt+1)));return call(name,args,attempt+1)}
 const text=await response.text();assert.equal(response.status,200,`${name}: ${text.slice(0,400)}`)
 const envelope=JSON.parse(text);assert.ok(!envelope.error,`${name}: ${JSON.stringify(envelope.error)}`)
 if(envelope.result.isError)throw new Error(`${name}: ${envelope.result.content?.map(c => c.text).join(' ').slice(0,800)}`)
 return envelope.result.structuredContent ?? JSON.parse(envelope.result.content?.[0]?.text ?? '{}')
}
async function pool(items,size,work){let i=0;await Promise.all(Array.from({length:size},async () => {while(i<items.length)await work(items[i++])}))}
function rng(seed){let a=seed;return () => {a|=0;a=a+0x6D2B79F5|0;let t=Math.imul(a^a>>>15,1|a);t=t+Math.imul(t^t>>>7,61|t)^t;return ((t^t>>>14)>>>0)/4294967296}}
const R=rng(20261030),pick=a => a[Math.floor(R()*a.length)],money=n => n.toFixed(2)

const ACCOUNTS=[
 ['assets:bank:everyday','Everyday card','asset','bank','everyday_money','Current','EUR',{note:'Where most spending starts'}],
 ['assets:bank:online','Online account','asset','bank','everyday_money','Current','EUR',{}],
 ['assets:bank:savings','Rainy day savings','asset','bank','long_term','Savings','EUR',{}],
 ['assets:cash:home','Home cash','asset','cash','cash_home','Cash','EUR',{note:'The envelope in the kitchen'}],
 ['assets:metal:gold','Gold','asset','metal_holding','long_term','Metal','XAU_g',{}],
 ['assets:bank:old_card','Old prepaid card','asset','bank','everyday_money','Current','EUR',{}],
 ['assets:bank:old_trip','Travel wallet','asset','bank','everyday_money','Current','EUR',{}],
 ['liabilities:card:travel','Travel credit card','liability','liability','','Debt','EUR',{}],
 ['equity:opening','Opening balances','equity','equity','','Equity','EUR',{commodities:['EUR','XAU_g']}],
 ['income:salary','Salary','income','income','','Income','EUR',{groups:{category:'Salary',type:'Work'}}],
 ['income:side','Client work','income','income','','Income','EUR',{groups:{category:'Side income',type:'Work'}}],
 ...[['home:rent','Rent','Rent','Fixed'],['travel','Travel','Travel','Trips'],['food:groceries','Groceries','Groceries','Everyday'],['food:restaurants','Restaurants','Restaurants','Everyday'],['shopping','Shopping','Shopping','Everyday'],['transport','Transport','Transport','Everyday'],['health','Health','Health','Everyday'],['subscriptions:streaming','Streaming','Subscriptions','Fixed'],['subscriptions:mobile','Mobile plan','Subscriptions','Fixed'],['subscriptions:cloud','Cloud and software','Subscriptions','Fixed'],['utilities:energy','Electric and gas','Utilities','Fixed'],['utilities:internet','Internet','Utilities','Fixed']].map(([k,name,category,type]) => [`expenses:${k}`,name,'expense','expense','','Expense','EUR',{groups:{category,type}}]),
 ['expenses:estimated:cash','Cash spending','expense','expense','','Expense','EUR',{estimated:true,groups:{category:'Estimated',type:'Everyday'},note:'Worked out from cash withdrawals'}],
]
const PAYEES={groceries:['KONZUM ZAGREB 0042','LIDL 1183','SPAR 22','Green Market'],restaurants:['CORNER BISTRO','NOODLE BAR','WOLT *DELIVERY','Cafe Central'],transport:['CITY TRANSIT','BOLT.EU','UBER *TRIP','Fuel Stop'],health:['City Pharmacy','Dental Studio','Gym Pass'],shopping:['IKEA','Decathlon','AMZN MKTP','Zara','Apple']}
const TRIPS=[['Lisbon',2024,4,12,8],['Vienna',2024,9,6,5],['Canada',2025,6,18,14],['Krakow',2025,10,2,5],['Tokyo',2026,3,10,12],['Athens',2026,8,1,7]].map(t => ({name:t[0],from:utc(t[1],t[2],t[3]),to:utc(t[1],t[2],t[3]+t[4])}))
function ledger(start,end){
 const out=[],post=(t,payee,account,from,amount,tags) => out.push({date:iso(t),payee,tags,commit:true,postings:[{account,amount:{c:'EUR',v:money(amount)}},{account:from,amount:{c:'EUR',v:money(-amount)}}]})
 for(let t=start;t<=end;t+=DAY){
  const d=new Date(t),dom=d.getUTCDate(),m=d.getUTCMonth(),y=d.getUTCFullYear(),drift=1+(t-start)/(end-start)*.25,trip=TRIPS.find(x => t>=x.from && t<=x.to),card='assets:bank:everyday',pay=() => R()<.7 ? card : 'assets:bank:online'
  if(dom===1)post(t,'LANDLORD RENT','expenses:home:rent','assets:bank:online',y>=2025 ? 1250 : 980)
  if(dom===5)post(t,'STREAMBOX*BUNDLE','expenses:subscriptions:streaming',card,28+R()*4)
  if(dom===9)post(t,'MOBILE PLAN','expenses:subscriptions:mobile',card,19)
  if(dom===14)post(t,'CLOUD AND SOFTWARE','expenses:subscriptions:cloud',card,64+R()*18)
  if(dom===20)post(t,'ENERGY CO','expenses:utilities:energy','assets:bank:online',70+R()*90)
  if(dom===24)post(t,'INTERNET PROVIDER','expenses:utilities:internet','assets:bank:online',40)
  if(!trip){
   if(R()<.2)post(t,pick(PAYEES.groceries),'expenses:food:groceries',pay(),(28+R()*120)*drift)
   if(R()<.14)post(t,pick(PAYEES.restaurants),'expenses:food:restaurants',pay(),(14+R()*90)*drift)
   if(R()<.08)post(t,pick(PAYEES.transport),'expenses:transport',pay(),3+R()*32)
   if(R()<.02)post(t,pick(PAYEES.health),'expenses:health',pay(),15+R()*120)
   if(R()<.04)post(t,pick(PAYEES.shopping),'expenses:shopping',pay(),18+R()*240)
  } else {
   const tags=[`trip:${trip.name}`]
   if(t===trip.from)post(t,'Airline tickets','expenses:travel','liabilities:card:travel',340+R()*900,tags)
   if(R()<.5)post(t,pick(['Hotel','Airbnb','Local tours','Train']),'expenses:travel','liabilities:card:travel',60+R()*320,tags)
   if(R()<.3)post(t,pick(['Harbor Grill','Old Town Cafe','Street food']),'expenses:food:restaurants','liabilities:card:travel',18+R()*90,tags)
  }
  if(R()<.003)post(t,'Laptop repair','expenses:shopping',card,500+R()*700)
  if(dom===28 && t<utc(2025,9,1))post(t,'Cash spending, worked out','expenses:estimated:cash','assets:cash:home',200+R()*500)
  if(dom===2 && m%3===0)post(t,'Card repayment','liabilities:card:travel','assets:bank:online',900+R()*1400)
  if(d.getUTCDay()===5 && Math.floor((t-start)/DAY/7)%2===0)out.push({date:iso(t),payee:'EMPLOYER PAYROLL',commit:true,postings:[{account:'assets:bank:online',amount:{c:'EUR',v:money(1850+(y-2023)*90)}},{account:'income:salary',amount:{c:'EUR',v:money(-(1850+(y-2023)*90))}}]})
  if(dom===16 && y>=2024){const v=420+R()*380;out.push({date:iso(t),payee:'CLIENT INVOICE',commit:true,postings:[{account:'assets:bank:online',amount:{c:'EUR',v:money(v)}},{account:'income:side',amount:{c:'EUR',v:money(-v)}}]})}
 }
 return out
}
const metric=(id,label,unit,kind,additivity,estimates,accounts,extra={}) => ({id,label,unit,kind,additivity,estimates,accounts,...extra})
const METRICS=[
 metric('spending','Spending','eur','flow','sum','split',['expenses:'],{description:'Everything that left, estimates marked.',dimensions:['category','payee','account','trip']}),
 metric('everyday','Everyday spending','eur','flow','sum','exclude',['expenses:food:','expenses:transport','expenses:shopping'],{dimensions:['category','payee']}),
 metric('fixed','Fixed costs','eur','flow','sum','exclude',['expenses:home:','expenses:utilities:','expenses:subscriptions:'],{dimensions:['category','payee']}),
 metric('income','Money in','eur','flow','sum','include',['income:'],{sign:-1,dimensions:['category','payee']}),
 metric('net_worth','Net worth','eur','balance','last','include',['assets:','liabilities:']),
 metric('net_worth_ex_gold','Net worth without gold','eur','balance','last','include',['assets:','liabilities:'],{where:"row.commodity != 'XAU_g'"}),
 metric('burn','Monthly burn','eur','rate','avg','include',['expenses:'],{description:'Average spending per calendar month.'}),
 metric('entries','Entries','int','flow','sum','include',['expenses:']),
]
const ANSWER=(metricId) => ({metric:metricId,facts:['period_change','top_contributors','estimate_share']})
const scope=(d='12mo',compare='previous_period') => ({time:{default:d,presets:['30d','90d','6mo','12mo','ytd','last year','all']},compare})
const stat=(id,m,title,extra={}) => ({id,type:'stat',metric:m,title,...extra})
const page=(id,title,...rows) => ({id,title,layout:rows.map(cols => ({cols:cols.map(([component,span]) => ({component,span}))}))})
const DASHBOARDS=[
 {id:'now',title:'Now',home:true,spec:{question:'Where do I stand, and what changed since I last looked?',scope:scope('90d'),explore:{dimensions:['category','payee','trip']},answer:ANSWER('spending'),
  components:[stat('nw','net_worth','Net worth',{compare:'previous_period'}),stat('in','income','Money in'),stat('out','spending','Money out'),stat('burn','burn','Monthly burn'),
   {id:'nw_trend',type:'time_chart',metric:'net_worth',title:'Net worth',mark:'line',annotate:'trips'},
   {id:'top',type:'ranked_bars',metric:'spending',title:'Where it went',split:'category'},
   {id:'latest',type:'entries',metric:'spending',title:'Latest spending'}],
  pages:[page('main','Now',[['nw',3],['in',3],['out',3],['burn',3]],[['nw_trend',7],['top',5]],[['latest',12]])]}},
 {id:'spending',title:'Spending',spec:{question:'Where did the money go, and why did it move?',scope:scope('12mo'),explore:{dimensions:['category','payee','account','trip']},answer:ANSWER('spending'),
  components:[stat('total','spending','Spending'),stat('every','everyday','Everyday spending'),stat('fix','fixed','Fixed costs'),stat('count','entries','Entries',{compare:'none'}),
   {id:'by_cat',type:'time_chart',metric:'spending',title:'Spending by category',mark:'stack',split:'category',estimates:'split'},
   {id:'ranked',type:'ranked_bars',metric:'spending',title:'Top categories',split:'category'},
   {id:'payees',type:'ranked_bars',metric:'spending',title:'Top payees',split:'payee'},
   {id:'trips',type:'ranked_bars',metric:'spending',title:'Trips',split:'trip'},
   {id:'lifetime',type:'stat',metric:'spending',title:'Spending since records began',fixed:{reason:'Always the whole history, whatever the range above.',range:'all'},compare:'none'}],
  pages:[page('main','Spending',[['total',3],['every',3],['fix',3],['count',3]],[['by_cat',12]],[['ranked',6],['payees',6]],[['trips',6],['lifetime',6]])]}},
 {id:'income',title:'Income',spec:{question:'What came in, and was it what I expected?',scope:scope('12mo'),explore:{dimensions:['category','payee']},answer:{metric:'income',facts:['period_change','top_contributors']},
  components:[stat('total','income','Money in'),stat('out','spending','Money out'),
   {id:'trend',type:'time_chart',metric:'income',title:'Money in by source',mark:'stack',split:'category'},
   {id:'by_source',type:'ranked_bars',metric:'income',title:'By source',split:'category'},
   {id:'flow',type:'sankey',metric:'income',title:'Where it came from',split:'category'}],
  pages:[page('main','Income',[['total',6],['out',6]],[['trend',8],['by_source',4]],[['flow',12]])]}},
 {id:'cash',title:'Cash flow',spec:{question:'Is more coming in than going out?',scope:scope('6mo'),answer:ANSWER('burn'),explore:{dimensions:['category']},
  data:{by_month:{from:'postings',where:'row.account.startsWith("expenses:")',derive:{month:'row.date.substring(0,7)'},group:['month'],agg:{amount:'sum(row.value_eur)'},sort:['month']},
        moves:{from:'postings',where:'row.account.startsWith("assets:")',limit:400,sort:['-date']}},
  components:[stat('burn','burn','Monthly burn'),
   {id:'monthly',type:'chart',title:'Spending each month',mark:'bar',format:'eur0',rows:'by_month',encoding:{x:{field:'month'},y:{field:'amount'}},rule:{y:'3000',label:'"Target"'}},
   {id:'table',type:'table',title:'Each month',rows:'by_month',columns:[{label:'Month',value:'row.month',type:'date'},{label:'Spent',value:'row.amount',format:'eur',type:'money',total:true}]},
   {id:'long',type:'table',title:'Every movement',rows:'moves',columns:[{label:'Date',value:'row.date',type:'date'},{label:'Payee',value:'row.payee',type:'text'},{label:'Amount',value:'row.value_eur',format:'eur',type:'money'}]},
   {id:'donut',type:'chart',title:'Share by month',mark:'donut',format:'eur0',rows:'by_month',encoding:{x:{field:'month'},y:{field:'amount'}}}],
  pages:[page('main','Cash flow',[['burn',12]],[['monthly',8],['donut',4]],[['table',6],['long',6]])]}},
 {id:'trips',title:'Trips',spec:{question:'What did each trip really cost?',scope:scope('all','none'),explore:{dimensions:['trip','category']},answer:{metric:'spending',facts:['top_contributors','largest_entry']},
  components:[{id:'by_trip',type:'ranked_bars',metric:'spending',title:'By trip',split:'trip'},{id:'over_time',type:'time_chart',metric:'spending',title:'Spending, with trips marked',mark:'bar',annotate:'trips'},{id:'n',type:'stat',metric:'entries',title:'Entries',compare:'none'}],
  pages:[page('main','Trips',[['n',4],['by_trip',8]],[['over_time',12]])]}},
]
async function main(){
 const started=Date.now();await signIn();console.log('signed in')
 // ONLY_DASHBOARD reapplies one spec without repeating every other write.
 if(process.env.ONLY_DASHBOARD){const d=DASHBOARDS.find(x => x.id===process.env.ONLY_DASHBOARD),t0=Date.now(),applied=await call('dashboard_apply',{dashboard_id:d.id,ops:Object.entries(d.spec).map(([k,v]) => ({op:'add',path:`/${k}`,value:v}))});console.log(d.id,`${Date.now()-t0} ms`,JSON.stringify(applied.validation ?? {}).slice(0,700));return}
 const existing=await call('accounts_list',{limit:500});const have=new Set((existing.items ?? []).map(a => a.id))
 for(const [aid,name,type,cls,pot,kind,commodity,extra] of ACCOUNTS)if(!have.has(aid))await call('account_create',{id:aid,name,type,class:cls,pot,commodities:extra.commodities ?? [commodity],...extra,groups:{...(extra.groups ?? {}),...(cls==='bank' || cls==='cash' || cls==='metal_holding' || cls==='liability' ? {source:kind} : {})}})
 console.log(`accounts ${ACCOUNTS.length}`)
 const end=Date.now()-Date.now()%DAY,start=process.env.SEED_MONTHS ? utc(new Date(end).getUTCFullYear(),new Date(end).getUTCMonth()-Number(process.env.SEED_MONTHS),1) : utc(new Date(end).getUTCFullYear()-3,new Date(end).getUTCMonth(),1)
 if(!have.has('assets:bank:everyday')){
  const open=iso(start-DAY)
  await call('entry_post',{date:open,payee:'Opening balance',commit:true,postings:[{account:'assets:bank:everyday',amount:{c:'EUR',v:'4200.00'}},{account:'assets:bank:online',amount:{c:'EUR',v:'9800.00'}},{account:'assets:bank:savings',amount:{c:'EUR',v:'26000.00'}},{account:'assets:cash:home',amount:{c:'EUR',v:'12000.00'}},{account:'equity:opening',amount:{c:'EUR',v:'-52000.00'}}]})
  await call('entry_post',{date:open,payee:'Opening balance',commit:true,postings:[{account:'assets:metal:gold',amount:{c:'XAU_g',v:'220'}},{account:'equity:opening',amount:{c:'XAU_g',v:'-220'}}]})
  // Monthly quotes: a plain time series that a text axis would have ruined.
  let fx=1.08,gold=58;const quotes=[]
  for(let t=start;t<=end;t+=30*DAY){fx=Math.max(.98,Math.min(1.22,fx*(1+(R()-.5)*.03)));gold=Math.max(40,gold*(1+(R()-.46)*.05));quotes.push({at:`${iso(t)}T12:00:00Z`,pair:'EUR/USD',value:fx.toFixed(4),source:'Synthetic example quote'},{at:`${iso(t)}T12:00:00Z`,pair:'XAU_g/USD',value:(gold*fx).toFixed(2),source:'Synthetic example quote'})}
  await pool(quotes,8,q => call('rate_set',q))
  console.log(`rates ${quotes.length}`)
 }
 for(const m of METRICS)await call('metric_upsert',m)
 for(const [kind,key,label] of [['pot','everyday_money','Everyday money'],['pot','long_term','Long-term'],['pot','cash_home','Cash at home']])await call('label_set',{kind,key,label})
 for(const [pattern,name] of [['^KONZUM','Konzum'],['^LIDL','Lidl'],['^SPAR','Spar'],['^CORNER BISTRO','Corner Bistro'],['^NOODLE BAR','Noodle Bar'],['^WOLT','Wolt'],['^CITY TRANSIT','City Transit'],['^BOLT','Bolt'],['^UBER','Uber'],['^AMZN','Amazon'],['^LANDLORD','Landlord'],['^STREAMBOX','Streaming bundle'],['^MOBILE PLAN','Mobile plan'],['^CLOUD AND','Cloud and software'],['^ENERGY CO','Electric and gas'],['^INTERNET PROVIDER','Internet'],['^EMPLOYER','Employer'],['^CLIENT','Client']])await call('payee_alias_upsert',{pattern,name})
 for(const [i,t] of TRIPS.entries())await call('annotation_upsert',{id:`trip-${i}`,label:t.name,from:iso(t.from),to:iso(t.to),kind:'trip',tag_rule:`trip:${t.name}`})
 for(const [key,name,limit,accounts] of [['home','Home',1400,['expenses:home:rent','expenses:utilities:energy','expenses:utilities:internet']],['food','Food',650,['expenses:food:groceries','expenses:food:restaurants']],['move','Getting around',180,['expenses:transport']],['subs','Subscriptions',130,['expenses:subscriptions:streaming','expenses:subscriptions:mobile','expenses:subscriptions:cloud']],['fun','Shopping and health',300,['expenses:shopping','expenses:health']],['trips','Travel',400,['expenses:travel']]])await call('budget_set_line',{id:key,name,limit:String(limit),accounts}).catch(e => console.log('budget',e.message.slice(0,160)))
 for(const s of [{id:'keep_going',name:'Keep going as now',overrides:[]},{id:'cut_travel',name:'Travel half as much',overrides:[]},{id:'buy_flat',name:'Put a deposit on a flat',overrides:[],events:[{month:6,amount:-30000}]}])await call('scenario_upsert',s).catch(e => console.log('scenario',e.message.slice(0,160)))
 const tables=await call('db_list',{})
 if(!(tables.items ?? []).some(t => t.name==='Long-range plan assumptions with a deliberately long dataset name to test the sidebar')){
  const made=await call('db_create',{name:'Long-range plan assumptions with a deliberately long dataset name to test the sidebar',columns:[{id:'o',type:'number'},{id:'assumption',type:'string'},{id:'value',type:'number'},{id:'tag',type:'string'}]})
  const rowsOf=[['Yearly raise',3],['Rent growth',2.5],['Gold yield',4]].flatMap(([assumption,value],i) => [{row_id:`r${i}`,column:'o',value:i},{row_id:`r${i}`,column:'assumption',value:assumption},{row_id:`r${i}`,column:'value',value},{row_id:`r${i}`,column:'tag',value:'plan'}])
  await call('db_update_cells',{db:made.id,updates:rowsOf})
 }
 for(const d of DASHBOARDS){
  const list=await call('dashboard_list',{});const found=(list.items ?? []).find(x => x.id===d.id)
  if(!found)await call('dashboard_create',{id:d.id,title:d.title})
  const ops=[{op:'replace',path:'/title',value:d.title},...Object.entries(d.spec).map(([k,v]) => ({op:'add',path:`/${k}`,value:v})),...(d.home ? [{op:'add',path:'/home',value:true}] : [])]
  const applied=await call('dashboard_apply',{dashboard_id:d.id,ops})
  if(applied.validation && !applied.validation.ok)console.log(d.id,'validation',JSON.stringify(applied.validation.errors).slice(0,500))
  console.log(`draft ${d.id}`)
 }
 await call('nav_set',{items:[{id:'now',label:'Now',icon:'gauge',route:'/'},{id:'spending',label:'Spending',icon:'receipt',dashboard:'spending'},{id:'income',label:'Income',icon:'banknote',dashboard:'income'},{id:'budget',label:'Budget',icon:'wallet',route:'/budget'},{id:'future',label:'Future',icon:'trending-up',route:'/future'},{id:'ledger',label:'Ledger',icon:'book-open',route:'/ledger'},{id:'data',label:'Data',icon:'database',route:'/data'},{id:'changes',label:'Changes',icon:'history',route:'/changes'}]})
 if(process.env.ONLY_SPECS)return
 // Pages work from here on, on whatever has landed. Entries go newest first so recent ranges fill up first.
 const posted=await call('entries_list',{limit:1})
 if(!(posted.items ?? []).some(e => e.payee!=='Opening balance')){
  const rows=ledger(start,end).reverse();console.log(`posting ${rows.length} entries`);let done=0
  await pool(rows,8,async row => {await call('entry_post',row);if(++done%100===0)console.log(`  ${done}/${rows.length}, ${Math.round((Date.now()-started)/1000)}s`)})
 }
 if(!process.env.SKIP_PUBLISH)for(const d of DASHBOARDS){
  try {const pub=await call('dashboard_publish',{dashboard_id:d.id,note:'Seeded by the synthetic demo.'});console.log(`published ${d.id} v${pub.version ?? ''}`)}
  catch(e){console.log(`publish ${d.id} refused: ${e.message.slice(0,600)}`)}
 }
 console.log(`seeded in ${Math.round((Date.now()-started)/1000)}s`)
}
await main()
