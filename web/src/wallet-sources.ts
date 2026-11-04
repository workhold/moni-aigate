import { errorText, request } from './api.ts'
import type { Envelope } from './api.ts'
import { commerceDecimal, commerceObject, commerceUUID } from './commerce.ts'
import { createCommerceScope } from './commerce-state.ts'

export type WalletSourceKind = 'manual_credit' | 'voucher_grant' | 'sandbox_capture' | 'wallet_opening'
export type WalletRefundClass = 'nonrefundable' | 'sandbox_original'
export interface WalletSourceRow {
  id: string; lot_id: string; source_kind: WalletSourceKind; refund_class: WalletRefundClass
  issued_amount: string; available_amount: string; held_amount: string; consumed_amount: string; created_at: string
}
export interface WalletSourcesTarget { platform?: boolean; tenantID?: string }
export interface WalletSourcesAccess extends WalletSourcesTarget {
  authenticated: boolean; blocked?: boolean; tenantRole?: string; platformRole?: string
}
export interface WalletSourcesState {
  rows: WalletSourceRow[]; loading: boolean; error: string; cursor: string; nextCursor: string; history: string[]
}
type Transport = (path: string, isCurrent: () => boolean) => Promise<Envelope<unknown>>
const fields = ['id', 'lot_id', 'source_kind', 'refund_class', 'issued_amount', 'available_amount', 'held_amount', 'consumed_amount', 'created_at'] as const
const kinds: readonly WalletSourceKind[] = ['manual_credit', 'voucher_grant', 'sandbox_capture', 'wallet_opening']

export function canReadWalletSources(access: WalletSourcesAccess): boolean {
  if (!access.authenticated || access.blocked) return false
  return access.platform
    ? ['admin', 'finance_operator', 'finance_approver'].includes(access.platformRole || '')
    : !!access.tenantID && ['owner', 'billing_viewer'].includes(access.tenantRole || '')
}
export function walletSourceLabel(kind: WalletSourceKind): string {
  return { manual_credit: '人工核款', voucher_grant: '运营赠额', sandbox_capture: '本地模拟入账', wallet_opening: '初始余额导入' }[kind]
}
export function walletRefundLabel(refundClass: WalletRefundClass): string {
  return refundClass === 'sandbox_original' ? '仅本地支付沙箱' : '不可原路退款'
}
function utcTime(value: unknown): string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) throw new Error('资金来源时间须为有效的 UTC RFC3339 格式。')
  const [whole, fraction = ''] = value.slice(0, -1).split('.')
  const milliseconds = `${whole}.${fraction.padEnd(3, '0').slice(0, 3)}Z`, date = new Date(milliseconds)
  if (!Number.isFinite(date.getTime()) || date.toISOString() !== milliseconds) throw new Error('资金来源时间无效。')
  return value
}
function cursorValue(value: unknown): string {
  if (typeof value !== 'string' || value.length > 512 || value !== '' && !/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('资金来源分页游标无效。')
  return value
}
export function walletSourcesPath(target: WalletSourcesTarget, cursor = ''): string {
  const query = new URLSearchParams()
  if (target.platform) query.set('tenant_id', commerceUUID(target.tenantID))
  if (cursorValue(cursor)) query.set('cursor', cursor)
  return `${target.platform ? '/platform/wallet-sources' : '/wallet/sources'}${query.size ? `?${query}` : ''}`
}
export function parseWalletSource(value: unknown): WalletSourceRow {
  const raw = commerceObject(value, fields)
  if (!kinds.includes(raw.source_kind as WalletSourceKind) || !['nonrefundable', 'sandbox_original'].includes(raw.refund_class as string)) throw new Error('资金来源类型或退款类别无效。')
  const source_kind = raw.source_kind as WalletSourceKind, refund_class = raw.refund_class as WalletRefundClass
  if ((source_kind === 'sandbox_capture') !== (refund_class === 'sandbox_original')) throw new Error('资金来源与退款类别不一致。')
  const row: WalletSourceRow = {
    id: commerceUUID(raw.id), lot_id: commerceUUID(raw.lot_id), source_kind, refund_class,
    issued_amount: commerceDecimal(raw.issued_amount, true), available_amount: commerceDecimal(raw.available_amount),
    held_amount: commerceDecimal(raw.held_amount), consumed_amount: commerceDecimal(raw.consumed_amount), created_at: utcTime(raw.created_at),
  }
  if (row.id !== row.lot_id) throw new Error('资金来源与批次标识不一致。')
  if (BigInt(row.issued_amount) !== BigInt(row.available_amount) + BigInt(row.held_amount) + BigInt(row.consumed_amount)) throw new Error('资金来源本金与可用、冻结、已消费金额不一致。')
  return row
}
export function parseWalletSourcesPage(data: unknown, nextCursor: unknown): { rows: WalletSourceRow[]; nextCursor: string } {
  if (!Array.isArray(data) || data.length > 50) throw new Error('资金来源分页响应无效，每页最多 50 条。')
  const rows = data.map(parseWalletSource), next = cursorValue(nextCursor)
  if (new Set(rows.map(row => row.id)).size !== rows.length || new Set(rows.map(row => row.lot_id)).size !== rows.length || next && rows.length !== 50) throw new Error('资金来源分页含重复记录或不完整的下一页信息。')
  return { rows, nextCursor: next }
}

export function createWalletSourcesReader(transport: Transport = (path, isCurrent) => request<unknown>(path, 'GET', undefined, undefined, isCurrent)) {
  const scope = createCommerceScope()
  let disposed = false
  let state: WalletSourcesState = { rows: [], loading: false, error: '', cursor: '', nextCursor: '', history: [] }
  const snapshot = (): WalletSourcesState => ({ ...state, rows: state.rows.map(row => ({ ...row })), history: [...state.history] })
  async function load(target: WalletSourcesTarget, navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
    if (disposed || state.loading) return
    if (navigation === 'next' && !state.nextCursor || navigation === 'previous' && !state.history.length) return
    const previous = state.cursor
    const page = navigation === 'next' ? state.nextCursor : navigation === 'previous' ? state.history.at(-1)! : state.cursor
    const ticket = scope.ticket('sources')
    state = { ...state, rows: [], nextCursor: '', error: '', loading: true }
    try {
      const response = await transport(walletSourcesPath(target, page), () => !disposed && scope.accepts(ticket))
      if (disposed || !scope.accepts(ticket)) return
      const parsed = parseWalletSourcesPage(response.data, response.next_cursor)
      if (page && parsed.nextCursor === page || parsed.nextCursor && state.history.includes(parsed.nextCursor)) throw new Error('资金来源分页没有向前推进，请刷新列表。')
      state = { rows: parsed.rows, loading: false, error: '', cursor: page, nextCursor: parsed.nextCursor, history: navigation === 'next' ? [...state.history, previous] : navigation === 'previous' ? state.history.slice(0, -1) : state.history }
    } catch (cause) {
      if (!disposed && scope.accepts(ticket)) state = { ...state, rows: [], nextCursor: '', loading: false, error: errorText(cause) }
    }
  }
  return {
    snapshot, load,
    reset(context: string) { scope.reset(context); state = { rows: [], loading: false, error: '', cursor: '', nextCursor: '', history: [] } },
    dispose() { disposed = true; scope.reset(); state = { rows: [], loading: false, error: '', cursor: '', nextCursor: '', history: [] } },
  }
}
