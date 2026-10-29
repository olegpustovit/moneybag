import { queryOptions } from '@tanstack/react-query'
import { tool, type Session } from '../../lib/api'
import type { ForecastRunOutput, ScenarioCompareOutput } from '../../lib/tools'
export const modelQuery=(session:Session) => queryOptions({queryKey:['model',session.user.id],queryFn:() => tool(session,'model_get',{})})
export const forecastQuery=(session:Session,ids:string[],horizon:number,currency:string) => queryOptions({queryKey:['forecast',session.user.id,ids,horizon,currency],queryFn:async () => {const series:NonNullable<ForecastRunOutput['series']>=[];for(let i=0;i<ids.length;i+=20) {const result=await tool(session,'forecast_run',{scenario_ids:ids.slice(i,i+20),horizon_months:horizon,value_in:currency});series.push(...(result.series ?? []))}return series},enabled:ids.length>0})
export const comparisonQuery=(session:Session,ids:string[]) => queryOptions({queryKey:['scenario-comparison',session.user.id,ids],queryFn:async () => {const rows:NonNullable<ScenarioCompareOutput['table']>=[];for(let i=0;i<ids.length;i+=20) {const result=await tool(session,'scenario_compare',{scenario_ids:ids.slice(i,i+20)});rows.push(...(result.table ?? []))}return rows},enabled:ids.length>0})
