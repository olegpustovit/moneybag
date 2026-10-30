import { strict as assert } from 'node:assert'
import { dateParams } from '../src/features/dashboard/date-params'
import type { DashboardGetOutput } from '../src/lib/tools'

type Spec=DashboardGetOutput['spec']
const spec=(data:Spec['data']={},components:Spec['components']=[],computed:Spec['computed']={}):Spec => ({id:'test',title:'Test',version:1,data,computed,pages:[],components})
assert.deepEqual(dateParams(spec({items:{from:'postings',where:"params.from != '' && params['to'] != ''"}})),{range:true,month:false})
assert.deepEqual(dateParams(spec({items:{from:'postings',derive:{month:'params [ "month" ]'}}})),{range:false,month:true})
assert.deepEqual(dateParams(spec({items:{from:'postings',where:`row.name == 'params.from' // params.month`}})),{range:false,month:false})
assert.deepEqual(dateParams(spec({items:{from:'postings',where:'data.params.from == 1'}})),{range:false,month:false})
assert.deepEqual(dateParams(spec({},[{id:'x',type:'text',title:'params.from',md:'Example params.month'}])),{range:false,month:false})
assert.deepEqual(dateParams(spec({},[{id:'x',type:'text',title:'Range {{ params.from }}',md:'{{ params["month"] }}'}])),{range:true,month:true})
assert.deepEqual(dateParams(spec({},[{id:'x',type:'table',columns:[{label:'At {{params.to}}',value:'row.value'}]}])),{range:true,month:false})
assert.deepEqual(dateParams(spec({},[],{x:'params[key]'})),{range:true,month:true})
assert.deepEqual(dateParams(spec({},[],{x:'params.monthly'})),{range:false,month:false})

assert.deepEqual(dateParams(spec({},[],{x:'params["fr" + "om"]'})),{range:true,month:true})
const pages=spec();pages.pages=[{id:'p',title:'{{params.to}}',layout:[]}];assert.deepEqual(dateParams(pages),{range:true,month:false})
assert.deepEqual(dateParams(spec({},[],{x:'size(params)'})),{range:true,month:true})
assert.deepEqual(dateParams(spec({},[],{x:`r"params.from" + """params.month"""`})),{range:false,month:false})
assert.deepEqual(dateParams(spec({},[],{x:`params['other']`})),{range:false,month:false})
assert.deepEqual(dateParams(spec({},[],{x:`params["\\u0066rom"]`})),{range:true,month:true})

assert.deepEqual(dateParams(spec({},[{id:'x',type:'kpi',value:'1',sparkline:'params.from == "" ? [0] : [1]'}])),{range:true,month:false})
