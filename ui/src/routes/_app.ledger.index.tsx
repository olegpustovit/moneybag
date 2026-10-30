import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { agentsQuery, tool } from '../lib/api'
import type { EntriesListInput, EntriesListOutput } from '../lib/tools'
import { commodityLabel, decimalLabel } from '../lib/format'
import { useCube } from '../features/telemetry/cube-store'
import { dim, suggestValues, totals, type Cube, type Pill } from '../features/telemetry/cube'
import { decodePills, encodePills, pillsToCel } from '../features/telemetry/filters'
import { TimeBar } from '../features/telemetry/time-bar'
import { FilterBar } from '../features/telemetry/filter-bar'
import { DataGrid, type GridColumn, type GridRow, type GridSort } from '../features/telemetry/data-grid'
import { money, percent } from '../features/telemetry/format'
import { ledgerSearch, type LedgerSearch, type LedgerSort } from '../features/telemetry/search'
import { useTimeState } from '../features/telemetry/use-time-state'
import { canonicalText, isoDay, rangeLabel } from '../features/telemetry/time-grammar'
import { defaultBounds } from '../features/telemetry/time-grammar'
import { describePage, openDetails, promptSaveView } from '../features/telemetry/shell-store'
import { Empty, PageHead, RowsSkeleton, SubNav } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/ledger/')({validateSearch:ledgerSearch,errorComponent:p => <RouteError {...p}/>,component:Ledger})
type Entry=NonNullable<EntriesListOutput['items']>[number]
type Mapped={input:Pick<EntriesListInput,'accounts'|'text'|'tag'>;over?:number;under?:number;none?:boolean}
const isFlow=(id:string) => id.startsWith('expenses:') || id.startsWith('income:')
// entries_list filters by exact accounts, one text and one tag; pills become those, and anything left runs on the rows.
function mapFilters(cube:Cube|null,pills:Pill[],prefixes:string[]|undefined,q:string|undefined):Mapped {
 const out:Mapped={input:{text:q}}
 let allowed:Set<string>|null=prefixes?.length && cube ? new Set(cube.accounts.filter(a => prefixes.some(p => a.id===p || a.id.startsWith(p))).map(a => a.id)) : null
 const restrict=(ids:string[]) => {const set=new Set(ids);allowed=allowed ? new Set([...allowed].filter(x => set.has(x))) : set}
 for(const p of pills){
  if(p.field==='amount'){const v=Number(p.values[0]);if(p.op==='over')out.over=v;else out.under=v;continue}
  if(p.field==='payee'){out.input.text=p.values[0];continue}
  if(p.field==='trip'){if(p.op==='is')out.input.tag=`trip:${p.values[0]}`;continue}
  if(!cube)continue
  const negative=p.op==='is not',match=(a:Cube['accounts'][number]) => {
   const value=p.field==='category' ? a.category : p.field==='account' ? a.label : p.field==='estimate' ? (a.estimated ? 'Estimated' : 'Recorded') : ''
   return p.values.includes(value)
  }
  restrict(cube.accounts.filter(a => isFlow(a.id) && match(a)!==negative).map(a => a.id))
 }
 if(allowed){if(!allowed.size)out.none=true;out.input.accounts=[...allowed].slice(0,1000)}
 return out
}
const amountOf=(a:{c:string;v:string}) => {
 const code=commodityLabel(a.c),negative=a.v.startsWith('-'),[whole,fraction='']=a.v.replace(/^-/,'').split('.'),text=decimalLabel(`${whole}.${(fraction+'00').slice(0,Math.max(2,fraction.replace(/0+$/,'').length))}`)
 return {text:code.length===1 ? `${negative ? '−' : ''}${code}${text}` : `${negative ? '−' : ''}${text} ${code}`,value:Number(a.v)}
}
function rowOf(e:Entry,cube:Cube|null,agents:Map<string,string>):GridRow {
 const postings=e.postings ?? [],acct=(id:string) => cube?.accounts.find(a => a.id===id),flow=postings.filter(p => isFlow(p.account)),money_=postings.filter(p => !isFlow(p.account) && !p.account.startsWith('equity:'))
 const shown=flow[0] ?? money_.find(p => !p.amount.v.startsWith('-')) ?? postings[0],income=shown?.account.startsWith('income:'),transfer=!flow.length
 const total=flow.length>1 && flow.every(p => p.amount.c===flow[0].amount.c) ? {c:flow[0].amount.c,v:String(flow.reduce((n,p) => n+Number(p.amount.v),0))} : shown?.amount
 const amt=total ? amountOf(income ? {c:total.c,v:total.v.replace(/^-/,'')} : total) : {text:'—',value:0}
 const estimated=flow.some(p => acct(p.account)?.estimated),imported=e.kind==='import' || (e.links ?? []).some(l => l.rel==='import')
 const by=e.actor.kind==='agent' ? agents.get(e.actor.id) ?? 'an agent' : e.actor.kind==='system' ? 'Moneybag' : 'you'
 const alias=cube?.aliases.find(a => a.re.test(e.payee))?.name
 return {id:e.id,date:e.date,payee:alias ?? (e.payee || (transfer ? 'Transfer' : 'Entry')),category:transfer ? 'Transfer' : acct(flow[0].account)?.category ?? 'Other',from:acct(money_[0]?.account ?? '')?.label ?? '',source:estimated ? 'Estimated' : imported ? 'From statement' : `Added by ${by}`,amount:{v:income ? amt.value : -amt.value,t:`${income ? '+' : ''}${amt.text}`,tone:income ? 'ok' : undefined}}
}
const COLUMNS:GridColumn[]=[{key:'date',label:'Date',type:'date'},{key:'payee',label:'Payee',type:'text',sortable:false},{key:'category',label:'Category',type:'text',sortable:false,width:'minmax(0,.7fr)'},{key:'from',label:'Account',type:'text',sortable:false,width:'minmax(0,.7fr)'},{key:'source',label:'Source',type:'text',sortable:false,width:'minmax(0,.7fr)'},{key:'amount',label:'Amount',type:'money',width:'minmax(96px,132px)'}]
function Ledger() {
 const {session}=Route.useRouteContext(),params=Route.useSearch(),navigate=Route.useNavigate(),cube=useCube(session.user.id),bounds=cube?.bounds ?? defaultBounds()
 const onSearch=(next:Partial<LedgerSearch>,{push}:{push:boolean}) => {void navigate({search:old => ({...old,...next}),replace:!push,resetScroll:false})}
 const time=useTimeState({bounds,search:params,onSearch,defaultText:'12mo',defaultCompare:'none'}),range=time.range,pills=decodePills(params.f)
 const setPills=(next:Pill[]) => onSearch({f:encodePills(next) || undefined},{push:true})
 const agents=useQuery(agentsQuery(session)),agentNames=new Map((agents.data ?? []).map(a => [a.client_id,a.client_name]))
 const mapped=mapFilters(cube,pills,params.accounts,params.q),sort=params.sort ?? '-date',waiting=(!!params.accounts?.length || pills.some(p => p.field==='category' || p.field==='account' || p.field==='estimate')) && !cube
 const input:EntriesListInput={...mapped.input,from:isoDay(range.from),to:isoDay(range.to),sort,limit:200}
 const entries=useInfiniteQuery({queryKey:['ledger',session.user.id,input],enabled:!waiting && !mapped.none,staleTime:15_000,initialPageParam:{cursor:0,date:'',amount:''},placeholderData:keepPreviousData,
  queryFn:({pageParam}) => tool(session,'entries_list',{...input,cursor:pageParam.cursor || undefined,cursor_date:pageParam.date || undefined,cursor_amount:pageParam.amount || undefined}),
  getNextPageParam:page => page.next_cursor ? {cursor:page.next_cursor,date:page.next_cursor_date ?? '',amount:page.next_cursor_amount ?? ''} : undefined})
 const all=mapped.none ? [] : (entries.data?.pages ?? []).flatMap(p => p.items ?? []),rows=all.map(e => rowOf(e,cube,agentNames)).filter(r => {const v=Math.abs((r.amount as {v:number}).v);return (mapped.over===undefined || v>mapped.over) && (mapped.under===undefined || v<mapped.under)})
 const gridSort:GridSort=sort.endsWith('amount') ? {key:'amount',desc:sort.startsWith('-')} : {key:'date',desc:sort.startsWith('-')}
 const q={range,pills},t=cube ? totals(cube,q) : null,cel=pillsToCel(pills)
 const estimateShare=t && t.out ? t.estimated/t.out : 0
 describePage({title:'Ledger entries',lines:[`Time range: ${rangeLabel(range)}`,...(cel ? [`Filter: ${cel}`] : [])]})
 const byId=new Map(all.map(e => [e.id,e]))
 const open=(row:GridRow) => {
  const e=byId.get(String(row.id));if(!e)return
  const account=(id:string) => cube?.accounts.find(a => a.id===id)?.label ?? id
  openDetails({historyOf:e.id,target:{value:typeof row.amount==='object' && row.amount && 't' in row.amount ? row.amount.t : undefined,params:{entry:e.id}},content:{title:String(row.payee),value:typeof row.amount==='object' && row.amount && 't' in row.amount ? row.amount.t : undefined,subtitle:`${String(row.category)} · ${String(row.source)}`,
   sourceRows:{columns:['Account','Amount','Memo'],rows:(e.postings ?? []).map(p => [account(p.account),amountOf(p.amount).text,p.memo ?? ''])},
   machine:[{label:'Entry',value:e.id},{label:'Bank text',value:e.payee || '—',mono:false},{label:'Status',value:e.status,mono:false},{label:'Sequence',value:String(e.seq)},...(e.tags?.length ? [{label:'Tags',value:e.tags.join(', ')}] : []),...(e.postings ?? []).map(p => ({label:'Account ID',value:p.account}))]}})
 }
 const summary=t ? `${t.entries.toLocaleString('en-US')} entries · ${money(t.out,cube?.base)} out · ${money(t.in,cube?.base)} in${estimateShare>.005 ? ` · ${percent(estimateShare*100,0)} estimated` : ''}` : undefined
 const asks=['Groceries over €50 last 3 months','Travel this year','Everything except rent, no estimates']
 return <>
  <PageHead title="Ledger" description="Show me the entries and the accounts."/>
  <SubNav of="ledger"/>
  <div className="tm-sheet">
   <TimeBar state={time} bounds={bounds} hideCompare/>
   <FilterBar pills={pills} onChange={setPills} formatTotal={c => money(c,cube?.base)} suggest={(field,text) => cube ? suggestValues(cube,q,field,text) : []} countAmount={(op,amount) => cube ? totals(cube,{range,pills:[...pills,{field:'amount',op,values:[String(amount)]}]}).entries : 0} summary={summary}
    asks={cube ? asks : undefined} ask={cube ? {dictionary:{category:dim(cube,'category').values,trip:dim(cube,'trip').values,account:dim(cube,'account').values,payee:dim(cube,'payee').values},bounds,onRange:text => time.setText(text)} : undefined}
    onSaveView={() => promptSaveView('/ledger',{t:canonicalText(range),...(encodePills(pills) ? {f:encodePills(pills)} : {})},'Entries')}/>
  </div>
  {entries.isPending && !mapped.none ? <RowsSkeleton rows={14}/> : entries.error ? <RouteError error={entries.error} reset={() => void entries.refetch()} title="Entries didn’t load"/> : !rows.length ? <Empty title="No entries here">{pills.length || params.q || params.accounts?.length ? 'Nothing matches this range and filter.' : 'Nothing was posted in this range.'}</Empty>
  : <><DataGrid label="Ledger entries" columns={COLUMNS} rows={rows} rowKey={r => String(r.id)} sort={gridSort} onSortChange={next => onSearch({sort:next ? ((next.desc ? '-' : '')+next.key) as LedgerSort : undefined},{push:false})} onDetails={open} onEndReached={entries.hasNextPage && !entries.isFetchingNextPage ? () => void entries.fetchNextPage() : undefined}/>{entries.isFetchingNextPage && <p className="tm-more" role="status">Loading more entries…</p>}</>}
 </>
}
