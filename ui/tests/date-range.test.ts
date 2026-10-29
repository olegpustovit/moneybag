import { strict as assert } from 'node:assert'
import { presetRange, stepRange } from '../src/features/dashboard/date-range'
const today=new Date(2024,2,31,12)
assert.deepEqual(presetRange('3',today),{from:'2023-12-01',to:'2024-02-29'})
assert.deepEqual(presetRange('ytd',today),{from:'2024-01-01',to:'2024-03-31'})
assert.deepEqual(presetRange('year',today),{from:'2023-01-01',to:'2023-12-31'})
assert.deepEqual(stepRange('2024-01-01','2024-02-29',-1),{from:'2023-11-01',to:'2023-12-31'})
assert.deepEqual(stepRange('2024-02-28','2024-03-01',1),{from:'2024-03-02',to:'2024-03-04'})
