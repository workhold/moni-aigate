import { request } from './api.ts'

export const pricingKinds = ['price_group.create', 'price_group.update', 'group_price.publish', 'group_price.compile', 'tenant_pricing.assign', 'default_price.publish', 'cost_price.publish'] as const
const publishKinds: readonly PricingKind[] = ['group_price.publish', 'group_price.compile', 'default_price.publish', 'cost_price.publish']
export type PricingKind = typeof pricingKinds[number]
export interface PriceGroup { id: string; label: string; state: 'active' | 'retired'; revision: string; created_at: string }
export interface PricingAssignment { tenant_id: string; price_group_id: string | null; pricing_revision: string }
export interface TextRates<S extends 'retail' | 'supplier'> { schema_id: string; version: '1'; side: S; currency: 'CNY'; rates: { dimension: 'text_input_token' | 'text_output_token'; unit: 'token'; numerator: string; denominator: string }[] }
export type TextRetailRates = TextRates<'retail'>
export type TextSupplierRates = TextRates<'supplier'>
export interface GroupPrice { id: string; model_id: string; price_group_id: string; schema_id: string; schema_digest: string; variant_key: 'default'; currency: 'CNY'; rates: TextRetailRates; effective_at: string; created_at: string }
export interface GroupPriceCompileItem { model_id: string; base_price_version_id: string; base_input_rate: string; base_output_rate: string; input_rate: string; output_rate: string; price_version_id: string | null }
export interface GroupPriceCompile { group_id: string; multiplier: string; effective_at: string; items: GroupPriceCompileItem[] }
export interface DefaultPrice extends Omit<GroupPrice, 'price_group_id'> { price_group_id: null }
export type CostBasis = 'unknown' | 'contract' | 'invoice'
export interface CostPrice { id: string; channel_id: string; upstream_model: string; schema_id: string; schema_digest: string; variant_key: 'default'; currency: 'CNY'; basis: CostBasis; rates: TextSupplierRates | null; effective_at: string; created_at: string; expected_channel_revision: string; channel_revision: string }
export interface PriceVersion { id: string; model_id: string; model_name: string; price_group_id: string | null; schema_id: string; schema_digest: string; variant_key: 'default'; currency: 'CNY'; rates: TextRetailRates; effective_at: string; created_at: string }
export interface RetailPrice { model_id: string; source: 'default' | 'group'; status: 'available' | 'unavailable'; price_group_id: string | null; price_version_id: string | null; schema_id: string; schema_digest: string; variant_key: 'default'; currency: 'CNY'; rates: TextRetailRates | null; effective_at: string | null; as_of: string }
export interface PricingOperation { operation_id: string; kind: PricingKind; scope: string; actor: string; resource_id: string; revision: string | null; created_at: string; target: string; input_fingerprint: string }
export interface PricingReceipt { operation: PricingOperation; result: PriceGroup | PricingAssignment | GroupPrice | GroupPriceCompile | DefaultPrice | CostPrice }
export interface PricingPreviewRow { model_id: string; public_name: string; protocol: string; retail_price: RetailPrice }
export interface PricingPreviewContext { tenant_id: string; tenant_status: 'active' | 'suspended'; current_price_group_id: string | null; current_pricing_revision: string; target_price_group_id: string | null; target_group_state: 'active' | 'retired' | null; assignable: boolean; as_of: string }
export interface PricingPage<T> { rows: T[]; next: string; context?: PricingPreviewContext }
export interface PricingCommandIdentity { operation_id: string; kind: PricingKind; scope: string; actor: string; target: string; input_fingerprint: string }
export interface PricingCommand extends PricingCommandIdentity { path: string; method: 'POST' | 'PUT'; body: Readonly<Record<string, unknown>>; previous?: PriceGroup | PricingAssignment }
const max = 9223372036854775807n, encoder = new TextEncoder()
function invalid(): never { throw new Error('售价合同响应或输入无效，请刷新核查；写操作只查询原回执。') }
export function pricingObject(value: unknown, keys?: readonly string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid()
  const raw = value as Record<string, unknown>
  if (keys && (Object.keys(raw).length !== keys.length || keys.some(key => !Object.hasOwn(raw, key)))) return invalid()
  return raw
}
function validUnicode(value: string) { return ![...value].some(char => { const cp = char.codePointAt(0)!; return cp >= 0xd800 && cp <= 0xdfff }) }
export function pricingID(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) || value === '00000000-0000-0000-0000-000000000000') return invalid()
  return value
}
export function pricingDecimal(value: unknown, positive = false): string {
  if (typeof value !== 'string' || !(positive ? /^[1-9][0-9]{0,18}$/ : /^(0|[1-9][0-9]{0,18})$/).test(value) || BigInt(value) > max) return invalid()
  return value
}
export function pricingMultiplier(value: unknown): string {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)(\.[0-9]{1,6})?$/.test(value)) return invalid()
  const [whole, fraction = ''] = value.split('.'), scaled = BigInt(whole!) * 1000000n + BigInt((fraction + '000000').slice(0, 6))
  if (scaled <= 0n || scaled > 100000000n) return invalid()
  return value
}
export function pricingLabel(value: unknown, trim = false): string {
  if (typeof value !== 'string' || !validUnicode(value) || /\p{Cc}/u.test(value)) return invalid()
  const label = trim ? value.trim() : value
  if (!label || label.trim() !== label || encoder.encode(label).length > 100) return invalid()
  return label
}
export function pricingTime(value: unknown, normalize = false): string {
  if (typeof value !== 'string') return invalid()
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,6}))?Z$/.exec(value)
  if (!match || (!normalize && match[7]?.length !== 6)) return invalid()
  const [y, m, d, h, min, sec] = match.slice(1, 7).map(Number) as [number, number, number, number, number, number]
  const leap = y % 4 === 0 && (y % 100 !== 0 || y % 400 === 0), days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  if (y < 1 || m < 1 || m > 12 || d < 1 || d > days[m - 1]! || h > 23 || min > 59 || sec > 59) return invalid()
  const result = `${value.slice(0, 19)}.${(match[7] || '').padEnd(6, '0')}Z`
  if (result === '0001-01-01T00:00:00.000000Z') return invalid()
  return result
}
function nullableID(value: unknown) { return value === null ? null : pricingID(value) }
function choice<T extends string>(value: unknown, allowed: readonly T[]): T { if (typeof value !== 'string' || !allowed.includes(value as T)) return invalid(); return value as T }
function text(value: unknown, bytes = 768): string { if (typeof value !== 'string' || !value || !validUnicode(value) || /\p{Cc}/u.test(value) || encoder.encode(value).length > bytes) return invalid(); return value }
export function parsePriceGroup(value: unknown, expectedID?: string): PriceGroup {
  const r = pricingObject(value, ['id', 'label', 'state', 'revision', 'created_at'])
  const row = { id: pricingID(r.id), label: pricingLabel(r.label), state: choice(r.state, ['active', 'retired'] as const), revision: pricingDecimal(r.revision, true), created_at: pricingTime(r.created_at) }
  if (expectedID !== undefined && row.id !== pricingID(expectedID)) return invalid()
  return row
}
export function parsePricingAssignment(value: unknown, tenant?: string): PricingAssignment {
  const r = pricingObject(value, ['tenant_id', 'price_group_id', 'pricing_revision'])
  const row = { tenant_id: pricingID(r.tenant_id), price_group_id: nullableID(r.price_group_id), pricing_revision: pricingDecimal(r.pricing_revision, true) }
  if (tenant !== undefined && row.tenant_id !== pricingID(tenant)) return invalid()
  return row
}
export function parseRetailVersion(value: unknown) {
  const r = pricingObject(value, ['id', 'model_id', 'price_group_id', 'schema_id', 'schema_digest', 'variant_key', 'currency', 'rates', 'effective_at', 'created_at'])
  const schema_id = pricingID(r.schema_id)
  return { group: r.price_group_id, row: { id: pricingID(r.id), model_id: pricingID(r.model_id), schema_id, schema_digest: pricingSchemaDigest(r.schema_digest), variant_key: choice(r.variant_key, ['default']), currency: choice(r.currency, ['CNY']), rates: parseTextRetailRates(r.rates, schema_id), effective_at: pricingTime(r.effective_at), created_at: pricingTime(r.created_at) } }
}
export function parseGroupPrice(value: unknown, group?: string): GroupPrice {
  const parsed = parseRetailVersion(value), row: GroupPrice = { ...parsed.row, price_group_id: pricingID(parsed.group) }
  if (group !== undefined && row.price_group_id !== pricingID(group)) return invalid()
  return row
}
export function parseGroupPriceCompile(value: unknown, group?: string): GroupPriceCompile {
  const r = pricingObject(value, ['group_id', 'multiplier', 'effective_at', 'items']), groupID = pricingID(r.group_id)
  if (group !== undefined && groupID !== pricingID(group)) return invalid()
  if (!Array.isArray(r.items) || r.items.length < 1 || r.items.length > 100) return invalid()
  const items = r.items.map(value => {
    const item = pricingObject(value, ['model_id', 'base_price_version_id', 'base_input_rate', 'base_output_rate', 'input_rate', 'output_rate', 'price_version_id'])
    return { model_id: pricingID(item.model_id), base_price_version_id: pricingID(item.base_price_version_id), base_input_rate: pricingDecimal(item.base_input_rate), base_output_rate: pricingDecimal(item.base_output_rate), input_rate: pricingDecimal(item.input_rate), output_rate: pricingDecimal(item.output_rate), price_version_id: item.price_version_id === null ? null : pricingID(item.price_version_id) }
  })
  if (new Set(items.map(item => item.model_id)).size !== items.length) return invalid()
  return { group_id: groupID, multiplier: pricingMultiplier(r.multiplier), effective_at: pricingTime(r.effective_at), items }
}
export function parseDefaultPrice(value: unknown): DefaultPrice {
  const parsed = parseRetailVersion(value)
  if (parsed.group !== null) return invalid()
  return { ...parsed.row, price_group_id: null }
}
export function parsePriceVersion(value: unknown): PriceVersion {
  const r = pricingObject(value, ['id', 'model_id', 'model_name', 'price_group_id', 'schema_id', 'schema_digest', 'variant_key', 'currency', 'rates', 'effective_at', 'created_at'])
  const parsed = parseRetailVersion({ id: r.id, model_id: r.model_id, price_group_id: r.price_group_id, schema_id: r.schema_id, schema_digest: r.schema_digest, variant_key: r.variant_key, currency: r.currency, rates: r.rates, effective_at: r.effective_at, created_at: r.created_at })
  return { ...parsed.row, model_name: text(r.model_name, 200), price_group_id: parsed.group === null ? null : pricingID(parsed.group) }
}
function upstreamModel(value: unknown): string {
  if (typeof value !== 'string' || !value || value.trim() !== value || !validUnicode(value) || /\p{Cc}/u.test(value) || encoder.encode(value).length > 200) return invalid()
  return value
}
export function parseCostPrice(value: unknown): CostPrice {
  const r = pricingObject(value, ['id', 'channel_id', 'upstream_model', 'schema_id', 'schema_digest', 'variant_key', 'currency', 'basis', 'rates', 'effective_at', 'created_at', 'expected_channel_revision', 'channel_revision'])
  const schema_id = pricingID(r.schema_id), basis = choice(r.basis, ['unknown', 'contract', 'invoice'] as const)
  const row: CostPrice = { id: pricingID(r.id), channel_id: pricingID(r.channel_id), upstream_model: upstreamModel(r.upstream_model), schema_id, schema_digest: pricingSchemaDigest(r.schema_digest), variant_key: choice(r.variant_key, ['default']), currency: choice(r.currency, ['CNY']), basis, rates: basis === 'unknown' ? (r.rates === null ? null : invalid()) : parseTextRates(r.rates, schema_id, 'supplier'), effective_at: pricingTime(r.effective_at), created_at: pricingTime(r.created_at), expected_channel_revision: pricingDecimal(r.expected_channel_revision, true), channel_revision: pricingDecimal(r.channel_revision, true) }
  if (BigInt(row.channel_revision) !== BigInt(row.expected_channel_revision) + 1n) return invalid()
  return row
}
export function pricingSchemaDigest(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{64}$/.test(value)) return invalid()
  return value
}
export interface PricingModelChoice { id: string; public_name: string; schema_id: string; schema_digest: string; variant_key: 'default'; inputOnly: boolean }
export function parsePricingModelChoice(value: unknown): PricingModelChoice {
  const r = pricingObject(value)
  const protocol = choice(r.protocol, ['openai_chat','openai_responses','anthropic_messages','gemini_generate_content','openai_embeddings','jina_rerank'])
  return { id: pricingID(r.id), public_name: text(r.public_name, 200), schema_id: pricingID(r.schema_id), schema_digest: pricingSchemaDigest(r.schema_digest), variant_key: choice(r.variant_key, ['default']), inputOnly: ['openai_embeddings','jina_rerank'].includes(protocol) }
}
function gcd(a: bigint, b: bigint): bigint { while (b) { const next = a % b; a = b; b = next }; return a }
export function parseTextRetailRates(value: unknown, schema: string): TextRetailRates { return parseTextRates(value, schema, 'retail') }
export function parseTextRates<S extends 'retail' | 'supplier'>(value: unknown, schema: string, side: S): TextRates<S> {
  const r = pricingObject(value, ['schema_id','version','side','currency','rates'])
  if (pricingID(r.schema_id) !== pricingID(schema) || r.version !== '1' || r.side !== side || r.currency !== 'CNY' || !Array.isArray(r.rates) || r.rates.length !== 2) return invalid()
  const rates = r.rates.map((value, index) => {
    const line = pricingObject(value, ['dimension','unit','numerator','denominator'])
    const dimension = index === 0 ? 'text_input_token' as const : 'text_output_token' as const
    const numerator = pricingDecimal(line.numerator), denominator = pricingDecimal(line.denominator, true)
    if (line.dimension !== dimension || line.unit !== 'token' || BigInt(denominator) > 1000000n || gcd(BigInt(numerator), BigInt(denominator)) !== 1n) return invalid()
    return { dimension, unit: 'token' as const, numerator, denominator }
  })
  return { schema_id: schema, version: '1', side, currency: 'CNY', rates }
}
// Management-only unit conversion; no amount calculation or scalar projection.
export function pricingInputRates<S extends 'retail' | 'supplier' = 'retail'>(schema: string, input: unknown, output: unknown, side: S = 'retail' as S): TextRates<S> {
  const rates = [input, output].map((value, index) => {
    const n = BigInt(pricingDecimal(value)), d = 1000000n, factor = gcd(n, d)
    return { dimension: index === 0 ? 'text_input_token' : 'text_output_token', unit: 'token', numerator: String(n / factor), denominator: String(d / factor) }
  })
  return parseTextRates({ schema_id: schema, version: '1', side, currency: 'CNY', rates }, schema, side)
}
export function parseRetailPrice(value: unknown, model?: string): RetailPrice {
  const r = pricingObject(value, ['model_id', 'source', 'status', 'price_group_id', 'price_version_id', 'schema_id', 'schema_digest', 'variant_key', 'currency', 'rates', 'effective_at', 'as_of'])
  const status = choice(r.status, ['available', 'unavailable']), schema_id = pricingID(r.schema_id)
  const row: RetailPrice = { model_id: pricingID(r.model_id), source: choice(r.source, ['default', 'group']), status, price_group_id: nullableID(r.price_group_id), price_version_id: nullableID(r.price_version_id), schema_id, schema_digest: pricingSchemaDigest(r.schema_digest), variant_key: choice(r.variant_key, ['default']), currency: choice(r.currency, ['CNY']), rates: status === 'available' ? parseTextRetailRates(r.rates, schema_id) : r.rates === null ? null : invalid(), effective_at: r.effective_at === null ? null : pricingTime(r.effective_at), as_of: pricingTime(r.as_of) }
  if ((model !== undefined && row.model_id !== pricingID(model)) || (row.source === 'default') !== (row.price_group_id === null)) return invalid()
  const values = [row.price_version_id, row.rates, row.effective_at]
  if (row.status === 'unavailable' ? values.some(value => value !== null) : values.some(value => value === null) || row.effective_at! > row.as_of) return invalid()
  return row
}
export function pricingCursor(value: unknown): string {
  if (typeof value !== 'string' || value.length > 1024) return invalid()
  if (value === '') return ''
  if (!/^[A-Za-z0-9_-]+$/.test(value)) return invalid()
  try { const raw = atob(value.replace(/-/g, '+').replace(/_/g, '/')); if (raw.length > 768 || btoa(raw).replace(/=+$/g, '').replace(/\+/g, '-').replace(/\//g, '_') !== value) return invalid() }
  catch { return invalid() }
  return value // 游标只作不透明边界，权限和过滤绑定由每次GET重新核验。
}
function envelope(value: unknown, list = false, preview = false): Record<string, unknown> {
  const r = pricingObject(value, list ? preview ? ['data', 'next_cursor', 'request_id', 'context'] : ['data', 'next_cursor', 'request_id'] : ['data', 'request_id'])
  text(r.request_id, 200)
  return r
}
export function parsePricingEnvelope<T>(value: unknown, parser: (data: unknown) => T): T { return parser(envelope(value).data) }
export function parsePricingPage<T>(value: unknown, parser: (data: unknown) => T, key: (row: T) => string): PricingPage<T> {
  const r = envelope(value, true)
  if (!Array.isArray(r.data) || r.data.length > 50) return invalid()
  const rows = r.data.map(parser)
  if (new Set(rows.map(key)).size !== rows.length) return invalid()
  return { rows, next: pricingCursor(r.next_cursor) }
}
export function parsePricingPreview(value: unknown, tenant: string, target: string | null): PricingPage<PricingPreviewRow> & { context: PricingPreviewContext } {
  const r = envelope(value, true, true), c = pricingObject(r.context, ['tenant_id', 'tenant_status', 'current_price_group_id', 'current_pricing_revision', 'target_price_group_id', 'target_group_state', 'assignable', 'as_of'])
  const context: PricingPreviewContext = { tenant_id: pricingID(c.tenant_id), tenant_status: choice(c.tenant_status, ['active', 'suspended']), current_price_group_id: nullableID(c.current_price_group_id), current_pricing_revision: pricingDecimal(c.current_pricing_revision, true), target_price_group_id: nullableID(c.target_price_group_id), target_group_state: c.target_group_state === null ? null : choice(c.target_group_state, ['active', 'retired'] as const), assignable: c.assignable as boolean, as_of: pricingTime(c.as_of) }
  if (context.tenant_id !== pricingID(tenant) || context.target_price_group_id !== target || typeof c.assignable !== 'boolean' || (target === null) !== (context.target_group_state === null) || context.assignable !== (context.target_group_state !== 'retired')) return invalid()
  if (!Array.isArray(r.data) || r.data.length > 50) return invalid()
  const rows = r.data.map(value => {
    const row = pricingObject(value, ['model_id', 'public_name', 'protocol', 'retail_price'])
    const model_id = pricingID(row.model_id), price = parseRetailPrice(row.retail_price, model_id)
    if (price.as_of !== context.as_of || price.price_group_id !== target) return invalid()
    const protocol = choice(row.protocol, ['openai_chat', 'openai_responses', 'anthropic_messages', 'gemini_generate_content', 'openai_embeddings', 'jina_rerank'])
    if (['openai_embeddings','jina_rerank'].includes(protocol) && price.status === 'available' && price.rates!.rates[1]!.numerator !== '0') return invalid()
    return { model_id, public_name: text(row.public_name), protocol, retail_price: price }
  })
  if (new Set(rows.map(row => row.model_id)).size !== rows.length) return invalid()
  return { rows, next: pricingCursor(r.next_cursor), context }
}

export function pricingFrame(parts: readonly (string | null)[], domain = 'moni-aigate/pricing-command/v1'): Uint8Array {
  const prefix = encoder.encode(domain), values = parts.map(part => part === null ? null : encoder.encode(part))
  const output = new Uint8Array(prefix.length + 1 + 4 + values.reduce((sum, bytes) => sum + 4 + (bytes?.length || 0), 0))
  output.set(prefix); let offset = prefix.length + 1
  const view = new DataView(output.buffer); view.setInt32(offset, values.length, false); offset += 4
  for (const bytes of values) { view.setInt32(offset, bytes === null ? -1 : bytes.length, false); offset += 4; if (bytes) { output.set(bytes, offset); offset += bytes.length } }
  return output
}
export async function pricingFingerprint(parts: readonly (string | null)[]): Promise<string> {
  const raw = pricingFrame(parts), digest = await crypto.subtle.digest('SHA-256', raw.buffer as ArrayBuffer)
  return [...new Uint8Array(digest)].map(byte => byte.toString(16).padStart(2, '0')).join('')
}
export function parsePricingIdentity(value: unknown): PricingCommandIdentity {
  const r = pricingObject(value, ['operation_id', 'kind', 'scope', 'actor', 'target', 'input_fingerprint'])
  const kind = choice(r.kind, pricingKinds), scope = kind === 'tenant_pricing.assign' ? pricingID(r.scope) : choice(r.scope, ['platform'])
  if (typeof r.input_fingerprint !== 'string' || !/^[0-9a-f]{64}$/.test(r.input_fingerprint)) return invalid()
  const target = text(r.target, 200)
  const groupID = kind === 'price_group.update' ? target.slice('/api/platform/price-groups/'.length) : kind === 'group_price.publish' ? target.slice('/api/platform/price-groups/'.length, -'/price-versions'.length) : kind === 'group_price.compile' ? target.slice('/api/platform/price-groups/'.length, -'/price-versions/compile'.length) : null
  if (kind === 'price_group.create' && target !== '/api/platform/price-groups' || kind === 'tenant_pricing.assign' && target !== `/api/platform/tenants/${scope}/pricing` || kind === 'price_group.update' && target !== `/api/platform/price-groups/${pricingID(groupID)}` || kind === 'group_price.publish' && target !== `/api/platform/price-groups/${pricingID(groupID)}/price-versions` || kind === 'group_price.compile' && target !== `/api/platform/price-groups/${pricingID(groupID)}/price-versions/compile` || kind === 'default_price.publish' && target !== '/api/platform/price-versions' || kind === 'cost_price.publish' && target !== '/api/platform/cost-versions') return invalid()
  return { operation_id: pricingID(r.operation_id), kind, scope, actor: pricingID(r.actor), target, input_fingerprint: r.input_fingerprint }
}
export function pricingIdentity(command: PricingCommandIdentity): PricingCommandIdentity { return parsePricingIdentity(Object.fromEntries(['operation_id','kind','scope','actor','target','input_fingerprint'].map(key => [key, command[key as keyof PricingCommandIdentity]]))) }
export async function pricingCommand(kind: PricingKind, operation: string, actor: string, value: unknown, targetID?: string, previous?: PriceGroup | PricingAssignment): Promise<PricingCommand> {
  pricingID(operation); pricingID(actor)
  let safe: Record<string, unknown>, parts: (string | null)[], target = '/api/platform/price-groups', scope = 'platform', method: 'POST' | 'PUT' = 'POST'
  if (kind === 'price_group.create') { const r = pricingObject(value, ['label']); safe = { label: pricingLabel(r.label, true) }; parts = [safe.label as string] }
  else if (kind === 'price_group.update') {
    const r = pricingObject(value, ['expected_revision', 'label', 'state'])
    safe = { expected_revision: pricingDecimal(r.expected_revision, true), label: pricingLabel(r.label, true), state: choice(r.state, ['active','retired']) }; parts = Object.values(safe) as string[]
    target += `/${pricingID(targetID)}`; method = 'PUT'
  } else if (kind === 'group_price.publish') {
    const r = pricingObject(value, ['model_id','schema_id','schema_digest','variant_key','input_rate','output_rate','effective_at'])
    safe = { model_id: pricingID(r.model_id), schema_id: pricingID(r.schema_id), schema_digest: pricingSchemaDigest(r.schema_digest), variant_key: choice(r.variant_key, ['default']), input_rate: pricingDecimal(r.input_rate), output_rate: pricingDecimal(r.output_rate), effective_at: pricingTime(r.effective_at, true) }
    parts = [safe.model_id as string, safe.schema_id as string, safe.schema_digest as string, 'default', 'CNY', JSON.stringify(pricingInputRates(safe.schema_id as string, safe.input_rate, safe.output_rate)), safe.effective_at as string]
    target += `/${pricingID(targetID)}/price-versions`
  } else if (kind === 'group_price.compile') {
    const r = pricingObject(value), allowed = ['multiplier', 'model_ids', 'effective_at', 'expected_base_version_ids']
    if (Object.keys(r).some(key => !allowed.includes(key)) || allowed.slice(0, 3).some(key => !Object.hasOwn(r, key))) return invalid()
    if (!Array.isArray(r.model_ids) || r.model_ids.length < 1 || r.model_ids.length > 100 || r.model_ids.some(id => typeof id !== 'string')) return invalid()
    const model_ids = [...new Set(r.model_ids.map(id => pricingID(id as string)))].sort()
    let expected: Record<string, string> | undefined
    if (Object.hasOwn(r, 'expected_base_version_ids')) {
      const raw = pricingObject(r.expected_base_version_ids), entries = Object.entries(raw)
      expected = Object.fromEntries(entries.map(([model, version]) => [pricingID(model), pricingID(version)]))
      if (entries.some(([model]) => !model_ids.includes(pricingID(model)))) return invalid()
    }
    safe = { multiplier: pricingMultiplier(r.multiplier), model_ids, effective_at: pricingTime(r.effective_at, true), ...(expected === undefined ? {} : { expected_base_version_ids: expected }) }
    parts = [safe.multiplier as string, JSON.stringify(model_ids), safe.effective_at as string, expected === undefined ? null : JSON.stringify(Object.fromEntries(Object.entries(expected).sort(([a], [b]) => a.localeCompare(b))))]
    target += `/${pricingID(targetID)}/price-versions/compile`
  } else if (kind === 'default_price.publish') {
    const r = pricingObject(value, ['model_id','schema_id','schema_digest','variant_key','input_rate','output_rate','effective_at'])
    safe = { model_id: pricingID(r.model_id), schema_id: pricingID(r.schema_id), schema_digest: pricingSchemaDigest(r.schema_digest), variant_key: choice(r.variant_key, ['default']), input_rate: pricingDecimal(r.input_rate), output_rate: pricingDecimal(r.output_rate), effective_at: pricingTime(r.effective_at, true) }
    parts = [safe.model_id as string, safe.schema_id as string, safe.schema_digest as string, 'default', 'CNY', JSON.stringify(pricingInputRates(safe.schema_id as string, safe.input_rate, safe.output_rate)), safe.effective_at as string]
    target = '/api/platform/price-versions'
  } else if (kind === 'cost_price.publish') {
    const r = pricingObject(value, ['channel_id','upstream_model','schema_id','schema_digest','variant_key','basis','input_rate','output_rate','effective_at','expected_channel_revision'])
    const basis = choice(r.basis, ['unknown', 'contract', 'invoice'] as const), known = basis !== 'unknown'
    safe = { channel_id: pricingID(r.channel_id), upstream_model: upstreamModel(r.upstream_model), schema_id: pricingID(r.schema_id), schema_digest: pricingSchemaDigest(r.schema_digest), variant_key: choice(r.variant_key, ['default']), basis, input_rate: known ? pricingDecimal(r.input_rate) : r.input_rate === null ? null : invalid(), output_rate: known ? pricingDecimal(r.output_rate) : r.output_rate === null ? null : invalid(), effective_at: pricingTime(r.effective_at, true), expected_channel_revision: pricingDecimal(r.expected_channel_revision, true) }
    parts = [safe.channel_id as string, safe.upstream_model as string, safe.schema_id as string, safe.schema_digest as string, 'default', 'CNY', basis, known ? JSON.stringify(pricingInputRates(safe.schema_id as string, safe.input_rate, safe.output_rate, 'supplier')) : null, safe.effective_at as string, safe.expected_channel_revision as string]
    target = '/api/platform/cost-versions'
  } else if (kind === 'tenant_pricing.assign') {
    const r = pricingObject(value, ['expected_revision','price_group_id'])
    safe = { expected_revision: pricingDecimal(r.expected_revision, true), price_group_id: nullableID(r.price_group_id) }; parts = Object.values(safe) as (string | null)[]
    scope = pricingID(targetID); target = `/api/platform/tenants/${scope}/pricing`; method = 'PUT'
  } else return invalid()
  const identity = { operation_id: operation, kind, scope, actor, target, input_fingerprint: await pricingFingerprint([kind, actor, scope, target, ...parts]) }
  return Object.freeze({ ...identity, path: target.slice(4), method, body: Object.freeze({ operation_id: operation, ...safe }), ...(previous ? { previous: Object.freeze({ ...previous }) } : {}) })
}
export function parsePricingReceipt(value: unknown, expected?: PricingCommandIdentity | PricingCommand): PricingReceipt {
  const r = pricingObject(value, ['operation','result']), raw = pricingObject(r.operation, ['operation_id','kind','scope','actor','resource_id','revision','created_at','target','input_fingerprint'])
  const identity = parsePricingIdentity(Object.fromEntries(['operation_id','kind','scope','actor','target','input_fingerprint'].map(key => [key, raw[key]])))
  const operation: PricingOperation = { ...identity, resource_id: pricingID(raw.resource_id), revision: raw.revision === null ? null : pricingDecimal(raw.revision, true), created_at: pricingTime(raw.created_at) }
  if (publishKinds.includes(operation.kind) !== (operation.revision === null)) return invalid()
  if (expected && Object.entries(pricingIdentity(expected)).some(([key, val]) => operation[key as keyof PricingOperation] !== val)) return invalid()
  let result: PricingReceipt['result']
  if (operation.kind === 'tenant_pricing.assign') {
    result = parsePricingAssignment(r.result, operation.scope)
    if (result.tenant_id !== operation.resource_id || result.pricing_revision !== operation.revision) return invalid()
  } else if (operation.kind === 'group_price.publish') {
    result = parseGroupPrice(r.result, operation.target.split('/')[4])
    if (result.id !== operation.resource_id || result.created_at !== operation.created_at) return invalid()
  } else if (operation.kind === 'group_price.compile') {
    result = parseGroupPriceCompile(r.result, operation.target.slice('/api/platform/price-groups/'.length, -'/price-versions/compile'.length))
    if (result.group_id !== operation.resource_id || result.items.some(item => item.price_version_id === null)) return invalid()
  } else if (operation.kind === 'default_price.publish') {
    result = parseDefaultPrice(r.result)
    if (result.id !== operation.resource_id || result.created_at !== operation.created_at) return invalid()
  } else if (operation.kind === 'cost_price.publish') {
    result = parseCostPrice(r.result)
    if (result.id !== operation.resource_id || result.created_at !== operation.created_at) return invalid()
  } else {
    result = parsePriceGroup(r.result, operation.resource_id)
    if (result.revision !== operation.revision || operation.kind === 'price_group.update' && operation.target !== `/api/platform/price-groups/${result.id}` || operation.kind === 'price_group.create' && (result.state !== 'active' || result.revision !== '1' || result.created_at !== operation.created_at)) return invalid()
  }
  if (expected && 'body' in expected) {
    const b = expected.body, data = result as unknown as Record<string, unknown>
    const fields = operation.kind === 'price_group.create' ? ['label'] : operation.kind === 'price_group.update' ? ['label','state'] : operation.kind === 'group_price.publish' || operation.kind === 'default_price.publish' ? ['model_id','schema_id','schema_digest','variant_key','effective_at'] : operation.kind === 'group_price.compile' ? ['multiplier','effective_at'] : operation.kind === 'cost_price.publish' ? ['channel_id','upstream_model','schema_id','schema_digest','variant_key','basis','effective_at','expected_channel_revision'] : ['price_group_id']
    if (fields.some(key => b[key] !== data[key])) return invalid()
    if ((operation.kind === 'group_price.publish' || operation.kind === 'default_price.publish') && JSON.stringify(pricingInputRates(b.schema_id as string, b.input_rate, b.output_rate)) !== JSON.stringify((result as GroupPrice | DefaultPrice).rates)) return invalid()
    if (operation.kind === 'group_price.compile') {
      const compiled = result as GroupPriceCompile, expectedModels = [...new Set(b.model_ids as string[])].sort()
      if (compiled.multiplier !== b.multiplier || compiled.effective_at !== b.effective_at || JSON.stringify(compiled.items.map(item => item.model_id)) !== JSON.stringify(expectedModels)) return invalid()
    }
    if (operation.kind === 'cost_price.publish' && JSON.stringify(b.basis === 'unknown' ? null : pricingInputRates(b.schema_id as string, b.input_rate, b.output_rate, 'supplier')) !== JSON.stringify((result as CostPrice).rates)) return invalid()
    if (operation.kind === 'price_group.update' || operation.kind === 'tenant_pricing.assign') {
      const before = pricingDecimal(b.expected_revision, true), actual = operation.revision!, prior = expected.previous as unknown as Record<string, unknown> | undefined
      const change = prior ? fields.some(key => prior[key] !== b[key]) : undefined
      if (change === undefined ? actual !== before && BigInt(actual) !== BigInt(before) + 1n : BigInt(actual) !== BigInt(before) + (change ? 1n : 0n)) return invalid()
    }
  }
  return { operation, result }
}
export function pricingQuery(path: string, cursor = '') { const c = pricingCursor(cursor); return c ? `${path}${path.includes('?') ? '&' : '?'}cursor=${encodeURIComponent(c)}` : path }
export function pricingPreviewPath(tenant: string, target: string | null, cursor = '') { return pricingQuery(`/platform/tenants/${pricingID(tenant)}/pricing-preview?price_group_id=${target === null ? 'default' : pricingID(target)}`, cursor) }
// The local deadline also suppresses late auth side effects; mutation completion is never auto-retried.
export async function pricingRequest(path: string, method = 'GET', body?: unknown, current: () => boolean = () => true): Promise<unknown> {
  let live = true, timer: ReturnType<typeof setTimeout> | undefined
  const deadline = new Promise<never>((_, reject) => { timer = setTimeout(() => { live = false; reject(new Error('请求等待超时；写操作结果待核查，请只读查询原回执。')) }, method === 'GET' ? 6000 : 10500) })
  try { return await Promise.race([request<unknown>(path, method, body, undefined, () => live && current()), deadline]) }
  finally { live = false; clearTimeout(timer) }
}
