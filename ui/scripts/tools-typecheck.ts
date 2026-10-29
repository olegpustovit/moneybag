import type { MoneybagTools } from '../src/lib/tools'

const entry: MoneybagTools['entry_post']['input'] = { postings: [{ account: 'assets:bank', amount: { v: '1.00', c: 'EUR' } }] }
const preview: MoneybagTools['dashboard_preview']['input'] = { dashboard_id: 'overview', values_only: true }
// @ts-expect-error Decimal values are strings, never floating-point numbers.
const invalid: MoneybagTools['entry_post']['input'] = { postings: [{ account: 'assets:bank', amount: { v: 1, c: 'EUR' } }] }
void [entry, preview, invalid]
