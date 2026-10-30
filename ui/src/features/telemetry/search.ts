// Search params are validated here without a schema library, because every route's validator ships in the entry bundle.
// A malformed URL degrades to the page default instead of erroring.
export const text=(value:unknown,max:number):string|undefined => typeof value==='string' && value.length<=max ? value : typeof value==='number' && String(value).length<=max ? String(value) : undefined
export const oneOf=<T extends string>(value:unknown,allowed:readonly T[]):T|undefined => allowed.includes(value as T) ? value as T : undefined
export const stringList=(value:unknown,max:number,each:number):string[]|undefined => {const list=Array.isArray(value) ? value : typeof value==='string' ? [value] : null;return list && list.length<=max && list.every(x => typeof x==='string' && x.length<=each) ? list as string[] : undefined}
export type CompareParam='previous_period'|'previous_year'|'none'
export type TelemetrySearch={t?:string;cmp?:CompareParam;f?:string}
export const telemetrySearch=(raw:Record<string,unknown>):TelemetrySearch => ({t:text(raw.t,64),cmp:oneOf(raw.cmp,['previous_period','previous_year','none'] as const),f:text(raw.f,2000)})
export type DashboardSearch=TelemetrySearch&{page?:string;table?:string}
export const dashboardSearch=(raw:Record<string,unknown>):DashboardSearch => ({...telemetrySearch(raw),page:text(raw.page,200),table:text(raw.table,200)})
export type LedgerSort='date'|'-date'|'amount'|'-amount'
export type LedgerSearch=TelemetrySearch&{q?:string;accounts?:string[];sort?:LedgerSort}
export const ledgerSearch=(raw:Record<string,unknown>):LedgerSearch => ({...telemetrySearch(raw),q:text(raw.q,200),accounts:stringList(raw.accounts,1000,160),sort:oneOf(raw.sort,['date','-date','amount','-amount'] as const)})
export type ChangesSearch={who?:string;what?:string;item?:string;routine?:boolean;since?:'7'|'30'|'90'|'365'}
export const changesSearch=(raw:Record<string,unknown>):ChangesSearch => ({who:text(raw.who,120),what:text(raw.what,200),item:text(raw.item,200),routine:raw.routine===true || undefined,since:oneOf(text(raw.since,3),['7','30','90','365'] as const)})
const MONTH=/^\d{4}-(0[1-9]|1[0-2])$/
export const budgetSearch=(raw:Record<string,unknown>):{month?:string} => ({month:typeof raw.month==='string' && MONTH.test(raw.month) ? raw.month : undefined})
