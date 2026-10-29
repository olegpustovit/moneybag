import { strict as assert } from 'node:assert'
import { compareDecimals } from '../src/features/dashboard/decimal-sort'

assert.equal(compareDecimals('1.0000000000000000001','1.0000000000000000002'),-1)
assert.equal(compareDecimals('-10','-2'),-1)
assert.equal(compareDecimals('1.00','1'),0)
assert.equal(compareDecimals('1e+21','999999999999999999999'),1)
assert.equal(compareDecimals('1e-7','0.0000001'),0)
