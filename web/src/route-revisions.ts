import { ApiError, request } from './api.ts'
import { unknownCostWriteResult } from './cost-aware-routing.ts'

export interface RouteRevisionBinding {
  channel_id: string; channel_name: string; channel_kind: 'direct' | 'new_api' | 'sub2api'; integration_mode: 'external_relay' | 'embedded_adapter' | 'migrated_capability'
  channel_version_id: string; upstream_model: string; priority: number; weight: number; max_inflight: number
}
export interface RouteRevision {
  id: string; revision: number; created_at: string; profile_version: string; release_intent: 'test_only' | 'commercial'
  active: boolean; current_profile: boolean; bindings: RouteRevisionBinding[]
}
export interface RouteActivation {
  modelID: string; target: number; expected: number; path: string; method: 'POST'; body: { expected_revision: number }
}
export interface RouteActivationReview {
  mode: 'submitting' | 'conflict' | 'unknown' | 'saved'; operation: RouteActivation; observedActive?: number
}

const invalid = () => new Error('路由历史响应格式无效，请刷新后重试。')
function object(value: unknown, keys: string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw invalid()
  const raw = value as Record<string, unknown>
  if (Object.keys(raw).length !== keys.length || keys.some(key => !Object.hasOwn(raw, key))) throw invalid()
  return raw
}
function id(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value)) throw invalid()
  return value
}
function integer(value: unknown, min: number, max: number): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min || value > max) throw invalid()
  return value
}
function text(value: unknown, max = 200): string {
  if (typeof value !== 'string' || !value || value.trim() !== value || /[\p{Cc}]/u.test(value) || new TextEncoder().encode(value).byteLength > max) throw invalid()
  return value
}
function storedText(value: unknown, maxBytes?: number): string {
  if (typeof value !== 'string' || !value || maxBytes !== undefined && new TextEncoder().encode(value).byteLength > maxBytes) throw invalid()
  return value
}
function bool(value: unknown): boolean { if (typeof value !== 'boolean') throw invalid(); return value }
function timestamp(value: unknown): string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z$/.test(value) || !Number.isFinite(new Date(value).getTime())) throw invalid()
  return value
}
function parseBinding(value: unknown): RouteRevisionBinding {
  const keys = ['channel_id', 'channel_name', 'channel_kind', 'integration_mode', 'channel_version_id', 'upstream_model', 'priority', 'weight', 'max_inflight']
  const raw = object(value, keys)
  if (!['direct', 'new_api', 'sub2api'].includes(String(raw.channel_kind)) || !['external_relay', 'embedded_adapter', 'migrated_capability'].includes(String(raw.integration_mode))) throw invalid()
  return { channel_id: id(raw.channel_id), channel_name: storedText(raw.channel_name, 200), channel_kind: raw.channel_kind as RouteRevisionBinding['channel_kind'], integration_mode: raw.integration_mode as RouteRevisionBinding['integration_mode'], channel_version_id: id(raw.channel_version_id), upstream_model: storedText(raw.upstream_model), priority: integer(raw.priority, -2147483648, 2147483647), weight: integer(raw.weight, 1, 1000000), max_inflight: integer(raw.max_inflight, 1, 1000) }
}
export function parseRouteRevision(value: unknown): RouteRevision {
  const keys = ['id', 'revision', 'created_at', 'profile_version', 'release_intent', 'active', 'current_profile', 'bindings']
  const raw = object(value, keys)
  if (!Array.isArray(raw.bindings) || raw.bindings.length > 1000 || !['test_only', 'commercial'].includes(String(raw.release_intent))) throw invalid()
  return { id: id(raw.id), revision: integer(raw.revision, 1, Number.MAX_SAFE_INTEGER), created_at: timestamp(raw.created_at), profile_version: text(raw.profile_version), release_intent: raw.release_intent as RouteRevision['release_intent'], active: bool(raw.active), current_profile: bool(raw.current_profile), bindings: raw.bindings.map(parseBinding) }
}
export function parseRouteRevisionList(value: unknown, nextCursor: unknown): { rows: RouteRevision[]; next: string } {
  if (!Array.isArray(value) || value.length > 50 || typeof nextCursor !== 'string' || nextCursor.length > 512) throw invalid()
  const rows = value.map(parseRouteRevision)
  if (rows.filter(row => row.active).length > 1 || new Set(rows.map(row => row.revision)).size !== rows.length) throw invalid()
  return { rows, next: nextCursor }
}
export function routeRevisionPath(modelID: string, cursor = ''): string {
  return `/platform/models/${encodeURIComponent(id(modelID))}/route-revisions${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`
}
export async function readRouteRevisions(modelID: string, active: () => boolean = () => true): Promise<RouteRevision[]> {
  const rows: RouteRevision[] = [], cursors = new Set<string>()
  let cursor = ''
  do {
    if (!active()) throw new Error('历史查看上下文已关闭。')
    const response = await request<unknown>(routeRevisionPath(modelID, cursor))
    const page = parseRouteRevisionList(response.data, response.next_cursor ?? '')
    rows.push(...page.rows)
    if (rows.length > 1000) throw new Error('路由历史超过本次核查范围，请使用审计记录。')
    if (page.next && (cursors.has(page.next) || cursors.size >= 20)) throw new Error('路由历史分页游标无效。')
    cursor = page.next; cursors.add(cursor)
  } while (cursor)
  if (rows.filter(row => row.active).length > 1 || new Set(rows.map(row => row.revision)).size !== rows.length) throw invalid()
  return rows
}
export function routeActivation(modelID: string, expected: number, target: number): RouteActivation {
  id(modelID); integer(expected, 0, Number.MAX_SAFE_INTEGER); integer(target, 1, Number.MAX_SAFE_INTEGER)
  if (expected === target) throw new Error('目标已经是当前路由版本。')
  return Object.freeze({ modelID, expected, target, path: `${routeRevisionPath(modelID)}/${target}/activate`, method: 'POST' as const, body: Object.freeze({ expected_revision: expected }) })
}
export function acceptRouteActivation(operation: RouteActivation, value: unknown): void {
  const raw = object(value, ['revision'])
  if (integer(raw.revision, 1, Number.MAX_SAFE_INTEGER) !== operation.target) throw new Error('激活结果不能证明目标路由版本，请只读核查。')
}

function copy<T>(value: T): T { return JSON.parse(JSON.stringify(value)) as T }
export function createRouteActivationJournal() {
  const entries = new Map<string, RouteActivationReview>()
  const key = (context: string, model: string) => JSON.stringify([context, model])
  return {
    get(context: string, model: string) { const item = entries.get(key(context, model)); return item ? copy(item) : null },
    begin(context: string, model: string, operation: RouteActivation) {
      const old = entries.get(key(context, model))
      if (old && old.mode !== 'saved') throw new Error('已有激活提交或结果待核查，请先只读刷新。')
      const item: RouteActivationReview = { mode: 'submitting', operation: copy(operation) }
      entries.set(key(context, model), item)
      return copy(item)
    },
    fail(context: string, model: string, error: unknown) {
      const item = entries.get(key(context, model))
      if (!item || item.mode !== 'submitting') return
      if (error instanceof ApiError && error.status === 409) item.mode = 'conflict'
      else if (unknownCostWriteResult(error)) item.mode = 'unknown'
      else entries.delete(key(context, model))
    },
    saved(context: string, model: string) { const item = entries.get(key(context, model)); if (item?.mode === 'submitting') item.mode = 'saved' },
    observe(context: string, model: string, rows: RouteRevision[]) {
      const item = entries.get(key(context, model))
      if (!item || item.mode !== 'unknown' && item.mode !== 'conflict') throw new Error('当前没有待核查的激活。')
      const active = rows.filter(row => row.active)
      if (active.length > 1 || rows.length && !active.length) throw new Error('只读历史缺少唯一活动版本，请重新核查。')
      item.observedActive = active[0]?.revision
      return copy(item)
    },
    resume(context: string, model: string) {
      const item = entries.get(key(context, model))
      if (!item || item.mode !== 'unknown' && item.mode !== 'conflict' || item.observedActive === undefined) throw new Error('请先只读读取当前活动版本。')
      entries.delete(key(context, model))
    },
  }
}
export const routeActivationJournal = createRouteActivationJournal()
