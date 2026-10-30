import { useEffect, useLayoutEffect, useRef, useState } from 'react'

export function useLoadingSkeleton(pending:boolean):boolean {
 const [visible,setVisible]=useState(false),started=useRef(0)
 useLayoutEffect(() => {if(visible)started.current=performance.now()},[visible])
 useEffect(() => {
  if(pending && !visible) {const timer=setTimeout(() => setVisible(true),150);return () => clearTimeout(timer)}
  if(!pending && visible) {const timer=setTimeout(() => setVisible(false),Math.max(0,300-(performance.now()-started.current)));return () => clearTimeout(timer)}
 },[pending,visible])
 return visible
}

