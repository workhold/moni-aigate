import { cnyToMicro } from './format.ts'

export const commerceKinds = ['voucher.batch.create', 'voucher.batch.approve', 'voucher.batch.void', 'voucher.revocation.create', 'voucher.revocation.approve', 'voucher.redeem'] as const
export type CommerceKind = typeof commerceKinds[number]
export interface CommerceExpectation { operation_id: string; kind: CommerceKind; scope: string; actor: string }
export interface CommerceReceipt extends CommerceExpectation { status: 'accepted' | 'rejected'; reason: string; resource_id: string | null; amount: string | null; funding_receipt_id: string | null; journal_id: string | null; created_at: string }
export interface CommerceCapabilities { vouchers_enabled: boolean; payments_mode: 'off' | 'sandbox' }
export interface VoucherPolicy { enabled: boolean; revision: string; effective_enabled: boolean; runtime_allowed: boolean }
export interface VoucherBatch { id: string; state: 'proposed' | 'active' | 'voided' | 'revoked'; face_amount: string; count: number; expires_at: string; purpose_ref: string; reason: string; maker: string; approver: string | null; activated_at: string | null; terminal_actor: string | null; terminal_at: string | null; revision: string; redeemed_count: string; remaining_count: string; created_at: string; expired: boolean; observed_at: string }
export interface VoucherDecision { id: string; operation_id: string; status: 'accepted' | 'rejected'; reason: string; approver: string; decided_at: string }
export interface VoucherRevocation { id: string; batch_id: string; expected_revision: string; expected_redeemed_count: string; reason: string; maker: string; created_at: string; decision: VoucherDecision | null }
export const fundingSourceKinds = ['manual_credit', 'voucher_grant', 'sandbox_capture', 'wallet_opening'] as const
export type FundingSourceKind = typeof fundingSourceKinds[number]
export interface FundingReceipt { id: string; tenant_id: string; source_kind: FundingSourceKind; source_id: string; amount: string; currency: 'CNY'; journal_id: string; created_at: string }
export interface FundingSummary { currency: 'CNY'; total_amount: string; manual_credit_amount: string; voucher_grant_amount: string; sandbox_capture_amount: string; wallet_opening_amount: string; as_of: string }
export interface VoucherRedemption { id: string; operation_id: string; tenant_id: string; voucher_id: string; batch_id: string; actor: string; amount: string; currency: 'CNY'; funding_receipt_id: string; journal_id: string; created_at: string }

export function commerceObject(value: unknown, fields: readonly string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('经营响应格式无效，请只读核查原操作。')
  const raw = value as Record<string, unknown>
  if (Object.keys(raw).length !== fields.length || fields.some(field => !Object.hasOwn(raw, field))) throw new Error('经营响应字段与合同不一致，请只读核查原操作。')
  return raw
}
export function commerceUUID(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) || value === '00000000-0000-0000-0000-000000000000') throw new Error('标识须为规范非零 UUID。')
  return value
}
export function commerceDecimal(value: unknown, positive = false, maxDigits = 19): string {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)$/.test(value) || value.length > maxDigits || positive && value === '0' || maxDigits === 19 && BigInt(value) > 9223372036854775807n) throw new Error('金额或版本须为规范十进制字符串。')
  return value
}
function bool(value: unknown): boolean { if (typeof value !== 'boolean') throw new Error('经营状态响应无效。'); return value }
function enumeration<T extends string>(value: unknown, values: readonly T[]): T { if (!values.includes(value as T)) throw new Error('经营状态响应无效。'); return value as T }
function nullable<T>(value: unknown, parser: (value: unknown) => T): T | null { return value === null ? null : parser(value) }
export function commerceText(value: unknown, max = 200): string {
  if (typeof value !== 'string' || value.length === 0 || new TextEncoder().encode(value).length > max || /\p{Cc}/u.test(value)) throw new Error(`请输入不含控制字符的文字，最多 ${max} 字节。`)
  return value
}
export function commerceReference(value: unknown): string {
  const result = commerceText(value, 256)
  if (result.includes('://') || /^(sk-|mg_|bearer)/i.test(result) || !/^[\p{L}\p{Nd}:_./-]+$/u.test(result)) throw new Error('用途请填写受控编号，不填写网址、密钥或凭证正文。')
  return result
}
export function commerceTime(value: unknown, response = true): string {
  if (typeof value !== 'string' || !(response ? /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z$/ : /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$/).test(value)) throw new Error('时间须为 UTC Z 格式，精度不超过六位微秒。')
  const [whole, fraction = ''] = value.slice(0, -1).split('.')
  const micro = fraction.padEnd(6, '0'), milliseconds = `${whole}.${micro.slice(0, 3)}Z`
  const date = new Date(milliseconds)
  if (!Number.isFinite(date.getTime()) || date.toISOString() !== milliseconds) throw new Error('UTC 日期或时间无效。')
  return `${whole}.${micro}Z`
}
export function parseCommerceCapabilities(value: unknown): CommerceCapabilities {
  const raw = commerceObject(value, ['vouchers_enabled', 'payments_mode'])
  return { vouchers_enabled: bool(raw.vouchers_enabled), payments_mode: enumeration(raw.payments_mode, ['off', 'sandbox']) }
}
export function parseVoucherPolicy(value: unknown): VoucherPolicy {
  const raw = commerceObject(value, ['enabled', 'revision', 'effective_enabled', 'runtime_allowed'])
  const result = { enabled: bool(raw.enabled), revision: commerceDecimal(raw.revision, true), effective_enabled: bool(raw.effective_enabled), runtime_allowed: bool(raw.runtime_allowed) }
  if (result.effective_enabled !== (result.enabled && result.runtime_allowed)) throw new Error('兑换开关响应不一致。')
  return result
}
export const commerceReasonLabels: Record<string, string> = { accepted: '操作已确认', vouchers_disabled: '兑换能力已关闭，未应用', voucher_unavailable: '兑换券不可用，未入账', funding_amount_overflow: '资金金额超限，未入账', voucher_revision_conflict: '批次版本已变化，未应用', voucher_state_conflict: '当前状态不允许，未应用', voucher_evidence_stale: '审批证据已变化，未应用' }
const rejectionReasons: Record<CommerceKind, readonly string[]> = {
  'voucher.batch.create': ['vouchers_disabled', 'voucher_state_conflict'], 'voucher.batch.approve': ['voucher_revision_conflict', 'voucher_state_conflict'], 'voucher.batch.void': ['voucher_revision_conflict', 'voucher_state_conflict'],
  'voucher.revocation.create': ['voucher_revision_conflict', 'voucher_evidence_stale', 'voucher_state_conflict'], 'voucher.revocation.approve': ['voucher_evidence_stale', 'voucher_state_conflict'], 'voucher.redeem': ['voucher_unavailable', 'vouchers_disabled', 'funding_amount_overflow'],
}
export function parseCommerceReceipt(value: unknown, expected?: Partial<CommerceExpectation>): CommerceReceipt {
  const raw = commerceObject(value, ['operation_id', 'kind', 'scope', 'actor', 'status', 'reason', 'resource_id', 'amount', 'funding_receipt_id', 'journal_id', 'created_at'])
  const kind = enumeration(raw.kind, commerceKinds), scope = raw.scope === 'platform' ? 'platform' : commerceUUID(raw.scope)
  const result: CommerceReceipt = { operation_id: commerceUUID(raw.operation_id), kind, scope, actor: commerceUUID(raw.actor), status: enumeration(raw.status, ['accepted', 'rejected']), reason: commerceText(raw.reason), resource_id: nullable(raw.resource_id, commerceUUID), amount: nullable(raw.amount, v => commerceDecimal(v, true)), funding_receipt_id: nullable(raw.funding_receipt_id, commerceUUID), journal_id: nullable(raw.journal_id, commerceUUID), created_at: commerceTime(raw.created_at) }
  if (kind === 'voucher.redeem' ? scope === 'platform' : scope !== 'platform') throw new Error('操作回执作用域不一致。')
  if (expected && Object.entries(expected).some(([key, item]) => result[key as keyof CommerceExpectation] !== item)) throw new Error('操作、身份、种类或租户不匹配，请只读核查原回执。')
  if (result.status === 'rejected') {
    if (!rejectionReasons[kind].includes(result.reason) || [result.resource_id, result.amount, result.funding_receipt_id, result.journal_id].some(v => v !== null)) throw new Error('拒绝回执含有不允许的关联或原因。')
  } else if (result.reason !== 'accepted' || result.resource_id === null || (kind === 'voucher.redeem' ? [result.amount, result.funding_receipt_id, result.journal_id].some(v => v === null) : [result.amount, result.funding_receipt_id, result.journal_id].some(v => v !== null))) throw new Error('成功回执的资金关联不完整。')
  return result
}
export function parseVoucherBatch(value: unknown, expectedID?: string): VoucherBatch {
  const raw = commerceObject(value, ['id', 'state', 'face_amount', 'count', 'expires_at', 'purpose_ref', 'reason', 'maker', 'approver', 'activated_at', 'terminal_actor', 'terminal_at', 'revision', 'redeemed_count', 'remaining_count', 'created_at', 'expired', 'observed_at'])
  if (typeof raw.count !== 'number' || !Number.isInteger(raw.count) || raw.count < 1 || raw.count > 100) throw new Error('批次数量无效。')
  const result: VoucherBatch = { id: commerceUUID(raw.id), state: enumeration(raw.state, ['proposed', 'active', 'voided', 'revoked']), face_amount: commerceDecimal(raw.face_amount, true), count: raw.count, expires_at: commerceTime(raw.expires_at), purpose_ref: commerceReference(raw.purpose_ref), reason: commerceText(raw.reason), maker: commerceUUID(raw.maker), approver: nullable(raw.approver, commerceUUID), activated_at: nullable(raw.activated_at, commerceTime), terminal_actor: nullable(raw.terminal_actor, commerceUUID), terminal_at: nullable(raw.terminal_at, commerceTime), revision: commerceDecimal(raw.revision, true), redeemed_count: commerceDecimal(raw.redeemed_count), remaining_count: commerceDecimal(raw.remaining_count), created_at: commerceTime(raw.created_at), expired: bool(raw.expired), observed_at: commerceTime(raw.observed_at) }
  if (expectedID && result.id !== expectedID || BigInt(result.face_amount) * BigInt(result.count) > 9223372036854775807n || BigInt(result.redeemed_count) + BigInt(result.remaining_count) !== BigInt(result.count) || result.expired !== (result.expires_at <= result.observed_at)) throw new Error('批次身份、数量或观察时间不一致。')
  const active = result.state === 'active' || result.state === 'revoked', terminal = result.state === 'voided' || result.state === 'revoked'
  if (active !== (result.approver !== null) || active !== (result.activated_at !== null) || terminal !== (result.terminal_actor !== null) || terminal !== (result.terminal_at !== null) || result.approver === result.maker || result.state === 'voided' && result.terminal_actor !== result.maker) throw new Error('批次审批状态不一致。')
  return result
}
export function parseVoucherRevocation(value: unknown, batchID?: string): VoucherRevocation {
  const raw = commerceObject(value, ['id', 'batch_id', 'expected_revision', 'expected_redeemed_count', 'reason', 'maker', 'created_at', 'decision'])
  let decision: VoucherDecision | null = null
  if (raw.decision !== null) {
    const d = commerceObject(raw.decision, ['id', 'operation_id', 'status', 'reason', 'approver', 'decided_at'])
    decision = { id: commerceUUID(d.id), operation_id: commerceUUID(d.operation_id), status: enumeration(d.status, ['accepted', 'rejected']), reason: commerceText(d.reason), approver: commerceUUID(d.approver), decided_at: commerceTime(d.decided_at) }
    if (decision.reason !== (decision.status === 'accepted' ? 'accepted' : 'voucher_evidence_stale')) throw new Error('撤销审批决定无效。')
  }
  const result: VoucherRevocation = { id: commerceUUID(raw.id), batch_id: commerceUUID(raw.batch_id), expected_revision: commerceDecimal(raw.expected_revision, true), expected_redeemed_count: commerceDecimal(raw.expected_redeemed_count), reason: commerceText(raw.reason), maker: commerceUUID(raw.maker), created_at: commerceTime(raw.created_at), decision }
  if (batchID && result.batch_id !== batchID || decision?.approver === result.maker) throw new Error('撤销提案身份或独立审批不一致。')
  return result
}
export function parseFundingReceipt(value: unknown, tenantID: string): FundingReceipt {
  const raw = commerceObject(value, ['id', 'tenant_id', 'source_kind', 'source_id', 'amount', 'currency', 'journal_id', 'created_at'])
  if (typeof raw.source_id !== 'string' || !raw.source_id.length) throw new Error('原资金来源标识无效。')
  const result: FundingReceipt = { id: commerceUUID(raw.id), tenant_id: commerceUUID(raw.tenant_id), source_kind: enumeration(raw.source_kind, fundingSourceKinds), source_id: raw.source_id, amount: commerceDecimal(raw.amount, true), currency: enumeration(raw.currency, ['CNY']), journal_id: commerceUUID(raw.journal_id), created_at: commerceTime(raw.created_at) }
  if (result.tenant_id !== tenantID) throw new Error('资金来源不属于当前租户。')
  if (result.source_kind !== 'manual_credit') commerceUUID(result.source_id)
  return result
}
export function fundingSourceLabel(kind: FundingSourceKind): string {
  switch (kind) {
    case 'manual_credit': return '人工核款'
    case 'voucher_grant': return '运营赠额'
    case 'sandbox_capture': return '本地模拟入账'
    case 'wallet_opening': return '初始余额导入（非现金）'
    default: throw new Error('资金来源类型无效。')
  }
}
export function parseFundingSummary(value: unknown): FundingSummary {
  const raw = commerceObject(value, ['currency', 'total_amount', 'manual_credit_amount', 'voucher_grant_amount', 'sandbox_capture_amount', 'wallet_opening_amount', 'as_of'])
  const result: FundingSummary = { currency: enumeration(raw.currency, ['CNY']), total_amount: commerceDecimal(raw.total_amount, false, 100), manual_credit_amount: commerceDecimal(raw.manual_credit_amount, false, 100), voucher_grant_amount: commerceDecimal(raw.voucher_grant_amount, false, 100), sandbox_capture_amount: commerceDecimal(raw.sandbox_capture_amount, false, 100), wallet_opening_amount: commerceDecimal(raw.wallet_opening_amount, false, 100), as_of: commerceTime(raw.as_of) }
  if (BigInt(result.total_amount) !== BigInt(result.manual_credit_amount) + BigInt(result.voucher_grant_amount) + BigInt(result.sandbox_capture_amount) + BigInt(result.wallet_opening_amount)) throw new Error('资金来源摘要合计不一致。')
  return result
}
export function fundingReceiptsPath(sourceKind = '', cursor = ''): string {
  if (sourceKind) enumeration(sourceKind, fundingSourceKinds)
  if (cursor && (cursor.length > 1024 || !/^[A-Za-z0-9_-]+$/.test(cursor))) throw new Error('来源分页游标无效。')
  const query = new URLSearchParams()
  if (sourceKind) query.set('source_kind', sourceKind)
  if (cursor) query.set('cursor', cursor)
  return `/wallet/funding-receipts${query.size ? `?${query}` : ''}`
}
export function parseFundingReceiptsPage(value: unknown, next: unknown, tenantID: string, sourceKind = '', cursor = ''): { rows: FundingReceipt[]; next: string } {
  if (sourceKind) enumeration(sourceKind, fundingSourceKinds)
  const page = parseCommercePage(value, next, raw => parseFundingReceipt(raw, tenantID))
  if (page.rows.some(row => sourceKind && row.source_kind !== sourceKind) || cursor && page.next === cursor) throw new Error('来源筛选或分页响应不一致。')
  return page
}
export function parseVoucherRedemption(value: unknown, tenantID: string, expectedID: string): VoucherRedemption {
  const raw = commerceObject(value, ['id', 'operation_id', 'tenant_id', 'voucher_id', 'batch_id', 'actor', 'amount', 'currency', 'funding_receipt_id', 'journal_id', 'created_at'])
  const result: VoucherRedemption = { id: commerceUUID(raw.id), operation_id: commerceUUID(raw.operation_id), tenant_id: commerceUUID(raw.tenant_id), voucher_id: commerceUUID(raw.voucher_id), batch_id: commerceUUID(raw.batch_id), actor: commerceUUID(raw.actor), amount: commerceDecimal(raw.amount, true), currency: enumeration(raw.currency, ['CNY']), funding_receipt_id: commerceUUID(raw.funding_receipt_id), journal_id: commerceUUID(raw.journal_id), created_at: commerceTime(raw.created_at) }
  if (result.id !== expectedID || result.tenant_id !== tenantID) throw new Error('兑换明细身份不一致。')
  return result
}
export function parseCommercePage<T extends { id: string }>(value: unknown, next: unknown, parser: (raw: unknown) => T): { rows: T[]; next: string } {
  if (!Array.isArray(value) || value.length > 50 || next !== null && (typeof next !== 'string' || !next || next.length > 1024 || !/^[A-Za-z0-9_-]+$/.test(next))) throw new Error('分页响应无效，每页须不超过 50 条且游标符合合同。')
  const rows = value.map(parser)
  if (new Set(rows.map(row => row.id)).size !== rows.length) throw new Error('分页含有重复记录。')
  return { rows, next: next === null ? '' : next as string }
}
export function voucherCode(value: unknown): string {
  if (typeof value !== 'string' || value.length !== 85 || !/^mav1\.[0-9a-f-]{36}\.[A-Za-z0-9_-]{43}$/.test(value)) throw new Error('兑换券格式无效，请完整粘贴，不能包含空白。')
  const [, id, secret] = value.split('.')
  commerceUUID(id)
  try { const bytes = atob(secret!.replace(/-/g, '+').replace(/_/g, '/') + '='); if (bytes.length !== 32 || btoa(bytes).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_') !== secret) throw new Error() }
  catch { throw new Error('兑换券编码无效。') }
  return value
}
export interface VoucherDraft { face: string; count: string; expires_at: string; purpose_ref: string; reason: string; code: string }
export function commerceWrite(kind: CommerceKind, operationID: string, draft: VoucherDraft, batch: VoucherBatch | null, proposal: VoucherRevocation | null) {
  const operation_id = commerceUUID(operationID)
  if (kind === 'voucher.batch.create') {
    const face_amount = commerceDecimal(cnyToMicro(draft.face), true)
    if (!/^[1-9][0-9]{0,2}$/.test(draft.count) || BigInt(draft.count) > 100n || BigInt(face_amount) * BigInt(draft.count) > 9223372036854775807n) throw new Error('批次须为 1 至 100 张且总额不超过 int64。')
    return { path: '/platform/voucher-batches', body: { operation_id, face_amount, count: Number(draft.count), expires_at: commerceTime(draft.expires_at, false), purpose_ref: commerceReference(draft.purpose_ref), reason: commerceText(draft.reason) } }
  }
  if (kind === 'voucher.redeem') return { path: '/wallet/voucher-redemptions', body: { operation_id, code: voucherCode(draft.code) } }
  if (kind === 'voucher.revocation.approve') {
    if (!proposal || proposal.decision) throw new Error('请读取尚未审批的撤销提案。')
    return { path: `/platform/voucher-revocations/${commerceUUID(proposal.id)}/approve`, body: { operation_id } }
  }
  if (!batch) throw new Error('请先读取原批次。')
  const path = `/platform/voucher-batches/${commerceUUID(batch.id)}`, expected_revision = commerceDecimal(batch.revision, true)
  if (kind === 'voucher.batch.approve') return { path: `${path}/approve`, body: { operation_id, expected_revision } }
  if (kind === 'voucher.batch.void') return { path: `${path}/void`, body: { operation_id, expected_revision, reason: commerceText(draft.reason) } }
  return { path: `${path}/revocations`, body: { operation_id, expected_revision, expected_redeemed_count: commerceDecimal(batch.redeemed_count), reason: commerceText(draft.reason) } }
}
export interface CommerceCreateResult { receipt: CommerceReceipt; batch: VoucherBatch | null; codes: { voucher_id: string; code: string }[] }
export function parseCommerceWriteResult(value: unknown, expected: CommerceExpectation): CommerceCreateResult {
  if (!value || typeof value !== 'object') throw new Error('操作响应无效。')
  const envelope = value as Record<string, unknown>, receipt = parseCommerceReceipt(envelope.data, expected)
  if (expected.kind !== 'voucher.batch.create') { commerceObject(envelope, ['data', 'request_id']); return { receipt, batch: null, codes: [] } }
  if (typeof envelope.secrets_available !== 'boolean') throw new Error('一次展示状态无效。')
  if (receipt.status === 'rejected') { commerceObject(envelope, ['data', 'secrets_available', 'request_id']); if (envelope.secrets_available) throw new Error('拒绝操作不能包含兑换券。'); return { receipt, batch: null, codes: [] } }
  const batch = parseVoucherBatch(envelope.batch, receipt.resource_id!)
  if (envelope.secrets_available === false) { commerceObject(envelope, ['data', 'batch', 'secrets_available', 'request_id']); return { receipt, batch, codes: [] } }
  commerceObject(envelope, ['data', 'batch', 'codes', 'secrets_available', 'request_id'])
  if (!Array.isArray(envelope.codes) || envelope.codes.length !== batch.count) throw new Error('一次展示的兑换券数量不完整。')
  const codes = envelope.codes.map(value => { const item = commerceObject(value, ['voucher_id', 'code']); const id = commerceUUID(item.voucher_id), code = voucherCode(item.code); if (code.split('.')[1] !== id) throw new Error('兑换券身份不一致。'); return { voucher_id: id, code } })
  if (new Set(codes.map(code => code.voucher_id)).size !== codes.length) throw new Error('一次展示含有重复兑换券。')
  return { receipt, batch, codes }
}
