import { infiniteQueryOptions } from '@tanstack/react-query'
import { tool, type Session } from '../../lib/api'
import type { EntriesListInput } from '../../lib/tools'

export const journalQuery = (session: Session, filters: EntriesListInput) => infiniteQueryOptions({
  queryKey: ['journal', session.user.id, filters],
  queryFn: ({ pageParam }) => tool(session, 'entries_list', { ...filters, cursor: pageParam.cursor || undefined, cursor_date: pageParam.date || undefined, cursor_amount: pageParam.amount || undefined, limit: 200 }),
  initialPageParam: {cursor:0,date:'',amount:''},
  getNextPageParam: (page) => page.next_cursor ? {cursor:page.next_cursor,date:page.next_cursor_date ?? '',amount:page.next_cursor_amount ?? ''} : undefined,
})
