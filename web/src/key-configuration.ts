import { ApiError } from './api.ts'
import { cnyToMicro } from './format.ts'
import { unknownKeyWriteResult } from './key-access-policy.ts'
import { keyCreateWithLimitsRequest, parseKeyLimitsRow, parseKeyLimitsRows, retainNewerKeyConfigurations } from './key-runtime-limits.ts'
import type { KeyLimitsRow } from './key-runtime-limits.ts'

export interface KeyConfigurationFields {
  name: string; scopes: readonly string[]; allowed_regions: readonly string[]
  lifetime_limit: string | null; expires_at: string | null
}
export interface KeyConfigurationView extends KeyConfigurationFields { id: string; configuration_revision: string; spent: string; held: string }
export interface KeyConfigurationRow extends KeyLimitsRow { allowed_regions: string[]; configuration_revision: string }
export interface KeyConfigurationDraft {
  name: string; scopes: string[]; allowed_regions: string[]; limit: string
  expiry_mode: 'keep' | 'set' | 'none'; expiry: string
}
export interface KeyConfigurationRequest {
  readonly path: string; readonly method: 'PUT'
  readonly body: Readonly<KeyConfigurationFields & { expected_revision: string }>
}
export interface KeyConfigurationReview {
  readonly view: KeyConfigurationView; readonly mode: 'editing' | 'submitting' | 'conflict' | 'unknown'
  readonly submitted?: KeyConfigurationRequest; readonly observed?: KeyConfigurationView
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('基本配置响应格式无效。')
  return value as Record<string, unknown>
}
function identity(value: unknown): string {
  if (typeof value !== 'string' || !value || value.length > 200 || value.trim() !== value || /\p{Cc}/u.test(value)) throw new Error('密钥标识无效。')
  return value
}
function decimal(value: unknown): string {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error('配置金额须为非负 int64 十进制字符串。')
  return value
}
function stringArray(value: unknown): string[] {
  if (!Array.isArray(value) || value.some(item => typeof item !== 'string')) throw new Error('模型范围或地域响应须为字符串数组。')
  return [...value]
}
function sameArray(left: readonly string[], right: readonly string[]): boolean { return left.length === right.length && left.every((value, index) => value === right[index]) }
function trimText(value: string): string { return value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '') }
export function keyConfigurationRevision(value: unknown): string {
  if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error('基本配置版本须为正 int64 十进制字符串。')
  return value
}
export function keyMicroToCNY(value: string): string {
  const amount = BigInt(decimal(value)), fraction = (amount % 1000000n).toString().padStart(6, '0').replace(/0+$/, '')
  return `${amount / 1000000n}${fraction ? `.${fraction}` : ''}`
}

// Date supplies calendar/offset arithmetic only at exact whole seconds. The
// fractional part stays in BigInt so microseconds never pass through Number.
function rfc3339(value: unknown): { micros: bigint; canonical: string } {
  if (typeof value !== 'string') throw new Error('有效期须为包含时区的 RFC3339 时间或明确移除期限。')
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/.exec(value)
  if (!match) throw new Error('请输入有效的 RFC3339 时间，例如 2030-01-02T03:04:05.123456+08:00。')
  const [year, month, day, hour, minute, second] = match.slice(1, 7).map(Number)
  if (month! < 1 || month! > 12 || day! < 1 || day! > 31 || hour! > 23 || minute! > 59 || second! > 59) throw new Error('有效期日期或时间无效。')
  const calendar = new Date(0)
  calendar.setUTCFullYear(year!, month! - 1, day!); calendar.setUTCHours(hour!, minute!, second!, 0)
  if (calendar.getUTCFullYear() !== year || calendar.getUTCMonth() !== month! - 1 || calendar.getUTCDate() !== day) throw new Error('有效期日期无效。')
  const zone = match[8]!, zoneHour = zone === 'Z' ? 0 : Number(zone.slice(1, 3)), zoneMinute = zone === 'Z' ? 0 : Number(zone.slice(4, 6))
  if (zoneHour > 23 || zoneMinute > 59) throw new Error('有效期时区无效。')
  const offset = (zoneHour * 60 + zoneMinute) * (zone.startsWith('-') ? -1 : 1)
  const micros = BigInt(calendar.getTime()) * 1000n - BigInt(offset) * 60000000n + BigInt((match[7] || '').slice(0, 6).padEnd(6, '0'))
  const fraction = ((micros % 1000000n) + 1000000n) % 1000000n, whole = (micros - fraction) / 1000000n
  const iso = new Date(Number(whole * 1000n)).toISOString()
  if (!/^\d{4}-/.test(iso)) throw new Error('有效期年份超出可接受范围。')
  const digits = fraction.toString().padStart(6, '0').replace(/0+$/, '')
  return { micros, canonical: `${iso.slice(0, -5)}${digits ? `.${digits}` : ''}Z` }
}
export function normalizeKeyConfigurationExpiry(value: string): string { return rfc3339(value).canonical }
function expiry(value: unknown): string | null { if (value === null) return null; rfc3339(value); return value as string }
function sameExpiry(left: string | null, right: string | null): boolean { return left === null || right === null ? left === right : rfc3339(left).micros === rfc3339(right).micros }
function fields(value: unknown): KeyConfigurationFields {
  const raw = record(value)
  if (typeof raw.name !== 'string') throw new Error('密钥名称响应无效。')
  return { name: raw.name, scopes: stringArray(raw.scopes), allowed_regions: stringArray(raw.allowed_regions), lifetime_limit: raw.lifetime_limit === null ? null : decimal(raw.lifetime_limit), expires_at: expiry(raw.expires_at) }
}
export function keyConfigurationPath(keyID: unknown): string { return `/keys/${encodeURIComponent(identity(keyID))}/configuration` }
export function parseKeyConfigurationView(value: unknown, expectedID?: string): KeyConfigurationView {
  const raw = record(value), id = identity(raw.id)
  if (expectedID !== undefined && id !== expectedID) throw new Error('基本配置返回了不同的密钥，请重新读取核实。')
  return { id, ...fields(raw), configuration_revision: keyConfigurationRevision(raw.configuration_revision), spent: decimal(raw.spent), held: decimal(raw.held) }
}
export function parseKeyConfigurationRow(value: unknown): KeyConfigurationRow {
  const row = parseKeyLimitsRow(value), view = parseKeyConfigurationView(value, row.id)
  return { ...row, name: view.name, scopes: [...view.scopes], allowed_regions: [...view.allowed_regions], lifetime_limit: view.lifetime_limit, expires_at: view.expires_at ?? undefined, configuration_revision: view.configuration_revision }
}
export function parseKeyConfigurationRows(value: unknown): KeyConfigurationRow[] {
  const rows = parseKeyLimitsRows(value)
  return rows.map((row, index) => { const view = parseKeyConfigurationView((value as unknown[])[index], row.id); return { ...row, allowed_regions: [...view.allowed_regions], configuration_revision: view.configuration_revision } })
}
export function keyConfigurationDraft(value: KeyConfigurationFields): KeyConfigurationDraft {
  const source = fields(value)
  return { name: source.name, scopes: [...source.scopes], allowed_regions: [...source.allowed_regions], limit: source.lifetime_limit === null ? '' : keyMicroToCNY(source.lifetime_limit), expiry_mode: 'keep', expiry: source.expires_at ?? '' }
}
function changedName(value: string): string {
  const name = trimText(value)
  if (!name || new TextEncoder().encode(name).length > 100 || /[\p{Cc}\p{Cs}]/u.test(name)) throw new Error('新名称须为 1—100 UTF-8 字节，且不能包含控制字符或无效 Unicode。')
  return name
}
function changedScopes(values: readonly string[]): string[] {
  if (values.length === 1 && values[0] === '*') return ['*']
  if (!values.length || values.length > 1000 || values.some(value => !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value))) throw new Error('新模型范围须为单独的 *，或 1—1000 个规范模型 UUID。旧值未改时会原样保留。')
  return [...new Set(values)].sort()
}
function changedRegions(values: readonly string[]): string[] {
  if (!values.length || values.length > 32) throw new Error('新地域范围须有 1—32 项。旧值未改时会原样保留。')
  const regions = values.map(value => trimText(value))
  if (regions.some(value => !value || new TextEncoder().encode(value).length > 80 || /[\p{Cc}\p{Cs}]/u.test(value)) || regions.includes('*') && regions.length !== 1) throw new Error('新地域标签须为 1—80 UTF-8 字节、有效 Unicode 且无控制字符；* 须单独一项。')
  // Go sorts valid UTF-8 strings by bytes, which follows Unicode code points.
  // JavaScript's default UTF-16 sort differs when astral characters are used.
  return [...new Set(regions)].sort((left, right) => {
    const a = Array.from(left, char => char.codePointAt(0)!), b = Array.from(right, char => char.codePointAt(0)!)
    for (let index = 0; index < Math.min(a.length, b.length); index++) if (a[index] !== b[index]) return a[index]! - b[index]!
    return a.length - b.length
  })
}
export function keyCreateWithConfigurationRequest(input: Parameters<typeof keyCreateWithLimitsRequest>[0] & { allowed_regions: readonly string[] }) {
  const base = keyCreateWithLimitsRequest(input)
  return Object.freeze({ ...base, body: Object.freeze({ ...base.body, allowed_regions: Object.freeze(changedRegions(input.allowed_regions)) }) })
}
export function keyConfigurationSaveRequest(view: KeyConfigurationView, draft: KeyConfigurationDraft): KeyConfigurationRequest {
  const source = parseKeyConfigurationView(view), scope = stringArray(draft.scopes), regions = stringArray(draft.allowed_regions)
  if (typeof draft.name !== 'string' || typeof draft.limit !== 'string') throw new Error('名称和金额输入无效。')
  let expires: string | null
  if (draft.expiry_mode === 'keep') expires = source.expires_at
  else if (draft.expiry_mode === 'none') expires = null
  else if (draft.expiry_mode === 'set') { expires = normalizeKeyConfigurationExpiry(draft.expiry); if (sameExpiry(expires, source.expires_at)) expires = source.expires_at }
  else throw new Error('请明确选择保留、设置或移除期限。')
  const body = { expected_revision: source.configuration_revision, name: draft.name === source.name ? source.name : changedName(draft.name), scopes: Object.freeze(sameArray(scope, source.scopes) ? [...source.scopes] : changedScopes(scope)), allowed_regions: Object.freeze(sameArray(regions, source.allowed_regions) ? [...source.allowed_regions] : changedRegions(regions)), lifetime_limit: draft.limit.trim() ? cnyToMicro(draft.limit) : null, expires_at: expires }
  return Object.freeze({ path: keyConfigurationPath(source.id), method: 'PUT', body: Object.freeze(body) })
}
function sameConfiguration(left: KeyConfigurationFields, right: KeyConfigurationFields): boolean { return left.name === right.name && sameArray(left.scopes, right.scopes) && sameArray(left.allowed_regions, right.allowed_regions) && left.lifetime_limit === right.lifetime_limit && sameExpiry(left.expires_at, right.expires_at) }
export function newKeyConfigurationReview(value: unknown): KeyConfigurationReview { return { view: parseKeyConfigurationView(value), mode: 'editing' } }
export function submitKeyConfigurationReview(review: KeyConfigurationReview, draft: KeyConfigurationDraft): KeyConfigurationReview {
  if (review.mode !== 'editing') throw new Error('请先只读刷新并复核当前基本配置，再明确开始新编辑。')
  return { ...review, mode: 'submitting', submitted: keyConfigurationSaveRequest(review.view, draft) }
}
export function failKeyConfigurationReview(review: KeyConfigurationReview, error: unknown): KeyConfigurationReview {
  if (review.mode !== 'submitting') throw new Error('基本配置没有待确认的提交。')
  return { ...review, mode: error instanceof ApiError && error.status === 409 ? 'conflict' : unknownKeyWriteResult(error) ? 'unknown' : 'editing', observed: undefined }
}
export function acceptKeyConfigurationSave(review: KeyConfigurationReview, value: unknown): KeyConfigurationReview {
  if (review.mode !== 'submitting' || !review.submitted) throw new Error('基本配置没有待确认的提交。')
  const view = parseKeyConfigurationView(value, review.view.id), before = BigInt(review.submitted.body.expected_revision), revision = BigInt(view.configuration_revision)
  if (!sameConfiguration(view, review.submitted.body) || revision < before || revision > before + 1n || (revision === before) !== sameConfiguration(view, review.view)) throw new Error('基本配置保存响应与冻结提交不一致，请只读刷新核实。')
  return newKeyConfigurationReview(view)
}
export function observeKeyConfiguration(review: KeyConfigurationReview, value: unknown): KeyConfigurationReview {
  if (review.mode !== 'conflict' && review.mode !== 'unknown') throw new Error('当前没有待复核的基本配置。')
  const observed = parseKeyConfigurationView(value, review.view.id), revision = BigInt(observed.configuration_revision), before = BigInt(review.view.configuration_revision)
  if (revision < before || revision === before && !sameConfiguration(observed, review.view)) throw new Error('当前基本配置响应版本不一致，请重新只读刷新。')
  return { ...review, observed }
}
export function resumeKeyConfigurationReview(review: KeyConfigurationReview): KeyConfigurationReview {
  if (!review.observed || review.mode !== 'conflict' && review.mode !== 'unknown') throw new Error('请先只读刷新当前基本配置并复核。')
  return newKeyConfigurationReview(review.observed)
}
function configurationRowFields(view: KeyConfigurationView) { return { name: view.name, scopes: [...view.scopes], allowed_regions: [...view.allowed_regions], lifetime_limit: view.lifetime_limit, expires_at: view.expires_at ?? undefined, configuration_revision: view.configuration_revision } }
export function applyKeyConfigurationToRows(rows: KeyConfigurationRow[], view: KeyConfigurationView): KeyConfigurationRow[] {
  return rows.map(row => row.id === view.id && BigInt(view.configuration_revision) >= BigInt(row.configuration_revision) ? { ...row, ...configurationRowFields(view) } : row)
}
export function retainNewerKeyBasicConfigurations(incoming: KeyConfigurationRow[], known: KeyConfigurationRow[]): KeyConfigurationRow[] {
  const previous = new Map(known.map(row => [row.id, row]))
  return incoming.map(row => { const existing = previous.get(row.id); return existing && BigInt(existing.configuration_revision) > BigInt(row.configuration_revision) ? { ...row, name: existing.name, scopes: [...existing.scopes], allowed_regions: [...existing.allowed_regions], lifetime_limit: existing.lifetime_limit, expires_at: existing.expires_at, configuration_revision: existing.configuration_revision } : row })
}
export function retainNewerKeySettings(incoming: KeyConfigurationRow[], known: KeyConfigurationRow[]): KeyConfigurationRow[] { return retainNewerKeyBasicConfigurations(retainNewerKeyConfigurations(incoming, known), known) }
export function keyConfigurationOptions(values: readonly string[], choices: { value: string; label: string }[]): { value: string; label: string }[] {
  const options = new Map(choices.map(option => [option.value, { ...option }]))
  for (const value of values) if (!options.has(value)) options.set(value, { value, label: `目录外或历史值 ${JSON.stringify(value)}` })
  return [...options.values()]
}
