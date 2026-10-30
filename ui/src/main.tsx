import { Profiler, StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, useRouterState } from '@tanstack/react-router'
import { queryClient, router } from './router'
import './index.css'

function App() {
  const empty = useRouterState({ router, select: state => state.matches.length === 0 })
  return <>{empty && <main className="loading" aria-busy="true">Loading workspace…</main>}<RouterProvider router={router} /></>
}

const app = <StrictMode><QueryClientProvider client={queryClient}><App /></QueryClientProvider></StrictMode>
createRoot(document.getElementById('root')!).render(import.meta.env.MODE === 'profile' ? <Profiler id="app" onRender={(_id, phase, actualDuration, _baseDuration, _startTime, commitTime) => window.dispatchEvent(new CustomEvent('moneybag:react-commit',{detail:{phase,actualDuration,commitTime}}))}>{app}</Profiler> : app)
