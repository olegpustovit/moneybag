import { Skeleton } from '../../components/ui/skeleton'

export function LoadingTable({columns}:{columns:string[]}) {
 return <div className="skeleton-table import-loading-table"><table style={{minWidth:`${columns.length*150}px`}}><thead><tr>{columns.map(column => <th key={column}>{column}</th>)}</tr></thead><tbody>{Array.from({length:8},(_,row) => <tr key={row}>{columns.map(column => <td key={column}><Skeleton key={column} className="skeleton-bar"/></td>)}</tr>)}</tbody></table></div>
}
