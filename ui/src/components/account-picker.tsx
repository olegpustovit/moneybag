import { useId, useState } from 'react'
import { Command } from 'cmdk'
import { ChevronDown } from 'lucide-react'
import { Button } from './ui/button'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'
import type { AccountsListOutput } from '../lib/tools'

export type Account = NonNullable<AccountsListOutput['items']>[number]

export function AccountPicker({accounts,value,recent,onChange,label="Account"}:{accounts:Account[];value:string;recent:string[];onChange:(id:string) => void;label?:string}) {
 const [open,setOpen]=useState(false),description=useId()
 const available=accounts.filter(account => !account.closed_at),selected=accounts.find(account => account.id===value)
 const recentAccounts=recent.flatMap(id => {const account=available.find(item => item.id===id);return account ? [account] : []})
 const groups=new Map<string,Account[]>()
 for(const account of available) {if(recent.includes(account.id))continue;const group=groups.get(account.type) ?? [];group.push(account);groups.set(account.type,group)}
 function item(account:Account) {return <Command.Item key={account.id} value={account.id} keywords={[account.name]} onSelect={() => {onChange(account.id);setOpen(false)}} className="account-picker-option"><span>{account.name}</span><small>{account.id}</small></Command.Item>}
 return <Popover open={open} onOpenChange={setOpen}><PopoverTrigger render={<Button type="button" variant="outline" role="combobox" aria-label={label} aria-describedby={description} aria-expanded={open} className="account-picker-trigger"/>}><span id={description}>{selected ? `${selected.name}${selected.closed_at ? ' · Closed' : ''}` : value || 'Choose an account'}</span><ChevronDown aria-hidden="true"/></PopoverTrigger><PopoverContent align="start" aria-label="Choose account" className="account-picker-popover"><Command label="Search accounts"><Command.Input placeholder="Search name or account ID" className="account-picker-search"/><Command.List className="account-picker-list"><Command.Empty className="account-picker-empty">No matching accounts.</Command.Empty>{recentAccounts.length>0 && <Command.Group heading="Recent">{recentAccounts.map(item)}</Command.Group>}{[...groups].map(([type,items]) => <Command.Group key={type} heading={type[0]?.toUpperCase()+type.slice(1)}>{items.map(item)}</Command.Group>)}</Command.List></Command></PopoverContent></Popover>
}
