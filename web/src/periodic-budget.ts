export type PeriodicKind = 'utc_day' | 'utc_month'
export type PeriodicScope = 'tenant' | 'key' | 'procurement'
export type PeriodicCommandKind = 'tenant_periodic_budget.configure' | 'key_periodic_budget.configure' | 'procurement_periodic_budget.configure'
export interface BudgetWindow { id: string; start_at: string; end_at: string; spent_amount: string; held_amount: string }
export interface BudgetAllocation { id: string; subject_kind: 'customer_request' | 'supplier_attempt' | 'legacy_customer' | 'legacy_supplier'; subject_id: string; window_id: string | null; upper_amount: string; held_amount: string; spent_amount: string; state: 'held' | 'disputed' | 'settled' | 'released'; created_at: string }
export interface PeriodicBudget { scope: PeriodicScope; scope_id: string; revision: string; period_kind: PeriodicKind | null; enabled: boolean; limit_amount: string | null; state: 'unconfigured' | 'scheduled' | 'active' | 'disabled'; effective_from: string | null; tracking_started_at: string | null; current_window: BudgetWindow | null; carry_held: string; as_of: string }
export interface PeriodicInput { operation_id: string; expected_revision: string; period_kind: PeriodicKind; enabled: boolean; limit_amount: string }
export interface PeriodicOperation { operation_id: string; kind: PeriodicCommandKind; scope: string; actor: string; target: string; input_fingerprint: string; resource_id: string; revision: string; created_at: string }
export interface PeriodicReceipt { operation: PeriodicOperation; result: PeriodicBudget }
export interface PeriodicPending { version: 1; operation_id: string; kind: PeriodicCommandKind; actor: string; identity_scope: string; target: string; input_fingerprint: string; state: 'pending' }
export interface PeriodicCommand { pending: Readonly<PeriodicPending>; body: Readonly<PeriodicInput> }
export interface PeriodicTarget { kind: 'wallet' | PeriodicScope; scope: PeriodicScope; id: string; identityScope: string; path: string; title: string }
export interface PeriodicPage<T> { rows: T[]; next: string }
const bad = (): never => { throw new Error('周期预算数据不符合合同，不能据此确认原操作。') }
function object(value: unknown, keys: string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return bad()
  const data = value as Record<string, unknown>, actual = Object.keys(data)
  if (actual.length !== keys.length || keys.some(key => !Object.hasOwn(data, key))) return bad()
  return data
}
export function periodicID(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) || value === '00000000-0000-0000-0000-000000000000') return bad()
  return value
}
export function periodicAmount(value: unknown, numeric100 = false): string {
  if (typeof value !== 'string' || !(numeric100 ? /^(?:0|[1-9][0-9]{0,99})$/ : /^(?:0|[1-9][0-9]{0,18})$/).test(value) || !numeric100 && BigInt(value) > 9223372036854775807n) return bad()
  return value
}
function positive(value: unknown): string { const result = periodicAmount(value); if (result === '0') return bad(); return result }
function hex(value: unknown): string { if (typeof value !== 'string' || !/^[0-9a-f]{64}$/.test(value)) return bad(); return value }
function period(value: unknown): PeriodicKind { if (value !== 'utc_day' && value !== 'utc_month') return bad(); return value }
function bool(value: unknown): boolean { if (typeof value !== 'boolean') return bad(); return value }
export function periodicUTC6(value: unknown): string {
  if (typeof value !== 'string' || !/^(?!0000)[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z$/.test(value)) return bad()
  const year = Number(value.slice(0, 4)), month = Number(value.slice(5, 7)), day = Number(value.slice(8, 10))
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  if (month < 1 || month > 12 || day < 1 || day > days[month - 1]! || Number(value.slice(11, 13)) > 23 || Number(value.slice(14, 16)) > 59 || Number(value.slice(17, 19)) > 59) return bad()
  return value
}
function bounds(kind: PeriodicKind, at: string): [string, string] {
  const start = kind === 'utc_month' ? `${at.slice(0, 7)}-01T00:00:00.000000Z` : `${at.slice(0, 10)}T00:00:00.000000Z`
  const date = new Date(start)
  if (kind === 'utc_month') date.setUTCMonth(date.getUTCMonth() + 1); else date.setUTCDate(date.getUTCDate() + 1)
  // Year 10000 cannot be represented by the contract, including a window end.
  const end = date.toISOString().replace('.000Z', '.000000Z')
  return [start, end]
}
export function parseBudgetWindow(value: unknown): BudgetWindow {
  const v = object(value, ['id', 'start_at', 'end_at', 'spent_amount', 'held_amount'])
  const result = { id: periodicID(v.id), start_at: periodicUTC6(v.start_at), end_at: periodicUTC6(v.end_at), spent_amount: periodicAmount(v.spent_amount, true), held_amount: periodicAmount(v.held_amount, true) }
  if (result.start_at >= result.end_at) return bad()
  return result
}
export function parseBudgetAllocation(value: unknown, scope?: PeriodicScope): BudgetAllocation {
  const v = object(value, ['id', 'subject_kind', 'subject_id', 'window_id', 'upper_amount', 'held_amount', 'spent_amount', 'state', 'created_at'])
  if (!['customer_request', 'supplier_attempt', 'legacy_customer', 'legacy_supplier'].includes(String(v.subject_kind)) || !['held', 'disputed', 'settled', 'released'].includes(String(v.state))) return bad()
  const customer = v.subject_kind === 'customer_request' || v.subject_kind === 'legacy_customer'
  const legacy = v.subject_kind === 'legacy_customer' || v.subject_kind === 'legacy_supplier'
  if (customer && v.state === 'disputed' || scope && (scope === 'procurement') === customer || legacy && v.window_id !== null || !legacy && v.window_id === null) return bad()
  return { id: periodicID(v.id), subject_kind: v.subject_kind as BudgetAllocation['subject_kind'], subject_id: periodicID(v.subject_id), window_id: v.window_id === null ? null : periodicID(v.window_id), upper_amount: periodicAmount(v.upper_amount), held_amount: periodicAmount(v.held_amount, true), spent_amount: periodicAmount(v.spent_amount, true), state: v.state as BudgetAllocation['state'], created_at: periodicUTC6(v.created_at) }
}
export function parsePeriodicBudget(value: unknown, expected?: Pick<PeriodicTarget, 'scope' | 'id'>): PeriodicBudget {
  const v = object(value, ['scope', 'scope_id', 'revision', 'period_kind', 'enabled', 'limit_amount', 'state', 'effective_from', 'tracking_started_at', 'current_window', 'carry_held', 'as_of'])
  if (!['tenant', 'key', 'procurement'].includes(String(v.scope))) return bad()
  const result: PeriodicBudget = { scope: v.scope as PeriodicScope, scope_id: periodicID(v.scope_id), revision: periodicAmount(v.revision), period_kind: v.period_kind === null ? null : period(v.period_kind), enabled: bool(v.enabled), limit_amount: v.limit_amount === null ? null : periodicAmount(v.limit_amount), state: v.state as PeriodicBudget['state'], effective_from: v.effective_from === null ? null : periodicUTC6(v.effective_from), tracking_started_at: v.tracking_started_at === null ? null : periodicUTC6(v.tracking_started_at), current_window: v.current_window === null ? null : parseBudgetWindow(v.current_window), carry_held: periodicAmount(v.carry_held, true), as_of: periodicUTC6(v.as_of) }
  if (expected && (result.scope !== expected.scope || result.scope_id !== expected.id)) return bad()
  if (result.state === 'unconfigured') {
    if (result.revision !== '0' || result.enabled || result.period_kind !== null || result.limit_amount !== null || result.effective_from !== null || result.tracking_started_at !== null || result.current_window !== null || result.carry_held !== '0') return bad()
  } else {
    if (result.revision === '0' || !result.period_kind || result.limit_amount === null || !result.effective_from || !result.tracking_started_at || bounds(result.period_kind, result.tracking_started_at)[1] !== result.effective_from) return bad()
    const state = !result.enabled ? 'disabled' : result.as_of < result.effective_from ? 'scheduled' : 'active'
    if (result.state !== state) return bad()
    if (result.current_window) { const [start, end] = bounds(result.period_kind, result.as_of); if (result.current_window.start_at !== start || result.current_window.end_at !== end) return bad() }
  }
  return result
}
export function periodicRequestID(value: unknown): string {
  if (typeof value !== 'string' || value.length === 0 || value.length > 200 || /\p{Cc}/u.test(value)) return bad()
  return value
}
export function parsePeriodicSingle<T>(raw: unknown, decode: (data: unknown) => T): T { const v = object(raw, ['data', 'request_id']); periodicRequestID(v.request_id); return decode(v.data) }
export function parsePeriodicPage<T>(raw: unknown, decode: (data: unknown) => T): PeriodicPage<T> {
  const v = object(raw, ['data', 'request_id', 'next_cursor']); periodicRequestID(v.request_id)
  if (!Array.isArray(v.data) || v.data.length > 50 || typeof v.next_cursor !== 'string' || v.next_cursor.length > 1024 || /[^\x21-\x7e]/.test(v.next_cursor)) return bad()
  return { rows: v.data.map(decode), next: v.next_cursor }
}
function ordered<T extends { id: string }>(page: PeriodicPage<T>, at: (value: T) => string): PeriodicPage<T> {
  if (new Set(page.rows.map(row => row.id)).size !== page.rows.length) return bad()
  for (let i = 1; i < page.rows.length; i++) {
    const previous = page.rows[i - 1]!, next = page.rows[i]!
    if (at(previous) < at(next) || at(previous) === at(next) && previous.id <= next.id) return bad()
  }
  return page
}
export function parsePeriodicWindows(raw: unknown): PeriodicPage<BudgetWindow> { return ordered(parsePeriodicPage(raw, parseBudgetWindow), row => row.start_at) }
export function parsePeriodicAllocations(raw: unknown, scope: PeriodicScope): PeriodicPage<BudgetAllocation> { return ordered(parsePeriodicPage(raw, value => parseBudgetAllocation(value, scope)), row => row.created_at) }
function commandTarget(kind: unknown, identityScope: unknown, target: unknown): Pick<PeriodicTarget, 'scope' | 'id'> {
  const map = { 'tenant_periodic_budget.configure': ['tenant', '/api/platform/tenants/'], 'key_periodic_budget.configure': ['key', '/api/keys/'], 'procurement_periodic_budget.configure': ['procurement', '/api/platform/procurement-accounts/'] } as const
  if (typeof kind !== 'string' || !Object.hasOwn(map, kind) || typeof target !== 'string') return bad()
  const [scope, prefix] = map[kind as PeriodicCommandKind]
  if (scope === 'key') periodicID(identityScope); else if (identityScope !== 'platform') return bad()
  const suffix = '/periodic-budget'
  if (!target.startsWith(prefix) || !target.endsWith(suffix)) return bad()
  const id = periodicID(target.slice(prefix.length, -suffix.length))
  if (target !== prefix + id + suffix) return bad()
  return { scope, id }
}
export function parsePeriodicPending(value: unknown): PeriodicPending {
  const v = object(value, ['version', 'operation_id', 'kind', 'actor', 'identity_scope', 'target', 'input_fingerprint', 'state'])
  if (v.version !== 1 || v.state !== 'pending') return bad()
  commandTarget(v.kind, v.identity_scope, v.target)
  return { version: 1, operation_id: periodicID(v.operation_id), kind: v.kind as PeriodicCommandKind, actor: periodicID(v.actor), identity_scope: v.identity_scope as string, target: v.target as string, input_fingerprint: hex(v.input_fingerprint), state: 'pending' }
}
export function parsePeriodicInput(value: unknown): PeriodicInput {
  const v = object(value, ['operation_id', 'expected_revision', 'period_kind', 'enabled', 'limit_amount'])
  return { operation_id: periodicID(v.operation_id), expected_revision: periodicAmount(v.expected_revision), period_kind: period(v.period_kind), enabled: bool(v.enabled), limit_amount: periodicAmount(v.limit_amount) }
}
export function parsePeriodicReceipt(value: unknown, expected: PeriodicPending, controlID?: string): PeriodicReceipt {
  const v = object(value, ['operation', 'result']), o = object(v.operation, ['operation_id', 'kind', 'scope', 'actor', 'target', 'input_fingerprint', 'resource_id', 'revision', 'created_at'])
  const target = commandTarget(o.kind, o.scope, o.target)
  const operation: PeriodicOperation = { operation_id: periodicID(o.operation_id), kind: o.kind as PeriodicCommandKind, scope: o.scope as string, actor: periodicID(o.actor), target: o.target as string, input_fingerprint: hex(o.input_fingerprint), resource_id: periodicID(o.resource_id), revision: positive(o.revision), created_at: periodicUTC6(o.created_at) }
  const result = parsePeriodicBudget(v.result, target), pending = parsePeriodicPending(expected)
  if (operation.operation_id !== pending.operation_id || operation.kind !== pending.kind || operation.scope !== pending.identity_scope || operation.actor !== pending.actor || operation.target !== pending.target || operation.input_fingerprint !== pending.input_fingerprint || operation.revision !== result.revision || operation.created_at !== result.as_of || controlID && operation.resource_id !== periodicID(controlID)) return bad()
  return { operation, result }
}
export async function periodicFingerprint(kind: PeriodicCommandKind, actor: string, identityScope: string, target: string, value: PeriodicInput): Promise<string> {
  commandTarget(kind, identityScope, target); periodicID(actor)
  const input = parsePeriodicInput(value), encoder = new TextEncoder()
  const fields = [kind, actor, identityScope, target, input.expected_revision, input.period_kind, input.enabled ? 'true' : 'false', input.limit_amount].map(s => encoder.encode(s))
  const framed = new Uint8Array(fields.reduce((size, field) => size + 4 + field.length, 0)), view = new DataView(framed.buffer)
  let offset = 0
  for (const field of fields) { view.setUint32(offset, field.length, false); offset += 4; framed.set(field, offset); offset += field.length }
  const digest = await globalThis.crypto.subtle.digest('SHA-256', framed)
  return [...new Uint8Array(digest)].map(byte => byte.toString(16).padStart(2, '0')).join('')
}
export async function newPeriodicCommand(target: PeriodicTarget, actor: string, input: Omit<PeriodicInput, 'operation_id'>): Promise<PeriodicCommand> {
  if (target.kind === 'wallet') throw new Error('钱包预算为只读，请联系平台管理员。')
  const kind: PeriodicCommandKind = `${target.scope}_periodic_budget.configure`
  const body = Object.freeze(parsePeriodicInput({ ...input, operation_id: globalThis.crypto.randomUUID() }))
  const pending = Object.freeze(parsePeriodicPending({ version: 1, operation_id: body.operation_id, kind, actor, identity_scope: target.identityScope, target: target.path, input_fingerprint: await periodicFingerprint(kind, actor, target.identityScope, target.path, body), state: 'pending' }))
  return Object.freeze({ pending, body })
}
export function parsePeriodicTarget(path: string, rawQuery: string, tenant?: string): PeriodicTarget {
  if (rawQuery === '?') return bad()
  const query = rawQuery.startsWith('?') ? rawQuery.slice(1) : rawQuery
  if (query.length > 4096 || /%(?![0-9a-fA-F]{2})/.test(query)) return bad()
  try { decodeURIComponent(query.replace(/\+/g, ' ')) } catch { return bad() }
  const entries = [...new URLSearchParams(query)]
  if (path === '/periodic-budget') {
    const identityScope = periodicID(tenant)
    if (entries.length === 0) return { kind: 'wallet', scope: 'tenant', id: identityScope, identityScope, path: '/api/wallet/periodic-budget', title: '租户周期预算' }
    if (entries.length === 1 && entries[0]![0] === 'key') { const id = periodicID(entries[0]![1]); return { kind: 'key', scope: 'key', id, identityScope, path: `/api/keys/${id}/periodic-budget`, title: 'API Key 周期预算' } }
  }
  if (path === '/platform/periodic-budget' && entries.length === 1) {
    const [key, value] = entries[0]!, id = periodicID(value)
    if (key === 'tenant') return { kind: 'tenant', scope: 'tenant', id, identityScope: 'platform', path: `/api/platform/tenants/${id}/periodic-budget`, title: '客户周期预算' }
    if (key === 'procurement') return { kind: 'procurement', scope: 'procurement', id, identityScope: 'platform', path: `/api/platform/procurement-accounts/${id}/periodic-budget`, title: '采购周期预算' }
  }
  return bad()
}
export function periodicIncompleteWindow(window: BudgetWindow, budget: PeriodicBudget): boolean { return budget.effective_from !== null && window.start_at < budget.effective_from }
export function periodicOperationPath(pending: PeriodicPending): string { parsePeriodicPending(pending); return `/api${pending.identity_scope === 'platform' ? '/platform' : ''}/periodic-budget-operations/${pending.operation_id}` }
export function periodicCanRead(target: PeriodicTarget, platformRole: string, tenantRole: string): boolean { return target.identityScope === 'platform' ? ['admin', 'finance_operator', 'finance_approver'].includes(platformRole) : target.kind === 'wallet' ? ['owner', 'billing_viewer'].includes(tenantRole) : ['owner', 'member', 'billing_viewer'].includes(tenantRole) }
export function periodicCanWrite(target: PeriodicTarget, platformRole: string, tenantRole: string): boolean { return target.kind !== 'wallet' && (target.identityScope === 'platform' ? platformRole === 'admin' : ['owner', 'member'].includes(tenantRole)) }
