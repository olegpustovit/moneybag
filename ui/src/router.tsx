import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './lib/api'
import { createRouter } from '@tanstack/react-router'
import { routeTree } from './routeTree.gen'

export const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000, gcTime: 300_000, retry: (count, error) => !(error instanceof ApiError && error.status >= 400 && error.status < 500) && count < 1 } } })
export const router = createRouter({ routeTree, context: { queryClient }, defaultPreload: 'intent', defaultPreloadStaleTime: 0, scrollRestoration: true })
declare module '@tanstack/react-router' { interface Register { router: typeof router } }
