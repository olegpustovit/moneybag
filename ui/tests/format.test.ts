import { strict as assert } from 'node:assert'
import { accountLabel, amountLabel, commodityLabel, dateLabel, decimalLabel, fieldLabel, relativeTime } from '../src/lib/format'

assert.equal(amountLabel({v:'-9007199254740993.123456789',c:'EUR'}),'−9,007,199,254,740,993.123456789 €')
assert.equal(amountLabel({v:'0.00',c:'USD'}),'0.00 $ USD')
assert.equal(decimalLabel('−1234.50'),'−1,234.50')
assert.equal(commodityLabel('UAH'),'₴')
assert.equal(commodityLabel('XAU_g'),'Gold (g)')
assert.equal(commodityLabel('UNKNOWN'),'UNKNOWN')
assert.equal(dateLabel('2026-10-29'),'29 Oct 2026')
assert.equal(dateLabel('2026-10'),'Oct 2026')
assert.equal(dateLabel('2024-02-29'),'29 Feb 2024')
assert.equal(dateLabel('2026-02-31'),'2026-02-31')
assert.deepEqual(accountLabel('expenses:everyday:groceries'),{name:'Groceries',trail:'Everyday'})
assert.deepEqual(accountLabel('expenses:побут:продукти'),{name:'Продукти',trail:'Побут'})
assert.deepEqual(accountLabel('assets:bank','My bank'),{name:'My bank',trail:''})
assert.equal(fieldLabel('avg6'),'6-month average')
assert.equal(fieldLabel('some_key'),'Some key')

assert.equal(relativeTime('2026-10-30T08:00:00Z',Date.parse('2026-10-30T10:00:00Z')),'2 h ago')
assert.equal(relativeTime('invalid'),undefined)
assert.equal(relativeTime('2026-10-30T10:01:00Z',Date.parse('2026-10-30T10:00:00Z')),'Just now')
