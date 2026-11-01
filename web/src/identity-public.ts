import { canonicalIdentityToken } from './identity-proof.ts'
import type { IdentityPurpose } from './identity-proof.ts'

export type IdentityUnavailableReason = 'available' | 'feature_disabled' | 'mail_not_configured' | 'setup_required' | 'recovery_mode'
export interface IdentityAvailability { available: boolean; reason: IdentityUnavailableReason }
export interface IdentityCapabilities { email_delivery_configured: boolean; registration: IdentityAvailability; password_reset: IdentityAvailability }
export interface IdentityEnvelope<T> { data: T; request_id: string }
export interface RegistrationCompletion { token: string; password: string; tenant_name: string }
export interface PasswordResetCompletion { token: string; password: string; mfa_code: string }
export type IdentityCompletion = RegistrationCompletion | PasswordResetCompletion
export const identityAcceptedMessage = '请求已接受；如果该邮箱符合条件，稍后会收到邮件。'
export class IdentityInputError extends Error {}
const invalidResponse = () => new Error('身份服务响应格式无效，请稍后重试。')
function object(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw invalidResponse()
  const raw = value as Record<string, unknown>, own = Object.keys(raw)
  if (own.length !== keys.length || own.some(key => !keys.includes(key))) throw invalidResponse()
  return raw
}
const byteLength = (value: string) => new TextEncoder().encode(value).length
export function identityEmail(value: string): string {
  const email = value.trim().toLowerCase()
  if (!email || byteLength(email) > 254 || /[^\x21-\x7e]/.test(email) || !/^[^@]+@[^@]+$/.test(email) || /[<>,;()]/.test(email)) throw new IdentityInputError('请输入有效的单个邮箱地址，最多254字节。')
  return email
}
export function identityPassword(value: string): string {
  const length = byteLength(value)
  if (length < 12 || length > 72) throw new IdentityInputError('密码须为12至72字节；中文等字符会占用多个字节。')
  return value
}
export function identityTenantName(value: string): string {
  const name = value.trim()
  if (!name || byteLength(name) > 200 || /\p{Cc}/u.test(name)) throw new IdentityInputError('工作空间名称须为1至200字节，不能包含控制字符。')
  return name
}
function purposePath(purpose: IdentityPurpose, action: 'request' | 'complete'): string {
  if (purpose !== 'registration' && purpose !== 'password_reset') throw new IdentityInputError('身份操作类型无效。')
  return `/api/auth/${purpose === 'registration' ? 'registration' : 'password-reset'}/${action}`
}
export function identityCompletionBody(purpose: IdentityPurpose, token: string, input: { password: string; tenant_name?: string; mfa_code?: string }): IdentityCompletion {
  if (!canonicalIdentityToken(token)) throw new IdentityInputError('请重新打开最新邮件中的链接。')
  const password = identityPassword(input.password)
  if (purpose === 'registration') return Object.freeze({ token, password, tenant_name: identityTenantName(input.tenant_name || '') })
  if (purpose !== 'password_reset') throw new IdentityInputError('身份操作类型无效。')
  const mfa_code = (input.mfa_code || '').trim()
  if (byteLength(mfa_code) > 128 || /\p{Cc}/u.test(mfa_code)) throw new IdentityInputError('验证器验证码或恢复码格式无效。')
  return Object.freeze({ token, password, mfa_code })
}
function availability(value: unknown): IdentityAvailability {
  const raw = object(value, ['available', 'reason'])
  if (typeof raw.available !== 'boolean' || typeof raw.reason !== 'string' || !['available', 'feature_disabled', 'mail_not_configured', 'setup_required', 'recovery_mode'].includes(raw.reason) || raw.available !== (raw.reason === 'available')) throw invalidResponse()
  return { available: raw.available, reason: raw.reason as IdentityUnavailableReason }
}
export function parseIdentityCapabilities(value: unknown): IdentityCapabilities {
  const raw = object(value, ['email_delivery_configured', 'registration', 'password_reset'])
  if (typeof raw.email_delivery_configured !== 'boolean') throw invalidResponse()
  const registration = availability(raw.registration), password_reset = availability(raw.password_reset)
  if ((!raw.email_delivery_configured && (registration.available || password_reset.available || registration.reason === 'setup_required')) || (raw.email_delivery_configured && [registration.reason, password_reset.reason].includes('mail_not_configured')) || password_reset.reason === 'setup_required') throw invalidResponse()
  return { email_delivery_configured: raw.email_delivery_configured, registration, password_reset }
}
function requestID(value: unknown): string {
  if (typeof value !== 'string' || !value || value.length > 200 || /[\s\p{Cc}]/u.test(value)) throw invalidResponse()
  return value
}
function envelope<T>(value: unknown, parse: (data: unknown) => T): IdentityEnvelope<T> {
  const raw = object(value, ['data', 'request_id'])
  return { data: parse(raw.data), request_id: requestID(raw.request_id) }
}
export function parseIdentityAccepted(value: unknown): { accepted: true } {
  if (object(value, ['accepted']).accepted !== true) throw invalidResponse()
  return { accepted: true }
}
export function parseIdentityCompleted(value: unknown): { completed: true; login_required: true } {
  const raw = object(value, ['completed', 'login_required'])
  if (raw.completed !== true || raw.login_required !== true) throw invalidResponse()
  return { completed: true, login_required: true }
}
export class IdentityPublicError extends Error {
  readonly code: string; readonly status: number; readonly requestId: string
  constructor(code: string, message: string, status: number, id = '') { super(message); this.code = code; this.status = status; this.requestId = id }
}
async function anonymous<T>(path: string, method: 'GET' | 'POST', body: unknown, expectedStatus: number, parse: (data: unknown) => T, signal?: AbortSignal): Promise<IdentityEnvelope<T>> {
  let response: Response
  try { response = await fetch(path, { method, headers: { Accept: 'application/json', ...(method === 'POST' ? { 'Content-Type': 'application/json' } : {}) }, credentials: 'omit', cache: 'no-store', redirect: 'error', body: method === 'POST' ? JSON.stringify(body) : undefined, signal }) }
  catch { throw new IdentityPublicError('network_error', '无法确认本次结果，请勿自动重试。', 0) }
  let raw: unknown
  try { raw = await response.json() } catch { throw new IdentityPublicError('invalid_response', '服务响应无法确认，请勿自动重试。', response.status) }
  if (!response.ok) {
    let code = 'identity_unavailable', id = ''
    try { const error = object(raw, ['error', 'request_id']), detail = object(error.error, ['code', 'message']); id = requestID(error.request_id); if (typeof detail.code === 'string' && /^[a-z_]{1,80}$/.test(detail.code)) code = detail.code } catch { /* Never include the raw response or submitted secrets in an error. */ }
    const message = response.status === 429 ? '操作较频繁，请稍后由你重新提交。' : code === 'identity_proof_invalid' ? '证明无效或已过期，请使用最新邮件并核对表单输入。' : response.status === 400 ? '表单格式无效，请检查输入。' : '自助身份服务暂时不可用，请稍后查看。'
    throw new IdentityPublicError(code, message, response.status, id)
  }
  try { if (response.status !== expectedStatus) throw invalidResponse(); return envelope(raw, parse) }
  catch { throw new IdentityPublicError('invalid_response', '服务响应无法确认，请勿自动重试。', response.status) }
}
export function fetchIdentityCapabilities(signal?: AbortSignal) { return anonymous('/api/auth/capabilities', 'GET', undefined, 200, parseIdentityCapabilities, signal) }
export function requestIdentityEmail(purpose: IdentityPurpose, email: string, signal?: AbortSignal) { return anonymous(purposePath(purpose, 'request'), 'POST', { email: identityEmail(email) }, 202, parseIdentityAccepted, signal) }
export function completeIdentityProof(purpose: IdentityPurpose, body: IdentityCompletion, signal?: AbortSignal) {
  const raw = object(body, purpose === 'registration' ? ['token', 'password', 'tenant_name'] : ['token', 'password', 'mfa_code'])
  if (typeof raw.token !== 'string' || typeof raw.password !== 'string' || (purpose === 'registration' ? typeof raw.tenant_name !== 'string' : typeof raw.mfa_code !== 'string')) throw new IdentityInputError('身份提交格式无效。')
  const input = identityCompletionBody(purpose, raw.token, { password: raw.password, tenant_name: raw.tenant_name as string | undefined, mfa_code: raw.mfa_code as string | undefined })
  return anonymous(purposePath(purpose, 'complete'), 'POST', input, 200, parseIdentityCompleted, signal)
}
export function identityErrorText(error: unknown): string {
  if (error instanceof IdentityPublicError) return `${error.message}${error.requestId ? `（请求 ${error.requestId}）` : ''}`
  return error instanceof IdentityInputError ? error.message : '无法确认本次结果，请先核查，勿自动重试。'
}
export function identityUnknownResult(error: unknown): boolean { return !(error instanceof IdentityPublicError) || error.status === 0 || error.status >= 500 || error.status === 408 || error.code === 'invalid_response' }
export function identityUnavailableText(value: IdentityAvailability): string {
  const messages: Record<IdentityUnavailableReason, string> = { available: '', feature_disabled: '此项自助功能暂未开放。', mail_not_configured: '平台尚未配置邮件服务，请联系运营人员。', setup_required: '平台尚未完成初始化，暂不开放自助注册。', recovery_mode: '平台正在恢复核查，暂时暂停自助身份操作。' }
  return messages[value.reason]
}
