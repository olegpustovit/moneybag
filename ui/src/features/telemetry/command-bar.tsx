import { useEffect, useState } from 'react'
import { Command } from 'cmdk'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '../../components/ui/dialog'
import './telemetry.css'

export type CommandItem={id:string;label:string;hint?:string;keywords?:string[]};export type CommandGroup={heading:string;items:CommandItem[]}
const LIMIT=40
export function CommandBar({groups,onSelect,onAsk,open:openProp,onOpenChange,placeholder='Jump to a page, account, payee or view. Or ask a question.'}:{groups:CommandGroup[];onSelect:(item:CommandItem,group:string) => void;onAsk?:(question:string) => void;open?:boolean;onOpenChange?:(open:boolean) => void;placeholder?:string}) {
 const [local,setLocal]=useState(false),[query,setQuery]=useState(''),open=openProp ?? local
 const set=(next:boolean) => {if(onOpenChange)onOpenChange(next);if(openProp===undefined)setLocal(next);if(!next)setQuery('')}
 const latest=open
 useEffect(() => {
  // ⌘K works from anywhere, including inside a text field, because it can't mean anything else there.
  const onKey=(e:KeyboardEvent) => {if((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase()==='k'){e.preventDefault();set(!latest)}}
  document.addEventListener('keydown',onKey);return () => document.removeEventListener('keydown',onKey)
 })
 const q=query.trim()
 return <Dialog open={open} onOpenChange={set}>
  <DialogContent className="tm-command" showCloseButton={false}>
   <DialogTitle className="sr-only">Command bar</DialogTitle>
   <DialogDescription className="sr-only">Type to jump somewhere, or ask an agent a question.</DialogDescription>
   <Command label="Command bar" loop>
    <Command.Input autoFocus value={query} onValueChange={setQuery} placeholder={placeholder} aria-label="Search or ask"/>
    <Command.List>
     <Command.Empty>{onAsk ? 'Nothing to jump to. Press Enter to ask an agent.' : 'Nothing found.'}</Command.Empty>
     {groups.map(group => <Command.Group key={group.heading} heading={group.heading}>{group.items.slice(0,q ? undefined : LIMIT).map(item => <Command.Item key={item.id} value={`${group.heading}/${item.id}`} keywords={[item.label,...(item.keywords ?? [])]} onSelect={() => {onSelect(item,group.heading);set(false)}}><span>{item.label}</span>{item.hint && <small>{item.hint}</small>}</Command.Item>)}</Command.Group>)}
     {onAsk && q && <Command.Group heading="Ask an agent" forceMount><Command.Item forceMount value="__ask" onSelect={() => {onAsk(q);set(false)}}><span>Ask: “{q}”</span><small>Sends this to an agent</small></Command.Item></Command.Group>}
    </Command.List>
   </Command>
   <div className="tm-command-foot"><kbd>↑↓</kbd> move <kbd>Enter</kbd> open <kbd>Esc</kbd> close</div>
  </DialogContent>
 </Dialog>
}
