import type { DashboardGetOutput } from '../../lib/tools'

type Spec = DashboardGetOutput['spec']
type DateParam = 'from' | 'to' | 'month'

// shortcut: dynamic or escaped parameter keys show both controls; use CEL analysis for exact detection.
function reads(expr:string,key:DateParam):boolean {
 const tokens=expr.match(/\/\/[^\n]*|(?:[rR])?(?:"""(?:\\[\s\S]|(?!""")[\s\S])*"""|'''(?:\\[\s\S]|(?!''')[\s\S])*'''|"(?:\\[\s\S]|[^"\\])*"|'(?:\\[\s\S]|[^'\\])*')|[A-Za-z_]\w*|[^\s]/g)?.filter(token => !token.startsWith('//')) ?? []
 for(let i=0;i<tokens.length;i++) {
  if(tokens[i]!=='params' || tokens[i-1]==='.')continue
  if(tokens[i+1]==='.' && tokens[i+2]===key)return true
  if(tokens[i+1]!=='.' && tokens[i+1]!=='[')return true
  if(tokens[i+1]==='[') {
   const value=tokens[i+2]
   if(tokens[i+3]!==']' || !value || value===`'${key}'` || value===`"${key}"` || !/^['"]/.test(value) || value.includes('\\'))return true
  }
 }
 return false
}
function holes(value:string|undefined):string[] {return [...(value ?? '').matchAll(/\{\{([^}]+)\}\}/g)].map(match => match[1])}
export function dateParams(spec:Spec):{range:boolean;month:boolean} {
 const expressions:string[]=[]
 for(const query of Object.values(spec.data ?? {}))expressions.push(query.where ?? '',query.n ?? '',...Object.values(query.derive ?? {}),...Object.values(query.agg ?? {}))
 expressions.push(...Object.values(spec.computed ?? {}))
 for(const page of spec.pages ?? [])expressions.push(...holes(page.title))
 for(const component of spec.components ?? []) {
  expressions.push(component.value ?? '',component.delta ?? '',component.sub ?? '',component.tone ?? '',component.rows ?? '',component.label ?? '',component.actual ?? '',component.limit ?? '',component.metric ?? '',component.rule?.y ?? '',component.rule?.label ?? '',component.encoding?.x.expr ?? '',component.encoding?.y.expr ?? '',component.encoding?.color?.expr ?? '',...holes(component.title),...holes(component.md),...holes(component.sub))
  for(const column of component.columns ?? [])expressions.push(column.value,column.tone ?? '',...holes(column.label))
 }
 return {range:expressions.some(expr => reads(expr,'from') || reads(expr,'to')),month:expressions.some(expr => reads(expr,'month'))}
}
