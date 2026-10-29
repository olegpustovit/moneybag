import { strict as assert } from 'node:assert'
import { postingTotals } from '../src/features/journal/totals'

assert.equal(postingTotals([{v:'10000000000000000',c:'EUR'},{v:'1.0000000000000000001',c:'EUR'},{v:'-10000000000000000',c:'EUR'},{v:'-0.2',c:'USD'},{v:'0.1',c:'USD'}]),'1.0000000000000000001 EUR · −0.1 USD')
assert.equal(postingTotals([{v:'1.20',c:'EUR'},{v:'-1.2',c:'EUR'},undefined]),'0 EUR')
assert.equal(postingTotals([{v:'1e6',c:'EUR'}]),undefined)
assert.equal(postingTotals([undefined]),undefined)
