import { errorText, request } from './api.ts'
import type { Envelope } from './api.ts'
import { commerceDecimal, commerceObject, commerceUUID } from './commerce.ts'
import { createCommerceScope } from './commerce-state.ts'
import type { WalletRefundClass, WalletSourceKind } from './wallet-sources.ts'

export interface RequestFundingRow {
  id: string; lot_id: string; source_kind: WalletSourceKind; refund_class: WalletRefundClass; created_at: string
  allocation_index: string; reserved_amount: string; charged_amount: string | null; released_amount: string | null; refunded_amount: string
}
export interface RequestFundingState {
  rows: RequestFundingRow[]; loading: boolean; error: string; cursor: string; nextCursor: string; history: string[]
}
export interface WalletSourceCheck {
  tenant_id: string; as_of: string; balanced: boolean; source_mismatches: number; event_mismatches: number; allocation_mismatches: number; wallet_mismatches: number
}
export interface WalletSourceAuditState { result: WalletSourceCheck | null; loading: boolean; error: string }
type Transport = (path: string, isCurrent: () => boolean) => Promise<Envelope<unknown>>
const transportDefault: Transport = (path, isCurrent) => request<unknown>(path, 'GET', undefined, undefined, isCurrent)
const fundingFields = ['id', 'lot_id', 'source_kind', 'refund_class', 'created_at', 'allocation_index', 'reserved_amount', 'charged_amount', 'released_amount', 'refunded_amount'] as const
const countFields = ['source_mismatches', 'event_mismatches', 'allocation_mismatches', 'wallet_mismatches'] as const

function utcTime(value: unknown): string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) throw new Error('资金来源时间须为有效的 UTC RFC3339 格式。')
  const [whole, fraction = ''] = value.slice(0, -1).split('.')
  const milliseconds = `${whole}.${fraction.padEnd(3, '0').slice(0, 3)}Z`, date = new Date(milliseconds)
  if (!Number.isFinite(date.getTime()) || date.toISOString() !== milliseconds) throw new Error('资金来源时间无效。')
  return value
}
function cursorValue(value: unknown): string {
  if (typeof value !== 'string' || value.length > 512 || value !== '' && !/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('资金分配分页游标无效。')
  return value
}
export function requestFundingPath(requestID: string, cursor = ''): string {
  return `/requests/${commerceUUID(requestID)}/funding${cursorValue(cursor) ? `?cursor=${encodeURIComponent(cursor)}` : ''}`
}
export function walletSourceAuditPath(tenantID: string): string { return `/platform/wallet-sources/check?tenant_id=${commerceUUID(tenantID)}` }
export function parseRequestFunding(value: unknown): RequestFundingRow {
  const raw = commerceObject(value, fundingFields)
  if (!['manual_credit', 'voucher_grant', 'sandbox_capture', 'wallet_opening'].includes(raw.source_kind as string) || !['nonrefundable', 'sandbox_original'].includes(raw.refund_class as string)) throw new Error('资金来源类型或退款类别无效。')
  const row: RequestFundingRow = {
    id: commerceUUID(raw.id), lot_id: commerceUUID(raw.lot_id), source_kind: raw.source_kind as WalletSourceKind, refund_class: raw.refund_class as WalletRefundClass,
    created_at: utcTime(raw.created_at), allocation_index: commerceDecimal(raw.allocation_index, true), reserved_amount: commerceDecimal(raw.reserved_amount, true),
    charged_amount: raw.charged_amount === null ? null : commerceDecimal(raw.charged_amount), released_amount: raw.released_amount === null ? null : commerceDecimal(raw.released_amount), refunded_amount: commerceDecimal(raw.refunded_amount),
  }
  if (row.id !== row.lot_id || (row.source_kind === 'sandbox_capture') !== (row.refund_class === 'sandbox_original')) throw new Error('资金来源标识或退款类别不一致。')
  if ((row.charged_amount === null) !== (row.released_amount === null)) throw new Error('资金分配结算状态不完整。')
  if (row.charged_amount === null) {
    if (row.refunded_amount !== '0') throw new Error('未结算分配不能包含消费退款。')
  } else if (BigInt(row.charged_amount) + BigInt(row.released_amount!) !== BigInt(row.reserved_amount) || BigInt(row.refunded_amount) > BigInt(row.charged_amount)) throw new Error('资金分配预占、结算或退款金额不一致。')
  return row
}
export function parseRequestFundingPage(data: unknown, nextCursor: unknown): { rows: RequestFundingRow[]; nextCursor: string } {
  if (!Array.isArray(data) || data.length > 50) throw new Error('资金分配分页响应无效，每页最多 50 条。')
  const rows = data.map(parseRequestFunding), next = cursorValue(nextCursor)
  if (new Set(rows.map(row => row.lot_id)).size !== rows.length || new Set(rows.map(row => row.allocation_index)).size !== rows.length || next && rows.length !== 50) throw new Error('资金分配分页包含重复记录或不完整的下一页信息。')
  return { rows, nextCursor: next }
}
export function parseWalletSourceCheck(value: unknown, tenantID: string): WalletSourceCheck {
  const raw = commerceObject(value, ['tenant_id', 'as_of', 'balanced', ...countFields])
  if (commerceUUID(raw.tenant_id) !== commerceUUID(tenantID)) throw new Error('核对结果与选定客户不一致。')
  if (typeof raw.balanced !== 'boolean' || countFields.some(field => typeof raw[field] !== 'number' || !Number.isSafeInteger(raw[field]) || (raw[field] as number) < 0)) throw new Error('资金来源核对计数无效。')
  if (raw.balanced !== countFields.every(field => raw[field] === 0)) throw new Error('资金来源核对状态与差异计数不一致。')
  return { tenant_id: tenantID, as_of: utcTime(raw.as_of), balanced: raw.balanced, source_mismatches: raw.source_mismatches as number, event_mismatches: raw.event_mismatches as number, allocation_mismatches: raw.allocation_mismatches as number, wallet_mismatches: raw.wallet_mismatches as number }
}

function emptyFunding(): RequestFundingState { return { rows: [], loading: false, error: '', cursor: '', nextCursor: '', history: [] } }
export function createRequestFundingReader(transport: Transport = transportDefault) {
  const scope = createCommerceScope()
  let disposed = false, target = '', state = emptyFunding()
  const snapshot = (): RequestFundingState => ({ ...state, rows: state.rows.map(row => ({ ...row })), history: [...state.history] })
  async function load(requestID: string, navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
    if (disposed) return
    if (target !== requestID) { scope.reset(); target = requestID; state = emptyFunding() }
    if (state.loading || navigation === 'next' && !state.nextCursor || navigation === 'previous' && !state.history.length) return
    const previous = state.cursor, page = navigation === 'next' ? state.nextCursor : navigation === 'previous' ? state.history.at(-1)! : state.cursor
    const ticket = scope.ticket('funding')
    state = { ...state, rows: [], nextCursor: '', error: '', loading: true }
    try {
      const response = await transport(requestFundingPath(requestID, page), () => !disposed && scope.accepts(ticket))
      if (disposed || !scope.accepts(ticket)) return
      const parsed = parseRequestFundingPage(response.data, response.next_cursor)
      if (page && parsed.nextCursor === page || parsed.nextCursor && state.history.includes(parsed.nextCursor)) throw new Error('资金分配分页没有向前推进，请刷新列表。')
      state = { rows: parsed.rows, loading: false, error: '', cursor: page, nextCursor: parsed.nextCursor, history: navigation === 'next' ? [...state.history, previous] : navigation === 'previous' ? state.history.slice(0, -1) : state.history }
    } catch (cause) { if (!disposed && scope.accepts(ticket)) state = { ...state, rows: [], nextCursor: '', loading: false, error: errorText(cause) } }
  }
  return {
    snapshot, load,
    reset(context: string) { scope.reset(context); target = ''; state = emptyFunding() },
    dispose() { disposed = true; scope.reset(); target = ''; state = emptyFunding() },
  }
}
export function createWalletSourceAuditReader(transport: Transport = transportDefault) {
  const scope = createCommerceScope()
  let disposed = false, target = '', state: WalletSourceAuditState = { result: null, loading: false, error: '' }
  const snapshot = (): WalletSourceAuditState => ({ ...state, result: state.result && { ...state.result } })
  async function load(tenantID: string) {
    if (disposed) return
    if (target !== tenantID) { scope.reset(); target = tenantID; state = { result: null, loading: false, error: '' } }
    if (state.loading) return
    const ticket = scope.ticket('audit')
    state = { result: null, loading: true, error: '' }
    try {
      const response = await transport(walletSourceAuditPath(tenantID), () => !disposed && scope.accepts(ticket))
      if (disposed || !scope.accepts(ticket)) return
      state = { result: parseWalletSourceCheck(response.data, tenantID), loading: false, error: '' }
    } catch (cause) { if (!disposed && scope.accepts(ticket)) state = { result: null, loading: false, error: errorText(cause) } }
  }
  return {
    snapshot, load,
    reset(context: string) { scope.reset(context); target = ''; state = { result: null, loading: false, error: '' } },
    dispose() { disposed = true; scope.reset(); target = ''; state = { result: null, loading: false, error: '' } },
  }
}
