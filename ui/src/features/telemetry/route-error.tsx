import { useRouter } from '@tanstack/react-router'

// Every route's error screen, so it lives in the entry bundle. It stays this small on purpose.
export function RouteError({error,reset,title='This page didn’t load'}:{error:unknown;reset:() => void;title?:string}) {
 const router=useRouter()
 return <section className="dashboard-error" role="alert" data-route-error><h1>{title}</h1><p>{error instanceof Error ? error.message : 'Something went wrong.'}</p><button type="button" className="tm-btn" onClick={() => {reset();void router.invalidate()}}>Try again</button></section>
}
