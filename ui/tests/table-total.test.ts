import { strict as assert } from 'node:assert'
import { tableTotal } from '../src/features/dashboard/table-total'
assert.equal(tableTotal([0.1,0.2],'number'),'0.3')
assert.equal(tableTotal([1e-7,2e-7],'number'),'0.0000003')
assert.equal(tableTotal([1000.005,-0.1],'usd'),'$999.91')
assert.equal(tableTotal([-1.25,0],'pct'),'−1.3%')
assert.equal(tableTotal([1,2,null],'int'),'3')
assert.equal(tableTotal([9007199254740991,2],'number'),'9,007,199,254,740,993')
assert.equal(tableTotal([9007199254740992],'number'),undefined)
assert.equal(tableTotal([1,'2'],'number'),undefined)
assert.equal(tableTotal([null,undefined],'number'),undefined)
