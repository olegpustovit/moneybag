import { queryOptions } from '@tanstack/react-query'
import { ApiError, tool, type Session } from './api'

export const dashboardsQuery = (session: Session) => queryOptions({ queryKey: ['dashboards', session.user.id], queryFn: () => tool(session, 'dashboard_list', {}) })
export const tablesQuery = (session: Session) => queryOptions({ queryKey: ['tables', session.user.id], queryFn: () => tool(session, 'db_list', {}) })

export const dashboardSpecQuery = (session: Session, id: string, version: string) => queryOptions({ queryKey: ['dashboard-spec', session.user.id, id, version], queryFn: () => tool(session, 'dashboard_get', { id, version }) })
export const dashboardPreviewQuery = (session: Session, id: string, version: string, params: Record<string, string>) => queryOptions({ queryKey: ['dashboard', session.user.id, id, version, params], queryFn: () => tool(session, 'dashboard_preview', { dashboard_id: id, version, params, values_only: true }) })

export interface SettingsData { feeds: { id: string; account: string; format: string; date_format?: string }[]; agents: { id: string; client_id?: string; client_name: string; scopes: string[] | null; revoked: boolean; resource: string; expires_at: string }[] }
export const settingsQuery = (session: Session) => queryOptions({ queryKey: ['settings', session.user.id], queryFn: async (): Promise<SettingsData> => {
  const response = await fetch('/api/settings', { credentials: 'same-origin' })
  if (!response.ok) throw new ApiError('Could not load settings.', response.status)
  return response.json() as Promise<SettingsData>
} })
