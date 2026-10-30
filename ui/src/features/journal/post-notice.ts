import type { QueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { tool, type Session } from '../../lib/api'

export function postedNotice(session:Session,client:QueryClient,eventID:string) {
 let running=false
 toast.success('Posted entry',{duration:10000,action:{label:'Undo',onClick:() => {
  if(running)return
  running=true
  void tool(session,'undo',{event_id:eventID,reason:'Undo from posting notification'}).then(async () => {
   await client.invalidateQueries({predicate:query => query.queryKey[1]===session.user.id})
   toast.success('Entry reversed')
  }).catch((error:unknown) => toast.error('Could not undo entry',{description:error instanceof Error ? error.message : String(error)}))
 }}})
}
