export interface Envelope<T> { data: T; request_id: string; next_cursor?: string }
export class ApiError extends Error {
  code: string
  requestId: string
  status: number
  discoveryId?: string
  constructor(code: string, message: string, requestId: string, status: number) { super(message); this.code = code; this.requestId = requestId; this.status = status }
}

let csrf = ''
const operationKeys = new Map<string, string>()
export function setCsrf(token: string) { csrf = token }
export function newOperationKey() { return crypto.randomUUID() }
export function clearOperationKeys() { operationKeys.clear() }

export async function request<T>(path: string, method = 'GET', body?: unknown, idempotencyKey?: string, eventIsCurrent?: () => boolean): Promise<Envelope<T>> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && csrf) headers['X-CSRF-Token'] = csrf
  if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey
  let response: Response
  try { response = await fetch(`/api${path}`, { method, headers, credentials: 'include', body: body === undefined ? undefined : JSON.stringify(body) }) }
  catch { throw new ApiError('network_error', '无法连接服务。写操作结果可能尚未确认，请先刷新列表核实，再使用相同内容重试。', '', 0) }
  let result: Envelope<T> & { discovery_id?: string; error?: { code: string; message: string; discovery_id?: string } }
  try { result = await response.json() }
  catch { throw new ApiError('invalid_response', '服务返回了无法识别的响应，请联系运营人员。', response.headers.get('X-Request-ID') || '', response.status) }
  if (!response.ok) {
    const error = new ApiError(result.error?.code || 'request_failed', result.error?.message || '操作未完成，请稍后重试。', result.request_id || '', response.status)
    error.discoveryId = result.discovery_id ?? result.error?.discovery_id
    if (eventIsCurrent?.() !== false) {
      if (response.status === 401 && !path.startsWith('/auth/')) window.dispatchEvent(new CustomEvent('moni:unauthenticated'))
      if (error.code === 'reauth_required') window.dispatchEvent(new CustomEvent('moni:reauth'))
    }
    throw error
  }
  return result
}

export function stableOperationKey(path: string, body: unknown) {
  const fingerprint = `${path}:${JSON.stringify(body)}`
  let key = operationKeys.get(fingerprint)
  if (!key) { key = newOperationKey(); operationKeys.set(fingerprint, key) }
  return key
}
export function errorText(error: unknown): string {
  if (error instanceof ApiError) return `${error.message}${error.requestId ? `（请求 ${error.requestId}）` : ''}`
  return error instanceof Error ? error.message : '操作未完成，请重试。'
}
