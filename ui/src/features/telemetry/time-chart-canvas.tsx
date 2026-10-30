import { useEffect, useRef, useState } from 'react'
import uPlot, { type AlignedData, type Options } from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { categoryTicks, stackSeries, timeTicks, yExtent, yRange, type Band, type ChartSeries, type Mark } from './chart-core'
import type { ValueFormat } from './format'

export type CanvasProps={
 kind:'time'|'category';xs:number[];edges:number[];mids:number[];categories?:string[];labels:string[];series:ChartSeries[];mark:Mark;format:ValueFormat;zero:boolean;height:number;title:string;
 view?:{from:number;to:number}|null;compare?:{label:string;values:ArrayLike<number|null>}|null;bands?:Band[];estimatedUntil?:number|null;rule?:{y:number;label:string};syncKey?:string;direct?:boolean;
 onDrag?:(from:number,to:number) => void;onWheelZoom?:(center:number,factor:number,final:boolean) => void;onDouble?:() => void;onPick?:(index:number) => void;onLive?:(text:string) => void;
}
const FONT='11px Geist, "Geist Fallback", system-ui, sans-serif'
const token=(name:string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888'
const tint=(hex:string,alpha:string) => hex.startsWith('#') && hex.length===7 ? hex+alpha : hex
// Canvas cannot read CSS custom properties, so colours resolve here and again whenever the theme flips.
function useThemeTick():number {
 const [tick,setTick]=useState(0)
 useEffect(() => {
  const bump=() => setTick(n => n+1),observer=new MutationObserver(bump),media=matchMedia('(prefers-color-scheme: dark)')
  observer.observe(document.documentElement,{attributes:true,attributeFilter:['class','data-theme']});media.addEventListener('change',bump)
  return () => {observer.disconnect();media.removeEventListener('change',bump)}
 },[])
 return tick
}
const hatchCache=new Map<string,CanvasPattern|null>()
function hatch(ctx:CanvasRenderingContext2D,color:string):CanvasPattern|null {
 const key=`${color}/${uPlot.pxRatio}`;if(hatchCache.has(key))return hatchCache.get(key)!
 const c=document.createElement('canvas'),s=Math.round(6*uPlot.pxRatio);c.width=c.height=s
 const x=c.getContext('2d')!;x.strokeStyle=color;x.lineWidth=1.5*uPlot.pxRatio;x.beginPath();x.moveTo(-s/2,s/2);x.lineTo(s/2,-s/2);x.moveTo(0,s);x.lineTo(s,0);x.moveTo(s/2,s*1.5);x.lineTo(s*1.5,s/2);x.stroke()
 const made=ctx.createPattern(c,'repeat');hatchCache.set(key,made);return made
}
type Model=CanvasProps&{stack:ReturnType<typeof stackSeries>|null}
const seriesKey=(p:CanvasProps) => JSON.stringify([p.kind,p.mark,p.height,p.zero,!!p.compare,!!p.direct,p.series.map(s => [s.key,s.color,s.estimated,s.dashed])])

// Bars and stacks draw through uPlot's series paths so its clip, order and pixel snapping still apply.
function barPaths(model:{current:Model},order:number):uPlot.Series.PathBuilder {
 return (u,sidx,i0,i1) => {
  const m=model.current,stacked=m.mark==='stack',count=stacked ? 1 : m.series.length,path=new Path2D(),left=u.bbox.left/uPlot.pxRatio,top=u.bbox.top/uPlot.pxRatio
  const values=u.data[sidx]
  for(let i=i0;i<=i1;i++){
   const v=values[i];if(v===null || v===undefined || v===0)continue
   const x0=u.valToPos(m.edges[i],'x'),x1=u.valToPos(m.edges[i+1],'x'),band=x1-x0,pad=band>6 ? band*.14 : band>3 ? .5 : 0,slot=(band-pad*2)/count,bx=x0+pad+slot*(stacked ? 0 : order)
   const lo=stacked ? m.stack!.lo[order][i] : Math.min(0,v as number),hi=stacked ? m.stack!.hi[order][i] : Math.max(0,v as number),y0=u.valToPos(hi,'y'),y1=u.valToPos(lo,'y')
   path.rect(left+bx,top+y0,Math.max(1,slot-(count>1 && slot>3 ? 1 : 0)),Math.max(1,y1-y0))
  }
  return {fill:path,stroke:path,clip:undefined} as uPlot.Series.Paths
 }
}
function stackedAreaPaths(model:{current:Model},order:number):uPlot.Series.PathBuilder {
 return (u,_sidx,i0,i1) => {
  const m=model.current,s=m.stack!,path=new Path2D(),line=new Path2D(),left=u.bbox.left/uPlot.pxRatio,top=u.bbox.top/uPlot.pxRatio,x=u.data[0]
  for(let i=i0;i<=i1;i++){const px=left+u.valToPos(x[i],'x'),py=top+u.valToPos(s.hi[order][i],'y');if(i===i0){path.moveTo(px,py);line.moveTo(px,py)}else {path.lineTo(px,py);line.lineTo(px,py)}}
  for(let i=i1;i>=i0;i--)path.lineTo(left+u.valToPos(x[i],'x'),top+u.valToPos(s.lo[order][i],'y'))
  path.closePath();return {fill:path,stroke:line} as uPlot.Series.Paths
 }
}
function buildOptions(model:{current:Model},width:number,tip:HTMLDivElement,handlers:{setTip:(u:uPlot) => void}):Options {
 const p=model.current,text=token('--text3'),grid=token('--bd2'),isBar=p.mark==='bar' || p.mark==='stack',time=p.kind==='time',linePaths=uPlot.paths.linear!()
 const series:uPlot.Series[]=[{}]
 p.series.forEach((s,i) => {
  const color=token(s.color)
  if(isBar)series.push({label:s.label,paths:barPaths(model,i),points:{show:false},width:0,stroke:'transparent',fill:s.estimated ? (u => hatch(u.ctx,color) ?? color) : color})
  else if(p.mark==='area')series.push({label:s.label,paths:p.series.length>1 ? stackedAreaPaths(model,i) : linePaths,points:{show:false},width:1.5,stroke:color,fill:s.estimated ? (u => hatch(u.ctx,color) ?? tint(color,'24')) : tint(color,p.series.length>1 ? 'b8' : '24')})
  else series.push({label:s.label,paths:linePaths,points:{show:false},width:s.dashed ? 1.5 : 2,dash:s.dashed || s.estimated ? [5,4] : undefined,stroke:color,spanGaps:false})
 })
 if(p.compare)series.push({label:p.compare.label,paths:linePaths,points:{show:false},width:1.5,dash:[4,4],stroke:token('--text3')})
 const drawBands=(u:uPlot) => {
  const m=model.current;if(!m.bands?.length || !time)return
  const ctx=u.ctx,{left,top,width:w,height:h}=u.bbox,r=uPlot.pxRatio;ctx.save();ctx.beginPath();ctx.rect(left,top,w,h);ctx.clip();ctx.font=`${10*r}px Geist, system-ui, sans-serif`
  const fill=tint(token('--c1'),'22'),label=token('--text2')
  for(const band of m.bands){const x0=u.valToPos(band.from,'x',true),x1=u.valToPos(band.to+864e5,'x',true);if(x1<left || x0>left+w)continue;ctx.fillStyle=fill;ctx.fillRect(x0,top,Math.max(2*r,x1-x0),h);if(ctx.measureText(band.label).width+6*r<x1-x0 || (m.xs.length && (u.scales.x.max!-u.scales.x.min!)<500*864e5)){ctx.fillStyle=label;ctx.fillText(band.label,x0+3*r,top+h-5*r)}}
  ctx.restore()
 }
 const drawOver=(u:uPlot) => {
  const m=model.current,ctx=u.ctx,{left,top,width:w,height:h}=u.bbox,r=uPlot.pxRatio
  if(m.estimatedUntil && time){const x=u.valToPos(m.estimatedUntil,'x',true);if(x>left){const pattern=hatch(ctx,token('--hatch'));if(pattern){ctx.save();ctx.fillStyle=pattern;ctx.fillRect(left,top,Math.min(x,left+w)-left,6*r);ctx.restore()}}}
  if(m.rule){const y=u.valToPos(m.rule.y,'y',true);if(y>=top && y<=top+h){ctx.save();ctx.strokeStyle=token('--text2');ctx.lineWidth=r;ctx.setLineDash([4*r,4*r]);ctx.beginPath();ctx.moveTo(left,y);ctx.lineTo(left+w,y);ctx.stroke();ctx.setLineDash([]);ctx.fillStyle=token('--text2');ctx.font=`${10*r}px Geist, system-ui, sans-serif`;ctx.textAlign='right';ctx.strokeStyle=token('--paper');ctx.lineWidth=3*r;ctx.lineJoin='round';ctx.strokeText(m.rule.label,left+w-3*r,y-4*r);ctx.fillText(m.rule.label,left+w-3*r,y-4*r);ctx.restore()}}
  if(m.direct && !isBar && m.series.length<=4){
   // Direct labels go at the right edge and are pushed apart so they never overlap.
   const placed=m.series.map(s => {let idx=-1;const hiIdx=u.valToIdx(u.scales.x.max!);for(let i=Math.min(hiIdx,s.values.length-1);i>=0;i--)if(s.values[i]!==null && s.values[i]!==undefined){idx=i;break}return idx<0 ? null : {s,x:u.valToPos(m.mids[idx],'x',true),y:u.valToPos(s.values[idx] as number,'y',true)}}).filter(x => x!==null).sort((a,b) => a.y-b.y)
   const gap=13*r;for(let i=1;i<placed.length;i++)placed[i].y=Math.max(placed[i].y,placed[i-1].y+gap);for(let i=placed.length-2;i>=0;i--)placed[i].y=Math.min(placed[i].y,placed[i+1].y-gap)
   ctx.save();ctx.font=`${11*r}px Geist, system-ui, sans-serif`;ctx.textBaseline='middle';ctx.textAlign='left'
   for(const d of placed){const label=d.s.label.length>14 ? `${d.s.label.slice(0,13)}…` : d.s.label;if(!label)continue;ctx.fillStyle=token(d.s.color);ctx.fillText(label,left+w+6*r,d.y)}
   ctx.restore()
  }
 }
 return {
  width,height:p.height,ms:1,legend:{show:false},padding:[8,p.direct && !isBar && p.series.length<=4 && width>=480 ? 96 : 8,0,0],
  cursor:{x:true,y:false,sync:p.syncKey ? {key:p.syncKey,setSeries:false} : undefined,drag:{x:time,y:false,setScale:false},focus:{prox:-1},points:{show:!isBar,size:7,width:1.5},bind:{dblclick:() => () => null}},
  select:{show:true,left:0,top:0,width:0,height:0},
  scales:{x:{time,range:(_u,mn,mx) => {const m=model.current,mini=m.view ? m.view.from : isBar ? m.edges[0] : mn,maxi=m.view ? m.view.to+864e5 : isBar ? m.edges.at(-1)! : mx;if(!time)return [-.5,m.xs.length-.5];return maxi>mini ? [mini,maxi] : [mini-432e5,mini+432e5]}},
   y:{range:(u) => {const m=model.current,min=u.scales.x.min ?? -Infinity,max=u.scales.x.max ?? Infinity;let lo=0;while(lo<m.mids.length && m.mids[lo]<min)lo++;let hi=m.mids.length;while(hi>lo && m.mids[hi-1]>max)hi--;const stacked=!!m.stack;let a:number,b:number;if(stacked){a=m.stack!.min;b=m.stack!.max}else [a,b]=yExtent(m.compare ? [...m.series,m.compare] : m.series,lo,hi);if(stacked && m.compare){const[c,e]=yExtent([m.compare],lo,hi);a=Math.min(a,c);b=Math.max(b,e)}if(m.rule){a=Math.min(a,m.rule.y);b=Math.max(b,m.rule.y)}return yRange(a,b,m.zero)}}},
  axes:[
   {stroke:text,font:FONT,gap:4,size:34,grid:{show:false},ticks:{show:true,stroke:grid,size:4,width:1},
    splits:(u) => {const m=model.current;return (time ? timeTicks(u.scales.x.min!,u.scales.x.max!,u.bbox.width/uPlot.pxRatio) : categoryTicks(m.categories ?? [],u.bbox.width/uPlot.pxRatio)).ticks},
    values:(u) => {const m=model.current;return (time ? timeTicks(u.scales.x.min!,u.scales.x.max!,u.bbox.width/uPlot.pxRatio) : categoryTicks(m.categories ?? [],u.bbox.width/uPlot.pxRatio)).labels}},
   {stroke:text,font:FONT,gap:6,space:36,grid:{stroke:grid,width:1},ticks:{show:false},
    values:(_u,splits) => {const step=splits.length>1 ? Math.abs(splits[1]-splits[0]) : 1;return splits.map(v => model.current.format.axis(v,step))},
    size:(u,values,axisIdx,cycle) => {const axis=u.axes[axisIdx] as uPlot.Axis&{_size?:number};if(cycle>1 && axis._size)return axis._size;u.ctx.font=`${11*uPlot.pxRatio}px Geist, system-ui, sans-serif`;const widest=(values ?? []).reduce((w,v) => Math.max(w,u.ctx.measureText(String(v)).width),0)/uPlot.pxRatio;return Math.ceil(widest)+10}},
  ],
  series,
  hooks:{drawClear:[drawBands],draw:[drawOver],setCursor:[handlers.setTip],
   setSelect:[u => {const s=u.select;if(s.width>6){const a=u.posToVal(s.left,'x'),b=u.posToVal(s.left+s.width,'x');u.setSelect({left:0,top:0,width:0,height:0},false);model.current.onDrag?.(a,b)}}],
   ready:[u => {u.over.appendChild(tip)}]},
 }
}
const toData=(m:CanvasProps):AlignedData => {
 const isBar=m.mark==='bar' || m.mark==='stack',x=m.kind==='category' ? m.xs : isBar ? m.mids : m.xs
 return [x,...m.series.map(s => s.values as number[]),...(m.compare ? [m.compare.values as number[]] : [])] as AlignedData
}
export default function TimeChartCanvas(props:CanvasProps) {
 const host=useRef<HTMLDivElement>(null),chart=useRef<uPlot|null>(null),tick=useThemeTick()
 const stacked=props.mark==='stack' || (props.mark==='area' && props.series.length>1)
 const model=useRef<Model>({...props,stack:null})
 const hovering=useRef(false),keyIndex=useRef<number|null>(null)
 const buildKey=`${seriesKey(props)}/${tick}`
 useEffect(() => {
  const el=host.current;if(!el)return
  const tip=document.createElement('div');tip.className='tm-tip';tip.setAttribute('aria-hidden','true')
  const setTip=(u:uPlot) => {
   const m=model.current,i=u.cursor.idx,show=(hovering.current || keyIndex.current!==null) && i!==null && i!==undefined && (u.cursor.left ?? -1)>=0
   if(!show){tip.style.display='none';return}
   const idx=i as number;tip.textContent=''
   const head=document.createElement('div');head.className='tm-tip-head';head.textContent=m.labels[idx] ?? '';tip.append(head)
   const row=(color:string|null,name:string,value:string,hatched=false,total=false) => {const r=document.createElement('div');r.className=`tm-tip-row${total ? ' total' : ''}`;const swatch=document.createElement('i');if(hatched)swatch.className='tm-hatch';else if(color)swatch.style.background=color;const n=document.createElement('span');n.textContent=name;const b=document.createElement('b');b.textContent=value;r.append(swatch,n,b);tip.append(r)}
   let sum=0,shown=0
   for(const s of m.series){const v=s.values[idx];if(v===null || v===undefined || (m.mark==='stack' && v===0))continue;row(`var(${s.color})`,s.label || 'Value',s.text?.[idx] ?? m.format.full(v),!!s.estimated);sum+=v;shown++}
   if((m.mark==='stack' || (m.mark==='area' && m.series.length>1)) && shown>1)row(null,'Total',m.format.full(sum),false,true)
   if(m.compare){const c=m.compare.values[idx];if(c!==null && c!==undefined){row('var(--text3)',m.compare.label,m.format.full(c));const cur=m.series.length===1 ? m.series[0].values[idx] : sum;if(typeof cur==='number'){const delta=cur-c;row(null,'Change',`${delta>0 ? '+' : ''}${m.format.full(delta)}`,false,true)}}}
   const x=m.mids[idx];const band=m.bands?.find(b => x>=b.from && x<b.to+864e5);if(band)row('var(--c1)',band.label,'')
   tip.style.display='block'
   const w=tip.offsetWidth,o=u.over.clientWidth,left=u.cursor.left!,place=left+12+w>o ? left-w-12 : left+12;tip.style.left=`${Math.max(0,Math.min(o-w,place))}px`;tip.style.top='6px'
   if(keyIndex.current!==null)model.current.onLive?.(tip.innerText.replace(/\n+/g,' '))
  }
  model.current={...props,stack:stacked ? stackSeries(props.series,props.xs.length) : null}
  const u=new uPlot(buildOptions(model,Math.max(200,el.clientWidth),tip,{setTip}),toData(props),el);chart.current=u
  const over=u.over
  // Plain scroll always stays page scroll: only Ctrl or Command takes the wheel, and a trackpad pinch arrives as Ctrl+wheel.
  let wheelDelta=1,wheelCenter=0,raf=0,settleTimer=0
  const flush=(final:boolean) => {raf=0;if(wheelDelta!==1 || final){model.current.onWheelZoom?.(wheelCenter,wheelDelta,final);wheelDelta=1}}
  const onWheel=(e:WheelEvent) => {if(!(e.ctrlKey || e.metaKey) || !model.current.onWheelZoom)return;e.preventDefault();const r=over.getBoundingClientRect();wheelCenter=u.posToVal(e.clientX-r.left,'x');wheelDelta*=e.deltaY>0 ? 1.2 : 1/1.2;if(!raf)raf=requestAnimationFrame(() => flush(false));clearTimeout(settleTimer);settleTimer=window.setTimeout(() => flush(true),200)}
  const onDouble=() => model.current.onDouble?.()
  const onEnter=() => {hovering.current=true},onLeave=() => {hovering.current=false;tip.style.display='none'}
  let downX=0,downY=0
  const onDown=(e:MouseEvent) => {downX=e.clientX;downY=e.clientY}
  const onClick=(e:MouseEvent) => {if(Math.abs(e.clientX-downX)>4 || Math.abs(e.clientY-downY)>4)return;const i=u.cursor.idx;if(i!==null && i!==undefined)model.current.onPick?.(i)}
  // One finger inspects, two fingers pinch. touch-action:pan-y in CSS keeps vertical swipes as page scroll.
  let pinch:{d:number;center:number;moved:boolean}|null=null
  const pos=(t:Touch) => t.clientX-over.getBoundingClientRect().left
  const onTouchStart=(e:TouchEvent) => {if(e.touches.length===2){const a=pos(e.touches[0]),b=pos(e.touches[1]);pinch={d:Math.abs(a-b)||1,center:u.posToVal((a+b)/2,'x'),moved:false};hovering.current=false;tip.style.display='none'}else if(e.touches.length===1){hovering.current=true;u.setCursor({left:pos(e.touches[0]),top:20})}}
  const onTouchMove=(e:TouchEvent) => {
   if(pinch && e.touches.length===2 && model.current.onWheelZoom){const d=Math.abs(pos(e.touches[0])-pos(e.touches[1]))||1,factor=pinch.d/d;if(Math.abs(factor-1)>.02){pinch.d=d;pinch.moved=true;wheelCenter=pinch.center;wheelDelta*=factor;if(!raf)raf=requestAnimationFrame(() => flush(false))}}
   else if(e.touches.length===1 && !pinch)u.setCursor({left:pos(e.touches[0]),top:20})
  }
  const onTouchEnd=(e:TouchEvent) => {if(!e.touches.length){if(pinch?.moved)flush(true);pinch=null;hovering.current=false;tip.style.display='none'}}
  over.addEventListener('wheel',onWheel,{passive:false});over.addEventListener('dblclick',onDouble);over.addEventListener('mouseenter',onEnter);over.addEventListener('mouseleave',onLeave);over.addEventListener('mousedown',onDown);over.addEventListener('click',onClick)
  over.addEventListener('touchstart',onTouchStart,{passive:true});over.addEventListener('touchmove',onTouchMove,{passive:true});over.addEventListener('touchend',onTouchEnd,{passive:true});over.addEventListener('touchcancel',onTouchEnd,{passive:true})
  let resizeFrame=0
  const observer=new ResizeObserver(() => {cancelAnimationFrame(resizeFrame);resizeFrame=requestAnimationFrame(() => {const w=Math.max(200,el.clientWidth);if(Math.abs(u.width-w)>1)u.setSize({width:w,height:model.current.height})})});observer.observe(el)
  const fonts=document.fonts;if(fonts?.status!=='loaded')void fonts?.ready.then(() => {if(chart.current===u)u.redraw(false,true)})
  return () => {observer.disconnect();cancelAnimationFrame(resizeFrame);clearTimeout(settleTimer);if(raf)cancelAnimationFrame(raf);u.destroy();chart.current=null}
  // Everything else flows through `model`; only a structural change rebuilds the plot.
  // eslint-disable-next-line react-hooks/exhaustive-deps
 },[buildKey])
 useEffect(() => {
  model.current={...props,stack:stacked ? stackSeries(props.series,props.xs.length) : null}
  const u=chart.current;if(u)u.setData(toData(props),true)
 })
 const onKey=(e:React.KeyboardEvent) => {
  const u=chart.current,m=model.current;if(!u || !m.mids.length)return
  if(e.key==='ArrowLeft' || e.key==='ArrowRight' || e.key==='Home' || e.key==='End'){
   e.preventDefault();const cur=keyIndex.current ?? (e.key==='ArrowLeft' ? m.mids.length : -1),big=e.shiftKey ? Math.max(1,Math.round(m.mids.length/12)) : 1
   const next=e.key==='Home' ? 0 : e.key==='End' ? m.mids.length-1 : Math.max(0,Math.min(m.mids.length-1,cur+(e.key==='ArrowLeft' ? -big : big)));keyIndex.current=next
   u.setCursor({left:u.valToPos(u.data[0][next],'x'),top:u.bbox.height/uPlot.pxRatio/2})
  } else if(e.key==='Enter' && keyIndex.current!==null)m.onPick?.(keyIndex.current)
  else if((e.key==='+' || e.key==='=' || e.key==='-') && m.onWheelZoom){e.preventDefault();const center=keyIndex.current!==null ? m.mids[keyIndex.current] : (u.scales.x.min!+u.scales.x.max!)/2;m.onWheelZoom(center,e.key==='-' ? 1.5 : 1/1.5,true)}
  else if(e.key==='Escape' && keyIndex.current!==null){keyIndex.current=null;u.setCursor({left:-10,top:-10})}
 }
 return <div ref={host} className={`tm-plot${props.onPick ? ' pickable' : ''}`} style={{minHeight:props.height}} tabIndex={0} role="group" aria-label={`${props.title}. Arrow keys read each point, plus and minus zoom.`} onKeyDown={onKey} onBlur={() => {if(keyIndex.current!==null){keyIndex.current=null;chart.current?.setCursor({left:-10,top:-10})}}}/>
}
