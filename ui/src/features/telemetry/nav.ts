import type { DashboardHead } from '../../lib/api'

// nav_set may still hold routes from before the redesign. They redirect anyway; mapping them here keeps the active marker right and saves the hop.
const MOVED:Record<string,string>={'/activity':'/changes','/journal':'/ledger','/accounts':'/ledger/accounts','/budgets':'/budget','/scenarios':'/future','/databases':'/data','/imports':'/data/imports','/reconcile':'/data/reconcile'}
export const normalizeRoute=(route:string) => MOVED[route] ?? route
export const homeDashboard=(dashboards:DashboardHead[]) => dashboards.find(d => d.home) ?? dashboards[0]
