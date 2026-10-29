import { queryOptions } from '@tanstack/react-query'
import { tool, type Session } from '../../lib/api'

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

export const balancesQuery = (session: Session, at: string | undefined, currency: string | undefined) => queryOptions({ queryKey: ['balances', session.user.id, at, currency], queryFn: () => tool(session, 'balances_get', { at, value_in: currency, group_by: 'account' }) })
