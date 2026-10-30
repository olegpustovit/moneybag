import { z } from 'zod'

const tone = z.string().optional()
const cell = z.object({ v: z.unknown(), t: z.string(), tone })
const column = z.object({ key: z.string(), label: z.string(), format: z.string(), align: z.string(), type: z.string().optional(), total: z.boolean().optional() })
const base = { title: z.string().optional() }
export const componentValue = z.discriminatedUnion('type', [
  z.object({ ...base, type: z.literal('kpi'), value: z.unknown(), text: z.string(), tone: z.string(), sub: z.string(), sub_text: z.string().optional(), delta: z.object({ text: z.string(), direction: z.enum(['up','down','unchanged','neutral']) }).optional(), sparkline: z.array(z.number().finite()).max(600).optional(), sparkline_text: z.array(z.string()).max(600).optional() }),
  z.object({ ...base, type: z.enum(['table', 'scenario_compare']), columns: z.array(column), rows: z.array(z.record(z.string(), cell)) }),
  z.object({ ...base, type: z.literal('budget'), rows: z.array(z.object({ label: z.string(), actual: z.number(), limit: z.number(), status: z.string(), actual_text: z.string(), limit_text: z.string(), average_text: z.string().optional(), average_label: z.string().optional(), accounts: z.array(z.string()).optional() })) }),
  z.object({ ...base, type: z.literal('text'), html: z.string() }),
  z.object({ ...base, type: z.literal('chart'), mark: z.enum(['bar','line','area','stack','donut']), x: z.object({ field: z.string(), kind: z.string() }), series: z.array(z.object({ name: z.string(), color_index: z.number() })), points: z.array(z.object({ x: z.string(), y: z.number(), y_text: z.string().optional(), series: z.string().optional().default('') })), rule: z.object({ y: z.number(), label: z.string() }).optional(), y_format: z.string() }),
])
export type ComponentValue = z.infer<typeof componentValue>
export type ChartValue = Extract<ComponentValue, { type: 'chart' }>
