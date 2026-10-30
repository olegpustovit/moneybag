import { strict as assert } from 'node:assert'
import { tableTotal } from '../src/features/dashboard/table-total'
assert.equal(tableTotal([0.1,0.2],'number'),undefined)
assert.equal(tableTotal([1e-7,2e-7],'number'),undefined)
assert.equal(tableTotal([1000.005,-0.1],'usd'),'$999.91')
assert.equal(tableTotal([12.5,0.5],'eur'),'€13.00')
assert.equal(tableTotal([-1.25,0],'pct'),undefined)
assert.equal(tableTotal([1,2,null],'int'),undefined)
assert.equal(tableTotal([10,20],'mo'),undefined)
assert.equal(tableTotal([9007199254740991,2],'usd'),'$9,007,199,254,740,993.00')
assert.equal(tableTotal([9007199254740992],'usd'),undefined)
assert.equal(tableTotal([1,'2'],'usd'),undefined)
assert.equal(tableTotal([null,undefined],'usd'),undefined)