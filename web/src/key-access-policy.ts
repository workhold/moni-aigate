import { ApiError } from './api.ts'

export interface KeyAccessPolicy {
  id: string
  allowed_client_cidrs: readonly string[]
  access_policy_revision: string
}
export interface KeyRow extends KeyAccessPolicy {
  name: string; prefix: string; scopes: string[]; lifetime_limit: string | null
  spent: string; held: string; expires_at?: string; revoked_at?: string; created_at?: string
}
export interface KeyAccessPolicyRequest {
  readonly path: string
  readonly method: 'PUT'
  readonly body: { readonly expected_revision: string; readonly allowed_client_cidrs: readonly string[] }
}
export interface KeyAccessPolicyReview {
  readonly view: KeyAccessPolicy
  readonly mode: 'editing' | 'submitting' | 'conflict' | 'unknown'
  readonly submitted?: KeyAccessPolicyRequest
  readonly observed?: KeyAccessPolicy
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('密钥响应格式无效。')
  return value as Record<string, unknown>
}
function identity(value: unknown): string {
  if (typeof value !== 'string' || !value || value.length > 200 || value.trim() !== value || /\p{Cc}/u.test(value)) throw new Error('密钥标识无效。')
  return value
}
export function keyAccessPolicyRevision(value: unknown): string {
  if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error('来源策略版本须为正 int64 十进制字符串。')
  return value
}

// This only rejects obvious input mistakes. In particular, IPv6 parsing,
// canonical masking and duplicate removal remain the server's responsibility.
function clientAddress(value: unknown): string {
  if (typeof value !== 'string' || !value || value.trim() !== value || /[\s\p{Cc}%\[\]]/u.test(value)) throw new Error('来源每行须为纯 IPv4、IPv6 或 CIDR，不能含空项、端口或 zone。')
  const parts = value.split('/')
  if (parts.length > 2) throw new Error('来源 CIDR 格式无效。')
  const address = parts[0]!, ipv6 = address.includes(':')
  if (ipv6) {
    // A dotted suffix can be valid IPv4-compatible IPv6. Do not confuse it
    // with mapped IPv6; the server's address parser distinguishes the two.
    if (!/^[0-9a-fA-F:.]+$/.test(address) || (address.match(/:/g)?.length ?? 0) < 2) throw new Error('来源须为 IP 地址，不能使用域名、URL或端口。')
  } else {
    const octets = address.split('.')
    if (octets.length !== 4 || octets.some(part => !/^\d{1,3}$/.test(part) || Number(part) > 255)) throw new Error('来源须为有效的 IPv4、IPv6 或 CIDR，不能使用域名、URL或端口。')
  }
  if (parts.length === 2 && (!/^[1-9][0-9]{0,2}$/.test(parts[1]!) || Number(parts[1]) > (ipv6 ? 128 : 32))) throw new Error('CIDR 前缀须为 IPv4 的 1—32 或 IPv6 的 1—128；不限来源请留空，不能使用 /0。')
  return value
}
export function keyClientCIDRs(values: unknown): string[] {
  if (!Array.isArray(values) || values.length > 32) throw new Error('来源最多 32 项。')
  const parsed = values.map(clientAddress)
  if (parsed.reduce((total, value) => total + new TextEncoder().encode(value).length, 0) > 4096) throw new Error('来源总长度不能超过 4096 字节。')
  return parsed
}
export function keyClientCIDRsFromText(value: string): string[] {
  if (!value.trim()) return []
  return keyClientCIDRs(value.split(/\r?\n/).map(line => line.trim()))
}
export function keyAccessPolicyPath(keyID: unknown): string { return `/keys/${encodeURIComponent(identity(keyID))}/access-policy` }
export function parseKeyAccessPolicy(value: unknown, expectedID?: string): KeyAccessPolicy {
  const raw = record(value), id = identity(raw.id)
  if (expectedID !== undefined && id !== expectedID) throw new Error('来源策略返回了不同的密钥，请重新读取核实。')
  return { id, allowed_client_cidrs: keyClientCIDRs(raw.allowed_client_cidrs), access_policy_revision: keyAccessPolicyRevision(raw.access_policy_revision) }
}
function decimal(value: unknown): string {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error('密钥额度响应无效。')
  return value
}
function timestamp(value: unknown): string | undefined {
  if (value === undefined || value === null) return undefined
  if (typeof value !== 'string' || !Number.isFinite(Date.parse(value))) throw new Error('密钥时间响应无效。')
  return value
}
export function parseKeyRow(value: unknown): KeyRow {
  const raw = record(value), policy = parseKeyAccessPolicy(raw)
  // Existing keys can contain names/scopes accepted by older writers. Display
  // these as escaped Vue text; new-form validation must not break old lists.
  if (typeof raw.name !== 'string' || typeof raw.prefix !== 'string' || !Array.isArray(raw.scopes) || raw.scopes.some(scope => typeof scope !== 'string')) throw new Error('密钥元数据响应无效。')
  return { ...policy, name: raw.name, prefix: raw.prefix, scopes: [...raw.scopes] as string[], lifetime_limit: raw.lifetime_limit === null || raw.lifetime_limit === undefined ? null : decimal(raw.lifetime_limit), spent: decimal(raw.spent), held: decimal(raw.held), expires_at: timestamp(raw.expires_at), revoked_at: timestamp(raw.revoked_at), created_at: timestamp(raw.created_at) }
}
export function parseKeyRows(value: unknown): KeyRow[] {
  if (!Array.isArray(value) || value.length > 50) throw new Error('密钥列表响应无效。')
  const rows = value.map(parseKeyRow)
  if (new Set(rows.map(row => row.id)).size !== rows.length) throw new Error('密钥列表包含重复标识。')
  return rows
}
export function retainNewerKeyPolicies<T extends KeyRow>(incoming: T[], known: T[]): T[] {
  const previous = new Map(known.map(row => [row.id, row]))
  return incoming.map(row => {
    const existing = previous.get(row.id)
    return existing && BigInt(existing.access_policy_revision) > BigInt(row.access_policy_revision) ? { ...row, allowed_client_cidrs: [...existing.allowed_client_cidrs], access_policy_revision: existing.access_policy_revision } : row
  })
}
export function keyCreatedSecret(value: unknown): string {
  const raw = record(value), secret = raw.key
  if (typeof secret !== 'string' || !secret || secret.length > 4096 || /[\s\p{Cc}]/u.test(secret)) throw new Error('创建响应缺少可展示的明文密钥。请先刷新列表核查，勿重复提交。')
  return secret
}
export function keyCreateRequest(input: { name: string; scopes: readonly string[]; lifetime_limit: string | null; expires_at: string | null; allowed_client_cidrs: readonly string[] }) {
  const name = input.name.trim()
  if (!name || new TextEncoder().encode(name).length > 100 || /\p{Cc}/u.test(name)) throw new Error('请输入密钥名称，最多 100 字节，且不能含控制字符。')
  if (!input.scopes.length || input.scopes.length > 1000 || input.scopes.some(scope => typeof scope !== 'string' || !scope || scope.length > 200 || /\p{Cc}/u.test(scope))) throw new Error('请选择至少一个授权模型，或明确允许全部已授权模型。')
  const expires = timestamp(input.expires_at)
  return Object.freeze({ path: '/keys', method: 'POST', body: Object.freeze({ name, scopes: Object.freeze([...input.scopes]), lifetime_limit: input.lifetime_limit === null ? null : decimal(input.lifetime_limit), expires_at: expires ?? null, allowed_client_cidrs: Object.freeze(keyClientCIDRs([...input.allowed_client_cidrs])) }) })
}
export function keyAccessPolicySaveRequest(view: KeyAccessPolicy, text: string): KeyAccessPolicyRequest {
  const source = parseKeyAccessPolicy(view)
  return Object.freeze({ path: keyAccessPolicyPath(source.id), method: 'PUT', body: Object.freeze({ expected_revision: source.access_policy_revision, allowed_client_cidrs: Object.freeze(keyClientCIDRsFromText(text)) }) })
}
export function newKeyAccessPolicyReview(value: unknown): KeyAccessPolicyReview { return { view: parseKeyAccessPolicy(value), mode: 'editing' } }
export function submitKeyAccessPolicyReview(review: KeyAccessPolicyReview, text: string): KeyAccessPolicyReview {
  if (review.mode !== 'editing') throw new Error('请先只读刷新并复核当前策略，再明确开始新编辑。')
  return { ...review, mode: 'submitting', submitted: keyAccessPolicySaveRequest(review.view, text) }
}
export function unknownKeyWriteResult(error: unknown): boolean { return !(error instanceof ApiError) || error.status === 0 || error.status >= 500 || error.status === 408 || error.code === 'invalid_response' }
export function failKeyAccessPolicyReview(review: KeyAccessPolicyReview, error: unknown): KeyAccessPolicyReview {
  if (review.mode !== 'submitting') throw new Error('来源策略没有待确认的提交。')
  return { ...review, mode: error instanceof ApiError && error.status === 409 ? 'conflict' : unknownKeyWriteResult(error) ? 'unknown' : 'editing', observed: undefined }
}
export function acceptKeyAccessPolicySave(review: KeyAccessPolicyReview, value: unknown): KeyAccessPolicyReview {
  if (review.mode !== 'submitting' || !review.submitted) throw new Error('来源策略没有待确认的提交。')
  const view = parseKeyAccessPolicy(value, review.view.id), previous = BigInt(review.submitted.body.expected_revision), revision = BigInt(view.access_policy_revision)
  const unchanged = JSON.stringify(view.allowed_client_cidrs) === JSON.stringify(review.view.allowed_client_cidrs)
  if (revision < previous || revision > previous + 1n || (revision === previous) !== unchanged) throw new Error('来源策略保存响应版本不一致，请只读刷新核实。')
  return newKeyAccessPolicyReview(view)
}
export function observeKeyAccessPolicy(review: KeyAccessPolicyReview, value: unknown): KeyAccessPolicyReview {
  if (review.mode !== 'conflict' && review.mode !== 'unknown') throw new Error('当前没有待复核的来源策略。')
  const observed = parseKeyAccessPolicy(value, review.view.id), revision = BigInt(observed.access_policy_revision), before = BigInt(review.view.access_policy_revision)
  if (revision < before || revision === before && JSON.stringify(observed.allowed_client_cidrs) !== JSON.stringify(review.view.allowed_client_cidrs)) throw new Error('当前来源策略响应版本不一致，请重新只读刷新。')
  return { ...review, observed }
}
export function resumeKeyAccessPolicyReview(review: KeyAccessPolicyReview): KeyAccessPolicyReview {
  if (!review.observed || review.mode !== 'conflict' && review.mode !== 'unknown') throw new Error('请先只读刷新当前策略并复核。')
  return newKeyAccessPolicyReview(review.observed)
}

// Context changes invalidate every lane; dialog close also invalidates its lane.
// Actor, tenant and role are all part of the context, even on the same page.
export function keyAccessContext(actor: string | undefined, tenant: string | undefined, role: string | undefined): string { return JSON.stringify([actor ?? '', tenant ?? '', role ?? '']) }
export function createKeyAccessScope() {
  let context = '', epoch = 0
  const lanes = new Map<string, number>()
  return {
    reset(nextContext = context) { context = nextContext; epoch++; lanes.clear() },
    ticket(lane: string) { const sequence = (lanes.get(lane) ?? 0) + 1; lanes.set(lane, sequence); return { context, epoch, lane, sequence } },
    invalidate(lane: string) { lanes.set(lane, (lanes.get(lane) ?? 0) + 1) },
    accepts(ticket: { context: string; epoch: number; lane: string; sequence: number }) { return ticket.context === context && ticket.epoch === epoch && lanes.get(ticket.lane) === ticket.sequence },
  }
}
