import { cnyToMicro } from './format.ts'
import { request } from './api.ts'
import { procurementOperationID } from './procurement-state.ts'
import type { ProcurementCommand } from './procurement-state.ts'

export interface ProcurementAccount {
  id: string; label: string; provider: string; state: 'active' | 'paused'
  limit_amount: string; held_amount: string; spent_amount: string; revision: string; created_at: string
}
export interface ProcurementBinding {
  id: string; credential_id: string; channel_id: string; account_id: string
  operation_id: string; bound_at: string; channel_revision: string
}
export interface ProcurementObservation {
  id: string; account_id: string; operation_id: string; actor: string
  status: 'known' | 'unknown' | 'unsupported' | 'error'; amount: string | null; currency: 'CNY'
  source: string; semantic: string; observed_at: string; provider_as_of: string | null; evidence_ref: string; created_at: string
}
export interface ProcurementReceipt {
  operation_id: string; actor: string; kind: string; target_id: string
  status: 'applied' | 'rejected'; reason: string; created_at: string
}
export interface ProcurementAccountDraft { label: string; provider: string; state: string; limit: string }
export interface ProcurementObservationDraft { status: string; amount: string; source: string; semantic: string; observed_at: string; provider_as_of: string; evidence_ref: string }
export interface ProcurementChannel { id: string; name: string; management_revision: string }
export interface ProcurementReservation {
  id: string; attempt_id: string; account_id: string; binding_id: string; tenant_id: string; request_id: string; channel_id: string
  input_bound: string; output_bound: string; upper_amount: string; held_amount: string; spent_amount: string
  state: 'prepared' | 'sending' | 'unknown' | 'disputed' | 'settled' | 'released'; anomaly: boolean; created_at: string
}
export interface ProcurementEvent { id: string; event_key: string; account_id: string; attempt_id: string; held_delta: string; spent_delta: string; type: string; evidence_ref: string; created_at: string }
export interface ProcurementProposal {
  id: string; tenant_id: string; attempt_id: string; amount: string; basis: 'contract' | 'invoice'; evidence_ref: string; reason: string
  requester: string; state: 'proposed' | 'approved'; approver: string | null; approval_operation_id: string | null; created_at: string; approved_at: string | null; expected_revision: string; expected_evidence_count: string
}
export interface ProcurementProposalDraft { tenant_id: string; attempt_id: string; amount: string; basis: string; evidence_ref: string; reason: string }
export type ProcurementResult = ProcurementReceipt | ProcurementProposal
export type ProcurementWriteKind = 'create' | 'configure' | 'bind' | 'observe' | 'propose' | 'approve'

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('采购响应格式无效。')
  return value as Record<string, unknown>
}
export function procurementText(value: unknown, title: string, max = 256, empty = false): string {
  if (typeof value !== 'string' || (!empty && !value.trim()) || new TextEncoder().encode(value).length > max || /\p{Cc}/u.test(value)) throw new Error(`${title}格式无效。`)
  return value
}
export function procurementIdentity(value: unknown): string { return procurementOperationID(value) }
export function procurementReference(value: unknown): string {
  const text = procurementText(value, '受控证据引用')
  if (text.includes('://') || /^(sk-|mg_|bearer)/i.test(text) || !/^[\p{L}\p{Nd}:_./-]+$/u.test(text)) throw new Error('证据请填受控编号，不填写网址、密钥或带空格的文本。')
  return text
}
function proposalReason(value: unknown): string {
  if (typeof value !== 'string' || !value.length || new TextEncoder().encode(value).length > 200) throw new Error('更正原因须为 1 至 200 字节。')
  return value
}
export function procurementInteger(value: unknown, signed = false, int64 = false): string {
  if (typeof value !== 'string' || !(signed ? /^(0|-?[1-9][0-9]*)$/ : /^(0|[1-9][0-9]*)$/).test(value) || (int64 && BigInt(value) > 9223372036854775807n)) throw new Error('采购金额须为规范整数十进制字符串。')
  return value
}
export function procurementRevision(value: unknown): string {
  const revision = procurementInteger(value, false, true)
  if (revision === '0') throw new Error('采购版本须为正 int64 十进制字符串。')
  return revision
}
export function procurementTimestamp(value: unknown): string {
  if (typeof value !== 'string' || !Number.isFinite(Date.parse(value))) throw new Error('采购时间无效。')
  return value
}
function timestampInput(value: string): string {
  const parsed = new Date(procurementTimestamp(value))
  if (parsed.getUTCFullYear() < 1 || parsed.getUTCFullYear() > 9999) throw new Error('采购时间超出可接受范围。')
  return parsed.toISOString()
}
export function procurementCny(value: unknown): string {
  const amount = BigInt(procurementInteger(value))
  return `${amount / 1000000n}.${(amount % 1000000n).toString().padStart(6, '0')}`
}
function accountState(value: unknown): ProcurementAccount['state'] {
  if (value !== 'active' && value !== 'paused') throw new Error('请选择启用或暂停采购账户。')
  return value
}
function observationStatus(value: unknown): ProcurementObservation['status'] {
  if (!['known', 'unknown', 'unsupported', 'error'].includes(String(value))) throw new Error('采购余额观察状态无效。')
  return value as ProcurementObservation['status']
}
export function parseProcurementAccount(value: unknown, expectedID?: string): ProcurementAccount {
  const row = record(value), id = procurementIdentity(row.id)
  if (expectedID !== undefined && id !== expectedID) throw new Error('采购账户响应对象不一致。')
  return { id, label: procurementText(row.label, '账户名称', 100), provider: procurementText(row.provider, '供应商标识', 100), state: accountState(row.state), limit_amount: procurementInteger(row.limit_amount, false, true), held_amount: procurementInteger(row.held_amount), spent_amount: procurementInteger(row.spent_amount), revision: procurementRevision(row.revision), created_at: procurementTimestamp(row.created_at) }
}
export function parseProcurementChannel(value: unknown): ProcurementChannel {
  const row = record(value)
  return { id: procurementIdentity(row.id), name: procurementText(row.name, '渠道名称', 200), management_revision: procurementRevision(row.management_revision) }
}
export function parseProcurementBinding(value: unknown): ProcurementBinding {
  const row = record(value)
  return { id: procurementIdentity(row.id), credential_id: procurementIdentity(row.credential_id), channel_id: procurementIdentity(row.channel_id), account_id: procurementIdentity(row.account_id), operation_id: procurementOperationID(row.operation_id), bound_at: procurementTimestamp(row.bound_at), channel_revision: procurementRevision(row.channel_revision) }
}
export function parseProcurementObservation(value: unknown): ProcurementObservation {
  const row = record(value), status = observationStatus(row.status)
  if (row.currency !== 'CNY' || (status === 'known') !== (row.amount !== null)) throw new Error('余额观察金额与状态不一致。')
  return { id: procurementIdentity(row.id), account_id: procurementIdentity(row.account_id), operation_id: procurementOperationID(row.operation_id), actor: procurementIdentity(row.actor), status, amount: row.amount === null ? null : procurementInteger(row.amount), currency: 'CNY', source: procurementText(row.source, '观察来源', 100), semantic: procurementText(row.semantic, '余额语义', 100), observed_at: procurementTimestamp(row.observed_at), provider_as_of: row.provider_as_of === null ? null : procurementTimestamp(row.provider_as_of), evidence_ref: procurementText(row.evidence_ref, '证据引用'), created_at: procurementTimestamp(row.created_at) }
}
export function parseProcurementReservation(value: unknown): ProcurementReservation {
  const row = record(value)
  if (!['prepared', 'sending', 'unknown', 'disputed', 'settled', 'released'].includes(String(row.state)) || typeof row.anomaly !== 'boolean') throw new Error('采购预占状态无效。')
  const attempt = procurementIdentity(row.attempt_id)
  if (row.id !== attempt) throw new Error('采购预占与尝试标识不一致。')
  return { id: attempt, attempt_id: attempt, account_id: procurementIdentity(row.account_id), binding_id: procurementIdentity(row.binding_id), tenant_id: procurementIdentity(row.tenant_id), request_id: procurementIdentity(row.request_id), channel_id: procurementIdentity(row.channel_id), input_bound: procurementInteger(row.input_bound), output_bound: procurementInteger(row.output_bound), upper_amount: procurementInteger(row.upper_amount), held_amount: procurementInteger(row.held_amount), spent_amount: procurementInteger(row.spent_amount), state: row.state as ProcurementReservation['state'], anomaly: row.anomaly, created_at: procurementTimestamp(row.created_at) }
}
export function parseProcurementEvent(value: unknown): ProcurementEvent {
  const row = record(value)
  return { id: procurementIdentity(row.id), event_key: procurementText(row.event_key, '事件标识'), account_id: procurementIdentity(row.account_id), attempt_id: procurementText(row.attempt_id, '尝试标识', 200, true), held_delta: procurementInteger(row.held_delta, true), spent_delta: procurementInteger(row.spent_delta, true), type: procurementText(row.type, '事件类型', 100), evidence_ref: procurementText(row.evidence_ref, '事件证据', 256, true), created_at: procurementTimestamp(row.created_at) }
}
export function parseProcurementProposal(value: unknown, expectedID?: string): ProcurementProposal {
  const row = record(value), id = procurementOperationID(row.id)
  if (expectedID !== undefined && id !== procurementOperationID(expectedID)) throw new Error('返回提案与原操作 ID 不一致。')
  if (!['contract', 'invoice'].includes(String(row.basis)) || !['proposed', 'approved'].includes(String(row.state))) throw new Error('采购更正提案状态无效。')
  const approver = row.approver === null ? null : procurementIdentity(row.approver), approvalID = row.approval_operation_id === null ? null : procurementOperationID(row.approval_operation_id), approvedAt = row.approved_at === null ? null : procurementTimestamp(row.approved_at)
  if ((row.state === 'approved') !== !!(approver && approvalID && approvedAt) || row.state === 'proposed' && (approver || approvalID || approvedAt) || approver === row.requester || approvalID === id) throw new Error('采购更正审批身份或状态不一致。')
  return { id, tenant_id: procurementIdentity(row.tenant_id), attempt_id: procurementIdentity(row.attempt_id), amount: procurementInteger(row.amount, false, true), basis: row.basis as ProcurementProposal['basis'], evidence_ref: procurementReference(row.evidence_ref), reason: proposalReason(row.reason), requester: procurementIdentity(row.requester), state: row.state as ProcurementProposal['state'], approver, approval_operation_id: approvalID, created_at: procurementTimestamp(row.created_at), approved_at: approvedAt, expected_revision: procurementRevision(row.expected_revision), expected_evidence_count: procurementInteger(row.expected_evidence_count) }
}
export function parseProcurementReceipt(value: unknown, operationID: string, actor?: string): ProcurementReceipt {
  const row = record(value), id = procurementOperationID(row.operation_id)
  if (id !== procurementOperationID(operationID) || actor !== undefined && row.actor !== actor) throw new Error('返回回执与原操作 ID 或操作账号不一致。')
  if (row.status !== 'applied' && row.status !== 'rejected') throw new Error('原操作尚无可确认的终态回执。')
  // Retain a whitelist only. Before/after snapshots are not needed to establish
  // command attribution and must never expand the browser journal projection.
  return { operation_id: id, actor: procurementIdentity(row.actor), kind: procurementText(row.kind, '操作类型', 100), target_id: procurementIdentity(row.target_id), status: row.status, reason: procurementText(row.reason, '回执原因', 256, true), created_at: procurementTimestamp(row.created_at) }
}
export function parseProcurementPage<T>(value: unknown, next: unknown, parse: (row: unknown) => T): { rows: T[]; next: string } {
  if (!Array.isArray(value) || value.length > 50 || next !== undefined && next !== null && (typeof next !== 'string' || next.length > 512)) throw new Error('采购列表或分页游标无效。')
  return { rows: value.map(row => parse(row)), next: typeof next === 'string' ? next : '' }
}
export async function readProcurementRows<T>(path: string, parse: (row: unknown) => T, active: () => boolean): Promise<T[]> {
  const rows: T[] = [], seen = new Set<string>()
  let cursor = ''
  do {
    if (!active()) throw new Error('采购编辑上下文已关闭。')
    const response = await request<unknown>(`${path}${cursor ? `${path.includes('?') ? '&' : '?'}cursor=${encodeURIComponent(cursor)}` : ''}`), page = parseProcurementPage(response.data, response.next_cursor, parse)
    rows.push(...page.rows)
    if (rows.length > 10000) throw new Error('配置超过本次读取范围，请联系运营核查。')
    cursor = page.next
    if (cursor && seen.has(cursor)) throw new Error('采购分页游标重复，未取得完整配置。')
    seen.add(cursor)
  } while (cursor)
  if (!active()) throw new Error('采购编辑上下文已关闭。')
  return rows
}
export function procurementAccountPath(id: string): string { return `/platform/procurement-accounts/${encodeURIComponent(procurementIdentity(id))}` }
export function procurementReceiptPath(operationID: string): string { return `/platform/procurement-operations/${procurementOperationID(operationID)}` }
function command(operationID: string, target: string, path: string, method: ProcurementCommand['method'], body: Record<string, unknown>): ProcurementCommand {
  const id = procurementOperationID(operationID)
  return Object.freeze({ operationID: id, target, path, method, body: Object.freeze({ operation_id: id, ...body }), lookupPath: procurementReceiptPath(id) })
}
export function procurementAccountCommand(draft: ProcurementAccountDraft, operationID: string, view?: ProcurementAccount): ProcurementCommand {
  const body = { label: procurementText(draft.label.trim(), '账户名称', 100), provider: procurementText(draft.provider.trim(), '供应商标识', 100), state: accountState(draft.state), limit_amount: cnyToMicro(draft.limit) }
  if (!view) return command(operationID, 'account:create', '/platform/procurement-accounts', 'POST', body)
  const account = parseProcurementAccount(view)
  return command(operationID, `account:${account.id}`, `${procurementAccountPath(account.id)}/configuration`, 'PUT', { expected_revision: account.revision, ...body })
}
export function procurementBindingCommand(channel: ProcurementChannel, credentialID: string, accountID: string, operationID: string): ProcurementCommand {
  const channelID = procurementIdentity(channel.id), credential = procurementIdentity(credentialID)
  return command(operationID, `credential:${credential}`, '/platform/procurement-bindings', 'POST', { channel_id: channelID, credential_id: credential, account_id: procurementIdentity(accountID), expected_channel_revision: procurementRevision(channel.management_revision) })
}
export function procurementObservationCommand(accountID: string, draft: ProcurementObservationDraft, operationID: string): ProcurementCommand {
  const id = procurementIdentity(accountID), status = observationStatus(draft.status)
  return command(operationID, `observation:${id}`, `${procurementAccountPath(id)}/balance-observations`, 'POST', { status, amount: status === 'known' ? cnyToMicro(draft.amount) : null, currency: 'CNY', source: procurementText(draft.source.trim(), '观察来源', 100), semantic: procurementText(draft.semantic.trim(), '余额语义', 100), observed_at: timestampInput(draft.observed_at), provider_as_of: draft.provider_as_of.trim() ? timestampInput(draft.provider_as_of) : null, evidence_ref: procurementReference(draft.evidence_ref.trim()) })
}
export function procurementProposalPath(id: string): string { return `/platform/procurement-cost-proposals/${procurementOperationID(id)}` }
export function procurementProposalCommand(draft: ProcurementProposalDraft, operationID: string): ProcurementCommand {
  if (draft.basis !== 'contract' && draft.basis !== 'invoice') throw new Error('更正依据须为合同或账单。减少成本必须提供账单依据。')
  const body = { tenant_id: procurementIdentity(draft.tenant_id.trim()), attempt_id: procurementIdentity(draft.attempt_id.trim()), amount: cnyToMicro(draft.amount), basis: draft.basis, evidence_ref: procurementReference(draft.evidence_ref.trim()), reason: proposalReason(draft.reason.trim()) }
  const value = command(operationID, `proposal:${body.attempt_id}`, '/platform/procurement-cost-proposals', 'POST', body)
  return Object.freeze({ ...value, lookupPath: procurementProposalPath(operationID) })
}
export function procurementApprovalCommand(proposal: ProcurementProposal, actor: string, operationID: string): ProcurementCommand {
  const view = parseProcurementProposal(proposal)
  if (view.state !== 'proposed' || view.requester === actor || view.id === operationID) throw new Error('须由不同的财务审批账号复核待审提案，审批须使用独立操作 ID。')
  const value = command(operationID, `approval:${view.id}`, `${procurementProposalPath(view.id)}/approve`, 'POST', {})
  return Object.freeze({ ...value, lookupPath: procurementProposalPath(view.id) })
}
export function acceptProcurementResult(command: ProcurementCommand, value: unknown, actor: string): ProcurementResult {
  if (command.path === '/platform/procurement-cost-proposals') {
    const proposal = parseProcurementProposal(value, command.operationID), body = command.body
    if (proposal.requester !== actor || ['tenant_id', 'attempt_id', 'amount', 'basis', 'evidence_ref', 'reason'].some(key => proposal[key as keyof ProcurementProposal] !== body[key])) throw new Error('返回提案与原制单内容或操作账号不一致。')
    return proposal
  }
  if (command.path.endsWith('/approve')) {
    const proposalID = command.lookupPath.split('/').at(-1)!, proposal = parseProcurementProposal(value, proposalID)
    if (proposal.state !== 'approved' || proposal.approval_operation_id !== command.operationID || proposal.approver !== actor) throw new Error('当前提案不能证明原审批操作已成功，保留原操作 ID 继续核查。')
    return proposal
  }
  return parseProcurementReceipt(value, command.operationID, actor)
}
