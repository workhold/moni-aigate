import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { ApiError, request, setCsrf } from '../src/api.ts'
import { clearCredentialPoolSecrets, createCredentialPoolScope, credentialPoolDrafts, credentialPoolPath, credentialPoolSaveRequest, credentialRevokeRequest, isCurrentCredentialPool, moveCredentialPoolMember, newCredentialPoolDraft, parseCredentialPool } from '../src/credential-pool.ts'
import { parseDiscoveryMetadata } from '../src/discovery.ts'

const primary = { credential_id: 'credential-one', position: 1, label: '主账号', weight: 3, max_inflight: 10, primary: true, revoked_at: null }
const secondary = { credential_id: 'credential-two', position: 2, label: '备用账号', weight: 1, max_inflight: 4, primary: false }
const current = { channel_id: 'channel-one', channel_version_id: 'version-one', current_version: 'version-one', stale: false, members: [primary, secondary] }
const view = () => parseCredentialPool(structuredClone(current))

test('credential metadata projects only bounded public fields and never recovers provider secrets', () => {
  const input = { ...current, secret: 'must-not-survive', secret_ciphertext: 'private', members: current.members.map(member => ({ ...member, secret: 'private', APIKey: 'private', endpoint: 'https://private.example' })) }
  const parsed = parseCredentialPool(input), drafts = credentialPoolDrafts(parsed)
  assert.equal(parsed.members[0].primary, true)
  assert.equal(parsed.members[0].label, '主账号')
  assert.equal(parsed.members[0].revoked_at, undefined)
  assert.ok(drafts.every(draft => draft.secret === ''))
  assert.ok(!JSON.stringify(parsed).includes('private'))
  assert.equal('secret' in parsed, false)
  assert.equal('endpoint' in parsed.members[0], false)
  assert.equal(input.secret, 'must-not-survive', 'projection must not change the response object')
})

test('fixed pool and revocation paths encode identities and historical version separately', () => {
  assert.equal(credentialPoolPath('channel/one?secret=x'), '/platform/channels/channel%2Fone%3Fsecret%3Dx/credential-pool')
  assert.equal(credentialPoolPath('channel-one', 'old/version?x=y'), '/platform/channels/channel-one/credential-pool?version_id=old%2Fversion%3Fx%3Dy')
  assert.deepEqual(credentialRevokeRequest('channel/one', 'credential/one'), { path: '/platform/channels/channel%2Fone/credentials/credential%2Fone', method: 'DELETE' })
  for (const bad of ['', ' ', '\nchannel', null]) assert.throws(() => credentialPoolPath(bad), /标识/)
  assert.throws(() => credentialPoolPath('channel', '\nversion'), /标识/)
})

test('replacement freezes expected_version and first-member primary order without publishing anything', () => {
  const source = view(), drafts = credentialPoolDrafts(source)
  moveCredentialPoolMember(drafts, 1, -1)
  drafts[0].weight = 8
  const added = newCredentialPoolDraft(); added.label = '新账号'; added.secret = 'sk-new-provider'; drafts.push(added)
  const operation = credentialPoolSaveRequest(source, drafts)
  assert.deepEqual(operation, { path: '/platform/channels/channel-one/credential-pool', method: 'PUT', body: { expected_version: 'version-one', members: [{ credential_id: 'credential-two', label: '备用账号', weight: 8, max_inflight: 4 }, { credential_id: 'credential-one', label: '主账号', weight: 3, max_inflight: 10 }, { secret: 'sk-new-provider', label: '新账号', weight: 1, max_inflight: 10 }] } })
  assert.equal('secret' in operation.body.members[0], false)
  assert.equal('credential_id' in operation.body.members[2], false)
  assert.deepEqual(source, view(), 'editing drafts must not mutate the frozen server view')
})

test('historical content remains read-only while its credential identity remains independently revocable', () => {
  const historical = parseCredentialPool({ ...current, current_version: 'version-two', stale: true })
  assert.equal(isCurrentCredentialPool(historical), false)
  assert.throws(() => credentialPoolSaveRequest(historical, credentialPoolDrafts(historical)), /历史.*只读/)
  assert.equal(credentialRevokeRequest(historical.channel_id, historical.members[0].credential_id).method, 'DELETE')
  assert.throws(() => parseCredentialPool({ ...historical, stale: false }), /历史/)
  const changedStatus = parseCredentialPool({ ...current, stale: true })
  assert.equal(isCurrentCredentialPool(changedStatus), true, 'status changes must not prevent repairing the current pool')
})

test('a revoked member can be removed from the current draft but can never be retained or silently replaced', () => {
  const revoked = parseCredentialPool({ ...current, members: [{ ...primary, revoked_at: '2026-10-31T00:00:00Z' }, secondary] })
  const drafts = credentialPoolDrafts(revoked)
  assert.equal(isCurrentCredentialPool(revoked), true)
  assert.throws(() => credentialPoolSaveRequest(revoked, drafts), /撤销/)
  drafts[0].revoked_at = undefined
  assert.throws(() => credentialPoolSaveRequest(revoked, drafts), /撤销/, 'server metadata remains authoritative even if draft flag is removed')
  drafts.shift()
  assert.deepEqual(credentialPoolSaveRequest(revoked, drafts).body.members.map(member => member.credential_id), ['credential-two'])
  assert.equal(revoked.members[0].revoked_at, '2026-10-31T00:00:00Z', 'removal does not revoke or modify an old version')
  const foreign = credentialPoolDrafts(view()); foreign[0].credential_id = 'outside-current-pool'
  assert.throws(() => credentialPoolSaveRequest(view(), foreign), /当前池/)
})

test('pool cardinality, duplicate identities, numeric limits and byte-sized labels match management validation', () => {
  for (const members of [[], Array.from({ length: 33 }, () => primary)]) assert.throws(() => parseCredentialPool({ ...current, members }))
  for (const changed of [{ position: 3 }, { primary: false }, { label: '' }, { label: '中'.repeat(22) }, { label: 'a\u0085b' }, { weight: 0 }, { weight: 1_000_001 }, { weight: 1.5 }, { max_inflight: 1001 }, { revoked_at: 'never' }]) assert.throws(() => parseCredentialPool({ ...current, members: [{ ...primary, ...changed }, secondary] }))
  assert.throws(() => parseCredentialPool({ ...current, members: [primary, { ...secondary, credential_id: primary.credential_id }] }), /重复/)
  assert.throws(() => credentialPoolSaveRequest(view(), []), /1 至 32/)
  const duplicate = credentialPoolDrafts(view()); duplicate[1].credential_id = duplicate[0].credential_id
  assert.throws(() => credentialPoolSaveRequest(view(), duplicate), /不同凭据/)
  for (const invalid of [0, 1.5, Number.NaN, 1_000_001]) { const drafts = credentialPoolDrafts(view()); drafts[0].weight = invalid; assert.throws(() => credentialPoolSaveRequest(view(), drafts), /权重/) }
  const maximum = Array.from({ length: 32 }, (_, index) => ({ ...newCredentialPoolDraft(), label: index === 0 ? '中'.repeat(21) + 'a' : `凭据${index}`, secret: `sk-${index}`, weight: 1_000_000, max_inflight: 1000 }))
  assert.equal(credentialPoolSaveRequest(view(), maximum).body.members.length, 32)
})

test('new secrets are input-only, validated without trimming, and explicitly cleared after save, close or selection change', () => {
  for (const secret of ['', ' ', 'sk key', 'sk\nkey', 'sk\u0085key', 'x'.repeat(4097)]) {
    const draft = { ...newCredentialPoolDraft(), secret }
    assert.throws(() => credentialPoolSaveRequest(view(), [draft]), /密钥/)
  }
  const existing = credentialPoolDrafts(view()); existing[0].secret = 'must-be-new-member'
  assert.throws(() => credentialPoolSaveRequest(view(), existing), /已有凭据/)
  const added = { ...newCredentialPoolDraft(), secret: 'sk-test-only' }, drafts = [...credentialPoolDrafts(view()), added]
  assert.equal(credentialPoolSaveRequest(view(), drafts).body.members.at(-1).secret, 'sk-test-only')
  clearCredentialPoolSecrets(drafts)
  assert.ok(drafts.every(draft => draft.secret === ''))
  assert.equal(added.secret, '', 'clear also removes secrets held by an existing row reference')
  assert.throws(() => credentialPoolSaveRequest(view(), drafts), /密钥/, 'cleared new members require fresh manual entry')
  assert.ok(credentialPoolDrafts(view()).every(draft => draft.secret === ''))
})

test('newer loads, close and channel changes reject late reads, writes and revocation confirmations', async () => {
  const scope = createCredentialPoolScope(), pending = scope.ticket()
  let release
  const oldResult = new Promise(resolve => { release = resolve })
  let selected = 'current'
  const lateRead = (async () => { await oldResult; if (scope.accepts(pending)) selected = 'old-history' })()
  const newer = scope.ticket(); selected = 'new-history'
  release(); await lateRead
  assert.equal(selected, 'new-history')
  assert.equal(scope.accepts(newer), true)
  const confirmation = scope.snapshot(), saved = scope.ticket()
  assert.equal(scope.accepts(confirmation), false)
  scope.reset() // close or channel switch clears state and invalidates all replies
  assert.equal(scope.accepts(saved), false)
  assert.equal(scope.accepts(newer), false)
  assert.equal(scope.accepts(scope.ticket()), true)
})

test('management uses existing session and CSRF transport, with no automatic route or financial writes', async () => {
  const originalFetch = globalThis.fetch, calls = []
  const saved = { ...current, channel_version_id: 'version-two', current_version: 'version-two' }
  globalThis.fetch = async (url, options) => { calls.push({ url, options }); return new Response(JSON.stringify({ data: options.method === 'DELETE' ? { ok: true } : options.method === 'PUT' ? saved : current, request_id: 'pool-request' })) }
  try {
    setCsrf('pool-csrf')
    const result = parseCredentialPool((await request(credentialPoolPath('channel-one'))).data)
    const operation = credentialPoolSaveRequest(result, credentialPoolDrafts(result))
    assert.equal(parseCredentialPool((await request(operation.path, operation.method, operation.body)).data).current_version, 'version-two')
    const revoke = credentialRevokeRequest('channel-one', 'credential-one')
    await request(revoke.path, revoke.method)
    await request(credentialPoolPath('channel-one', 'version-one'))
    assert.deepEqual(calls.map(call => call.options.method), ['GET', 'PUT', 'DELETE', 'GET'])
    assert.equal(JSON.parse(calls[1].options.body).expected_version, 'version-one')
    assert.equal(calls[1].options.headers['X-CSRF-Token'], 'pool-csrf')
    assert.equal(calls[2].options.headers['X-CSRF-Token'], 'pool-csrf')
    assert.equal(calls[2].options.body, undefined)
    assert.ok(calls.every(call => call.options.credentials === 'include'))
    assert.ok(calls.every(call => !/\/bindings|\/models|\/credits|\/refunds|\/price-versions/.test(call.url)))
    assert.ok(calls.every(call => !call.options.headers.Authorization && !call.options.headers['x-api-key'] && !call.options.headers['Idempotency-Key']))
  } finally { globalThis.fetch = originalFetch; setCsrf('') }
})

test('a stale expected_version fails once without retrying or replacing the frozen version', async () => {
  const originalFetch = globalThis.fetch; let calls = 0
  globalThis.fetch = async () => { calls++; return new Response(JSON.stringify({ error: { code: 'version_conflict', message: '渠道版本已变化，请刷新后重试。' }, request_id: 'conflict-request' }), { status: 409 }) }
  try {
    const source = view(), operation = credentialPoolSaveRequest(source, credentialPoolDrafts(source))
    await assert.rejects(request(operation.path, operation.method, operation.body), error => error instanceof ApiError && error.status === 409)
    assert.equal(calls, 1)
    assert.equal(source.current_version, 'version-one')
    assert.equal(operation.body.expected_version, 'version-one')
  } finally { globalThis.fetch = originalFetch }
})

test('pool editor never stores secrets or creates persisted operation keys and displays actual discovery identity compatibly', async () => {
  const [helper, component] = await Promise.all(['../src/credential-pool.ts', '../src/components/CredentialPool.vue'].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  for (const source of [helper, component]) assert.doesNotMatch(source, /localStorage|sessionStorage|indexedDB|stableOperationKey|console\./)
  const legacy = { id: 'discovery-one', channel_id: 'channel-one', channel_version_id: 'version-one', format: 'openai', status: 'completed', created_at: '2026-10-31T00:00:00Z', pages: 1, stale: false }
  assert.equal(parseDiscoveryMetadata(legacy).credential_id, undefined)
  assert.equal(parseDiscoveryMetadata({ ...legacy, credential_id: 'credential-two' }).credential_id, 'credential-two')
  assert.throws(() => parseDiscoveryMetadata({ ...legacy, credential_id: 'credential\nprivate' }), /标识/)
})
