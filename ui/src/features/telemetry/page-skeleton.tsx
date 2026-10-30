export function PageSkeleton() {
 return <div className="tm-page-skeleton" aria-hidden="true"><div className="tm-skel-title"/><div className="tm-skel-time"/><div className="dashboard-grid">{[3,3,3,3,7,5].map((span,i) => <section key={i} className="panel" style={{gridColumn:`span ${span}`}}><i className="tm-skel" style={{width:'40%'}}/><div style={{height:i<4 ? 88 : 232}}/></section>)}</div></div>
}
