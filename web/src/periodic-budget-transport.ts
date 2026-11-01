import { PERIODIC_RESPONSE_BYTES, parsePeriodicJSON } from './periodic-budget-json.ts'
import { parsePeriodicInput, periodicID, periodicRequestID } from './periodic-budget.ts'

const pairs: Record<string, number> = {
  invalid_request: 400, invalid_cursor: 400, unauthorized: 401, invalid_credentials: 401,
  forbidden: 403, csrf_invalid: 403, mfa_required: 403, reauth_required: 403, not_found: 404,
  periodic_budget_conflict: 409, periodic_calendar_immutable: 409,
  periodic_legacy_import_requires_maintenance: 409, policy_operation_conflict: 409,
  periodic_revision_exhausted: 409, request_too_large: 413, rate_limited: 429,
  periodic_clock_regression: 503, periodic_budget_invariant: 503,
  periodic_budget_outcome_unknown: 503, periodic_budget_unavailable: 503,
  recovery_mode: 503, instance_unavailable: 503,
}
const messages: Record<string, string> = {
  invalid_request: '周期预算请求未通过校验。', invalid_cursor: '分页已失效，请刷新第一页。',
  unauthorized: '请重新登录并核验当前身份。', invalid_credentials: '登录已失效，请重新登录。',
  forbidden: '当前身份没有此项预算权限，请刷新身份。', csrf_invalid: '会话校验失败，请刷新身份。',
  mfa_required: '请完成已配置的账号认证。', reauth_required: '请完成敏感操作再认证。',
  not_found: '尚未查到该目标或原操作；原操作不存在的响应仍不能证明原提交失败。',
  periodic_budget_conflict: '预算版本已改变，请重新读取当前预算后再编辑。',
  periodic_calendar_immutable: '首次配置后不能更改日/月周期。',
  periodic_legacy_import_requires_maintenance: '历史未决占用超过在线导入上限，请安排维护处理。',
  policy_operation_conflict: '原操作 ID 与命令身份不一致，请按原编号人工核查。',
  periodic_revision_exhausted: '预算配置版本已耗尽，请联系维护人员。',
  request_too_large: '周期预算请求超过大小上限。', rate_limited: '请求过于频繁，请稍后核查。',
  periodic_clock_regression: '检测到周期时钟回退，当前预算暂不可读；历史记录仍可核查。',
  periodic_budget_invariant: '周期预算暂不可用，请联系维护人员并保留原操作 ID。',
  periodic_budget_outcome_unknown: '原操作结果待确认，只能查询原操作回执。',
  periodic_budget_unavailable: '周期预算暂不可用；已提交命令请仅查询原操作回执。',
  recovery_mode: '系统处于恢复模式，请保留原操作 ID。', instance_unavailable: '当前实例暂不可用，请保留原操作 ID。',
}
export class PeriodicBudgetError extends Error {
  readonly code: string
  readonly status: number
  readonly knownFailure: boolean
  readonly retryAfterMs: number
  constructor(code: string, status = 0, knownFailure = false, retryAfterMs = 0) {
    super(messages[code] || (code === 'invalid_response' ? '周期预算响应不符合合同；写操作结果待核查。' : '周期预算连接中断或等待超时；写操作结果待核查。'))
    this.name = 'PeriodicBudgetError'; this.code = code; this.status = status; this.knownFailure = knownFailure; this.retryAfterMs = retryAfterMs
  }
}
export function periodicUnknownWrite(cause: unknown): boolean { return !(cause instanceof PeriodicBudgetError) || !cause.knownFailure }
export function periodicIdentityFailure(cause: unknown): boolean { return cause instanceof PeriodicBudgetError && [401, 403].includes(cause.status) }
function invalid(status = 0): PeriodicBudgetError { return new PeriodicBudgetError('invalid_response', status) }
function exact(value: unknown, keys: string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== keys.length || keys.some(k => !Object.hasOwn(value, k))) throw invalid()
  return value as Record<string, unknown>
}
export function parsePeriodicError(raw: unknown, status: number, retryAfter = ''): PeriodicBudgetError {
  try {
    const v = exact(raw, ['error', 'request_id']), e = exact(v.error, ['code', 'message'])
    periodicRequestID(v.request_id)
    if (typeof e.code !== 'string' || !Object.hasOwn(pairs, e.code) || pairs[e.code] !== status || typeof e.message !== 'string' || e.message.length === 0 || e.message.length > 1000 || /\p{Cc}/u.test(e.message)) throw invalid(status)
    let retryAfterMs = 0
    if (status === 429) {
      if (/^(?:0|[1-9][0-9]{0,8})$/.test(retryAfter)) retryAfterMs = Math.max(1000, Number(retryAfter) * 1000)
      else { const date = Date.parse(retryAfter); retryAfterMs = Number.isFinite(date) ? Math.max(1000, date - Date.now()) : 1000 }
    }
    return new PeriodicBudgetError(e.code, status, status < 500, retryAfterMs)
  } catch { throw invalid(status) }
}
function validatePath(path: string, method: 'GET' | 'PUT'): void {
  const [resource, query, extra] = path.split('?')
  if (!resource || extra !== undefined || path.includes('#') || path.length > 5000) throw invalid()
  const match = /^\/api\/(?:wallet\/periodic-budget|keys\/([0-9a-f-]{36})\/periodic-budget|platform\/tenants\/([0-9a-f-]{36})\/periodic-budget|platform\/procurement-accounts\/([0-9a-f-]{36})\/periodic-budget)(\/(?:windows|allocations))?$/.exec(resource)
  const operation = /^\/api\/(?:platform\/)?periodic-budget-operations\/([0-9a-f-]{36})$/.exec(resource)
  if (!match && !operation) throw invalid()
  for (const id of match ? match.slice(1, 4) : [operation![1]]) if (id) periodicID(id)
  if (method === 'PUT' && (operation || match?.[4] || resource === '/api/wallet/periodic-budget')) throw invalid()
  if (query !== undefined) {
    if (method !== 'GET' || !match?.[4] || !query || query.length > 4096 || /%(?![0-9a-fA-F]{2})/.test(query)) throw invalid()
    try { decodeURIComponent(query.replace(/\+/g, ' ')) } catch { throw invalid() }
    const entries = [...new URLSearchParams(query)]
    if (entries.length !== 1 || entries[0]![0] !== 'cursor' || entries[0]![1].length === 0 || entries[0]![1].length > 1024 || /[^\x21-\x7e]/.test(entries[0]![1])) throw invalid()
  }
}
async function boundedBody(response: Response, signal: AbortSignal): Promise<string> {
  if (!response.body) throw invalid(response.status)
  const reader = response.body.getReader(), chunks: Uint8Array[] = []
  let length = 0
  try {
    while (true) {
      if (signal.aborted) throw new PeriodicBudgetError('network_error')
      const part = await reader.read(); if (part.done) break
      length += part.value.length
      if (length > PERIODIC_RESPONSE_BYTES) { void reader.cancel().catch(() => {}); throw invalid(response.status) }
      chunks.push(part.value)
    }
    const bytes = new Uint8Array(length); let offset = 0
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length }
    return new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes)
  } finally { reader.releaseLock() }
}
export interface PeriodicRequestOptions<T> { method?: 'GET' | 'PUT'; body?: unknown; csrf?: string; signal?: AbortSignal; decode: (raw: unknown) => T; fetcher?: typeof fetch; timeoutMs?: number }
export async function periodicRequest<T>(path: string, options: PeriodicRequestOptions<T>): Promise<T> {
  const method = options.method || 'GET'; validatePath(path, method)
  const headers: Record<string, string> = { Accept: 'application/json' }, controller = new AbortController()
  let body: string | undefined
  if (method === 'PUT') {
    // Recheck without coercing or storing any secret in the pending journal.
    body = JSON.stringify(parsePeriodicInput(options.body))
    // Local preflight errors are not a strictly decoded server failure. Once
    // pending was persisted they must retain the original unknown operation.
    if (!options.csrf) throw new PeriodicBudgetError('csrf_invalid', 403)
    if (new TextEncoder().encode(body).length > 8192) throw new PeriodicBudgetError('request_too_large', 413)
    headers['Content-Type'] = 'application/json'; headers['X-CSRF-Token'] = options.csrf
  } else if (options.body !== undefined) throw invalid()
  let timer: ReturnType<typeof setTimeout> | undefined
  let rejectAbort: ((error: PeriodicBudgetError) => void) | undefined
  const aborted = new Promise<never>((_, reject) => { rejectAbort = reject })
  const abort = () => { controller.abort(); rejectAbort?.(new PeriodicBudgetError('network_error')) }
  if (options.signal?.aborted) abort(); else options.signal?.addEventListener('abort', abort, { once: true })
  timer = setTimeout(abort, options.timeoutMs ?? (method === 'GET' ? 6000 : 11000))
  const work = async () => {
    if (controller.signal.aborted) throw new PeriodicBudgetError('network_error')
    let response: Response
    try { response = await (options.fetcher || fetch)(path, { method, headers, credentials: 'include', cache: 'no-store', redirect: 'error', signal: controller.signal, body }) }
    catch { throw new PeriodicBudgetError('network_error') }
    let payload: unknown
    try {
      if (!/^application\/json(?:\s*;\s*charset\s*=\s*utf-8)?\s*$/i.test(response.headers.get('Content-Type') || '')) throw invalid(response.status)
      payload = parsePeriodicJSON(await boundedBody(response, controller.signal))
    } catch { throw invalid(response.status) }
    if (!response.ok) throw parsePeriodicError(payload, response.status, response.headers.get('Retry-After') || '')
    if (method === 'GET' ? response.status !== 200 : response.status !== 200 && response.status !== 201) throw invalid(response.status)
    try { return options.decode(payload) } catch { throw invalid(response.status) }
  }
  try { return await Promise.race([work(), aborted]) }
  finally { clearTimeout(timer); options.signal?.removeEventListener('abort', abort) }
}
