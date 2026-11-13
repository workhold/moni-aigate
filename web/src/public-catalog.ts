export const publicProtocols = [
  { id: 'openai_chat', title: 'OpenAI Chat' },
  { id: 'openai_responses', title: 'OpenAI Responses' },
  { id: 'anthropic_messages', title: 'Anthropic Messages' },
  { id: 'openai_embeddings', title: 'OpenAI Embeddings' },
  { id: 'jina_rerank', title: 'Jina Rerank' },
  { id: 'gemini_generate_content', title: 'Gemini 原生文本' },
] as const
export type PublicProtocol = typeof publicProtocols[number]['id']
import { parseTextRetailRates } from './pricing.ts'
import { rateIsZero } from './metering-wire.ts'
import type { TextRetailRates } from './pricing.ts'
export interface PublicFilters { q: string; protocol: PublicProtocol | '' }
export interface PublicModel {
  id: string; public_name: string; aliases?: string[]; protocol: PublicProtocol
  capabilities: { streaming: boolean; tools: boolean; input_only: boolean; text_only: true }
  limits: { context_tokens: number; input_token_upper_bound: number; max_output_tokens: number; default_output_tokens: number }
  pricing: { status: 'available' | 'unavailable'; price_version_id: string | null; schema_id: string; schema_digest: string; variant_key: 'default'; currency: 'CNY'; rates: TextRetailRates | null; effective_at: string | null }
}
export interface PublicCatalogPage { data: PublicModel[]; request_id: string; next_cursor: string; as_of: string }
export interface ModelPublication { id: string; public_name: string; enabled: boolean; public_visible: boolean; public_revision: string }
export interface PublicationRequest { readonly path: string; readonly method: 'PUT'; readonly body: { readonly expected_revision: string; readonly public_visible: boolean } }

const invalid = () => new Error('目录响应格式无效，请刷新后重试。')
function object(value: unknown, keys: readonly string[], optional: readonly string[] = []): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw invalid()
  const raw = value as Record<string, unknown>, own = Object.keys(raw), allowed = [...keys, ...optional]
  if (own.some(key => !allowed.includes(key)) || keys.some(key => !own.includes(key))) throw invalid()
  return raw
}
function bytes(value: string) { return new TextEncoder().encode(value).length }
function revision(value: unknown): string { if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) throw invalid(); return value }
export function catalogID(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) || value === '00000000-0000-0000-0000-000000000000') throw invalid()
  return value
}
function name(value: unknown): string {
  if (typeof value !== 'string' || !value || bytes(value) > 100 || /[\s\p{Cc}]/u.test(value)) throw invalid()
  return value
}
export function catalogTimestamp(value: unknown): string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$/.test(value)) throw invalid()
  const date = new Date(value)
  if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0, 19) !== value.slice(0, 19)) throw invalid()
  return value
}
function protocol(value: unknown): PublicProtocol {
  if (!publicProtocols.some(item => item.id === value)) throw invalid()
  return value as PublicProtocol
}
function bool(value: unknown): boolean { if (typeof value !== 'boolean') throw invalid(); return value }
function tokenLimit(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0 || value > 10000000) throw invalid()
  return value
}
export function parsePublicModel(value: unknown): PublicModel {
  const raw = object(value, ['id', 'public_name', 'protocol', 'capabilities', 'limits', 'pricing'], ['aliases'])
  const id = catalogID(raw.id), public_name = name(raw.public_name), kind = protocol(raw.protocol)
  let aliases: string[] | undefined
  if (raw.aliases !== undefined) {
    if (!Array.isArray(raw.aliases) || raw.aliases.length > 100) throw invalid()
    aliases = raw.aliases.map(name)
    if (new Set(aliases).size !== aliases.length) throw invalid()
  }
  const caps = object(raw.capabilities, ['streaming', 'tools', 'input_only', 'text_only'])
  const inputOnly = kind === 'openai_embeddings' || kind === 'jina_rerank'
  const capabilities = { streaming: bool(caps.streaming), tools: bool(caps.tools), input_only: bool(caps.input_only), text_only: bool(caps.text_only) }
  if (!capabilities.text_only || capabilities.input_only !== inputOnly || capabilities.streaming === inputOnly || (inputOnly || kind === 'gemini_generate_content') && capabilities.tools) throw invalid()
  const limitsRaw = object(raw.limits, ['context_tokens', 'input_token_upper_bound', 'max_output_tokens', 'default_output_tokens'])
  const limits = { context_tokens: tokenLimit(limitsRaw.context_tokens), input_token_upper_bound: tokenLimit(limitsRaw.input_token_upper_bound), max_output_tokens: tokenLimit(limitsRaw.max_output_tokens), default_output_tokens: tokenLimit(limitsRaw.default_output_tokens) }
  if (!limits.context_tokens || !limits.input_token_upper_bound || limits.input_token_upper_bound > limits.context_tokens || (inputOnly ? limits.max_output_tokens !== 0 || limits.default_output_tokens !== 0 : limits.default_output_tokens <= 0 || limits.max_output_tokens < limits.default_output_tokens || limits.max_output_tokens > limits.context_tokens)) throw invalid()
  const price = object(raw.pricing, ['status', 'price_version_id', 'schema_id', 'schema_digest', 'variant_key', 'currency', 'rates', 'effective_at'])
  if (price.currency !== 'CNY' || price.variant_key !== 'default' || price.status !== 'available' && price.status !== 'unavailable') throw invalid()
  const schema_id = catalogID(price.schema_id), schema_digest = price.schema_digest
  if (typeof schema_digest !== 'string' || !/^[0-9a-f]{64}$/.test(schema_digest)) throw invalid()
  let pricing: PublicModel['pricing']
  if (price.status === 'unavailable') {
    if ([price.price_version_id, price.rates, price.effective_at].some(v => v !== null)) throw invalid()
    pricing = { status: 'unavailable', currency: 'CNY', price_version_id: null, schema_id, schema_digest, variant_key: 'default', rates: null, effective_at: null }
  } else {
    if (price.price_version_id === null || price.effective_at === null) throw invalid()
    const rates = parseTextRetailRates(price.rates, schema_id)
    if (inputOnly && !rateIsZero(rates, 'text_output_token')) throw invalid()
    pricing = { status: 'available', currency: 'CNY', price_version_id: catalogID(price.price_version_id), schema_id, schema_digest, variant_key: 'default', rates, effective_at: catalogTimestamp(price.effective_at) }
  }
  return { id, public_name, ...(aliases === undefined ? {} : { aliases }), protocol: kind, capabilities: { ...capabilities, text_only: true }, limits, pricing }
}
function requestID(value: unknown): string {
  if (typeof value !== 'string' || !value || value.length > 200 || /[\s\p{Cc}]/u.test(value)) throw invalid()
  return value
}
function cursor(value: unknown): string {
  if (typeof value !== 'string' || value.length > 1024 || value && !/^[A-Za-z0-9_-]+$/.test(value)) throw invalid()
  return value
}
export function parsePublicCatalogPage(value: unknown): PublicCatalogPage {
  const raw = object(value, ['data', 'request_id', 'next_cursor', 'as_of'])
  if (!Array.isArray(raw.data) || raw.data.length > 50) throw invalid()
  const data = raw.data.map(parsePublicModel), as_of = catalogTimestamp(raw.as_of)
  const orderTime = (timestamp: string) => `${timestamp.slice(0, 19)}.${(timestamp.split('.')[1]?.slice(0, -1) || '').padEnd(6, '0')}`
  if (new Set(data.map(row => row.id)).size !== data.length || data.some(row => row.pricing.effective_at !== null && orderTime(row.pricing.effective_at) > orderTime(as_of))) throw invalid()
  return { data, request_id: requestID(raw.request_id), next_cursor: cursor(raw.next_cursor), as_of }
}
export function publicFilters(q: string, selectedProtocol: string): PublicFilters {
  const query = q.trim()
  if (bytes(query) > 100 || /\p{Cc}/u.test(query)) throw new Error('模型名称前缀最多100字节，不能包含控制字符。')
  return { q: query, protocol: selectedProtocol === '' ? '' : protocol(selectedProtocol) }
}
export function publicCatalogPath(filters: PublicFilters, nextCursor = ''): string {
  const normalized = publicFilters(filters.q, filters.protocol), query = new URLSearchParams()
  if (normalized.q) query.set('q', normalized.q)
  if (normalized.protocol) query.set('protocol', normalized.protocol)
  if (nextCursor) query.set('cursor', cursor(nextCursor))
  return `/api/public/models${query.size ? `?${query}` : ''}`
}
export class PublicCatalogError extends Error {
  readonly code: string
  readonly requestId: string
  readonly status: number
  constructor(code: string, message: string, requestId: string, status: number) { super(message); this.code = code; this.requestId = requestId; this.status = status }
}
// This path deliberately never imports the private request/session machinery.
export async function fetchPublicCatalog(filters: PublicFilters, nextCursor = '', signal?: AbortSignal): Promise<PublicCatalogPage> {
  let response: Response
  try { response = await fetch(publicCatalogPath(filters, nextCursor), { method: 'GET', headers: { Accept: 'application/json' }, credentials: 'omit', cache: 'no-store', redirect: 'error', signal }) }
  catch (error) { if (error instanceof Error && error.name === 'AbortError') throw error; throw new PublicCatalogError('network_error', '无法连接公开目录，请稍后刷新。', '', 0) }
  let raw: unknown
  try { raw = await response.json() } catch { throw new PublicCatalogError('invalid_response', '目录响应格式无效，请稍后刷新。', '', response.status) }
  if (!response.ok) {
    let code = 'public_catalog_unavailable', id = ''
    try { const envelope = object(raw, ['error', 'request_id']), detail = object(envelope.error, ['code', 'message']); id = requestID(envelope.request_id); if (typeof detail.code === 'string' && /^[a-z_]{1,80}$/.test(detail.code)) code = detail.code } catch { /* Use safe local copy, never reflect an untrusted response body. */ }
    const message = response.status === 429 ? '公开目录访问较频繁，请稍后重试。' : response.status === 400 ? '筛选或分页信息已失效，请重新筛选。' : '公开目录暂时不可用，请稍后刷新。'
    throw new PublicCatalogError(code, message, id, response.status)
  }
  try { return parsePublicCatalogPage(raw) } catch { throw new PublicCatalogError('invalid_response', '目录响应格式无效，请稍后刷新。', '', response.status) }
}
export function publicCatalogErrorText(error: unknown): string {
  if (error instanceof PublicCatalogError) return `${error.message}${error.requestId ? `（请求 ${error.requestId}）` : ''}`
  return error instanceof Error ? error.message : '公开目录暂时不可用。'
}
export function parsePublication(value: unknown, expectedID?: string): ModelPublication {
  const raw = object(value, ['id', 'public_name', 'enabled', 'public_visible', 'public_revision']), id = catalogID(raw.id)
  if (expectedID !== undefined && id !== expectedID) throw new Error('公开配置返回了不同模型，请重新读取。')
  const version = revision(raw.public_revision)
  return { id, public_name: name(raw.public_name), enabled: bool(raw.enabled), public_visible: bool(raw.public_visible), public_revision: version }
}
export function publicationPath(id: string) { return `/platform/models/${catalogID(id)}/publication` }
export function publicationWrite(view: ModelPublication, visible: boolean): PublicationRequest {
  const parsed = parsePublication(view)
  return Object.freeze({ path: publicationPath(parsed.id), method: 'PUT', body: Object.freeze({ expected_revision: parsed.public_revision, public_visible: bool(visible) }) })
}
export function publicationLabel(view: Pick<ModelPublication, 'enabled' | 'public_visible'>): string { return view.public_visible ? view.enabled ? '已公开' : '已设公开 · 停用隐藏' : '私有' }
