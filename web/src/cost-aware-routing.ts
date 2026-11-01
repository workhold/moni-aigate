import { ApiError, request } from './api.ts'
import { cnyToMicro } from './format.ts'
import { channelManagementRevision } from './channel-operations.ts'

export interface CostPolicy { mode: 'manual' | 'cost_first'; unknown_cost_policy: 'allow' | 'deny' }
export interface CostBinding { channel_id: string; upstream_model: string; priority: number; weight: number; max_inflight: number; max_input_cost_rate: string | null; max_output_cost_rate: string | null }
export interface RouteView { id: string; public_name: string; active_revision: number; cost_policy: CostPolicy; bindings: CostBinding[] }
export interface BindingDraft extends Omit<CostBinding, 'max_input_cost_rate' | 'max_output_cost_rate'> { max_input_cost_rate: string; max_output_cost_rate: string }
export interface CostChannel { id: string; name: string; management_revision: string; enabled: boolean }
export interface CostDraft { upstream_model: string; basis: 'unknown' | 'contract' | 'invoice'; input_rate: string; output_rate: string; effective_at: string }
export interface CostVersion { id: string; channel_id: string; channel_revision: string; upstream_model: string; basis: CostDraft['basis']; input_rate: string | null; output_rate: string | null; effective_at: string }
export interface RouteWrite { path: string; method: 'PUT'; body: { expected_revision: number; cost_policy: CostPolicy; bindings: CostBinding[] } }
export interface CostWrite { path: string; method: 'POST'; body: { channel_id: string; expected_channel_revision: string; upstream_model: string; basis: CostDraft['basis']; input_rate: string | null; output_rate: string | null; effective_at: string | null } }

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('采购配置响应格式无效。')
  return value as Record<string, unknown>
}
function identity(value: unknown): string {
  if (typeof value !== 'string' || !value || value.trim() !== value || /\p{Cc}/u.test(value) || value.length > 200) throw new Error('配置标识无效。')
  return value
}
function integer(value: unknown, min: number, max: number): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min || value > max) throw new Error('路由优先级、权重、并发或版本无效。')
  return value
}
function rate(value: unknown): string | null {
  if (value === null) return null
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error('采购单价须为非负 int64 十进制字符串或 null。')
  return value
}
export function costRateToCny(value: string | null): string {
  const parsed = rate(value)
  if (parsed === null) return ''
  const amount = BigInt(parsed)
  return `${amount / 1000000n}.${(amount % 1000000n).toString().padStart(6, '0')}`
}
export function costPolicy(value: unknown): CostPolicy {
  const raw = record(value)
  if ((raw.mode !== 'manual' && raw.mode !== 'cost_first') || (raw.unknown_cost_policy !== 'allow' && raw.unknown_cost_policy !== 'deny') || raw.mode === 'cost_first' && raw.unknown_cost_policy !== 'deny') throw new Error('成本优先必须排除未知报价，请检查路由策略。')
  return { mode: raw.mode, unknown_cost_policy: raw.unknown_cost_policy }
}
function binding(value: unknown): CostBinding {
  const raw = record(value), model = raw.upstream_model
  if (typeof model !== 'string' || !model.trim() || model.trim() !== model || /\p{Cc}/u.test(model) || new TextEncoder().encode(model).length > 200) throw new Error('请填写精确上游模型名称（最多 200 字节）。')
  return { channel_id: identity(raw.channel_id), upstream_model: model, priority: integer(raw.priority, -2147483648, 2147483647), weight: integer(raw.weight, 1, 1000000), max_inflight: integer(raw.max_inflight, 1, 1000), max_input_cost_rate: rate(raw.max_input_cost_rate ?? null), max_output_cost_rate: rate(raw.max_output_cost_rate ?? null) }
}
export function parseRouteView(value: unknown, expectedID?: string): RouteView {
  const raw = record(value), id = identity(raw.id)
  if (expectedID && id !== expectedID) throw new Error('读到了不同模型的路由。')
  if (typeof raw.public_name !== 'string' || !Array.isArray(raw.bindings) || raw.bindings.length > 1000) throw new Error('模型路由响应无效。')
  const bindings = raw.bindings.map(binding)
  if (new Set(bindings.map(b => b.channel_id)).size !== bindings.length) throw new Error('路由包含重复渠道。')
  return { id, public_name: raw.public_name, active_revision: integer(raw.active_revision ?? raw.revision, 0, Number.MAX_SAFE_INTEGER), cost_policy: costPolicy(raw.cost_policy ?? { mode: 'manual', unknown_cost_policy: 'allow' }), bindings }
}
export function routeDrafts(view: RouteView): BindingDraft[] { return view.bindings.map(b => ({ ...b, max_input_cost_rate: costRateToCny(b.max_input_cost_rate), max_output_cost_rate: costRateToCny(b.max_output_cost_rate) })) }
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') { Object.values(value).forEach(freeze); Object.freeze(value) }
  return value
}
export function routeWrite(view: RouteView, policy: CostPolicy, drafts: BindingDraft[]): RouteWrite {
  const canonical = parseRouteView(view)
  if (!drafts.length || drafts.length > 1000) throw new Error('完整路由池须有 1 至 1000 个渠道；停服请停用模型。')
  const bindings = drafts.map(draft => binding({ ...draft, upstream_model: draft.upstream_model.trim(), max_input_cost_rate: draft.max_input_cost_rate.trim() ? cnyToMicro(draft.max_input_cost_rate) : null, max_output_cost_rate: draft.max_output_cost_rate.trim() ? cnyToMicro(draft.max_output_cost_rate) : null }))
  if (new Set(bindings.map(b => b.channel_id)).size !== bindings.length) throw new Error('同一模型每个渠道只能绑定一次。')
  return freeze({ path: `/platform/models/${encodeURIComponent(canonical.id)}/bindings`, method: 'PUT', body: { expected_revision: canonical.active_revision, cost_policy: costPolicy(policy), bindings } })
}
export function acceptRouteWrite(operation: RouteWrite, value: unknown): void {
  const raw = record(value)
  if (integer(raw.revision, 1, Number.MAX_SAFE_INTEGER) !== operation.body.expected_revision + 1) throw new Error('发布结果不能证明预期路由版本，请只读核查。')
}
export function parseCostChannel(value: unknown, expectedID?: string): CostChannel {
  const raw = record(value), id = identity(raw.id)
  if (expectedID && id !== expectedID) throw new Error('读到了不同渠道。')
  if (typeof raw.name !== 'string' || typeof raw.enabled !== 'boolean') throw new Error('渠道元数据无效。')
  return { id, name: raw.name, enabled: raw.enabled, management_revision: channelManagementRevision(raw.management_revision) }
}
export function costWrite(channel: CostChannel, draft: CostDraft): CostWrite {
  const selected = parseCostChannel(channel), model = draft.upstream_model.trim()
  if (!model || /\p{Cc}/u.test(model) || new TextEncoder().encode(model).length > 200) throw new Error('请填写精确上游模型名称（最多 200 字节）。')
  if (!['unknown', 'contract', 'invoice'].includes(draft.basis)) throw new Error('请选择成本依据。')
  let effective: string | null = null
  if (draft.effective_at) {
    const date = new Date(draft.effective_at)
    if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() < 1 || date.getUTCFullYear() > 9999) throw new Error('请选择有效的生效时间。')
    effective = date.toISOString()
  }
  if (draft.basis !== 'unknown' && (!draft.input_rate.trim() || !draft.output_rate.trim())) throw new Error('合同或账单依据须明确填写两维单价，免费请填 0。')
  return freeze({ path: '/platform/cost-versions', method: 'POST', body: { channel_id: selected.id, expected_channel_revision: selected.management_revision, upstream_model: model, basis: draft.basis, input_rate: draft.basis === 'unknown' ? null : cnyToMicro(draft.input_rate), output_rate: draft.basis === 'unknown' ? null : cnyToMicro(draft.output_rate), effective_at: effective } })
}
export function acceptCostWrite(operation: CostWrite, value: unknown): { id: string; channel_revision: string } {
  const raw = record(value), revision = channelManagementRevision(raw.channel_revision)
  if (BigInt(revision) !== BigInt(operation.body.expected_channel_revision) + 1n) throw new Error('发布结果不能证明预期渠道版本，请只读核查。')
  return { id: identity(raw.id), channel_revision: revision }
}
export function parseCostVersion(value: unknown): CostVersion {
  const raw = record(value)
  if (!['unknown', 'contract', 'invoice'].includes(String(raw.basis)) || typeof raw.upstream_model !== 'string' || typeof raw.effective_at !== 'string' || !Number.isFinite(Date.parse(raw.effective_at))) throw new Error('成本版本响应无效。')
  const input = rate(raw.input_rate), output = rate(raw.output_rate)
  // Legacy rows may have incomplete dimensions. Keep history inspectable; a
  // new known-price publication still requires both dimensions in costWrite.
  return { id: identity(raw.id), channel_id: identity(raw.channel_id), channel_revision: channelManagementRevision(raw.channel_revision), upstream_model: raw.upstream_model, basis: raw.basis as CostVersion['basis'], input_rate: input, output_rate: output, effective_at: raw.effective_at }
}

export interface WriteReview<V, O> { mode: 'submitting' | 'conflict' | 'unknown' | 'saved'; view: V; submitted: O; observed?: V; previousUnknown: boolean }
export function unknownCostWriteResult(error: unknown): boolean { return !(error instanceof ApiError) || error.status === 0 || error.status >= 500 || error.status === 408 || error.code === 'invalid_response' }
// A tab-local journal survives closing an editor and changing pages. It holds
// safe metadata only. A GET is an observation, never proof of an old command.
export function createCostWriteJournal<V, O>(revision: (view: V) => bigint, same: (a: V, b: V) => boolean) {
  // Vue refs expose nested proxies. Rebuild the safe JSON metadata tree before
  // retaining it; structuredClone rejects a Proxy even when its fields are safe.
  function copy<T>(value: T): T {
    if (Array.isArray(value)) return value.map(copy) as T
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([name, item]) => [name, copy(item)])) as T
    return value
  }
  const entries = new Map<string, WriteReview<V, O>>()
  const uncertain = new Set<string>()
  const key = (context: string, target: string) => JSON.stringify([context, target])
  return {
    get(context: string, target: string) { const item = entries.get(key(context, target)); return item ? copy(item) : null },
    hadUnknown(context: string, target: string) { return uncertain.has(key(context, target)) },
    begin(context: string, target: string, view: V, operation: O, previousUnknown = false) {
      const old = entries.get(key(context, target))
      if (old && old.mode !== 'saved') throw new Error('已有提交或结果待核查，请先只读刷新并显式重新编辑。')
      const item: WriteReview<V, O> = { mode: 'submitting', view: copy(view), submitted: copy(operation), previousUnknown: previousUnknown || uncertain.has(key(context, target)) }
      entries.set(key(context, target), item)
      return copy(item)
    },
    fail(context: string, target: string, error: unknown) {
      const item = entries.get(key(context, target))
      if (!item || item.mode !== 'submitting') return
      if (error instanceof ApiError && error.status === 409) item.mode = 'conflict'
      else if (unknownCostWriteResult(error)) { item.mode = 'unknown'; uncertain.add(key(context, target)) }
      else entries.delete(key(context, target))
    },
    saved(context: string, target: string) { const item = entries.get(key(context, target)); if (item?.mode === 'submitting') item.mode = 'saved' },
    observe(context: string, target: string, view: V) {
      const item = entries.get(key(context, target))
      if (!item || item.mode !== 'unknown' && item.mode !== 'conflict') throw new Error('当前没有可核查的提交。')
      const before = item.observed ?? item.view
      if (revision(view) < revision(before) || revision(view) === revision(before) && !same(view, before)) throw new Error('只读结果版本回退或同版本内容不同，请重新核查。')
      item.observed = copy(view)
      return copy(item)
    },
    resume(context: string, target: string) {
      const item = entries.get(key(context, target))
      if (!item?.observed || item.mode !== 'unknown' && item.mode !== 'conflict') throw new Error('请先只读刷新当前配置，再显式重新编辑。')
      const result = { view: copy(item.observed), previousUnknown: item.previousUnknown || item.mode === 'unknown' }
      entries.delete(key(context, target))
      return result
    },
  }
}
export const routeWriteJournal = createCostWriteJournal<RouteView, RouteWrite>(v => BigInt(v.active_revision), (a, b) => JSON.stringify(parseRouteView(a)) === JSON.stringify(parseRouteView(b)))
export const costWriteJournal = createCostWriteJournal<CostChannel, CostWrite>(v => BigInt(v.management_revision), (a, b) => JSON.stringify(parseCostChannel(a)) === JSON.stringify(parseCostChannel(b)))

export async function readConfigurationRows(path: '/platform/models' | '/platform/channels' | '/platform/cost-versions', active: () => boolean = () => true): Promise<unknown[]> {
  const rows: unknown[] = [], cursors = new Set<string>()
  let cursor = ''
  do {
    if (!active()) throw new Error('编辑上下文已关闭。')
    const response = await request<unknown>(`${path}${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`)
    if (!Array.isArray(response.data) || response.data.length > (path === '/platform/models' ? 1000 : 50)) throw new Error('配置列表响应无效。')
    rows.push(...response.data)
    if (rows.length > 10000) throw new Error('配置记录超过本次核查范围，请联系管理员。')
    if (response.next_cursor !== undefined && response.next_cursor !== null && typeof response.next_cursor !== 'string') throw new Error('配置分页游标无效。')
    cursor = response.next_cursor ?? ''
    if (cursor && (cursors.has(cursor) || cursor.length > 512)) throw new Error('配置分页游标无效。')
    cursors.add(cursor)
  } while (cursor)
  return rows
}
