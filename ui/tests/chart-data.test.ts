import { strict as assert } from 'node:assert'
import { chartNumber, chartRows, previousValue } from '../src/features/dashboard/chart-data'
const rows=chartRows([{x:'a',y:1,series:'x'},{x:'b',y:3,series:'x'},{x:'a',y:2,series:'y'},{x:'a',y:4,series:'x'}],[{name:'x',color_index:0},{name:'y',color_index:1}])
assert.deepEqual(rows,[{x:'a',s0:1,s1:2},{x:'b',s0:3},{x:'a',s0:4}])
assert.equal(previousValue(rows,2,'s0'),3)
assert.equal(previousValue(rows,2,'s1'),2)
assert.equal(previousValue(rows,0,'s0'),undefined)
assert.equal(chartNumber(-1234.5,'usd'),'$−1,234.50')
assert.equal(chartNumber(12.3,'pct'),'12.3%')
assert.equal(chartNumber(1.5,'mo'),'1.5 mo')
