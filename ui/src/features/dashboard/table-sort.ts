import { z } from 'zod'

export const tableSort=z.string().max(1000).regex(/^-?[A-Za-z0-9_][A-Za-z0-9_.:-]*(,-?[A-Za-z0-9_][A-Za-z0-9_.:-]*)*$/).refine(value => {const keys=value.split(',').map(item => item.replace(/^-/,''));return new Set(keys).size===keys.length}).optional().catch(undefined)
