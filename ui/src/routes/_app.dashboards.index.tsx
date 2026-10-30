import { createFileRoute } from '@tanstack/react-router'
import { DashboardList } from '../features/dashboard/dashboard-list'

export const Route = createFileRoute('/_app/dashboards/')({ component: DashboardList })
