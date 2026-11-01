export const discoveryFormats = [
  { id: 'openai', title: 'OpenAI 兼容目录' },
  { id: 'anthropic', title: 'Anthropic 原生目录' },
  { id: 'gemini', title: 'Gemini 原生目录' },
] as const
export type DiscoveryFormat = typeof discoveryFormats[number]['id']
export type DiscoveryStatus = 'running' | 'interrupted' | 'completed' | 'failed'
export interface DiscoveryChannel { id: string; name: string; current_version: string; enabled?: boolean; quarantined?: boolean }
export interface DiscoveryBinding { model_id: string; public_name: string; upstream_model: string; revision: number; channel_version_id: string; enabled?: boolean }
export interface DiscoveryMetadata {
  id: string; channel_id: string; channel_version_id: string; credential_id?: string; format: DiscoveryFormat; status: DiscoveryStatus
  created_at: string; completed_at?: string; error_code?: string; pages: number; stale: boolean
}
export interface DiscoveryView extends DiscoveryMetadata { models: string[]; unbound: string[]; present: DiscoveryBinding[]; missing: DiscoveryBinding[] }

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('模型发现响应格式无效。')
  return value as Record<string, unknown>
}
function identity(value: unknown): string {
  if (typeof value !== 'string' || !value || value.length > 200 || value.trim() !== value || /[\x00-\x1f\x7f]/.test(value)) throw new Error('模型发现标识无效。')
  return value
}
function date(value: unknown): string {
  if (typeof value !== 'string' || !value || !Number.isFinite(Date.parse(value))) throw new Error('模型发现时间无效。')
  return value
}
export function discoveryFormat(value: unknown): DiscoveryFormat {
  if (!discoveryFormats.some(format => format.id === value)) throw new Error('请选择有效的供应商目录格式。')
  return value as DiscoveryFormat
}
export function discoveryStatusText(status: DiscoveryStatus): string {
  return { running: '执行中', interrupted: '执行中断', completed: '已完成', failed: '发现失败' }[status]
}
function channelPath(channel: unknown): string { return `/platform/channels/${encodeURIComponent(identity(channel))}` }
export function discoveryRequest(channel: unknown, version: unknown, format: unknown) {
  return { path: `${channelPath(channel)}/discover-models`, method: 'POST' as const, body: { format: discoveryFormat(format), expected_version: identity(version) } }
}
export function discoveryHistoryPath(channel: unknown, cursor = ''): string {
  if (cursor.length > 512) throw new Error('分页游标无效。')
  return `${channelPath(channel)}/model-discoveries${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`
}
export function discoveryDetailPath(channel: unknown, id: unknown): string { return `${channelPath(channel)}/model-discoveries/${encodeURIComponent(identity(id))}` }

export function parseDiscoveryMetadata(value: unknown): DiscoveryMetadata {
  const raw = record(value)
  const status = raw.status
  if (!['running', 'interrupted', 'completed', 'failed'].includes(String(status))) throw new Error('模型发现状态无效。')
  if (typeof raw.stale !== 'boolean') throw new Error('模型发现版本状态无效。')
  const pages = raw.pages ?? 0
  if (typeof pages !== 'number' || !Number.isSafeInteger(pages) || pages < 0 || pages > 10) throw new Error('模型发现页数无效。')
  if (raw.error_code !== undefined && raw.error_code !== '' && (typeof raw.error_code !== 'string' || !/^[a-z][a-z0-9_]{0,79}$/.test(raw.error_code))) throw new Error('模型发现错误代码无效。')
  return {
    id: identity(raw.id), channel_id: identity(raw.channel_id), channel_version_id: identity(raw.channel_version_id),
    ...(raw.credential_id ? { credential_id: identity(raw.credential_id) } : {}),
    format: discoveryFormat(raw.format), status: status as DiscoveryStatus, stale: raw.stale, pages,
    created_at: date(raw.created_at),
    ...(raw.completed_at ? { completed_at: date(raw.completed_at) } : {}),
    ...(raw.error_code ? { error_code: raw.error_code as string } : {}),
  }
}
function modelID(value: unknown): string {
  if (typeof value !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._/:\-]{0,199}$/.test(value) || value.includes('..') || value.includes('//')) throw new Error('目录中的模型名称无效。')
  return value
}
function modelList(raw: unknown): string[] {
  if (!Array.isArray(raw) || raw.length > 1000) throw new Error('模型目录超出范围或格式无效。')
  const models = raw.map(modelID)
  if (new Set(models).size !== models.length) throw new Error('模型目录包含重复名称。')
  return models
}
function bindingList(raw: unknown): DiscoveryBinding[] {
  if (!Array.isArray(raw) || raw.length > 1000) throw new Error('模型绑定快照格式无效。')
  return raw.map(value => {
    const binding = record(value)
    if (!Number.isSafeInteger(binding.revision) || Number(binding.revision) < 0) throw new Error('模型路由版本无效。')
    // Published mappings predate discovery's stricter directory IDs. Preserve
    // their literal names for comparison, including Unicode or whitespace.
    if (typeof binding.upstream_model !== 'string' || !binding.upstream_model || binding.upstream_model.length > 256_000) throw new Error('模型绑定名称无效。')
    if (binding.enabled !== undefined && typeof binding.enabled !== 'boolean') throw new Error('模型启用状态无效。')
    return { model_id: identity(binding.model_id), public_name: identity(binding.public_name), upstream_model: binding.upstream_model, revision: binding.revision as number, channel_version_id: identity(binding.channel_version_id), ...(binding.enabled === undefined ? {} : { enabled: binding.enabled }) }
  })
}
export function parseDiscoveryView(value: unknown): DiscoveryView {
  const raw = record(value)
  const metadata = parseDiscoveryMetadata(raw)
  const models = modelList(raw.models), unbound = modelList(raw.unbound)
  const present = bindingList(raw.present), missing = bindingList(raw.missing)
  const modelSet = new Set(models)
  if (metadata.status !== 'completed' && (models.length || unbound.length || present.length || missing.length)) throw new Error('未完成的发现记录不能作为目录结果。')
  if (unbound.some(model => !modelSet.has(model)) || present.some(binding => !modelSet.has(binding.upstream_model)) || missing.some(binding => modelSet.has(binding.upstream_model))) throw new Error('模型目录与绑定差异不一致。')
  return { ...metadata, models, unbound, present, missing }
}
export function discoveryErrorID(error: unknown): string {
  if (!error || typeof error !== 'object') return ''
  const raw = error as Record<string, unknown>
  try { return identity(raw.discoveryId ?? raw.discovery_id) } catch { return '' }
}

// Each channel/close changes the epoch. Separate operation lanes also reject a
// previous detail/history request that completes after a newer selection.
export function createDiscoveryScope() {
  let epoch = 0
  const lanes = new Map<string, number>()
  return {
    reset() { epoch++; lanes.clear() },
    ticket(lane: string) { const sequence = (lanes.get(lane) ?? 0) + 1; lanes.set(lane, sequence); return { epoch, lane, sequence } },
    snapshot(lane: string) { return { epoch, lane, sequence: lanes.get(lane) ?? 0 } },
    accepts(ticket: { epoch: number; lane: string; sequence: number }) { return ticket.epoch === epoch && (lanes.get(ticket.lane) ?? 0) === ticket.sequence },
  }
}
