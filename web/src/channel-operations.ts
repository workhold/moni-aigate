import { ApiError } from './api.ts'

export type ChannelOperationAction = 'enable' | 'disable'
export interface ChannelOperationTarget { channel_id: string; expected_version: string; expected_revision: string }
export interface ChannelOperationCommand { action: ChannelOperationAction; targets: ChannelOperationTarget[] }
export interface ChannelOperationState { current_version: string; management_revision: string; enabled: boolean; quarantined: boolean }
export type ChannelOperationItemStatus = 'ready' | 'unchanged' | 'blocked' | 'changed' | 'not_applied'
export type ChannelOperationReason = '' | 'not_found' | 'stale_version' | 'stale_revision' | 'channel_quarantined' | 'no_live_credential'
export interface ChannelOperationItem extends ChannelOperationTarget {
  before: ChannelOperationState | null; after?: ChannelOperationState
  live_credentials: number; published_bindings: number; older_version_bindings: number
  status: ChannelOperationItemStatus; reason: ChannelOperationReason
}
export interface ChannelOperationPreview { action: ChannelOperationAction; can_apply: boolean; changed_count: number; items: ChannelOperationItem[] }
export interface ChannelOperationSummary {
  operation_id: string; actor: string; action: ChannelOperationAction; status: 'applied' | 'rejected'
  target_count: number; changed_count: number; created_at: string
}
export interface ChannelOperationReceipt extends ChannelOperationSummary { items: ChannelOperationItem[] }
export interface SubmittedChannelOperation {
  operation_id: string; command: ChannelOperationCommand; state: 'unknown' | 'resolved' | 'not_submitted'; receipt?: ChannelOperationReceipt
}

const basePath = '/platform/channel-operations'
function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('批量操作响应格式无效。')
  return value as Record<string, unknown>
}
function uuid(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value)) throw new Error('批量操作标识须为规范 UUID。')
  return value
}
export function channelManagementRevision(value: unknown): string {
  if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error('渠道管理 revision 须为有效的正 int64 十进制字符串。')
  return value
}
function action(value: unknown): ChannelOperationAction {
  if (value !== 'enable' && value !== 'disable') throw new Error('请选择批量启用或停用。')
  return value
}
function count(value: unknown, maximum = Number.MAX_SAFE_INTEGER): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new Error('批量操作计数无效。')
  return value
}
function target(value: unknown): ChannelOperationTarget {
  const raw = record(value)
  return { channel_id: uuid(raw.channel_id), expected_version: uuid(raw.expected_version), expected_revision: channelManagementRevision(raw.expected_revision) }
}
export function channelOperationCommand(value: unknown): ChannelOperationCommand {
  const raw = record(value)
  if (!Array.isArray(raw.targets) || raw.targets.length < 1 || raw.targets.length > 100) throw new Error('批量操作须有 1 至 100 个渠道。')
  const targets = raw.targets.map(target).sort((a, b) => a.channel_id.localeCompare(b.channel_id))
  if (new Set(targets.map(item => item.channel_id)).size !== targets.length) throw new Error('批量操作不能包含重复渠道。')
  return { action: action(raw.action), targets }
}
// UI selections are deliberately stricter than the API's 100-target contract:
// every selected identity must belong to the current, visible 50-row page.
export function selectedChannelOperationTargets(visibleRows: Record<string, unknown>[], selectedIDs: string[]): ChannelOperationTarget[] {
  if (visibleRows.length > 50 || selectedIDs.length < 1 || selectedIDs.length > 50 || new Set(selectedIDs).size !== selectedIDs.length) throw new Error('请只选择当前页的 1 至 50 个不同渠道。')
  const rows = new Map(visibleRows.map(row => [String(row.id), row]))
  return selectedIDs.map(id => {
    const row = rows.get(id)
    if (!row) throw new Error('选择已不属于当前显示页，请重新勾选。')
    return target({ channel_id: row.id, expected_version: row.current_version, expected_revision: row.management_revision })
  }).sort((a, b) => a.channel_id.localeCompare(b.channel_id))
}
export function channelOperationPreflightRequest(command: ChannelOperationCommand) {
  return { path: `${basePath}/preflight`, method: 'POST' as const, body: channelOperationCommand(command) }
}
export function channelOperationApplyRequest(command: ChannelOperationCommand, operationID: string) {
  return { path: basePath, method: 'POST' as const, body: { operation_id: uuid(operationID), ...channelOperationCommand(command) } }
}
export function channelOperationHistoryPath(cursor = ''): string {
  if (cursor.length > 512) throw new Error('回执分页游标无效。')
  return `${basePath}${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`
}
export function channelOperationDetailPath(operationID: string): string { return `${basePath}/${encodeURIComponent(uuid(operationID))}` }

function parseState(value: unknown): ChannelOperationState {
  const raw = record(value)
  if (typeof raw.enabled !== 'boolean' || typeof raw.quarantined !== 'boolean') throw new Error('渠道状态快照无效。')
  return { current_version: uuid(raw.current_version), management_revision: channelManagementRevision(raw.management_revision), enabled: raw.enabled, quarantined: raw.quarantined }
}
function parseItems(value: unknown): ChannelOperationItem[] {
  if (!Array.isArray(value) || value.length < 1 || value.length > 100) throw new Error('批量操作明细数量无效。')
  const seen = new Set<string>()
  return value.map(value => {
    const raw = record(value), identity = target(raw)
    if (seen.has(identity.channel_id)) throw new Error('批量操作明细包含重复渠道。')
    seen.add(identity.channel_id)
    if (!['ready', 'unchanged', 'blocked', 'changed', 'not_applied'].includes(String(raw.status)) || !['', 'not_found', 'stale_version', 'stale_revision', 'channel_quarantined', 'no_live_credential'].includes(String(raw.reason))) throw new Error('批量操作明细状态无效。')
    const before = raw.before === null ? null : parseState(raw.before)
    const after = raw.after === undefined ? undefined : parseState(raw.after)
    const item: ChannelOperationItem = { ...identity, before, ...(after ? { after } : {}), status: raw.status as ChannelOperationItemStatus, reason: raw.reason as ChannelOperationReason, live_credentials: count(raw.live_credentials, 32), published_bindings: count(raw.published_bindings, 2147483647), older_version_bindings: count(raw.older_version_bindings, 2147483647) }
    if (item.older_version_bindings > item.published_bindings || (item.status === 'blocked') !== (item.reason !== '') || (item.before === null) !== (item.reason === 'not_found')) throw new Error('批量操作状态与快照不一致。')
    return item
  })
}
function matchesState(item: ChannelOperationItem, operationAction: ChannelOperationAction, changed: boolean): boolean {
  return !!item.before && item.before.current_version === item.expected_version && item.before.management_revision === item.expected_revision && item.before.enabled === (changed ? operationAction === 'disable' : operationAction === 'enable') && (operationAction !== 'enable' || !item.before.quarantined && item.live_credentials > 0)
}
function coherentBlock(item: ChannelOperationItem, operationAction: ChannelOperationAction): boolean {
  if (item.reason === 'not_found') return item.before === null && item.live_credentials === 0 && item.published_bindings === 0
  if (!item.before) return false
  if (item.reason === 'stale_version') return item.before.current_version !== item.expected_version
  if (item.before.current_version !== item.expected_version) return false
  if (item.reason === 'stale_revision') return item.before.management_revision !== item.expected_revision
  if (item.before.management_revision !== item.expected_revision || operationAction !== 'enable') return false
  if (item.reason === 'channel_quarantined') return item.before.quarantined
  return item.reason === 'no_live_credential' && !item.before.quarantined && item.live_credentials === 0
}
function assertCommandItems(command: ChannelOperationCommand, operationAction: ChannelOperationAction, items: ChannelOperationItem[]): void {
  const expected = channelOperationCommand(command)
  if (expected.action !== operationAction || expected.targets.length !== items.length) throw new Error('回执或预检与冻结命令不匹配。')
  const identities = new Map(items.map(item => [item.channel_id, item]))
  if (expected.targets.some(target => { const item = identities.get(target.channel_id); return !item || target.expected_version !== item.expected_version || target.expected_revision !== item.expected_revision })) throw new Error('回执或预检与冻结渠道快照不匹配。')
}
export function parseChannelOperationPreview(value: unknown, command?: ChannelOperationCommand): ChannelOperationPreview {
  const raw = record(value), operationAction = action(raw.action), items = parseItems(raw.items)
  if (typeof raw.can_apply !== 'boolean') throw new Error('批量操作预检结果无效。')
  const blocked = items.some(item => item.status === 'blocked'), changed = items.filter(item => item.status === 'ready').length
  if (raw.can_apply === blocked || count(raw.changed_count, 100) !== (blocked ? 0 : changed) || items.some(item => !['ready', 'unchanged', 'blocked'].includes(item.status) || item.after || (item.status === 'blocked' ? !coherentBlock(item, operationAction) : !matchesState(item, operationAction, item.status === 'ready')))) throw new Error('预检结果不能证明整批可执行。')
  if (command) assertCommandItems(command, operationAction, items)
  return { action: operationAction, can_apply: raw.can_apply, changed_count: raw.changed_count as number, items }
}
export function parseChannelOperationSummary(value: unknown): ChannelOperationSummary {
  const raw = record(value)
  if ((raw.status !== 'applied' && raw.status !== 'rejected') || typeof raw.actor !== 'string' || !raw.actor || raw.actor.length > 200 || /\p{Cc}/u.test(raw.actor) || typeof raw.created_at !== 'string' || !Number.isFinite(Date.parse(raw.created_at))) throw new Error('批量操作回执摘要无效。')
  const targets = count(raw.target_count, 100), changed = count(raw.changed_count, targets)
  if (!targets || raw.status === 'rejected' && changed !== 0) throw new Error('整批拒绝不能包含已变更渠道。')
  return { operation_id: uuid(raw.operation_id), actor: raw.actor, action: action(raw.action), status: raw.status, target_count: targets, changed_count: changed, created_at: raw.created_at }
}
export function parseChannelOperationReceipt(value: unknown, command?: ChannelOperationCommand): ChannelOperationReceipt {
  const raw = record(value), summary = parseChannelOperationSummary(raw), items = parseItems(raw.items)
  if (items.length !== summary.target_count || items.filter(item => item.status === 'changed').length !== summary.changed_count) throw new Error('回执变更数量与明细不一致。')
  if (summary.status === 'applied') {
    if (items.some(item => !['changed', 'unchanged'].includes(item.status))) throw new Error('整批应用回执包含未应用渠道。')
  } else if (!items.some(item => item.status === 'blocked') || items.some(item => !['blocked', 'not_applied', 'unchanged'].includes(item.status))) throw new Error('整批拒绝回执状态不一致。')
  for (const item of items) {
    if (summary.status === 'rejected' && item.after) throw new Error('整批拒绝的任何渠道都不能有后置状态。')
    if (summary.status === 'applied' && !item.after) throw new Error('整批应用的每个渠道都须有后置状态。')
    if (item.status === 'blocked') { if (!coherentBlock(item, summary.action)) throw new Error('阻断原因与渠道快照不一致。'); continue }
    if (!matchesState(item, summary.action, item.status !== 'unchanged')) throw new Error('回执前置状态与命令不一致。')
    if (item.status === 'changed') {
      if (!item.before || !item.after || item.after.enabled !== (summary.action === 'enable') || item.after.current_version !== item.before.current_version || item.after.quarantined !== item.before.quarantined || BigInt(item.after.management_revision) !== BigInt(item.before.management_revision) + 1n) throw new Error('回执不能证明只变更渠道启用状态。')
    } else if (item.after && JSON.stringify(item.after) !== JSON.stringify(item.before)) throw new Error('未应用渠道不能有不同的后置状态。')
  }
  if (command) assertCommandItems(command, summary.action, items)
  return { ...summary, items }
}
export function channelOperationReasonText(reason: ChannelOperationReason): string {
  return { '': '—', not_found: '渠道不存在', stale_version: '渠道版本已变化，请刷新后重新预检', stale_revision: '渠道管理状态已变化，请刷新后重新预检', channel_quarantined: '渠道被隔离，不能批量解除隔离', no_live_credential: '当前凭据池没有未撤销的成员' }[reason]
}
export function channelOperationItemText(status: ChannelOperationItemStatus): string {
  return { ready: '待变更', unchanged: '状态未变化', blocked: '阻断', changed: '已变更', not_applied: '整批拒绝，未应用' }[status]
}
export function unknownChannelOperationResult(error: unknown): boolean {
  return !(error instanceof ApiError) || error.status === 0 || error.status >= 500 || error.status === 408 || error.code === 'invalid_response'
}
export function createChannelOperationsScope() {
  let epoch = 0
  const lanes = new Map<string, number>()
  return {
    reset() { epoch++; lanes.clear() },
    ticket(lane: string) { const sequence = (lanes.get(lane) ?? 0) + 1; lanes.set(lane, sequence); return { epoch, lane, sequence } },
    accepts(ticket: { epoch: number; lane: string; sequence: number }) { return ticket.epoch === epoch && lanes.get(ticket.lane) === ticket.sequence },
  }
}
// This tab's public operation IDs survive closing dialogs and changing routes.
// No session credentials, provider secrets, or browser storage are involved.
export function createChannelOperationJournal() {
  const actors = new Map<string, SubmittedChannelOperation[]>()
  function entries(actor: string) { const current = actors.get(actor) ?? []; actors.set(actor, current); return current }
  return {
    list(actor: string) { return structuredClone([...entries(actor)].reverse()) },
    begin(actor: string, command: ChannelOperationCommand, createID: () => string = () => crypto.randomUUID()): SubmittedChannelOperation {
      const canonical = channelOperationCommand(command), history = entries(actor)
      if (history.some(item => item.state === 'unknown')) throw new Error('已有结果待确认的操作，请先查询原操作 ID。')
      const id = uuid(createID())
      if (history.some(item => item.operation_id === id)) throw new Error('操作 ID 已使用，请重新预检。')
      const submitted: SubmittedChannelOperation = { operation_id: id, command: canonical, state: 'unknown' }
      history.push(submitted)
      return structuredClone(submitted)
    },
    resolve(actor: string, operationID: string, value: unknown): ChannelOperationReceipt {
      const entry = entries(actor).find(item => item.operation_id === operationID)
      const receipt = parseChannelOperationReceipt(value, entry?.command)
      if (receipt.operation_id !== operationID || entry && receipt.actor !== actor) throw new Error('回执的操作 ID 或操作者不匹配。')
      if (entry) { entry.state = 'resolved'; entry.receipt = structuredClone(receipt) }
      return receipt
    },
    notSubmitted(actor: string, operationID: string) { const entry = entries(actor).find(item => item.operation_id === operationID); if (entry && entry.state === 'unknown') entry.state = 'not_submitted' },
  }
}
export const channelOperationJournal = createChannelOperationJournal()
