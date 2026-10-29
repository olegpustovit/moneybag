import { queryOptions } from '@tanstack/react-query'
import { tool, type Session } from './api'

export const dashboardsQuery = (session: Session) => queryOptions({ queryKey: ['dashboards', session.user.id], queryFn: () => tool(session, 'dashboard_list', {}) })
export const tablesQuery = (session: Session) => queryOptions({ queryKey: ['tables', session.user.id], queryFn: () => tool(session, 'db_list', {}) })
