import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { ApiError, request, setCsrf } from '../src/api.ts'
import { acceptKeyAccessPolicySave, createKeyAccessScope, failKeyAccessPolicyReview, keyAccessContext, keyAccessPolicyPath, keyAccessPolicyRevision, keyAccessPolicySaveRequest, keyClientCIDRs, keyClientCIDRsFromText, keyCreatedSecret, keyCreateRequest, newKeyAccessPolicyReview, observeKeyAccessPolicy, parseKeyAccessPolicy, parseKeyRow, parseKeyRows, retainNewerKeyPolicies, resumeKeyAccessPolicyReview, submitKeyAccessPolicyReview, unknownKeyWriteResult } from '../src/key-access-policy.ts'

const keyID = 'a9f89d66-fbef-4183-a811-e16b37ba62bb'
const policy = { id: keyID, allowed_client_cidrs: ['203.0.113.0/24', '2001:db8::/64'], access_policy_revision: '9007199254740993' }
const row = { ...policy, name: '应用一', prefix: 'mg_safe_prefix', scopes: ['*'], lifetime_limit: null, spent: '7', held: '3', expires_at: null, revoked_at: null, created_at: '2026-10-31T00:00:00Z' }
const initial = () => newKeyAccessPolicyReview(structuredClone(policy))

test('source text distinguishes unrestricted input from empty items and leaves normalization to the server', () => {
  assert.deepEqual(keyClientCIDRsFromText(''), [])
  assert.deepEqual(keyClientCIDRsFromText(' \t\r\n '), [])
  assert.deepEqual(keyClientCIDRsFromText(' 203.0.113.8 \r\n 2001:DB8::/64 '), ['203.0.113.8', '2001:DB8::/64'])
  assert.deepEqual(keyClientCIDRsFromText('203.0.113.8\n203.0.113.8'), ['203.0.113.8', '203.0.113.8'], 'deduplication and masking belong to backend netip validation')
  assert.deepEqual(keyClientCIDRsFromText('::192.0.2.1\n2001:db8::192.0.2.1/128'), ['::192.0.2.1', '2001:db8::192.0.2.1/128'], 'valid dotted IPv6 suffixes must not be mistaken for mapped IPv6')
  assert.deepEqual(keyClientCIDRsFromText('::ffff:192.0.2.1'), ['::ffff:192.0.2.1'], 'the server parser remains responsible for rejecting forbidden mapped IPv6')
  for (const value of ['203.0.113.8\n', '\n203.0.113.8', '203.0.113.8\n \n2001:db8::1']) assert.throws(() => keyClientCIDRsFromText(value), /空项/)
  assert.equal(keyClientCIDRsFromText(Array.from({ length: 32 }, () => '2001:db8::1/128').join('\n')).length, 32)
  assert.throws(() => keyClientCIDRsFromText(Array.from({ length: 33 }, () => '203.0.113.1').join('\n')), /32/)
})

test('obvious address mistakes, forbidden universal masks and byte bounds fail before writing', () => {
  for (const value of ['example.com', 'https://203.0.113.8', '203.0.113.8:443', '[2001:db8::1]:443', 'fe80::1%en0', '0.0.0.0/0', '::/0', '1.2.3.256', '1.2.3', '1.2.3.4/33', '::1/129', '1.2.3.4/01', '1.2.3.4/', '::1/64/1', '2001:db8::g', '1:2', '1.2.3.4\u0085']) assert.throws(() => keyClientCIDRsFromText(value), undefined, value)
  for (const value of [null, {}, '1.2.3.4', [null], [''], [' 1.2.3.4']]) assert.throws(() => keyClientCIDRs(value))
  assert.throws(() => keyClientCIDRs(Array.from({ length: 32 }, () => ':'.repeat(129))), /4096/)
  assert.deepEqual(keyClientCIDRs(['0.0.0.0/32', '::/128', '255.255.255.255', '2001:db8::1']), ['0.0.0.0/32', '::/128', '255.255.255.255', '2001:db8::1'])
})

test('revision preserves positive int64 precision and rejects numeric or noncanonical responses', () => {
  for (const value of ['1', '9007199254740993', '9223372036854775807']) assert.equal(keyAccessPolicyRevision(value), value)
  for (const value of [1, 9007199254740993, null, '', '0', '-1', '+1', '01', '1.0', '1e3', ' 1', '9223372036854775808']) assert.throws(() => keyAccessPolicyRevision(value), /int64/)
})

test('metadata projection includes only safe policy and key display fields', () => {
  const input = { ...structuredClone(row), key: 'mg_do_not_retain', secret: 'private', owner_id: 'private-user', tenant_id: 'private-tenant', wallet: { balance: 'private' } }
  const parsed = parseKeyRow(input)
  assert.equal(parsed.id, keyID)
  assert.equal(parsed.access_policy_revision, policy.access_policy_revision)
  assert.equal(parsed.lifetime_limit, null)
  assert.equal(parsed.expires_at, undefined)
  assert.deepEqual(parsed.allowed_client_cidrs, policy.allowed_client_cidrs)
  assert.doesNotMatch(JSON.stringify(parsed), /do_not_retain|private|owner_id|tenant_id|wallet|secret/)
  assert.equal(input.key, 'mg_do_not_retain')
  parsed.allowed_client_cidrs.push('192.0.2.1/32')
  assert.equal(input.allowed_client_cidrs.length, 2)
  assert.deepEqual(parseKeyAccessPolicy({ ...policy, key: 'secret' }), policy)
  assert.throws(() => parseKeyAccessPolicy(policy, 'another-key'), /不同/)
  assert.throws(() => parseKeyRows([row, row]), /重复/)
  assert.throws(() => parseKeyRows(Array.from({ length: 51 }, (_, i) => ({ ...row, id: `key-${i}` }))), /列表/)
  for (const invalid of [{ access_policy_revision: 1 }, { allowed_client_cidrs: null }, { spent: 1 }, { held: '01' }, { lifetime_limit: '-1' }, { expires_at: 'tomorrow' }, { scopes: [null] }, { name: 1 }, { prefix: null }]) assert.throws(() => parseKeyRow({ ...row, ...invalid }))
})

test('legacy safe display text cannot invalidate the whole key page because new forms use narrower validation', () => {
  const legacy = { ...row, name: '\n', prefix: 'mg_\nlegacy', scopes: ['', 'legacy\nscope', 'x'.repeat(201), '<img src=x onerror=alert(1)>'] }
  const parsed = parseKeyRows([row, { ...legacy, id: 'legacy-key' }])
  assert.equal(parsed.length, 2)
  assert.equal(parsed[1].name, '\n')
  assert.equal(parsed[1].prefix, legacy.prefix)
  assert.deepEqual(parsed[1].scopes, legacy.scopes)
  assert.equal(parseKeyRow({ ...row, name: '<script>text only</script>', scopes: [] }).name, '<script>text only</script>')
  assert.deepEqual(parseKeyRow({ ...row, scopes: [] }).scopes, [])
})

test('creation sends explicit source policy and only the established create fields', () => {
  const input = { name: '  应用一  ', scopes: ['*'], lifetime_limit: '9007199254740993', expires_at: null, allowed_client_cidrs: [] }
  const operation = keyCreateRequest({ ...input, tenant_id: 'must-not-send', access_policy_revision: '9' })
  assert.deepEqual(operation, { path: '/keys', method: 'POST', body: { name: '应用一', scopes: ['*'], lifetime_limit: '9007199254740993', expires_at: null, allowed_client_cidrs: [] } })
  input.scopes.push('late-model'); input.allowed_client_cidrs.push('203.0.113.1')
  assert.deepEqual(operation.body.scopes, ['*'])
  assert.deepEqual(operation.body.allowed_client_cidrs, [])
  assert.ok(Object.isFrozen(operation.body.allowed_client_cidrs))
  assert.equal(keyCreatedSecret({ key: 'mg_one_time_only' }), 'mg_one_time_only')
  for (const value of [{}, { key: '' }, { key: 'bad secret' }, { key: 'secret\n' }, { secret: 'legacy-secret' }]) assert.throws(() => keyCreatedSecret(value), /明文/)
  for (const bad of [{ name: '' }, { name: '中'.repeat(34) }, { name: 'bad\nname' }, { scopes: [] }, { lifetime_limit: '1.5' }, { expires_at: 'invalid' }]) assert.throws(() => keyCreateRequest({ ...input, ...bad }))
})

test('a late list or metadata read cannot replace an already observed newer source policy', () => {
  const newer = parseKeyRow({ ...row, allowed_client_cidrs: [], access_policy_revision: '9007199254740994', spent: '8' })
  const older = parseKeyRow({ ...row, spent: '10' })
  const merged = retainNewerKeyPolicies([older], [newer])
  assert.equal(merged[0].access_policy_revision, newer.access_policy_revision)
  assert.deepEqual(merged[0].allowed_client_cidrs, [])
  assert.equal(merged[0].spent, '10', 'policy freshness must not replace unrelated current list fields')
  assert.deepEqual(retainNewerKeyPolicies([newer], [older]), [newer])
  assert.deepEqual(older.allowed_client_cidrs, policy.allowed_client_cidrs)
})

test('CAS request freezes identity revision and complete source array without touching scopes or money', () => {
  const source = structuredClone(policy), operation = keyAccessPolicySaveRequest({ ...source, scopes: ['*'], lifetime_limit: '9' }, '198.51.100.9\n2001:db8::1/128')
  assert.deepEqual(operation, { path: `/keys/${keyID}/access-policy`, method: 'PUT', body: { expected_revision: '9007199254740993', allowed_client_cidrs: ['198.51.100.9', '2001:db8::1/128'] } })
  source.access_policy_revision = '9007199254740994'; source.allowed_client_cidrs.length = 0
  assert.equal(operation.body.expected_revision, '9007199254740993')
  assert.ok(Object.isFrozen(operation) && Object.isFrozen(operation.body) && Object.isFrozen(operation.body.allowed_client_cidrs))
  assert.throws(() => { operation.body.allowed_client_cidrs.push('192.0.2.1') }, TypeError)
  assert.equal(keyAccessPolicyPath('key/one?x=y'), '/keys/key%2Fone%3Fx%3Dy/access-policy')
  for (const bad of ['', ' ', '\nkey', null]) assert.throws(() => keyAccessPolicyPath(bad), /标识/)
  assert.deepEqual(keyAccessPolicySaveRequest(policy, '').body.allowed_client_cidrs, [])
})

test('successful save accepts exactly current or next revision and never treats inconsistent data as success', () => {
  const submitted = submitKeyAccessPolicyReview(initial(), '198.51.100.9')
  const saved = acceptKeyAccessPolicySave(submitted, { id: keyID, allowed_client_cidrs: ['198.51.100.9/32'], access_policy_revision: '9007199254740994', key: 'must-drop' })
  assert.equal(saved.mode, 'editing')
  assert.deepEqual(saved.view.allowed_client_cidrs, ['198.51.100.9/32'])
  assert.doesNotMatch(JSON.stringify(saved), /must-drop/)
  const noOp = acceptKeyAccessPolicySave(submitKeyAccessPolicyReview(initial(), policy.allowed_client_cidrs.join('\n')), policy)
  assert.equal(noOp.view.access_policy_revision, policy.access_policy_revision)
  for (const invalid of [{ ...policy, id: 'different-key' }, { ...policy, access_policy_revision: '9007199254740992' }, { ...policy, access_policy_revision: '9007199254740994' }, { ...policy, access_policy_revision: '9007199254740995' }, { ...policy, allowed_client_cidrs: [] }]) assert.throws(() => acceptKeyAccessPolicySave(submitted, invalid))
})

test('conflict and uncertain outcomes stay blocked after observation until explicit review', () => {
  const submitted = submitKeyAccessPolicyReview(initial(), '198.51.100.9')
  for (const failure of [new ApiError('revision_conflict', 'changed', '', 409), new ApiError('network_error', 'unknown', '', 0), new ApiError('unavailable', 'unknown', '', 503), new ApiError('request_timeout', 'unknown', '', 408), new ApiError('invalid_response', 'unknown', '', 200), new Error('invalid success metadata')]) {
    const failed = failKeyAccessPolicyReview(submitted, failure)
    assert.equal(failed.mode, failure.status === 409 ? 'conflict' : 'unknown')
    assert.throws(() => submitKeyAccessPolicyReview(failed, ''), /刷新/)
    assert.throws(() => resumeKeyAccessPolicyReview(failed), /刷新/)
    const observed = observeKeyAccessPolicy(failed, { ...policy, allowed_client_cidrs: [], access_policy_revision: '9007199254740996' })
    assert.equal(observed.mode, failed.mode, 'GET metadata is not an immutable receipt and cannot resolve the old write')
    assert.equal(observed.submitted.body.expected_revision, policy.access_policy_revision)
    assert.deepEqual(observed.submitted.body.allowed_client_cidrs, ['198.51.100.9'])
    assert.throws(() => submitKeyAccessPolicyReview(observed, ''), /刷新/)
    assert.throws(() => observeKeyAccessPolicy(failed, { ...policy, id: 'another-key' }), /不同/)
    assert.throws(() => observeKeyAccessPolicy(failed, { ...policy, access_policy_revision: '9007199254740992' }), /版本/)
    assert.throws(() => observeKeyAccessPolicy(failed, { ...policy, allowed_client_cidrs: [] }), /版本/)
    const reviewed = resumeKeyAccessPolicyReview(observed)
    assert.equal(reviewed.mode, 'editing')
    assert.equal(submitKeyAccessPolicyReview(reviewed, '').submitted.body.expected_revision, '9007199254740996')
  }
  for (const status of [400, 401, 403, 404, 422, 429]) {
    const failure = new ApiError('rejected', 'not applied', '', status)
    assert.equal(unknownKeyWriteResult(failure), false)
    assert.equal(failKeyAccessPolicyReview(submitted, failure).mode, 'editing')
  }
  assert.throws(() => submitKeyAccessPolicyReview(submitted, ''), /刷新/)
})

test('actor tenant role changes and close reject late results and confirmations while lanes stay independent', async () => {
  const scope = createKeyAccessScope(), actor = keyAccessContext('actor-a', 'tenant-a', 'owner')
  scope.reset(actor)
  const load = scope.ticket('list'), policyRead = scope.ticket('policy'), create = scope.ticket('create'), confirmation = scope.ticket('revoke')
  const newerList = scope.ticket('list')
  assert.equal(scope.accepts(load), false)
  for (const ticket of [newerList, policyRead, create, confirmation]) assert.equal(scope.accepts(ticket), true)
  scope.invalidate('policy'); assert.equal(scope.accepts(policyRead), false); assert.equal(scope.accepts(create), true)
  let release; const response = new Promise(resolve => { release = resolve }); let plaintext = ''
  const lateSecret = (async () => { await response; if (scope.accepts(create)) plaintext = 'must-not-restore' })()
  for (const next of [keyAccessContext('actor-b', 'tenant-a', 'owner'), keyAccessContext('actor-a', 'tenant-b', 'owner'), keyAccessContext('actor-a', 'tenant-a', 'member')]) {
    scope.reset(actor); const pending = scope.ticket('policy'); scope.reset(next); assert.equal(scope.accepts(pending), false)
  }
  scope.reset(actor); release(); await lateSecret
  assert.equal(plaintext, '')
  assert.equal(scope.accepts(confirmation), false)
  const unmount = scope.ticket('create'); scope.reset(); assert.equal(scope.accepts(unmount), false)
})

test('policy uses session CSRF and an uncertain write permits only an explicit GET check', async () => {
  const originalFetch = globalThis.fetch, calls = []
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    if (options.method === 'PUT') throw new TypeError('connection lost')
    return new Response(JSON.stringify({ data: { ...policy, key: 'never-read' }, request_id: 'safe-request' }))
  }
  try {
    setCsrf('key-policy-csrf')
    const submitted = submitKeyAccessPolicyReview(initial(), '')
    let failed
    try { const operation = submitted.submitted; await request(operation.path, operation.method, operation.body) }
    catch (error) { failed = failKeyAccessPolicyReview(submitted, error) }
    assert.equal(failed.mode, 'unknown')
    assert.throws(() => submitKeyAccessPolicyReview(failed, ''), /刷新/)
    const checked = observeKeyAccessPolicy(failed, (await request(keyAccessPolicyPath(keyID))).data)
    assert.equal(checked.mode, 'unknown')
    assert.deepEqual(calls.map(call => call.options.method), ['PUT', 'GET'])
    assert.deepEqual(JSON.parse(calls[0].options.body), { expected_revision: policy.access_policy_revision, allowed_client_cidrs: [] })
    assert.equal(calls[0].options.headers['X-CSRF-Token'], 'key-policy-csrf')
    assert.equal(calls[1].options.headers['X-CSRF-Token'], undefined)
    assert.ok(calls.every(call => call.options.credentials === 'include'))
    assert.ok(calls.every(call => !call.options.headers.Authorization && !call.options.headers['x-api-key'] && !call.options.headers['Idempotency-Key']))
    assert.ok(calls.every(call => !/\/wallet|\/credits|\/refunds|\/channels|\/models/.test(call.url)))
    assert.doesNotMatch(JSON.stringify(checked), /never-read/)
  } finally { globalThis.fetch = originalFetch; setCsrf('') }
})

test('key page keeps secrets in memory and clears dialogs on context changes and unmount', async () => {
  const [helper, component] = await Promise.all(['../src/key-access-policy.ts', '../src/views/KeysView.vue'].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  for (const source of [helper, component]) assert.doesNotMatch(source, /localStorage|sessionStorage|indexedDB|stableOperationKey|console\./)
  assert.doesNotMatch(component, /v-html/)
  assert.match(component, /watch\(context,.*reset\(\)/)
  assert.match(component, /onBeforeUnmount\(reset\)/)
  assert.match(component, /plaintext\.value = ''/)
  assert.match(component, /if \(!canManageKeys\.value \|\| !createOpen\.value \|\| saving\.value \|\| createUncertain\.value\) return/)
  assert.match(component, /:disabled="!!row\.revoked_at \|\| policySaving"/)
})
