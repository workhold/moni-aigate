export interface CredentialPoolChannel { id: string; name: string; current_version: string }
export interface CredentialPoolMember {
  credential_id: string; position: number; label: string; weight: number; max_inflight: number; primary: boolean; revoked_at?: string
}
export interface CredentialPoolView {
  channel_id: string; channel_version_id: string; current_version: string; stale: boolean; members: CredentialPoolMember[]
}
// Drafts live only in the open editor. A GET response never supplies a secret.
export interface CredentialPoolDraft {
  credential_id?: string; label: string; weight: number; max_inflight: number; secret: string; revoked_at?: string
}

function identity(value: unknown): string {
  if (typeof value !== 'string' || !value || value.length > 200 || value.trim() !== value || /[\x00-\x1f\x7f]/.test(value)) throw new Error('凭据池标识无效。')
  return value
}
function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('凭据池响应格式无效。')
  return value as Record<string, unknown>
}
function boundedInteger(value: unknown, max: number, title: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 1 || value > max) throw new Error(`${title}须为 1 至 ${max} 的整数。`)
  return value
}
function memberLabel(value: unknown): string {
  if (typeof value !== 'string' || !value.trim() || new TextEncoder().encode(value).length > 64 || /\p{Cc}/u.test(value)) throw new Error('请填写凭据名称，最多 64 字节（中文通常每字 3 字节），且不能含控制字符。')
  return value
}
function channelPath(channel: unknown): string { return `/platform/channels/${encodeURIComponent(identity(channel))}` }
export function credentialPoolPath(channel: unknown, version = ''): string {
  return `${channelPath(channel)}/credential-pool${version ? `?version_id=${encodeURIComponent(identity(version))}` : ''}`
}
export function credentialRevokeRequest(channel: unknown, credential: unknown) {
  return { path: `${channelPath(channel)}/credentials/${encodeURIComponent(identity(credential))}`, method: 'DELETE' as const }
}
export function isCurrentCredentialPool(view: CredentialPoolView): boolean { return view.channel_version_id === view.current_version }

export function parseCredentialPool(value: unknown): CredentialPoolView {
  const raw = record(value)
  if (typeof raw.stale !== 'boolean' || !Array.isArray(raw.members) || raw.members.length < 1 || raw.members.length > 32) throw new Error('凭据池版本状态或成员数量无效。')
  const seen = new Set<string>()
  const members = raw.members.map((value, index): CredentialPoolMember => {
    const member = record(value), id = identity(member.credential_id)
    if (seen.has(id) || member.position !== index + 1 || member.primary !== (index === 0)) throw new Error('凭据池成员重复或顺序无效。')
    seen.add(id)
    if (member.revoked_at !== undefined && member.revoked_at !== null && (typeof member.revoked_at !== 'string' || !Number.isFinite(Date.parse(member.revoked_at)))) throw new Error('凭据撤销时间无效。')
    return { credential_id: id, position: index + 1, primary: index === 0, label: memberLabel(member.label), weight: boundedInteger(member.weight, 1_000_000, '凭据权重'), max_inflight: boundedInteger(member.max_inflight, 1000, '凭据并发'), ...(member.revoked_at ? { revoked_at: member.revoked_at as string } : {}) }
  })
  const view = { channel_id: identity(raw.channel_id), channel_version_id: identity(raw.channel_version_id), current_version: identity(raw.current_version), stale: raw.stale, members }
  if (!isCurrentCredentialPool(view) && !view.stale) throw new Error('历史凭据池的版本状态无效。')
  return view
}
export function credentialPoolDrafts(view: CredentialPoolView): CredentialPoolDraft[] {
  return view.members.map(member => ({ credential_id: member.credential_id, label: member.label, weight: member.weight, max_inflight: member.max_inflight, secret: '', ...(member.revoked_at ? { revoked_at: member.revoked_at } : {}) }))
}
export function newCredentialPoolDraft(): CredentialPoolDraft { return { label: '新增凭据', weight: 1, max_inflight: 10, secret: '' } }
export function clearCredentialPoolSecrets(drafts: CredentialPoolDraft[]): void { for (const draft of drafts) draft.secret = '' }
export function moveCredentialPoolMember(drafts: CredentialPoolDraft[], index: number, direction: -1 | 1): void {
  const target = index + direction
  if (!Number.isInteger(index) || index < 0 || index >= drafts.length || target < 0 || target >= drafts.length) return
  const member = drafts.splice(index, 1)[0]!
  drafts.splice(target, 0, member)
}
export function credentialPoolSaveRequest(view: CredentialPoolView, drafts: CredentialPoolDraft[]) {
  if (!isCurrentCredentialPool(view)) throw new Error('历史凭据池只读，请先恢复查看当前配置。')
  if (drafts.length < 1 || drafts.length > 32) throw new Error('凭据池须保留 1 至 32 个成员。')
  const known = new Map(view.members.map(member => [member.credential_id, member])), seen = new Set<string>()
  const members = drafts.map(draft => {
    const common = { label: memberLabel(draft.label), weight: boundedInteger(draft.weight, 1_000_000, '凭据权重'), max_inflight: boundedInteger(draft.max_inflight, 1000, '凭据并发') }
    if (draft.credential_id !== undefined) {
      const id = identity(draft.credential_id), existing = known.get(id)
      if (!existing || seen.has(id)) throw new Error('只能保留当前池中的不同凭据。')
      if (existing.revoked_at || draft.revoked_at) throw new Error('请先移出或替换已永久撤销的凭据。')
      if (draft.secret) throw new Error('已有凭据不能填写新秘密，请新增成员后移出旧成员。')
      seen.add(id)
      return { credential_id: id, ...common }
    }
    if (typeof draft.secret !== 'string' || !draft.secret || new TextEncoder().encode(draft.secret).length > 4096 || /[\s\p{Cc}]/u.test(draft.secret)) throw new Error('新增供应商密钥须为 1 至 4096 字节，且不能含空白或控制字符。')
    return { secret: draft.secret, ...common }
  })
  return { path: credentialPoolPath(view.channel_id), method: 'PUT' as const, body: { expected_version: view.channel_version_id, members } }
}

// A single operation epoch covers reads, writes, confirmations and close or
// channel changes. An old response cannot restore its editor or its secrets.
export function createCredentialPoolScope() {
  let generation = 0
  return { reset() { generation++ }, ticket() { return ++generation }, snapshot() { return generation }, accepts(ticket: number) { return ticket === generation } }
}
