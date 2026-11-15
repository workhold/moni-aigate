import { commerceObject, commerceText, commerceUUID } from './commerce.ts'

export type WalletRequestKind = 'topup' | 'cash_refund'
export type WalletRequestState = 'open' | 'linked' | 'closed'
export interface WalletRequest {
  id: string
  tenant_id: string
  kind: WalletRequestKind
  requested_by: string
  note: string
  state: WalletRequestState
  linked_order_id: string | null
  created_at: string
}

function requestTime(value: unknown): string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) throw new Error('钱包申请时间无效。')
  const [whole, fraction = ''] = value.slice(0, -1).split('.')
  const milliseconds = `${whole}.${fraction.padEnd(3, '0').slice(0, 3)}Z`, date = new Date(milliseconds)
  if (!Number.isFinite(date.getTime()) || date.toISOString() !== milliseconds) throw new Error('钱包申请时间无效。')
  return value
}
function cursor(value: unknown): string {
  if (typeof value !== 'string' || value.length > 512 || value !== '' && !/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('钱包申请分页游标无效。')
  return value
}
export function walletRequestsPath(page = ''): string { return `/wallet/requests${cursor(page) ? `?cursor=${encodeURIComponent(page)}` : ''}` }
export function walletRequestLabel(kind: WalletRequestKind): string { return kind === 'topup' ? '充值申请' : '现金退款申请' }
export function walletRequestStateLabel(state: WalletRequestState): string { return { open: '待财务核对', linked: '已关联，待审批', closed: '已关闭' }[state] }

export function parseWalletRequest(value: unknown, tenantID: string): WalletRequest {
  const raw = commerceObject(value, ['id', 'tenant_id', 'kind', 'requested_by', 'note', 'state', 'linked_order_id', 'created_at'])
  if (!['topup', 'cash_refund'].includes(raw.kind as string) || !['open', 'linked', 'closed'].includes(raw.state as string)) throw new Error('钱包申请类型或状态无效。')
  const note = commerceText(raw.note, 1000)
  if (note.trim() !== note) throw new Error('钱包申请说明含有首尾空白。')
  const result: WalletRequest = {
    id: commerceUUID(raw.id), tenant_id: commerceUUID(raw.tenant_id), kind: raw.kind as WalletRequestKind,
    requested_by: commerceUUID(raw.requested_by), note, state: raw.state as WalletRequestState,
    linked_order_id: raw.linked_order_id === null ? null : commerceUUID(raw.linked_order_id), created_at: requestTime(raw.created_at),
  }
  if (result.tenant_id !== commerceUUID(tenantID)) throw new Error('钱包申请不属于当前租户。')
  if (result.state === 'open' && result.linked_order_id !== null || result.state === 'linked' && result.linked_order_id === null) throw new Error('钱包申请状态与关联单据不一致。')
  return result
}
export function parseWalletRequestPage(data: unknown, next: unknown, tenantID: string): { rows: WalletRequest[]; next: string } {
  if (!Array.isArray(data) || data.length > 50) throw new Error('钱包申请分页响应无效。')
  const rows = data.map(item => parseWalletRequest(item, tenantID)), nextCursor = cursor(next)
  if (new Set(rows.map(row => row.id)).size !== rows.length || nextCursor && rows.length !== 50) throw new Error('钱包申请分页含重复记录或无效续页。')
  return { rows, next: nextCursor }
}
