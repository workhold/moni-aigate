import { ApiError } from './api.ts'
import { keyCreateRequest, parseKeyRow, parseKeyRows, retainNewerKeyPolicies, unknownKeyWriteResult } from './key-access-policy.ts'
import type { KeyRow } from './key-access-policy.ts'

export interface KeyRuntimeLimits { rpm_limit: number; burst_limit: number; concurrency_limit: number }
export interface KeyLimitsView extends KeyRuntimeLimits { id: string; limit_revision: string }
export type KeyLimitsRow = KeyRow & KeyLimitsView
export interface KeyLimitsDraft { rpm_limit: number | undefined; burst_limit: number | undefined; concurrency_limit: number | undefined }
export interface KeyLimitsRequest {
  readonly path: string
  readonly method: 'PUT'
  readonly body: Readonly<KeyRuntimeLimits & { expected_revision: string }>
}
export interface KeyLimitsReview {
  readonly view: KeyLimitsView
  readonly mode: 'editing' | 'submitting' | 'conflict' | 'unknown'
  readonly submitted?: KeyLimitsRequest
  readonly observed?: KeyLimitsView
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('运行限额响应格式无效。')
  return value as Record<string, unknown>
}
function identity(value: unknown): string {
  if (typeof value !== 'string' || !value || value.length > 200 || value.trim() !== value || /\p{Cc}/u.test(value)) throw new Error('密钥标识无效。')
  return value
}
function integer(value: unknown, maximum: number, label: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 1 || value > maximum) throw new Error(`${label}须为 1—${maximum} 的整数，不能留空。`)
  return value
}
export function keyLimitRevision(value: unknown): string {
  if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error('运行限额版本须为正 int64 十进制字符串。')
  return value
}
export function parseKeyRuntimeLimits(value: unknown): KeyRuntimeLimits {
  const raw = record(value)
  return { rpm_limit: integer(raw.rpm_limit, 6000, 'RPM'), burst_limit: integer(raw.burst_limit, 200, '突发容量'), concurrency_limit: integer(raw.concurrency_limit, 16, '并发许可上限') }
}
export function keyLimitsDraft(value: KeyRuntimeLimits = { rpm_limit: 60, burst_limit: 20, concurrency_limit: 16 }): KeyLimitsDraft { return { ...parseKeyRuntimeLimits(value) } }
export function keyRuntimeLimitsPath(keyID: unknown): string { return `/keys/${encodeURIComponent(identity(keyID))}/runtime-limits` }
export function parseKeyLimitsView(value: unknown, expectedID?: string): KeyLimitsView {
  const raw = record(value), id = identity(raw.id)
  if (expectedID !== undefined && id !== expectedID) throw new Error('运行限额返回了不同的密钥，请重新读取核实。')
  return { id, ...parseKeyRuntimeLimits(raw), limit_revision: keyLimitRevision(raw.limit_revision) }
}
export function parseKeyLimitsRow(value: unknown): KeyLimitsRow { return { ...parseKeyRow(value), ...parseKeyLimitsView(value) } }
export function parseKeyLimitsRows(value: unknown): KeyLimitsRow[] {
  const rows = parseKeyRows(value)
  return rows.map((row, index) => ({ ...row, ...parseKeyLimitsView((value as unknown[])[index], row.id) }))
}
export function retainNewerKeyLimits<T extends KeyLimitsView>(incoming: T[], known: T[]): T[] {
  const previous = new Map(known.map(row => [row.id, row]))
  return incoming.map(row => {
    const existing = previous.get(row.id)
    return existing && BigInt(existing.limit_revision) > BigInt(row.limit_revision) ? { ...row, ...parseKeyRuntimeLimits(existing), limit_revision: existing.limit_revision } : row
  })
}
export function retainNewerKeyConfigurations<T extends KeyLimitsRow>(incoming: T[], known: T[]): T[] {
  return retainNewerKeyLimits(retainNewerKeyPolicies(incoming, known), known)
}
export function keyCreateWithLimitsRequest(input: Parameters<typeof keyCreateRequest>[0] & KeyLimitsDraft) {
  const base = keyCreateRequest(input), limits = parseKeyRuntimeLimits(input)
  return Object.freeze({ ...base, body: Object.freeze({ ...base.body, ...limits }) })
}
export function keyLimitsSaveRequest(view: KeyLimitsView, draft: KeyLimitsDraft): KeyLimitsRequest {
  const source = parseKeyLimitsView(view), limits = parseKeyRuntimeLimits(draft)
  return Object.freeze({ path: keyRuntimeLimitsPath(source.id), method: 'PUT', body: Object.freeze({ expected_revision: source.limit_revision, ...limits }) })
}
function sameLimits(left: KeyRuntimeLimits, right: KeyRuntimeLimits): boolean { return left.rpm_limit === right.rpm_limit && left.burst_limit === right.burst_limit && left.concurrency_limit === right.concurrency_limit }
export function newKeyLimitsReview(value: unknown): KeyLimitsReview { return { view: parseKeyLimitsView(value), mode: 'editing' } }
export function submitKeyLimitsReview(review: KeyLimitsReview, draft: KeyLimitsDraft): KeyLimitsReview {
  if (review.mode !== 'editing') throw new Error('请先只读刷新并复核当前限额，再明确开始新编辑。')
  return { ...review, mode: 'submitting', submitted: keyLimitsSaveRequest(review.view, draft) }
}
export function failKeyLimitsReview(review: KeyLimitsReview, error: unknown): KeyLimitsReview {
  if (review.mode !== 'submitting') throw new Error('运行限额没有待确认的提交。')
  return { ...review, mode: error instanceof ApiError && error.status === 409 ? 'conflict' : unknownKeyWriteResult(error) ? 'unknown' : 'editing', observed: undefined }
}
export function acceptKeyLimitsSave(review: KeyLimitsReview, value: unknown): KeyLimitsReview {
  if (review.mode !== 'submitting' || !review.submitted) throw new Error('运行限额没有待确认的提交。')
  const view = parseKeyLimitsView(value, review.view.id), before = BigInt(review.submitted.body.expected_revision), revision = BigInt(view.limit_revision)
  if (!sameLimits(view, review.submitted.body) || revision < before || revision > before + 1n || (revision === before) !== sameLimits(view, review.view)) throw new Error('运行限额保存响应与冻结提交不一致，请只读刷新核实。')
  return newKeyLimitsReview(view)
}
export function observeKeyLimits(review: KeyLimitsReview, value: unknown): KeyLimitsReview {
  if (review.mode !== 'conflict' && review.mode !== 'unknown') throw new Error('当前没有待复核的运行限额。')
  const observed = parseKeyLimitsView(value, review.view.id), revision = BigInt(observed.limit_revision), before = BigInt(review.view.limit_revision)
  if (revision < before || revision === before && !sameLimits(observed, review.view)) throw new Error('当前运行限额响应版本不一致，请重新只读刷新。')
  return { ...review, observed }
}
export function resumeKeyLimitsReview(review: KeyLimitsReview): KeyLimitsReview {
  if (!review.observed || review.mode !== 'conflict' && review.mode !== 'unknown') throw new Error('请先只读刷新当前限额并复核。')
  return newKeyLimitsReview(review.observed)
}
