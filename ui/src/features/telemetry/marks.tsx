import { useEffect, useId, useRef, useState } from 'react'
import './telemetry.css'

export function Sparkline({values,color='var(--c0)',label,height=30,tip}:{values:ArrayLike<number|null>;color?:string;label?:string;height?:number;tip?:(index:number) => string}) {
 const [at,setAt]=useState<number|null>(null)
 const nums:number[]=[],indices:number[]=[];for(let i=0;i<values.length;i++){const v=values[i];if(v!==null && v!==undefined && Number.isFinite(v)){nums.push(v);indices.push(i)}}
 if(nums.length<2)return <svg className="tm-spark" height={height} aria-hidden="true"/>
 const min=Math.min(...nums),max=Math.max(...nums),y=(v:number) => (height-3-(max>min ? (v-min)/(max-min) : .5)*(height-6)).toFixed(1)
 const points=nums.map((v,i) => `${(i/(nums.length-1)*200).toFixed(1)},${y(v)}`).join(' ')
 const svg=<svg className="tm-spark" viewBox={`0 0 200 ${height}`} height={height} preserveAspectRatio="none" role={label ? 'img' : undefined} aria-label={label} aria-hidden={label ? undefined : true}><polyline points={points} fill="none" stroke={color} strokeWidth="1.5" vectorEffect="non-scaling-stroke"/></svg>
 if(!tip)return svg
 const yAt=(v:number) => height-3-(max>min ? (v-min)/(max-min) : .5)*(height-6),spot=at===null ? null : Math.min(at,nums.length-1),left=spot===null ? 0 : spot/(nums.length-1)*100
 return <div className="tm-spark-hover" style={{color}} onPointerMove={e => {const r=e.currentTarget.getBoundingClientRect();setAt(Math.max(0,Math.min(nums.length-1,Math.round((e.clientX-r.left)/r.width*(nums.length-1)))))}} onPointerLeave={() => setAt(null)}>
  {svg}
  {spot!==null && <><i className="tm-spark-dot" style={{left:`${left}%`,top:yAt(nums[spot])}}/><span className="tm-spark-tip" style={left>60 ? {right:`${100-left}%`} : {left:`${left}%`}}>{tip(indices[spot])}</span></>}
 </div>
}

export type RankedRow={key:string;label:string;value:number;text:string;compare?:number;compareText?:string;color?:string;estimated?:boolean;note?:string}
// Sorted by size with direct labels. Rows too small to read collapse into one line that expands on request.
export function RankedBars({rows,onSelect,selectLabel='Filter by',collapseBelow=.01,maxRows=10,noun='items',barColor='var(--text3)',emptyText='Nothing to rank in this range.',ariaLabel}:{rows:RankedRow[];onSelect?:(key:string) => void;selectLabel?:string;collapseBelow?:number;maxRows?:number;noun?:string;barColor?:string;emptyText?:string;ariaLabel?:string}) {
 const [expanded,setExpanded]=useState(false),[fullest,setFullest]=useState(0),count=Math.min(rows.length,maxRows+1)
 if(count>fullest)setFullest(count)
 // Keeps the height of the fullest list seen, so stepping the range never moves what sits below.
 if(!rows.length)return <p className="tm-empty" style={{minHeight:fullest*29}}>{emptyText}</p>
 const sorted=[...rows].sort((a,b) => Math.abs(b.value)-Math.abs(a.value)),peak=Math.max(1e-9,...sorted.flatMap(r => [Math.abs(r.value),Math.abs(r.compare ?? 0)]))
 let shown=sorted,small:RankedRow[]=[]
 if(!expanded){const keep=sorted.filter((r,i) => i<maxRows && Math.abs(r.value)>=peak*collapseBelow);if(sorted.length-keep.length>=2){shown=keep;small=sorted.slice(keep.length)}}
 const body=(r:RankedRow) => <><span className="n" title={r.label}>{r.label}</span><span className="track"><i className={r.estimated ? 'tm-hatch' : undefined} style={{width:`${Math.abs(r.value)/peak*100}%`,background:r.estimated ? undefined : r.color ?? barColor}}/>{r.compare!==undefined && <b style={{left:`${Math.abs(r.compare)/peak*100}%`}} title={r.compareText}/>}</span><span className="t">{r.text}{r.note && <small>{r.note}</small>}</span></>
 return <ol className="tm-ranked" aria-label={ariaLabel} style={{minHeight:fullest*29}}>
  {shown.map((r,i) => <li key={i}>{onSelect ? <button type="button" aria-label={`${selectLabel} ${r.label}: ${r.text}${r.compareText ? `, was ${r.compareText}` : ''}`} onClick={() => onSelect(r.key)}>{body(r)}</button> : <div>{body(r)}</div>}</li>)}
  {small.length>0 && <li className="more"><button type="button" onClick={() => setExpanded(true)}><span className="n">{small.length} small {noun}</span><span className="track"/><span className="t">Show</span></button></li>}
 </ol>
}

export type DonutSlice={key:string;label:string;value:number;text:string;color:string;estimated?:boolean}
const TAU=Math.PI*2
export function Donut({slices,center,centerLabel,size=180,onSelect,ariaLabel}:{slices:DonutSlice[];center?:string;centerLabel?:string;size?:number;onSelect?:(key:string) => void;ariaLabel?:string}) {
 const hatchId=useId()
 const items=slices.filter(s => s.value>0),total=items.reduce((n,s) => n+s.value,0)
 if(!total)return <p className="tm-empty">Nothing to show.</p>
 const r=size/2-14,width=22,c=TAU*r;let at=0
 return <figure className="tm-donut" style={{maxWidth:size}}>
  <svg viewBox={`0 0 ${size} ${size}`} role="img" aria-label={ariaLabel ?? slices.map(s => `${s.label} ${s.text}`).join(', ')}>
   <defs><pattern id={hatchId} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><rect width="6" height="6" fill="var(--paper)"/><rect width="2" height="6" fill="var(--hatch)"/></pattern></defs>
   <g transform={`rotate(-90 ${size/2} ${size/2})`}>{items.map(s => {const len=s.value/total*c,gap=items.length>1 ? Math.min(1.5,len/2) : 0,dash=`${Math.max(.5,len-gap)} ${c-Math.max(.5,len-gap)}`,offset=-at;at+=len;const pick=onSelect ? {tabIndex:0,role:'button',onClick:() => onSelect(s.key),onKeyDown:(e:React.KeyboardEvent) => {if(e.key==='Enter' || e.key===' '){e.preventDefault();onSelect(s.key)}},'aria-label':`${s.label}: ${s.text}`,className:'pick'} : {};return <circle key={s.key} cx={size/2} cy={size/2} r={r} fill="none" stroke={s.estimated ? `url(#${hatchId})` : s.color} strokeWidth={width} strokeDasharray={dash} strokeDashoffset={offset} {...pick}><title>{`${s.label}: ${s.text}`}</title></circle>})}</g>
   {center && <text x="50%" y={centerLabel ? '48%' : '50%'} textAnchor="middle" dominantBaseline="middle" className="tm-donut-value">{center}</text>}
   {centerLabel && <text x="50%" y="59%" textAnchor="middle" dominantBaseline="middle" className="tm-donut-label">{centerLabel}</text>}
  </svg>
 </figure>
}

export type SankeyNode={key:string;label:string;color?:string;text?:string};export type SankeyLink={from:string;to:string;value:number}
// Columns come from link depth. Node height follows the larger of what flows in and out, so an imbalance stays visible.
export function layoutSankey(nodes:SankeyNode[],links:SankeyLink[],width:number,height:number,{nodeWidth=10,gap=10,marginLeft=110,marginRight=110}:{nodeWidth?:number;gap?:number;marginLeft?:number;marginRight?:number}={}) {
 const live=links.filter(l => l.value>0),depth=new Map(nodes.map(n => [n.key,0]))
 for(let pass=0;pass<nodes.length;pass++){for(const l of live)depth.set(l.to,Math.max(depth.get(l.to) ?? 0,(depth.get(l.from) ?? 0)+1))}
 const columns=Math.max(0,...depth.values())+1,inSum=new Map<string,number>(),outSum=new Map<string,number>()
 for(const l of live){outSum.set(l.from,(outSum.get(l.from) ?? 0)+l.value);inSum.set(l.to,(inSum.get(l.to) ?? 0)+l.value)}
 const size=(k:string) => Math.max(inSum.get(k) ?? 0,outSum.get(k) ?? 0),used=nodes.filter(n => size(n.key)>0)
 const byCol=Array.from({length:columns},(_,c) => used.filter(n => depth.get(n.key)===c).sort((a,b) => size(b.key)-size(a.key)))
 const scale=Math.min(...byCol.filter(c => c.length).map(c => (height-gap*(c.length-1))/Math.max(1,c.reduce((n,x) => n+size(x.key),0))))
 const placed=new Map<string,{key:string;label:string;color?:string;text?:string;x:number;y:number;h:number;col:number;out:number;in:number}>()
 const span=Math.max(1,width-marginLeft-marginRight-nodeWidth)
 byCol.forEach((col,c) => {const need=col.reduce((n,x) => n+size(x.key)*scale,0)+gap*(col.length-1);let y=(height-need)/2;for(const n of col){const h=Math.max(1,size(n.key)*scale);placed.set(n.key,{...n,x:marginLeft+(columns>1 ? c/(columns-1)*span : 0),y,h,col:c,out:0,in:0});y+=h+gap}})
 const ribbons=live.filter(l => placed.has(l.from) && placed.has(l.to)).sort((a,b) => placed.get(a.from)!.y-placed.get(b.from)!.y || placed.get(a.to)!.y-placed.get(b.to)!.y).map(l => {
  const a=placed.get(l.from)!,b=placed.get(l.to)!,h=Math.max(.75,l.value*scale),y0=a.y+a.out+h/2,y1=b.y+b.in+h/2;a.out+=h;b.in+=h
  const x0=a.x+nodeWidth,x1=b.x,mx=(x0+x1)/2
  return {from:l.from,to:l.to,value:l.value,h,d:`M${x0.toFixed(1)} ${y0.toFixed(1)} C${mx.toFixed(1)} ${y0.toFixed(1)} ${mx.toFixed(1)} ${y1.toFixed(1)} ${x1.toFixed(1)} ${y1.toFixed(1)}`,color:a.col>0 || a.color==='var(--ink)' ? b.color ?? a.color : a.color ?? b.color}
 })
 return {nodes:[...placed.values()],ribbons,columns}
}
export function Sankey({nodes,links,height=280,formatValue,onSelect,ariaLabel='Where the money went'}:{nodes:SankeyNode[];links:SankeyLink[];height?:number;formatValue:(value:number) => string;onSelect?:(key:string) => void;ariaLabel?:string}) {
 const box=useRef<HTMLDivElement>(null),[width,setWidth]=useState(0)
 useEffect(() => {const el=box.current;if(!el)return;const measure=() => setWidth(el.clientWidth);measure();const observer=new ResizeObserver(measure);observer.observe(el);return () => observer.disconnect()},[])
 const narrow=width<520,margin=narrow ? 78 : 200,fit=narrow ? 12 : 26,tall=Math.max(narrow ? height+90 : height,40+nodes.length*(narrow ? 40 : 28))
 const layout=width ? layoutSankey(nodes,links,width,tall,{marginLeft:margin,marginRight:margin,gap:narrow ? 26 : 14}) : null
 const cut=(s:string) => s.length>fit ? `${s.slice(0,fit-1)}…` : s
 return <div ref={box} className="tm-sankey" style={{height:tall+14}}>
  {layout && <svg width={width} height={tall+14} role="img" aria-label={`${ariaLabel}: ${links.map(l => `${l.from} to ${l.to} ${formatValue(l.value)}`).join(', ')}`}>
   {layout.ribbons.map(r => <path key={`${r.from}-${r.to}`} d={r.d} fill="none" stroke={r.color ?? 'var(--series-other)'} strokeOpacity=".28" strokeWidth={r.h}><title>{`${r.from} to ${r.to}: ${formatValue(r.value)}`}</title></path>)}
   {layout.nodes.map(n => {const last=n.col===layout.columns-1,first=n.col===0,middle=!first && !last,tx=first ? n.x-6 : last ? n.x+16 : n.x+5,anchor=first ? 'end' : 'start',cy=n.y+n.h/2,value=n.text ?? '';return <g key={n.key} className={onSelect ? 'pick' : undefined} tabIndex={onSelect ? 0 : undefined} role={onSelect ? 'button' : undefined} aria-label={onSelect ? `${n.label} ${value}` : undefined} onClick={onSelect ? () => onSelect(n.key) : undefined} onKeyDown={onSelect ? e => {if(e.key==='Enter' || e.key===' '){e.preventDefault();onSelect(n.key)}} : undefined}>
    <rect x={n.x} y={n.y} width={10} height={n.h} fill={n.color ?? 'var(--series-other)'}/>
    {middle ? <text x={n.x+5} y={Math.max(11,n.y-6)} textAnchor="middle" className="tm-sankey-label">{cut(n.label)}{value && ` · ${value}`}</text> : <text x={tx} y={cy} textAnchor={anchor} dominantBaseline="middle" className="tm-sankey-label">{narrow ? <><tspan x={tx} dy="-.55em">{cut(n.label)}</tspan><tspan x={tx} dy="1.15em" className="v">{value}</tspan></> : <>{cut(n.label)} <tspan className="v">{value}</tspan></>}</text>}
   </g>})}
  </svg>}
 </div>
}
