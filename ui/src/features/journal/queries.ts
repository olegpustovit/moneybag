import { infiniteQueryOptions } from '@tanstack/react-query'
import { tool, type Session } from '../../lib/api'
import type { EntriesListInput } from '../../lib/tools'

export const journalQuery = (session: Session, filters: EntriesListInput) => infiniteQueryOptions({
  queryKey: ['journal', session.user.id, filters],
  queryFn: ({ pageParam }) => tool(session, 'entries_list', { ...filters, cursor: pageParam || undefined, limit: 200 }),
  initialPageParam: 0,
  getNextPageParam: (page) => page.next_cursor || undefined,
})
