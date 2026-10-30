import { createFileRoute, redirect } from '@tanstack/react-router'

type Old={from?:string;to?:string;text?:string;account?:string;accounts?:string[];tag?:string;sort?:string}
// The old filters become the Ledger's: dates become the clock, a tag becomes a trip pill, and text and accounts carry over.
export const Route=createFileRoute('/_app/journal')({beforeLoad:({search}) => {
 const old=search as Old,tag=old.tag?.startsWith('trip:') ? old.tag.slice(5) : undefined,accounts=old.accounts ?? (old.account ? [old.account] : undefined)
 throw redirect({to:'/ledger',replace:true,search:{t:old.from && old.to ? `${old.from} to ${old.to}` : old.from ? `since ${old.from}` : undefined,q:old.text,accounts,f:tag ? `trip:is:${encodeURIComponent(tag)}` : undefined,sort:['date','-date','amount','-amount'].includes(old.sort ?? '') ? old.sort as 'date' : undefined}})
}})
