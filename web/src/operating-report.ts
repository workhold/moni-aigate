import { ApiError, errorText } from './api.ts'
import { money } from './format.ts'

export interface OperatingTotals {
  request_count: number; unsettled_requests: number; attempt_count: number; unknown_provider_costs: number
  charged: string; refunded: string; net_revenue: string; known_provider_cost: string; gross_margin: string | null
}
export interface OperatingModel extends OperatingTotals { model_id: string; model_name: string }
export interface OperatingChannel { channel_id: string; channel_name: string; request_count: number; attempt_count: number; unknown_provider_costs: number; known_provider_cost: string }
export type OperatingProtocol = 'openai_chat' | 'openai_responses' | 'anthropic_messages' | 'openai_embeddings' | 'jina_rerank' | 'gemini_generate_content' | 'image_generation' | 'tts' | 'video_generation' | 'realtime_turn'
export type OperatingExecutionKind = 'synchronous' | 'asynchronous'
export interface OperatingRequest extends OperatingTotals { request_id: string; tenant_id: string; tenant_name: string; model_id: string; model_name: string; status: string; billing_status: string; created_at: string; protocol: OperatingProtocol; execution_kind: OperatingExecutionKind; profile_version: string; schema_id: string; schema_digest: string; variant_key: 'default' }
export interface OperatingReport {
  from: string; to: string; as_of: string; currency: 'CNY'; amount_unit: 'micro_cny'; scope: 'request_created_window'
  summary: OperatingTotals; models: OperatingModel[]; channels: OperatingChannel[]; requests: OperatingRequest[]
}

const reportProtocols: OperatingProtocol[] = ['openai_chat', 'openai_responses', 'anthropic_messages', 'openai_embeddings', 'jina_rerank', 'gemini_generate_content', 'image_generation', 'tts', 'video_generation', 'realtime_turn']
const reportModelKeys = ['mock-image-generation', 'mock-tts', 'mock-video-generation', 'mock-realtime']
const totalFields = ['request_count', 'unsettled_requests', 'attempt_count', 'unknown_provider_costs', 'charged', 'refunded', 'net_revenue', 'known_provider_cost', 'gross_margin'] as const
function reportInvalid(): never { throw new Error('经营报表响应含未定义或旧标量字段。') }
function reportObject(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return reportInvalid()
  const raw = value as Record<string, unknown>
  if (Object.keys(raw).length !== keys.length || keys.some(key => !Object.hasOwn(raw, key))) return reportInvalid()
  return raw
}
function reportCount(value: unknown): number { if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) return reportInvalid(); return value }
function reportAmount(value: unknown, nullable = false): string | null { if (nullable && value === null) return null; if (typeof value !== 'string' || !/^-?(0|[1-9][0-9]*)$/.test(value)) return reportInvalid(); return value }
function reportText(value: unknown): string { if (typeof value !== 'string' || !value) return reportInvalid(); return value }
function reportTimeValue(value: unknown): string { if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value) || !Number.isFinite(Date.parse(value))) return reportInvalid(); return value }
function parseTotals(value: unknown): OperatingTotals {
  const raw = reportObject(value, totalFields)
  return { request_count: reportCount(raw.request_count), unsettled_requests: reportCount(raw.unsettled_requests), attempt_count: reportCount(raw.attempt_count), unknown_provider_costs: reportCount(raw.unknown_provider_costs), charged: reportAmount(raw.charged)!, refunded: reportAmount(raw.refunded)!, net_revenue: reportAmount(raw.net_revenue)!, known_provider_cost: reportAmount(raw.known_provider_cost)!, gross_margin: reportAmount(raw.gross_margin, true) }
}
export function parseOperatingReport(value: unknown): OperatingReport {
  const raw = reportObject(value, ['from', 'to', 'as_of', 'currency', 'amount_unit', 'scope', 'summary', 'models', 'channels', 'requests'])
  if (raw.currency !== 'CNY' || raw.amount_unit !== 'micro_cny' || raw.scope !== 'request_created_window' || !Array.isArray(raw.models) || !Array.isArray(raw.channels) || !Array.isArray(raw.requests) || raw.requests.length > 5000) return reportInvalid()
  const models = raw.models.map(value => { const row = reportObject(value, ['model_id', 'model_name', ...totalFields]); return { ...parseTotals(Object.fromEntries(totalFields.map(key => [key, row[key]]))), model_id: reportText(row.model_id), model_name: reportText(row.model_name) } })
  const channels = raw.channels.map(value => { const row = reportObject(value, ['channel_id', 'channel_name', 'request_count', 'attempt_count', 'unknown_provider_costs', 'known_provider_cost']); return { channel_id: reportText(row.channel_id), channel_name: reportText(row.channel_name), request_count: reportCount(row.request_count), attempt_count: reportCount(row.attempt_count), unknown_provider_costs: reportCount(row.unknown_provider_costs), known_provider_cost: reportAmount(row.known_provider_cost)! } })
  const requests = raw.requests.map(value => { const row = reportObject(value, ['request_id', 'tenant_id', 'tenant_name', 'model_id', 'model_name', 'status', 'billing_status', 'created_at', 'protocol', 'execution_kind', 'profile_version', 'schema_id', 'schema_digest', 'variant_key', ...totalFields]); if (!reportProtocols.includes(row.protocol as OperatingProtocol) || !['synchronous', 'asynchronous'].includes(String(row.execution_kind)) || row.variant_key !== 'default') return reportInvalid(); if (typeof row.model_id !== 'string' || (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(row.model_id) && !reportModelKeys.includes(row.model_id))) return reportInvalid(); return { request_id: reportText(row.request_id), tenant_id: reportText(row.tenant_id), tenant_name: reportText(row.tenant_name), model_id: reportText(row.model_id), model_name: reportText(row.model_name), status: reportText(row.status), billing_status: reportText(row.billing_status), created_at: reportTimeValue(row.created_at), protocol: row.protocol as OperatingProtocol, execution_kind: row.execution_kind as OperatingExecutionKind, profile_version: reportText(row.profile_version), schema_id: reportText(row.schema_id), schema_digest: reportText(row.schema_digest), variant_key: 'default' as const, ...parseTotals(Object.fromEntries(totalFields.map(key => [key, row[key]]))) } })
  return { from: reportTimeValue(raw.from), to: reportTimeValue(raw.to), as_of: reportTimeValue(raw.as_of), currency: 'CNY', amount_unit: 'micro_cny', scope: 'request_created_window', summary: parseTotals(raw.summary), models, channels, requests }
}
export interface OperatingFilterDraft { from: string | null; to: string | null; tenant_id: string; model_id: string; protocol: OperatingProtocol | ''; execution_kind: OperatingExecutionKind | '' }
export interface OperatingFilter { from: string; to: string; tenant_id?: string; model_id?: string; protocol?: OperatingProtocol; execution_kind?: OperatingExecutionKind }

export function defaultOperatingFilter(now = new Date()): OperatingFilterDraft {
  return { from: new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000).toISOString(), to: now.toISOString(), tenant_id: '', model_id: '', protocol: '', execution_kind: '' }
}

function reportTime(value: string | null): Date {
  const match = typeof value === 'string' && /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/.exec(value)
  if (!match) throw new Error('请选择完整的开始与结束时间。时间必须包含时区。')
  const year = Number(match[1]), month = Number(match[2]), day = Number(match[3])
  const days = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  const date = new Date(value!)
  if (year < 1 || month < 1 || month > 12 || day < 1 || day > days[month - 1]! || Number(match[4]) > 23 || Number(match[5]) > 59 || Number(match[6]) > 59 || !Number.isFinite(date.getTime()) || date.getUTCFullYear() < 1 || date.getUTCFullYear() > 9999) throw new Error('报表时间无效，请重新选择。')
  return date
}

export function operatingFilter(draft: OperatingFilterDraft): OperatingFilter {
  const from = reportTime(draft.from), to = reportTime(draft.to)
  if (from >= to) throw new Error('结束时间必须晚于开始时间。')
  if (to.getTime() - from.getTime() > 31 * 24 * 60 * 60 * 1000) throw new Error('一次最多查询 31 天，请缩短时间窗口。')
  const filter: OperatingFilter = { from: from.toISOString(), to: to.toISOString() }
  for (const field of ['tenant_id', 'model_id'] as const) {
    const value = draft[field].trim()
    if (!value) continue
    if (field === 'model_id' ? !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value) && !reportModelKeys.includes(value) : !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)) throw new Error(`${field === 'tenant_id' ? '客户' : '模型'} ID 须为 UUID 或当前媒体模型标识。`)
    filter[field] = value
  }
  if (draft.protocol && !reportProtocols.includes(draft.protocol)) throw new Error('协议筛选无效。')
  if (draft.execution_kind && !['synchronous', 'asynchronous'].includes(draft.execution_kind)) throw new Error('执行类型筛选无效。')
  if (draft.protocol) filter.protocol = draft.protocol
  if (draft.execution_kind) filter.execution_kind = draft.execution_kind
  return filter
}

export function operatingReportPath(filter: OperatingFilter, csv = false): string {
  const query = new URLSearchParams({ from: filter.from, to: filter.to })
  if (filter.tenant_id) query.set('tenant_id', filter.tenant_id)
  if (filter.model_id) query.set('model_id', filter.model_id)
  if (filter.protocol) query.set('protocol', filter.protocol)
  if (filter.execution_kind) query.set('execution_kind', filter.execution_kind)
  return `/platform/reports/operations${csv ? '.csv' : ''}?${query}`
}

export function operatingMarginKnown(totals: OperatingTotals): boolean {
  return totals.gross_margin !== null && totals.unsettled_requests === 0 && totals.unknown_provider_costs === 0
}
export function operatingMarginText(totals: OperatingTotals): string { return operatingMarginKnown(totals) ? money(totals.gross_margin) : '待核对' }
export function operatingReportError(error: unknown): string {
  if (error instanceof ApiError && error.code === 'report_range_too_large') return '该范围超过 5,000 个请求，请缩短时间窗口或增加客户 / 模型筛选。'
  return errorText(error)
}

type ReportTicket = Readonly<{ generation: number; sequence: number; kind: 'read' | 'download' }>
export function createOperatingReportState() {
  let generation = 0, disposed = false, readSequence = 0, downloadSequence = 0
  let report: OperatingReport | null = null, loading = false, downloading = false, error = '', downloadError = ''
  const current = (ticket: ReportTicket) => !disposed && ticket.generation === generation && ticket.sequence === (ticket.kind === 'read' ? readSequence : downloadSequence)
  function invalidate(message = '') { generation++; report = null; loading = false; downloading = false; error = message; downloadError = '' }
  return {
    invalidate,
    current,
    beginRead(): ReportTicket | null {
      if (disposed) return null
      report = null; loading = true; error = ''
      return Object.freeze({ generation, sequence: ++readSequence, kind: 'read' })
    },
    accept(ticket: ReportTicket, value: OperatingReport) { if (!current(ticket) || ticket.kind !== 'read') return false; report = value; loading = false; return true },
    fail(ticket: ReportTicket, cause: unknown) { if (!current(ticket) || ticket.kind !== 'read') return false; report = null; loading = false; error = operatingReportError(cause); return true },
    beginDownload(): ReportTicket | null {
      if (disposed || downloading) return null
      downloading = true; downloadError = ''
      return Object.freeze({ generation, sequence: ++downloadSequence, kind: 'download' })
    },
    finishDownload(ticket: ReportTicket, cause?: unknown) { if (!current(ticket) || ticket.kind !== 'download') return false; downloading = false; downloadError = cause === undefined ? '' : operatingReportError(cause); return true },
    snapshot() { return { report, loading, downloading, error, downloadError } },
    dispose() { disposed = true; invalidate() },
  }
}

// The shared JSON request helper cannot consume CSV. Keep its session cookie
// and current-request authentication events for this binary response.
export async function fetchOperatingCSV(filter: OperatingFilter, isCurrent: () => boolean): Promise<Blob | null> {
  const path = operatingReportPath(filter, true)
  let response: Response
  try { response = await fetch(`/api${path}`, { method: 'GET', credentials: 'include', headers: { Accept: 'text/csv' } }) }
  catch { throw new ApiError('network_error', '无法连接服务，报表未下载。请稍后重新查询。', '', 0) }
  if (!isCurrent()) return null
  if (!response.ok) {
    let result: { request_id?: string; error?: { code?: string; message?: string } } = {}
    try { result = (await response.json()) || {} } catch { /* Do not expose proxy HTML or other response bodies. */ }
    const cause = new ApiError(result.error?.code || 'request_failed', result.error?.message || '报表导出未完成，请稍后重试。', result.request_id || response.headers.get('X-Request-ID') || '', response.status)
    if (isCurrent()) {
      if (response.status === 401) window.dispatchEvent(new CustomEvent('moni:unauthenticated'))
      if (cause.code === 'reauth_required') window.dispatchEvent(new CustomEvent('moni:reauth'))
    }
    throw cause
  }
  if (!/^text\/csv(?:\s*;|$)/i.test(response.headers.get('Content-Type') || '')) throw new ApiError('invalid_response', '服务未返回有效的 CSV，未保存文件。', response.headers.get('X-Request-ID') || '', response.status)
  let blob: Blob
  try { blob = await response.blob() }
  catch { throw new ApiError('network_error', '导出传输中断，未保存文件。请重新下载。', response.headers.get('X-Request-ID') || '', response.status) }
  return isCurrent() ? blob : null
}

interface CSVDownloadTarget { createObjectURL(blob: Blob): string; revokeObjectURL(url: string): void; click(url: string, filename: string): void }
export function saveOperatingCSV(blob: Blob, target: CSVDownloadTarget = {
  createObjectURL: value => URL.createObjectURL(value),
  revokeObjectURL: url => URL.revokeObjectURL(url),
  click(url, filename) {
    const anchor = document.createElement('a'); anchor.href = url; anchor.download = filename
    document.body.append(anchor)
    try { anchor.click() } finally { anchor.remove() }
  },
}) {
  const url = target.createObjectURL(blob)
  try { target.click(url, 'moni-aigate-operations.csv') } finally { target.revokeObjectURL(url) }
}
