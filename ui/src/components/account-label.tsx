import { useState } from 'react'
import { Copy } from 'lucide-react'
import { accountLabel } from '../lib/format'

export function AccountLabel({id,name}:{id:string;name?:string}) {
 const label=accountLabel(id,name),[status,setStatus]=useState('')
 return <span className="readable-account" title={id}><span>{label.name}{label.trail && <span className="account-trail"> · {label.trail}</span>}</span><button type="button" data-account-copy aria-label={`Copy account ID for ${label.name}`} onClick={event => {event.stopPropagation();void (async () => {try {await navigator.clipboard.writeText(id);setStatus('Account ID copied.')} catch {setStatus('Could not copy account ID.')}})()}}><Copy size={12} aria-hidden="true"/></button><span className="sr-only" role="status">{status}</span></span>
}
