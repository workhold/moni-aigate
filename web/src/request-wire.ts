import { parseMeterSnapshotProjection } from './metering-wire.ts'
import type { MeterQuantityStatus, TextRetailBounds, TextRetailQuantityLines } from './metering-wire.ts'
import type { TextRetailRates } from './pricing.ts'

export interface RequestRow {
  id: string
  model_name?: string
  model_id: string
  profile_version?: string
  reservation_id?: string
  attempt_id?: string
  status: string
  billing_status: string
  charged_amount?: string
  reserved_amount?: string
  reservation_amount?: string
  created_at?: string
  accepted_at?: string
  settlement_id?: string
  protocol_outcome?: string
  transport_end_reason?: string
  delivery_stage?: string
  finish_reason?: string
  usage_status: MeterQuantityStatus
  schema_id: string
  schema_digest: string
  variant_key: 'default'
  retail_rates: TextRetailRates
  retail_bounds: TextRetailBounds
  retail_quantities: TextRetailQuantityLines
  price_version_id?: string
  ended_at?: string
}

const requestFields = new Set([
  'id', 'model_name', 'model_id', 'profile_version', 'reservation_id', 'attempt_id', 'status', 'billing_status',
  'charged_amount', 'reserved_amount', 'reservation_amount', 'created_at', 'accepted_at', 'settlement_id',
  'protocol_outcome', 'transport_end_reason', 'delivery_stage', 'final_local_delivery', 'finish_reason',
  'usage_status', 'schema_id', 'schema_digest', 'variant_key', 'retail_rates', 'retail_bounds', 'retail_quantities',
  'price_version_id', 'ended_at',
])

function invalid(): never { throw new Error('请求记录响应含未定义或旧标量字段。') }
function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid()
  return value as Record<string, unknown>
}

export function parseRequestRows(value: unknown): RequestRow[] {
  if (!Array.isArray(value) || value.length > 50) return invalid()
  return value.map(raw => {
    const row = record(raw)
    if (Object.keys(row).some(key => !requestFields.has(key))) return invalid()
    if (typeof row.id !== 'string' || !row.id || typeof row.model_id !== 'string' || !row.model_id || typeof row.status !== 'string' || typeof row.billing_status !== 'string') return invalid()
    const snapshot = parseMeterSnapshotProjection({ schema_id: row.schema_id, schema_digest: row.schema_digest, variant_key: row.variant_key, retail_rates: row.retail_rates, retail_bounds: row.retail_bounds, usage_status: row.usage_status, retail_quantities: row.retail_quantities })
    const safe = Object.fromEntries([...requestFields].filter(key => key !== 'final_local_delivery' && key in row).map(key => [key, row[key]]))
    if (!('delivery_stage' in safe) && 'final_local_delivery' in row) safe.delivery_stage = row.final_local_delivery
    return { ...safe, ...snapshot } as RequestRow
  })
}
