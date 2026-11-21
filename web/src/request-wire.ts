import { parseMeterSnapshotProjection } from './metering-wire.ts'
import { periodicAmount, periodicUTC6 } from './periodic-budget.ts'
import type { MeterQuantityStatus, TextRetailBounds, TextRetailQuantityLines } from './metering-wire.ts'
import type { TextRetailRates } from './pricing.ts'

export interface BudgetRejection {
  scope: 'tenant' | 'key'
  reason: 'limit_exceeded'
  window_used: string
  limit_amount: string
  window_reset_at: string
}

export interface RequestRow {
  id: string
  model_name?: string
  model_id: string
  profile_version?: string
  reservation_id?: string
  attempt_id?: string
  service_tier?: 'economy' | 'standard' | 'premium'
  physical_model_id?: string
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
  scope?: 'tenant' | 'key'
  reason?: 'limit_exceeded'
  window_used?: string
  limit_amount?: string
  window_reset_at?: string
  cache_hit?: boolean
  finalization_source?: 'adapter' | 'retrieval' | 'gateway' | 'approved_reconciliation' | 'cache' | null
}

const requestFields = new Set([
  'id', 'model_name', 'model_id', 'profile_version', 'reservation_id', 'attempt_id', 'service_tier', 'physical_model_id', 'status', 'billing_status',
  'charged_amount', 'reserved_amount', 'reservation_amount', 'created_at', 'accepted_at', 'settlement_id',
  'protocol_outcome', 'transport_end_reason', 'delivery_stage', 'final_local_delivery', 'finish_reason',
  'usage_status', 'schema_id', 'schema_digest', 'variant_key', 'retail_rates', 'retail_bounds', 'retail_quantities',
  'price_version_id', 'ended_at', 'scope', 'reason', 'window_used', 'limit_amount', 'window_reset_at', 'cache_hit', 'finalization_source',
])

const budgetRejectionFields = ['scope', 'reason', 'window_used', 'limit_amount', 'window_reset_at'] as const

function invalid(): never { throw new Error('请求记录响应含未定义或旧标量字段。') }
function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid()
  return value as Record<string, unknown>
}

// Accept either a protocol error envelope or a request-detail projection. No
// body, SQL, provider message or unknown field is copied into the safe result.
export function parseBudgetRejection(value: unknown): BudgetRejection | null {
  const outer = record(value)
  const data = 'error' in outer ? record(outer.error) : outer
  if (data.code !== undefined && data.code !== 'budget_exceeded') return null
  if (!budgetRejectionFields.some(field => Object.hasOwn(data, field))) return null
  if (budgetRejectionFields.some(field => !Object.hasOwn(data, field))) return invalid()
  if (data.scope !== 'tenant' && data.scope !== 'key') return invalid()
  if (data.reason !== 'limit_exceeded') return invalid()
  return {
    scope: data.scope,
    reason: data.reason,
    window_used: periodicAmount(data.window_used, true),
    limit_amount: periodicAmount(data.limit_amount),
    window_reset_at: periodicUTC6(data.window_reset_at),
  }
}

export function tryBudgetRejection(value: unknown): BudgetRejection | null {
  try { return parseBudgetRejection(value) } catch { return null }
}

export function parseRequestRows(value: unknown): RequestRow[] {
  if (!Array.isArray(value) || value.length > 50) return invalid()
  return value.map(raw => {
    const row = record(raw)
    if (Object.keys(row).some(key => !requestFields.has(key))) return invalid()
    if (typeof row.id !== 'string' || !row.id || typeof row.model_id !== 'string' || !row.model_id || typeof row.status !== 'string' || typeof row.billing_status !== 'string') return invalid()
    if (row.service_tier != null && row.service_tier !== 'economy' && row.service_tier !== 'standard' && row.service_tier !== 'premium') return invalid()
    if (row.physical_model_id != null && (typeof row.physical_model_id !== 'string' || !row.physical_model_id)) return invalid()
    const snapshot = parseMeterSnapshotProjection({ schema_id: row.schema_id, schema_digest: row.schema_digest, variant_key: row.variant_key, retail_rates: row.retail_rates, retail_bounds: row.retail_bounds, usage_status: row.usage_status, retail_quantities: row.retail_quantities })
    parseBudgetRejection(row)
    if (row.cache_hit !== undefined && typeof row.cache_hit !== 'boolean') return invalid()
    if (row.finalization_source !== undefined && row.finalization_source !== null && !['adapter', 'retrieval', 'gateway', 'approved_reconciliation', 'cache'].includes(String(row.finalization_source))) return invalid()
    if (row.cache_hit === true && row.finalization_source !== 'cache') return invalid()
    const safe = Object.fromEntries([...requestFields].filter(key => key !== 'final_local_delivery' && key in row).map(key => [key, row[key]]))
    if (!('delivery_stage' in safe) && 'final_local_delivery' in row) safe.delivery_stage = row.final_local_delivery
    return { ...safe, ...snapshot } as RequestRow
  })
}
