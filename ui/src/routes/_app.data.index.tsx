import { RouteError } from '../features/telemetry/route-error'
import { Link, createFileRoute } from '@tanstack/react-router'
import { Empty, PageHead, SubNav } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/data/')({errorComponent:p => <RouteError {...p}/>,component:Datasets})
function Datasets() {
 const {bootstrap}=Route.useRouteContext(),tables=bootstrap.tables.filter(t => !t.archived)
 return <>
  <PageHead title="Data" description="What feeds this, and can I trust it?"/>
  <SubNav of="data" counts={{'/data/imports':bootstrap.counts.imports,'/data/reconcile':bootstrap.counts.reconcile}}/>
  {tables.length ? <ul className="tm-list">{tables.map(t => <li key={t.id}><Link to="/data/$id" params={{id:t.id}}><span className="n" title={t.name}>{t.name}</span><span className="t">{(t.columns ?? []).filter(c => !c.id.startsWith('_')).length} columns</span></Link></li>)}</ul> : <Empty title="No datasets yet">Agents keep reference tables here, such as plans or price lists.</Empty>}
 </>
}
