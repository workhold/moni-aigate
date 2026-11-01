import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { ApiError, request, setCsrf } from '../src/api.ts'
import { createKeyAccessScope, failKeyAccessPolicyReview, keyAccessContext, newKeyAccessPolicyReview, submitKeyAccessPolicyReview } from '../src/key-access-policy.ts'
import { acceptKeyLimitsSave, failKeyLimitsReview, keyCreateWithLimitsRequest, keyLimitRevision, keyLimitsDraft, keyLimitsSaveRequest, keyRuntimeLimitsPath, newKeyLimitsReview, observeKeyLimits, parseKeyLimitsRow, parseKeyLimitsRows, parseKeyLimitsView, parseKeyRuntimeLimits, retainNewerKeyConfigurations, resumeKeyLimitsReview, submitKeyLimitsReview } from '../src/key-runtime-limits.ts'

const keyID = 'a9f89d66-fbef-4183-a811-e16b37ba62bb'
const limits = { rpm_limit: 60, burst_limit: 20, concurrency_limit: 16 }
const view = { id: keyID, ...limits, limit_revision: '9007199254740993' }
const row = { ...view, name: '应用', prefix: 'mg_prefix', scopes: ['*'], allowed_client_cidrs: ['203.0.113.0/24'], access_policy_revision: '9007199254741001', lifetime_limit: null, spent: '7', held: '3', expires_at: null, revoked_at: null }
const createInput = () => ({ name: ' 应用 ', scopes: ['*'], lifetime_limit: null, expires_at: null, allowed_client_cidrs: [], ...keyLimitsDraft() })
const initial = () => newKeyLimitsReview(structuredClone(view))

test('runtime limits enforce exact numeric integers and bounds without coercing cleared values or defaulting omissions', () => {
  assert.deepEqual(parseKeyRuntimeLimits(limits), limits)
  assert.deepEqual(parseKeyRuntimeLimits({ rpm_limit: 1, burst_limit: 1, concurrency_limit: 1 }), { rpm_limit: 1, burst_limit: 1, concurrency_limit: 1 })
  assert.deepEqual(parseKeyRuntimeLimits({ rpm_limit: 6000, burst_limit: 200, concurrency_limit: 16 }), { rpm_limit: 6000, burst_limit: 200, concurrency_limit: 16 })
  assert.deepEqual(parseKeyRuntimeLimits({ rpm_limit: 1, burst_limit: 200, concurrency_limit: 16 }), { rpm_limit: 1, burst_limit: 200, concurrency_limit: 16 }, 'burst is independent of RPM and may exceed it')
  for (const [field, maximum] of [['rpm_limit', 6000], ['burst_limit', 200], ['concurrency_limit', 16]]) {
    for (const invalid of [undefined, null, '', '60', true, false, 0, -1, 1.5, NaN, Infinity, -Infinity, maximum + 1, Number.MAX_SAFE_INTEGER + 1]) assert.throws(() => parseKeyRuntimeLimits({ ...limits, [field]: invalid }), /整数/, `${field}: ${invalid}`)
    const missing = { ...limits }; delete missing[field]
    assert.throws(() => parseKeyRuntimeLimits(missing), /不能留空/)
  }
  for (const invalid of [undefined, null, [], 60]) assert.throws(() => parseKeyRuntimeLimits(invalid), /格式/)
})

test('default create draft is fresh while a cleared or invalid submitted draft stays invalid', () => {
  const first = keyLimitsDraft(), second = keyLimitsDraft()
  assert.deepEqual(first, { rpm_limit: 60, burst_limit: 20, concurrency_limit: 16 })
  first.rpm_limit = undefined
  assert.equal(first.rpm_limit, undefined)
  assert.equal(second.rpm_limit, 60)
  assert.throws(() => keyLimitsSaveRequest(view, first), /不能留空/)
  assert.equal(first.rpm_limit, undefined, 'validation must not silently restore a cleared input to the default')
  assert.deepEqual(keyLimitsDraft({ rpm_limit: 90, burst_limit: 10, concurrency_limit: 4 }), { rpm_limit: 90, burst_limit: 10, concurrency_limit: 4 })
})

test('limit revision remains a canonical positive int64 string with full precision', () => {
  for (const revision of ['1', '9007199254740993', '9223372036854775807']) assert.equal(keyLimitRevision(revision), revision)
  for (const invalid of [1, 9007199254740993, null, '', '0', '-1', '+1', '01', '1e3', '1.0', ' 1', '9223372036854775808']) assert.throws(() => keyLimitRevision(invalid), /int64/)
})

test('GET and list metadata project only safe fields and retain legacy display text without inferring missing limits', () => {
  const input = { ...row, name: '\n', prefix: 'mg_\nold', scopes: ['', 'legacy\nscope', 'x'.repeat(201), '<img src=x onerror=alert(1)>'], key: 'mg_do_not_retain', digest: 'private', owner_id: 'private', tenant_id: 'private', tokens: 99 }
  assert.deepEqual(parseKeyLimitsView(input), view)
  const parsed = parseKeyLimitsRows([input])[0]
  assert.equal(parsed.name, '\n')
  assert.equal(parsed.prefix, input.prefix)
  assert.deepEqual(parsed.scopes, input.scopes)
  assert.equal(parsed.access_policy_revision, row.access_policy_revision)
  assert.equal(parsed.limit_revision, view.limit_revision)
  assert.doesNotMatch(JSON.stringify(parsed), /do_not_retain|private|owner_id|tenant_id|tokens|digest/)
  assert.throws(() => parseKeyLimitsView(view, 'other-key'), /不同/)
  for (const field of ['rpm_limit', 'burst_limit', 'concurrency_limit', 'limit_revision']) { const missing = { ...row }; delete missing[field]; assert.throws(() => parseKeyLimitsRow(missing)) }
  assert.throws(() => parseKeyLimitsRows([row, row]), /重复/)
  assert.throws(() => parseKeyLimitsRows(Array.from({ length: 51 }, (_, index) => ({ ...row, id: `key-${index}` }))), /列表/)
})

test('create request sends all three explicit runtime settings and source policy without unrelated write fields', () => {
  const input = createInput(), operation = keyCreateWithLimitsRequest({ ...input, tenant_id: 'must-not-send', limit_revision: '99', access_policy_revision: '88', key: 'must-not-send' })
  assert.deepEqual(operation, { path: '/keys', method: 'POST', body: { name: '应用', scopes: ['*'], lifetime_limit: null, expires_at: null, allowed_client_cidrs: [], rpm_limit: 60, burst_limit: 20, concurrency_limit: 16 } })
  input.rpm_limit = 120; input.scopes.push('late-model'); input.allowed_client_cidrs.push('203.0.113.1')
  assert.equal(operation.body.rpm_limit, 60)
  assert.deepEqual(operation.body.scopes, ['*'])
  assert.deepEqual(operation.body.allowed_client_cidrs, [])
  assert.ok(Object.isFrozen(operation) && Object.isFrozen(operation.body))
  for (const field of ['rpm_limit', 'burst_limit', 'concurrency_limit']) {
    const missing = createInput(); delete missing[field]
    assert.throws(() => keyCreateWithLimitsRequest(missing), /不能留空/)
    assert.throws(() => keyCreateWithLimitsRequest({ ...createInput(), [field]: null }), /不能留空/)
  }
})

test('runtime CAS freezes identity, revision and all three settings independently of source policy', () => {
  const source = structuredClone(view), draft = { rpm_limit: 120, burst_limit: 30, concurrency_limit: 8 }
  const operation = keyLimitsSaveRequest({ ...source, access_policy_revision: '999', allowed_client_cidrs: [] }, draft)
  assert.deepEqual(operation, { path: `/keys/${keyID}/runtime-limits`, method: 'PUT', body: { expected_revision: view.limit_revision, rpm_limit: 120, burst_limit: 30, concurrency_limit: 8 } })
  source.limit_revision = '9007199254740994'; draft.rpm_limit = 30; draft.concurrency_limit = 1
  assert.equal(operation.body.expected_revision, view.limit_revision)
  assert.equal(operation.body.rpm_limit, 120)
  assert.equal(operation.body.concurrency_limit, 8)
  assert.ok(Object.isFrozen(operation) && Object.isFrozen(operation.body))
  assert.throws(() => { operation.body.rpm_limit = 30 }, TypeError)
  assert.equal(keyRuntimeLimitsPath('key/one?x=y'), '/keys/key%2Fone%3Fx%3Dy/runtime-limits')
  for (const invalid of ['', ' ', '\nkey', null]) assert.throws(() => keyRuntimeLimitsPath(invalid), /标识/)
})

test('save success must match the frozen tuple and changed or no-op revision exactly', () => {
  const nextLimits = { rpm_limit: 120, burst_limit: 30, concurrency_limit: 8 }, submitted = submitKeyLimitsReview(initial(), nextLimits)
  const saved = acceptKeyLimitsSave(submitted, { id: keyID, ...nextLimits, limit_revision: '9007199254740994', key: 'must-drop', access_policy_revision: 'must-drop' })
  assert.equal(saved.mode, 'editing')
  assert.deepEqual(saved.view, { id: keyID, ...nextLimits, limit_revision: '9007199254740994' })
  assert.doesNotMatch(JSON.stringify(saved), /must-drop/)
  const unchanged = acceptKeyLimitsSave(submitKeyLimitsReview(initial(), keyLimitsDraft()), view)
  assert.equal(unchanged.view.limit_revision, view.limit_revision)
  for (const invalid of [{ id: 'different-key' }, { rpm_limit: 60 }, { burst_limit: 20 }, { concurrency_limit: 16 }, { limit_revision: view.limit_revision }, { limit_revision: '9007199254740992' }, { limit_revision: '9007199254740995' }]) assert.throws(() => acceptKeyLimitsSave(submitted, { id: keyID, ...nextLimits, limit_revision: '9007199254740994', ...invalid }))
  assert.throws(() => acceptKeyLimitsSave(submitKeyLimitsReview(initial(), limits), { ...view, limit_revision: '9007199254740994' }))
  const maximum = { ...view, limit_revision: '9223372036854775807' }
  assert.equal(acceptKeyLimitsSave(submitKeyLimitsReview(newKeyLimitsReview(maximum), limits), maximum).view.limit_revision, maximum.limit_revision)
})

test('409 and unknown writes remain blocked through GET observation until explicit review, with no replacement of the old command', () => {
  const submitted = submitKeyLimitsReview(initial(), { rpm_limit: 90, burst_limit: 10, concurrency_limit: 4 })
  for (const failure of [new ApiError('limit_conflict', 'conflict', '', 409), new ApiError('network_error', 'unknown', '', 0), new ApiError('unavailable', 'unknown', '', 503), new ApiError('request_timeout', 'unknown', '', 408), new ApiError('invalid_response', 'unknown', '', 200), new Error('mismatched success response')]) {
    const failed = failKeyLimitsReview(submitted, failure)
    assert.equal(failed.mode, failure.status === 409 ? 'conflict' : 'unknown')
    assert.throws(() => submitKeyLimitsReview(failed, limits), /刷新/)
    assert.throws(() => resumeKeyLimitsReview(failed), /刷新/)
    const observed = observeKeyLimits(failed, { ...view, rpm_limit: 240, limit_revision: '9007199254740997' })
    assert.equal(observed.mode, failed.mode, 'current configuration does not prove who applied a previous write')
    assert.equal(observed.submitted.body.expected_revision, view.limit_revision)
    assert.equal(observed.submitted.body.rpm_limit, 90)
    assert.throws(() => submitKeyLimitsReview(observed, limits), /刷新/)
    assert.throws(() => observeKeyLimits(failed, { ...view, id: 'other-key' }), /不同/)
    assert.throws(() => observeKeyLimits(failed, { ...view, limit_revision: '9007199254740992' }), /版本/)
    assert.throws(() => observeKeyLimits(failed, { ...view, rpm_limit: 240 }), /版本/)
    const reviewed = resumeKeyLimitsReview(observed)
    assert.equal(reviewed.mode, 'editing')
    assert.equal(submitKeyLimitsReview(reviewed, limits).submitted.body.expected_revision, '9007199254740997')
  }
  for (const status of [400, 401, 403, 404, 422, 429]) assert.equal(failKeyLimitsReview(submitted, new ApiError('rejected', 'not applied', '', status)).mode, 'editing')
  assert.throws(() => submitKeyLimitsReview(submitted, limits), /刷新/)
})

test('independent revisions merge interleaved old source and runtime snapshots without rolling either configuration back', () => {
  const newerSource = parseKeyLimitsRow({ ...row, allowed_client_cidrs: [], access_policy_revision: '9007199254741002', spent: '8' })
  const newerLimits = parseKeyLimitsRow({ ...row, rpm_limit: 90, concurrency_limit: 4, limit_revision: '9007199254740994', spent: '9' })
  const first = retainNewerKeyConfigurations([newerLimits], [newerSource])[0]
  assert.equal(first.access_policy_revision, newerSource.access_policy_revision)
  assert.deepEqual(first.allowed_client_cidrs, [])
  assert.equal(first.limit_revision, newerLimits.limit_revision)
  assert.equal(first.rpm_limit, 90)
  assert.equal(first.spent, '9')
  const reverse = retainNewerKeyConfigurations([newerSource], [newerLimits])[0]
  assert.equal(reverse.limit_revision, newerLimits.limit_revision)
  assert.equal(reverse.access_policy_revision, newerSource.access_policy_revision)
  assert.equal(reverse.spent, '8', 'only the two policy snapshots merge; unrelated fields come from the current list')
  assert.equal(newerSource.rpm_limit, 60)
  assert.deepEqual(newerLimits.allowed_client_cidrs, row.allowed_client_cidrs)
})

test('source and runtime unknown outcomes stay independent, while context changes invalidate both request lanes', () => {
  const source = failKeyAccessPolicyReview(submitKeyAccessPolicyReview(newKeyAccessPolicyReview(row), ''), new ApiError('network_error', 'unknown', '', 0))
  const runtime = failKeyLimitsReview(submitKeyLimitsReview(initial(), { rpm_limit: 90, burst_limit: 10, concurrency_limit: 4 }), new ApiError('network_error', 'unknown', '', 0))
  const sourceBefore = structuredClone(source), checked = observeKeyLimits(runtime, { ...view, rpm_limit: 90, burst_limit: 10, concurrency_limit: 4, limit_revision: '9007199254740994' })
  assert.equal(resumeKeyLimitsReview(checked).mode, 'editing')
  assert.deepEqual(source, sourceBefore)
  assert.equal(source.mode, 'unknown')
  const scope = createKeyAccessScope(); scope.reset(keyAccessContext('actor', 'tenant', 'owner'))
  const access = scope.ticket('policy'), runtimeTicket = scope.ticket('runtime-limits')
  scope.invalidate('policy'); assert.equal(scope.accepts(access), false); assert.equal(scope.accepts(runtimeTicket), true)
  const newAccess = scope.ticket('policy'); scope.invalidate('runtime-limits')
  assert.equal(scope.accepts(newAccess), true); assert.equal(scope.accepts(runtimeTicket), false)
  for (const context of [keyAccessContext('other-actor', 'tenant', 'owner'), keyAccessContext('actor', 'other-tenant', 'owner'), keyAccessContext('actor', 'tenant', 'member')]) {
    const pendingAccess = scope.ticket('policy'), pendingRuntime = scope.ticket('runtime-limits')
    scope.reset(context); assert.equal(scope.accepts(pendingAccess), false); assert.equal(scope.accepts(pendingRuntime), false)
  }
})

test('runtime transport writes exactly once with session CSRF and only explicitly GETs after an uncertain result', async () => {
  const originalFetch = globalThis.fetch, calls = []
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    if (options.method === 'PUT') throw new TypeError('lost response')
    return new Response(JSON.stringify({ data: { ...view, key: 'never-retain' }, request_id: 'runtime-request' }))
  }
  try {
    setCsrf('runtime-csrf')
    const submitted = submitKeyLimitsReview(initial(), { rpm_limit: 90, burst_limit: 10, concurrency_limit: 4 })
    let failed
    try { const operation = submitted.submitted; await request(operation.path, operation.method, operation.body) }
    catch (error) { failed = failKeyLimitsReview(submitted, error) }
    assert.equal(failed.mode, 'unknown')
    assert.throws(() => submitKeyLimitsReview(failed, limits), /刷新/)
    const observed = observeKeyLimits(failed, (await request(keyRuntimeLimitsPath(keyID))).data)
    assert.equal(observed.mode, 'unknown')
    assert.deepEqual(calls.map(call => call.options.method), ['PUT', 'GET'])
    assert.deepEqual(JSON.parse(calls[0].options.body), { expected_revision: view.limit_revision, rpm_limit: 90, burst_limit: 10, concurrency_limit: 4 })
    assert.equal(calls[0].options.headers['X-CSRF-Token'], 'runtime-csrf')
    assert.equal(calls[1].options.headers['X-CSRF-Token'], undefined)
    assert.ok(calls.every(call => call.options.credentials === 'include'))
    assert.ok(calls.every(call => !call.options.headers.Authorization && !call.options.headers['x-api-key'] && !call.options.headers['Idempotency-Key']))
    assert.ok(calls.every(call => !/\/access-policy|\/wallet|\/credits|\/refunds|\/channels|\/models/.test(call.url)))
    assert.doesNotMatch(JSON.stringify(observed), /never-retain/)
  } finally { globalThis.fetch = originalFetch; setCsrf('') }
})

test('runtime editor keeps source state separate and explains runtime boundaries without persisting secrets', async () => {
  const [helper, component] = await Promise.all(['../src/key-runtime-limits.ts', '../src/views/KeysView.vue'].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  for (const source of [helper, component]) assert.doesNotMatch(source, /localStorage|sessionStorage|indexedDB|stableOperationKey|console\./)
  assert.match(component, /const unresolvedPolicies = new Map/)
  assert.match(component, /const unresolvedLimits = new Map/)
  assert.match(component, /scope\.invalidate\('runtime-limits'\)/)
  assert.match(component, /unresolvedPolicies\.clear\(\); unresolvedLimits\.clear\(\)/)
  assert.match(component, /已获得运行态准入许可/)
  for (const meaning of ['保留旧桶', '不会补满令牌', '单活动实例', '重启后令牌重新满桶', '不是额外的每分钟额度', '下次新调用准入观察到时切换']) assert.ok(component.includes(meaning), meaning)
})
