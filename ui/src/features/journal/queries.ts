import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { tool, type Session } from '../../lib/api'
import type { EntriesListInput } from '../../lib/tools'

export const journalQuery = (session: Session, filters: EntriesListInput) => infiniteQueryOptions({
  queryKey: ['journal', session.user.id, filters],
  queryFn: ({ pageParam }) => tool(session, 'entries_list', { ...filters, cursor: pageParam || undefined, limit: 200 }),
  initialPageParam: 0,
  getNextPageParam: (page) => page.next_cursor || undefined,
})
export const accountsQuery = (session: Session) => queryOptions({ queryKey: ['accounts', session.user.id], queryFn: async () => {
  const items: NonNullable<Awaited<ReturnType<typeof tool<'accounts_list'>>>['items']> = []
  let cursor: string | undefined
  const seen = new Set<string>()
  do {
    const page = await tool(session, 'accounts_list', { include_closed: true, limit: 500, cursor })
    items.push(...(page.items ?? []))
    cursor = page.next_cursor
    if (cursor && seen.has(cursor)) throw new Error('Account pagination repeated.')
    if (cursor) seen.add(cursor)
  } while (cursor)
  return items
} })
