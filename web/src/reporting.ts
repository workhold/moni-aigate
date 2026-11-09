import { money } from './format.ts'

// A populated amount alone cannot turn an unresolved cost fact into known cost.
export function providerCostText(amount: unknown, status: unknown): string {
  if (!['contract', 'invoice'].includes(String(status)) || amount === null || amount === undefined || amount === '') return '待核对'
  return money(amount)
}

export interface ReportSummary { tenant_id?: string; credited: string; charged: string; refunded: string; released: string; requests: number; known_provider_cost?: string; unknown_provider_costs?: number; unknown_probe_costs?: number; projection_pending?: number; as_of?: string }
export interface ProviderCost { id: string; tenant_id: string; tenant_name?: string; request_id: string; channel_id: string; channel_name?: string; upstream_model: string; cost_amount: string | null; cost_status: string; created_at: string }
function reportingInvalid(): never { throw new Error('报表响应含未定义或旧标量字段。') }
function reportingObject(value: unknown, keys: readonly string[]): Record<string, unknown> { if (!value || typeof value !== 'object' || Array.isArray(value)) return reportingInvalid(); const raw = value as Record<string, unknown>; if (Object.keys(raw).length !== keys.length || keys.some(key => !Object.hasOwn(raw, key))) return reportingInvalid(); return raw }
function reportingAmount(value: unknown): string { if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)$/.test(value)) return reportingInvalid(); return value }
function reportingCount(value: unknown): number { if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) return reportingInvalid(); return value }
export function parseReportSummary(value: unknown, platform: boolean): ReportSummary {
  const keys = ['tenant_id', 'credited', 'charged', 'refunded', 'released', 'requests', 'projection_pending', 'as_of', ...(platform ? ['known_provider_cost', 'unknown_provider_costs', 'unknown_probe_costs'] : [])]
  const raw = reportingObject(value, keys)
  return { tenant_id: typeof raw.tenant_id === 'string' ? raw.tenant_id : reportingInvalid(), credited: reportingAmount(raw.credited), charged: reportingAmount(raw.charged), refunded: reportingAmount(raw.refunded), released: reportingAmount(raw.released), requests: reportingCount(raw.requests), projection_pending: reportingCount(raw.projection_pending), as_of: typeof raw.as_of === 'string' ? raw.as_of : reportingInvalid(), ...(platform ? { known_provider_cost: reportingAmount(raw.known_provider_cost), unknown_provider_costs: reportingCount(raw.unknown_provider_costs), unknown_probe_costs: reportingCount(raw.unknown_probe_costs) } : {}) }
}
export function parseProviderCostRows(value: unknown): ProviderCost[] {
  if (!Array.isArray(value) || value.length > 50) return reportingInvalid()
  return value.map(item => {
    const raw = reportingObject(item, ['id', 'tenant_id', 'tenant_name', 'request_id', 'channel_id', 'channel_name', 'upstream_model', 'cost_amount', 'cost_status', 'cost_version_id', 'cost_basis', 'created_at'])
    if (raw.cost_amount !== null && typeof raw.cost_amount !== 'string') return reportingInvalid()
    return { id: typeof raw.id === 'string' ? raw.id : reportingInvalid(), tenant_id: typeof raw.tenant_id === 'string' ? raw.tenant_id : reportingInvalid(), tenant_name: typeof raw.tenant_name === 'string' ? raw.tenant_name : reportingInvalid(), request_id: typeof raw.request_id === 'string' ? raw.request_id : reportingInvalid(), channel_id: typeof raw.channel_id === 'string' ? raw.channel_id : reportingInvalid(), channel_name: typeof raw.channel_name === 'string' ? raw.channel_name : reportingInvalid(), upstream_model: typeof raw.upstream_model === 'string' ? raw.upstream_model : reportingInvalid(), cost_amount: raw.cost_amount, cost_status: typeof raw.cost_status === 'string' ? raw.cost_status : reportingInvalid(), created_at: typeof raw.created_at === 'string' ? raw.created_at : reportingInvalid() }
  })
}
