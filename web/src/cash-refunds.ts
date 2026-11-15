import { commerceDecimal, commerceObject, commerceReference, commerceText, commerceTime, commerceUUID } from './commerce.ts'

export const cashRefundStates = ['requested', 'approved', 'transfer_executed', 'completed', 'cancelled', 'failed'] as const
export type CashRefundState = typeof cashRefundStates[number]
export type CashRefundAction = 'approve' | 'execute' | 'complete' | 'cancel' | 'fail'
export interface CashRefundOrder {
  id: string; tenant_id: string; lot_id: string; business_ref: string; coin_amount: string | null; reason: string; state: CashRefundState
  requested_by: string; maker: string; approver: string; transfer_executor: string; fiat_currency: string; fiat_amount: string | null
  buy_rate_version_id: string; refund_rate_version_id: string; single_operator: boolean; transfer_order_ref: string; transfer_proof_ref: string
  paid_fiat_amount: string | null; hold_journal_id: string; retire_journal_id: string; release_journal_id: string; approved_at: string | null; created_at: string
  request_id: string; single_operator_exception: boolean; preview_coin_amount: string | null; preview_fiat_amount: string | null; preview_fiat_currency: string
  tenant_name?: string
}
const fields = ['id','tenant_id','lot_id','business_ref','coin_amount','reason','state','requested_by','maker','approver','transfer_executor','fiat_currency','fiat_amount','buy_rate_version_id','refund_rate_version_id','single_operator','transfer_order_ref','transfer_proof_ref','paid_fiat_amount','hold_journal_id','retire_journal_id','release_journal_id','approved_at','created_at','request_id','single_operator_exception','preview_coin_amount','preview_fiat_amount','preview_fiat_currency'] as const
function optionalText(value: unknown, max = 256): string { return value === null || value === '' ? '' : commerceText(value, max) }
function optionalUUID(value: unknown): string { return value === null || value === '' ? '' : commerceUUID(value) }
function optionalTime(value: unknown): string | null { return value === null ? null : commerceTime(value) }
function optionalDecimal(value: unknown): string | null { return value === null ? null : commerceDecimal(value) }
function optionalCurrency(value: unknown): string { return value === null || value === '' ? '' : commerceText(value, 3) }
function bool(value: unknown): boolean { if (typeof value !== 'boolean') throw new Error('现金退款状态字段无效。'); return value }
export function parseCashRefund(value: unknown, withTenantName = false): CashRefundOrder {
  const raw = commerceObject(value, withTenantName ? [...fields, 'tenant_name'] : fields)
  const state = raw.state as CashRefundState
  if (!cashRefundStates.includes(state)) throw new Error('现金退款状态无效。')
  const row: CashRefundOrder = {
    id: commerceUUID(raw.id), tenant_id: commerceUUID(raw.tenant_id), lot_id: commerceUUID(raw.lot_id), business_ref: commerceReference(raw.business_ref), coin_amount: optionalDecimal(raw.coin_amount), reason: commerceText(raw.reason, 200), state,
    requested_by: commerceUUID(raw.requested_by), maker: commerceUUID(raw.maker), approver: optionalUUID(raw.approver), transfer_executor: optionalUUID(raw.transfer_executor), fiat_currency: optionalCurrency(raw.fiat_currency), fiat_amount: optionalDecimal(raw.fiat_amount),
    buy_rate_version_id: optionalUUID(raw.buy_rate_version_id), refund_rate_version_id: optionalUUID(raw.refund_rate_version_id), single_operator: bool(raw.single_operator), transfer_order_ref: optionalText(raw.transfer_order_ref), transfer_proof_ref: optionalText(raw.transfer_proof_ref), paid_fiat_amount: optionalDecimal(raw.paid_fiat_amount),
    hold_journal_id: optionalUUID(raw.hold_journal_id), retire_journal_id: optionalUUID(raw.retire_journal_id), release_journal_id: optionalUUID(raw.release_journal_id), approved_at: optionalTime(raw.approved_at), created_at: commerceTime(raw.created_at), request_id: optionalUUID(raw.request_id), single_operator_exception: bool(raw.single_operator_exception),
    preview_coin_amount: optionalDecimal(raw.preview_coin_amount), preview_fiat_amount: optionalDecimal(raw.preview_fiat_amount), preview_fiat_currency: optionalCurrency(raw.preview_fiat_currency),
  }
  if (withTenantName) row.tenant_name = commerceText(raw.tenant_name, 200)
  if (state === 'requested' || state === 'cancelled' && !row.hold_journal_id) {
    if (row.coin_amount !== null || row.fiat_amount !== null || row.approved_at !== null || row.approver || row.transfer_executor || row.buy_rate_version_id || row.refund_rate_version_id || row.single_operator || row.hold_journal_id || row.retire_journal_id || row.release_journal_id) throw new Error('未批准现金退款不得包含冻结事实。')
  } else {
    if (row.coin_amount === null || row.fiat_amount === null || row.approved_at === null || !row.approver || !row.buy_rate_version_id || !row.refund_rate_version_id || !row.fiat_currency || !row.hold_journal_id) throw new Error('已批准现金退款缺少冻结事实。')
    if (state !== 'approved' && state !== 'cancelled' && !row.transfer_executor) throw new Error('现金退款缺少转账执行人。')
    if (state === 'transfer_executed' || state === 'completed') {
      if (!row.transfer_order_ref) throw new Error('现金退款缺少转账指令编号。')
      if (state === 'completed' && (!row.transfer_proof_ref || row.paid_fiat_amount !== row.fiat_amount || !row.retire_journal_id)) throw new Error('现金退款完成事实不完整。')
    }
    if (state === 'cancelled' && !row.release_journal_id || state === 'failed' && !row.release_journal_id) throw new Error('现金退款释放事实不完整。')
    if (state !== 'completed' && row.retire_journal_id || state === 'completed' && row.release_journal_id) throw new Error('现金退款退出与释放事实互斥。')
    if (state !== 'completed' && (row.transfer_proof_ref || row.paid_fiat_amount !== null)) throw new Error('未完成现金退款不得包含完成凭证。')
    if (state === 'approved' && (row.transfer_executor || row.transfer_order_ref)) throw new Error('现金退款批准阶段不得包含执行事实。')
  }
  if (state === 'requested' && (row.preview_coin_amount === null || row.preview_fiat_amount === null || !row.preview_fiat_currency) || state !== 'requested' && (row.preview_coin_amount !== null || row.preview_fiat_amount !== null || row.preview_fiat_currency)) throw new Error('现金退款预览与状态不一致。')
  return row
}
export function parseCashRefundPage(data: unknown, next: unknown): { rows: CashRefundOrder[]; next: string } {
  if (!Array.isArray(data) || data.length > 50 || typeof next !== 'string' || next.length > 512 || next && !/^[A-Za-z0-9_-]+$/.test(next)) throw new Error('现金退款分页响应无效。')
  const rows = data.map(row => parseCashRefund(row, true))
  if (new Set(rows.map(row => row.id)).size !== rows.length || next && rows.length !== 50) throw new Error('现金退款分页含重复记录或无效下一页。')
  return { rows, next }
}
export function cashRefundsPath(filters: { tenantID?: string; state?: string; businessRef?: string; cursor?: string } = {}): string {
  const query = new URLSearchParams()
  if (filters.tenantID) query.set('tenant_id', commerceUUID(filters.tenantID))
  if (filters.state) { if (!cashRefundStates.includes(filters.state as CashRefundState)) throw new Error('现金退款筛选状态无效。'); query.set('state', filters.state) }
  if (filters.businessRef) query.set('business_ref', commerceReference(filters.businessRef))
  if (filters.cursor) { if (filters.cursor.length > 512 || !/^[A-Za-z0-9_-]+$/.test(filters.cursor)) throw new Error('现金退款游标无效。'); query.set('cursor', filters.cursor) }
  return `/platform/cash-refunds${query.size ? `?${query}` : ''}`
}
export function cashRefundActionPath(id: string, action: CashRefundAction): string {
  if (!['approve', 'execute', 'complete', 'cancel', 'fail'].includes(action)) throw new Error('现金退款动作无效。')
  return `/platform/cash-refunds/${commerceUUID(id)}/${action}`
}
export function cashRefundBusinessPath(reference: string): string { return `/platform/cash-refunds/by-business/${encodeURIComponent(commerceReference(reference))}` }
export function cashRefundCreateBody(input: unknown): { tenant_id: string; lot_id: string; business_ref: string; reason: string; request_id: string } {
  const raw = commerceObject(input, ['tenant_id', 'lot_id', 'business_ref', 'reason', 'request_id'])
  return { tenant_id: commerceUUID(raw.tenant_id), lot_id: commerceUUID(raw.lot_id), business_ref: commerceReference(raw.business_ref), reason: commerceText(raw.reason, 200), request_id: optionalUUID(raw.request_id) }
}
export function cashRefundCompleteBody(input: unknown): { transfer_proof_ref: string; paid_fiat_amount: string } {
  const raw = commerceObject(input, ['transfer_proof_ref', 'paid_fiat_amount'])
  return { transfer_proof_ref: commerceReference(raw.transfer_proof_ref), paid_fiat_amount: commerceDecimal(raw.paid_fiat_amount) }
}
export function cashRefundExecuteBody(input: unknown): { transfer_order_ref: string } {
  const raw = commerceObject(input, ['transfer_order_ref'])
  return { transfer_order_ref: commerceReference(raw.transfer_order_ref) }
}
export function cashRefundReasonBody(input: unknown): { reason: string } {
  const raw = commerceObject(input, ['reason'])
  return { reason: commerceText(raw.reason, 200) }
}
