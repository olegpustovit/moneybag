import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/')({ component: Home })

function Home() {
  return <main><h1>Moneybag</h1><p>The new interface is being prepared.</p></main>
}
