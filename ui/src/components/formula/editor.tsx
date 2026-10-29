import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { EditorState } from '@codemirror/state'
import { EditorView, hoverTooltip, keymap } from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, insertNewline } from '@codemirror/commands'
import { autocompletion, completionKeymap, completionStatus, closeCompletion, snippetCompletion, type Completion, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { StreamLanguage, defaultHighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { setDiagnostics } from '@codemirror/lint'
import { json as jsonLanguage } from '@codemirror/lang-json'
import { catalogQuery, type FormulaCatalog } from './catalog'
import type { FormulaProps } from './input'

type Check={ok:boolean;type:string;issues:{msg:string;start:number;end:number}[]}
type Props=FormulaProps | {label:string;value:string;onChange:(value:string) => void;json:true}
const cel=StreamLanguage.define({token(stream) {if(stream.eatSpace())return null;if(stream.match(/\/\/.*$/))return 'comment';if(stream.match(/(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')/))return 'string';if(stream.match(/\d+(?:\.\d+)?/))return 'number';if(stream.match(/(?:true|false|null|in|has)\b/))return 'keyword';if(stream.match(/[A-Za-z_][\w]*/))return 'variableName';stream.next();return 'operator'}})
const macros=[{name:'exists',signature:'exists(item, predicate) → bool',description:'True if any item matches.'},{name:'all',signature:'all(item, predicate) → bool',description:'True if every item matches.'},{name:'filter',signature:'filter(item, predicate) → list',description:'Keep matching items.'},{name:'map',signature:'map(item, expression) → list',description:'Transform each item.'}]
function completions(context:CompletionContext,catalog:FormulaCatalog|undefined,props:FormulaProps):CompletionResult | null {
 const word=context.matchBefore(/[\w]*/),prefix=context.state.sliceDoc(0,word?.from ?? context.pos),member=/(row|data|computed|params)\.$/.exec(prefix)?.[1],options:Completion[]=[]
 const listField=/\brow\.([A-Za-z_]\w*)\.$/.exec(prefix)?.[1],fieldType=catalog?.sources.find(source => source.name===props.from)?.fields[listField ?? '']
 if(member==='row') {const fields=catalog?.sources.find(source => source.name===props.from)?.fields ?? {};for(const [name,type] of Object.entries(fields))options.push({label:name,type:'property',detail:type});for(const name of props.derived ?? [])if(!fields[name])options.push({label:name,type:'property',detail:'derived'})}
 else if(member) {const values=member==='data' ? props.data : member==='computed' ? props.computed : props.params;for(const name of values ?? [])options.push({label:name,type:'property'})}
 else if(listField && fieldType?.startsWith('list')) {for(const macro of macros)options.push(snippetCompletion(`${macro.name}(\${item}, \${expression})`,{label:macro.name,type:'function',detail:macro.signature,info:macro.description}))}
 else if(prefix.endsWith('.')) return null
 else {const funcs=[...(props.role==='agg' ? catalog?.aggregates ?? [] : []),...(catalog?.functions ?? [])].filter(fn => !fn.member);for(const fn of funcs)options.push(snippetCompletion(`${fn.name}(\${})`,{label:fn.name,type:'function',detail:fn.signature,info:fn.description}));for(const name of catalog?.variables ?? ['row','data','computed','params'])options.push({label:name,type:'variable'})}
 if(!options.length || (!context.explicit && !word?.text && !member && !listField)) return null
 return {from:word?.from ?? context.pos,options,validFor:/^\w*$/}
}
export default function Editor(props:Props) {
 const host=useRef<HTMLDivElement>(null),view=useRef<EditorView>(null),latest=useRef(props),[check,setCheck]=useState<Check|null>(null),[error,setError]=useState(''),[signature,setSignature]=useState('')
 latest.current=props
 const formula='json' in props ? undefined : props
 const catalog=useQuery({...catalogQuery(formula?.session,formula?.sharedId),enabled:!!formula})
 const catalogRef=useRef(catalog.data);catalogRef.current=catalog.data
 useEffect(() => {if(!host.current)return
  const isJSON='json' in latest.current
  const readOnly=!isJSON && 'readOnly' in latest.current && !!latest.current.readOnly
  const extensions=[EditorState.readOnly.of(readOnly),EditorView.editable.of(!readOnly),history(),keymap.of([...completionKeymap,...('json' in latest.current ? [] : [{key:'Enter',run:() => true},{key:'Shift-Enter',run:insertNewline}]),...defaultKeymap,...historyKeymap]),EditorView.lineWrapping,syntaxHighlighting(defaultHighlightStyle),EditorView.contentAttributes.of({'aria-label':latest.current.label,role:'textbox','aria-multiline':isJSON ? 'true' : 'false','aria-readonly':readOnly ? 'true' : 'false'}),EditorView.updateListener.of(update => {if(update.docChanged)latest.current.onChange(update.state.doc.toString());if(!isJSON && (update.selectionSet || update.docChanged)){const text=update.state.sliceDoc(0,update.state.selection.main.head),name=/([A-Za-z_]\w*)\([^()]*$/.exec(text)?.[1],fn=[...(catalogRef.current?.functions ?? []),...(catalogRef.current?.aggregates ?? []),...macros].find(item => item.name===name);setSignature(fn ? `${fn.signature} · ${fn.description}` : '')}})]
  if(isJSON)extensions.push(jsonLanguage())
  else {extensions.push(cel,autocompletion({override:[context => completions(context,catalogRef.current,latest.current as FormulaProps)]}),hoverTooltip((v,pos) => {const text=v.state.doc.toString(),before=text.slice(0,pos).match(/[\w]+$/)?.[0] ?? '',after=text.slice(pos).match(/^\w+/)?.[0] ?? '',name=before+after,fn=[...(catalogRef.current?.functions ?? []),...(catalogRef.current?.aggregates ?? []),...macros].find(item => item.name===name),current=latest.current as FormulaProps,isField=/\brow\.$/.test(text.slice(0,pos-before.length)),fieldType=isField ? catalogRef.current?.sources.find(source => source.name===current.from)?.fields[name] : undefined,description=fieldType ? `${name} · ${fieldType}` : fn ? `${fn.signature} · ${fn.description}` : '';if(!description)return null;return {pos:pos-before.length,end:pos+after.length,above:true,create:() => {const dom=document.createElement('div');dom.className='formula-tooltip';dom.textContent=description;return {dom}}}}))}
  const current=new EditorView({state:EditorState.create({doc:latest.current.value,extensions}),parent:host.current});view.current=current
  return () => {current.destroy();view.current=null}
 },[props.label,'json' in props])
 useEffect(() => {const current=view.current;if(current && current.state.doc.toString()!==props.value)current.dispatch({changes:{from:0,to:current.state.doc.length,insert:props.value}})},[props.value])
 useEffect(() => {if(!formula || !view.current)return;const current=view.current;if(!formula.value.trim()){setCheck(null);setError('');current.dispatch(setDiagnostics(current.state,[]));return}const controller=new AbortController(),timer=window.setTimeout(async () => {try {const response=await fetch('/api/expr/check',{method:'POST',credentials:'same-origin',signal:controller.signal,headers:{'Content-Type':'application/json','X-CSRF-Token':formula.session.csrf},body:JSON.stringify({expr:formula.value,role:formula.role,from:formula.from ?? ''})});if(!response.ok)throw Error('Formula check is unavailable.');const result=await response.json() as Check;if(controller.signal.aborted || view.current!==current)return;setCheck(result);setError('');const length=current.state.doc.length;current.dispatch(setDiagnostics(current.state,result.issues.map(issue => ({from:Math.min(length,Math.max(0,issue.start)),to:Math.min(length,Math.max(issue.start,issue.end)),severity:'error',message:issue.msg}))))}catch(e){if(!controller.signal.aborted){setCheck(null);setError(e instanceof Error ? e.message : 'Formula check is unavailable.')}}},250);return () => {window.clearTimeout(timer);controller.abort()}},[formula?.value,formula?.role,formula?.from,formula?.session.csrf])
 return <div onKeyDownCapture={event => {if(event.key==='Escape' && view.current && completionStatus(view.current.state)){closeCompletion(view.current);event.stopPropagation();event.preventDefault()}}} className={`code-editor ${'json' in props ? 'json-editor' : 'formula-editor'}`}><div ref={host}/>{formula && <><div className="formula-feedback" aria-live="polite">{error || (check?.ok ? `→ ${check.type}` : check?.issues.map(issue => issue.msg).join('; '))}</div>{signature && <p className="formula-signature">{signature}</p>}{catalog.isError && <p className="muted">Formula suggestions are unavailable.</p>}</>}</div>
}
