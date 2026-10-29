import { queryOptions } from '@tanstack/react-query'
import { ApiError, type Session } from '../../lib/api'
export type FormulaFunction={name:string;signature:string;description:string;member:boolean}
export type FormulaCatalog={revision:number;variables:string[];sources:{name:string;label:string;fields:Record<string,string>}[];functions:FormulaFunction[];aggregates:FormulaFunction[]}
export function catalogQuery(session:Session|undefined,sharedId?:string) {return queryOptions({queryKey:sharedId ? ['shared-expression-catalog',session?.user.id,sharedId] : ['expression-catalog',session?.user.id],staleTime:30_000,queryFn:async ({signal}):Promise<FormulaCatalog> => {const path=sharedId ? `/api/shared/${encodeURIComponent(sharedId)}/catalog` : '/api/expr/catalog';const response=await fetch(path,{credentials:'same-origin',signal});if(!response.ok)throw new ApiError('Could not load formula suggestions.',response.status);return response.json() as Promise<FormulaCatalog>}})}
