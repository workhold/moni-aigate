import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { ApiError, request, setCsrf } from '../src/api.ts'
import { cnyToMicro } from '../src/format.ts'
import { createKeyAccessScope, failKeyAccessPolicyReview, keyAccessContext, newKeyAccessPolicyReview, submitKeyAccessPolicyReview } from '../src/key-access-policy.ts'
import { failKeyLimitsReview, newKeyLimitsReview, submitKeyLimitsReview } from '../src/key-runtime-limits.ts'
import { acceptKeyConfigurationSave, applyKeyConfigurationToRows, failKeyConfigurationReview, keyConfigurationDraft, keyConfigurationOptions, keyConfigurationPath, keyConfigurationRevision, keyConfigurationSaveRequest, keyCreateWithConfigurationRequest, keyMicroToCoin, newKeyConfigurationReview, normalizeKeyConfigurationExpiry, observeKeyConfiguration, parseKeyConfigurationRow, parseKeyConfigurationRows, parseKeyConfigurationView, retainNewerKeySettings, resumeKeyConfigurationReview, submitKeyConfigurationReview } from '../src/key-configuration.ts'

const keyID = 'a9f89d66-fbef-4183-a811-e16b37ba62bb'
const modelA = '00000000-0000-0000-0000-000000000001', modelB = '00000000-0000-0000-0000-000000000002'
const view = { id: keyID, name: '应用', scopes: ['*'], allowed_regions: ['global'], lifetime_limit: '9007199254740993', expires_at: '2030-01-02T03:04:05.123456Z', configuration_revision: '9007199254740993', spent: '7', held: '3' }
const row = { ...view, prefix: 'mg_prefix', allowed_client_cidrs: ['203.0.113.0/24'], access_policy_revision: '9007199254741001', rpm_limit: 60, burst_limit: 20, concurrency_limit: 16, limit_revision: '9007199254741011', revoked_at: null }
const initial = () => newKeyConfigurationReview(structuredClone(view))
const draft = changes => ({ ...keyConfigurationDraft(view), ...changes })
const createInput = () => ({ name: ' 应用 ', scopes: ['*'], allowed_regions: ['global'], lifetime_limit: null, expires_at: null, allowed_client_cidrs: [], rpm_limit: 60, burst_limit: 20, concurrency_limit: 16 })
const changedResponse = (submitted, changes = {}) => ({ id: keyID, ...submitted.submitted.body, configuration_revision: '9007199254740994', spent: '11', held: '4', ...changes })

test('configuration revision and CNY drafts retain exact int64 precision, including zero and no cap', () => {
  for (const revision of ['1', '9007199254740993', '9223372036854775807']) assert.equal(keyConfigurationRevision(revision), revision)
  for (const invalid of [undefined, null, 1, '', '0', '-1', '+1', '01', '1.0', '1e3', ' 1', '9223372036854775808']) assert.throws(() => keyConfigurationRevision(invalid), /int64/)
  for (const [micro, cny] of [['0', '0'], ['1', '0.000001'], ['1000000', '1'], ['1001000', '1.001'], ['9007199254740993', '9007199254.740993'], ['9223372036854775807', '9223372036854.775807']]) {
    assert.equal(keyMicroToCoin(micro), cny)
    assert.equal(cnyToMicro(cny), micro)
    assert.equal(keyConfigurationSaveRequest({ ...view, lifetime_limit: micro }, keyConfigurationDraft({ ...view, lifetime_limit: micro })).body.lifetime_limit, micro)
  }
  assert.equal(keyConfigurationSaveRequest(view, draft({ limit: '0' })).body.lifetime_limit, '0')
  assert.equal(keyConfigurationSaveRequest(view, draft({ limit: '' })).body.lifetime_limit, null)
  for (const limit of ['-1', '0.0000001', '1e3', 'NaN', '9223372036854.775808']) assert.throws(() => keyConfigurationSaveRequest(view, draft({ limit })), /金额/)
})

test('configuration GET safely projects nine explicit fields and does not infer nullable omissions', () => {
  const parsed = parseKeyConfigurationView({ ...row, owner_id: 'private-owner', tenant_id: 'private-tenant', key: 'private-secret', digest: 'private-digest' })
  assert.deepEqual(parsed, view)
  assert.deepEqual(Object.keys(parsed).sort(), ['id', 'name', 'scopes', 'allowed_regions', 'lifetime_limit', 'expires_at', 'configuration_revision', 'spent', 'held'].sort())
  assert.doesNotMatch(JSON.stringify(parsed), /private-|owner_id|tenant_id|digest|limit_revision|access_policy_revision/)
  for (const field of Object.keys(view)) { const missing = { ...view }; delete missing[field]; assert.throws(() => parseKeyConfigurationView(missing), field) }
  for (const field of ['spent', 'held', 'lifetime_limit']) for (const invalid of [0, '-1', '01', '9223372036854775808']) assert.throws(() => parseKeyConfigurationView({ ...view, [field]: invalid }))
  for (const field of ['scopes', 'allowed_regions']) for (const invalid of [null, '', ['*', null], [1]]) assert.throws(() => parseKeyConfigurationView({ ...view, [field]: invalid }))
  assert.deepEqual(parseKeyConfigurationView({ ...view, lifetime_limit: null, expires_at: null }).expires_at, null)
  assert.throws(() => parseKeyConfigurationView(view, 'other-key'), /不同/)
  assert.throws(() => parseKeyConfigurationView({ ...view, expires_at: '' }), /RFC3339/)
})

test('unchanged legacy fields and expired microseconds survive unrelated edits without normalization', () => {
  for (const scopes of [[], ['old-scope', '', 'old-scope', '<img src=x onerror=alert(1)>'], Array(1001).fill('old')]) {
    const legacy = { ...view, name: '\n legacy\t', scopes, allowed_regions: [' legacy\nregion ', '', 'global', 'global'], expires_at: '2001-02-03T04:05:06.000001+08:00' }
    const legacyDraft = keyConfigurationDraft(legacy)
    assert.equal(legacyDraft.name, legacy.name)
    assert.deepEqual(legacyDraft.scopes, scopes)
    assert.deepEqual(legacyDraft.allowed_regions, legacy.allowed_regions)
    assert.equal(legacyDraft.expiry_mode, 'keep')
    const operation = keyConfigurationSaveRequest(legacy, { ...legacyDraft, limit: '1' })
    assert.equal(operation.body.name, legacy.name)
    assert.deepEqual(operation.body.scopes, legacy.scopes)
    assert.deepEqual(operation.body.allowed_regions, legacy.allowed_regions)
    assert.equal(operation.body.expires_at, legacy.expires_at)
    assert.deepEqual(parseKeyConfigurationRow({ ...row, ...legacy }).scopes, scopes)
    assert.deepEqual(parseKeyConfigurationRows([{ ...row, ...legacy }])[0].allowed_regions, legacy.allowed_regions)
  }
  const legacy = { ...view, scopes: [], allowed_regions: [] }
  assert.deepEqual(keyConfigurationSaveRequest(legacy, keyConfigurationDraft(legacy)).body.allowed_regions, [])
})

test('only changed labels and arrays normalize with Go whitespace, UTF-8 bounds and codepoint ordering', () => {
  const operation = keyConfigurationSaveRequest(view, draft({ name: '\u0085 新名称 \u0085', scopes: [modelB, modelA, modelB], allowed_regions: ['\u{10000}', ' us-east ', '\uE000', 'global', 'us-east'] }))
  assert.equal(operation.body.name, '新名称')
  assert.deepEqual(operation.body.scopes, [modelA, modelB])
  assert.deepEqual(operation.body.allowed_regions, ['global', 'us-east', '\uE000', '\u{10000}'], 'Go UTF-8 sorting differs from UTF-16 default sorting')
  assert.equal(keyConfigurationSaveRequest(view, draft({ name: '汉'.repeat(33) + 'a' })).body.name, '汉'.repeat(33) + 'a')
  assert.deepEqual(keyConfigurationSaveRequest(view, draft({ allowed_regions: ['汉'.repeat(26) + 'ab'] })).body.allowed_regions, ['汉'.repeat(26) + 'ab'])
  assert.deepEqual(keyConfigurationSaveRequest(view, draft({ allowed_regions: [' * '] })).body.allowed_regions, ['*'])
  for (const name of ['', ' ', '汉'.repeat(34), 'a\nb', 'a\u007fb', 'a\u0085b', '\uD800']) assert.throws(() => keyConfigurationSaveRequest(view, draft({ name })), /名称/)
  for (const regions of [[], [''], [' '], ['a\nb'], ['a\u0085b'], ['\uD800'], ['汉'.repeat(27)], Array(33).fill('global'), ['*', '*'], ['*', 'global'], [' * ', ' * ']]) assert.throws(() => keyConfigurationSaveRequest(view, draft({ allowed_regions: regions })), /地域/)
})

test('changed scopes enforce canonical UUID inputs and original wildcard cardinality before deduplication', () => {
  const legacy = { ...view, scopes: ['old'] }
  assert.deepEqual(keyConfigurationSaveRequest(legacy, { ...keyConfigurationDraft(legacy), scopes: ['*'] }).body.scopes, ['*'])
  for (const scopes of [[], ['*', '*'], ['*', modelA], [' ' + modelA], [modelA.toUpperCase().replace('00000000', 'ABCDEF00')], ['model-name'], Array(1001).fill(modelA)]) assert.throws(() => keyConfigurationSaveRequest(legacy, { ...keyConfigurationDraft(legacy), scopes }), /模型范围/)
  const scopes = Array.from({ length: 1000 }, (_, index) => `00000000-0000-0000-0000-${(index + 1).toString(16).padStart(12, '0')}`)
  const operation = keyConfigurationSaveRequest(view, draft({ scopes }))
  assert.equal(operation.body.scopes.length, 1000)
  assert.ok(new TextEncoder().encode(JSON.stringify(operation.body)).length < 64 * 1024)
  const created = keyCreateWithConfigurationRequest({ ...createInput(), scopes })
  assert.ok(new TextEncoder().encode(JSON.stringify(created.body)).length < 64 * 1024)
})

test('RFC3339 expiry retains seconds, truncates nanos and normalizes offsets using exact microseconds', () => {
  for (const [input, output] of [
    ['2030-01-02T03:04:05.123456789+08:00', '2030-01-01T19:04:05.123456Z'],
    ['2030-01-02T03:04:05.000001-07:30', '2030-01-02T10:34:05.000001Z'],
    ['1969-12-31T23:59:59.000001Z', '1969-12-31T23:59:59.000001Z'],
    ['2030-01-02T03:04:05.100000000Z', '2030-01-02T03:04:05.1Z'],
    ['2000-02-29T03:04:05Z', '2000-02-29T03:04:05Z'],
    ['0001-01-01T00:00:00Z', '0001-01-01T00:00:00Z'],
    ['9999-12-31T23:59:59.999999Z', '9999-12-31T23:59:59.999999Z'],
  ]) assert.equal(normalizeKeyConfigurationExpiry(input), output)
  for (const expiry of ['', '2030-01-02', '2030-01-02T03:04:05', '2030-02-29T03:04:05Z', '2030-13-01T03:04:05Z', '2030-01-02T24:00:00Z', '2030-01-02T03:04:60Z', '2030-01-02T03:04:05+24:00', '2030-01-02T03:04:05+08:60', '9999-12-31T23:59:59-00:01', '0000-01-01T00:00:00+00:01']) assert.throws(() => normalizeKeyConfigurationExpiry(expiry), /有效期|RFC3339/)
})

test('expiry keep, set-same-instant and explicit removal do not silently alter old microseconds', () => {
  const legacy = { ...view, expires_at: '2001-02-03T04:05:06.123456+08:00' }, form = keyConfigurationDraft(legacy)
  assert.equal(keyConfigurationSaveRequest(legacy, { ...form, expiry: 'ignored-invalid-date' }).body.expires_at, legacy.expires_at)
  assert.equal(keyConfigurationSaveRequest(legacy, { ...form, expiry_mode: 'set', expiry: '2001-02-02T20:05:06.123456999Z' }).body.expires_at, legacy.expires_at)
  assert.equal(keyConfigurationSaveRequest(legacy, { ...form, expiry_mode: 'none' }).body.expires_at, null)
  assert.equal(keyConfigurationSaveRequest(legacy, { ...form, expiry_mode: 'set', expiry: '2001-02-03T04:05:07.654321987+08:00' }).body.expires_at, '2001-02-02T20:05:07.654321Z')
  assert.throws(() => keyConfigurationSaveRequest(legacy, { ...form, expiry_mode: 'set', expiry: '' }), /RFC3339/)
  assert.throws(() => keyConfigurationSaveRequest(legacy, { ...form, expiry_mode: '' }), /明确/)
})

test('configuration CAS freezes exactly six fields independently of source, runtime and financial observations', () => {
  const source = structuredClone(view), form = draft({ name: ' 新名称 ', scopes: [modelB, modelA], allowed_regions: [' us-east ', 'global'], limit: '1', expiry_mode: 'none' })
  const operation = keyConfigurationSaveRequest({ ...source, key: 'must-not-send', tenant_id: 'must-not-send', limit_revision: '99', access_policy_revision: '88' }, form)
  assert.deepEqual(operation, { path: `/keys/${keyID}/configuration`, method: 'PUT', body: { expected_revision: view.configuration_revision, name: '新名称', scopes: [modelA, modelB], allowed_regions: ['global', 'us-east'], lifetime_limit: '1000000', expires_at: null } })
  assert.equal(Object.keys(operation.body).length, 6)
  assert.doesNotMatch(JSON.stringify(operation), /must-not-send|spent|held|tenant_id|key_secret|limit_revision|access_policy_revision/)
  form.name = 'late'; form.scopes.push('late'); form.allowed_regions.push('late'); source.configuration_revision = '9'
  assert.equal(operation.body.name, '新名称')
  assert.deepEqual(operation.body.scopes, [modelA, modelB])
  assert.equal(operation.body.expected_revision, view.configuration_revision)
  assert.ok(Object.isFrozen(operation) && Object.isFrozen(operation.body) && Object.isFrozen(operation.body.scopes) && Object.isFrozen(operation.body.allowed_regions))
  assert.throws(() => operation.body.scopes.push(modelA), TypeError)
  assert.equal(keyConfigurationPath('key/one?x=y'), '/keys/key%2Fone%3Fx%3Dy/configuration')
  for (const invalid of ['', ' ', '\nkey', null]) assert.throws(() => keyConfigurationPath(invalid), /标识/)
})

test('creation explicitly submits normalized regions and three limits while retaining the existing safe create shape', () => {
  const input = { ...createInput(), allowed_regions: [' us-east ', 'global', 'us-east'], key: 'private', configuration_revision: '99', owner_id: 'private' }
  const operation = keyCreateWithConfigurationRequest(input)
  assert.deepEqual(operation, { path: '/keys', method: 'POST', body: { name: '应用', scopes: ['*'], lifetime_limit: null, expires_at: null, allowed_client_cidrs: [], rpm_limit: 60, burst_limit: 20, concurrency_limit: 16, allowed_regions: ['global', 'us-east'] } })
  input.allowed_regions.push('late')
  assert.deepEqual(operation.body.allowed_regions, ['global', 'us-east'])
  assert.ok(Object.isFrozen(operation.body.allowed_regions))
  assert.doesNotMatch(JSON.stringify(operation), /private|configuration_revision|owner_id/)
  assert.throws(() => keyCreateWithConfigurationRequest({ ...createInput(), allowed_regions: [] }), /地域/)
})

test('successful saves require the complete frozen tuple and exact changed or no-op revision', () => {
  const submitted = submitKeyConfigurationReview(initial(), draft({ name: ' 新名称 ', scopes: [modelB, modelA], allowed_regions: [' us-east ', 'global'], limit: '1', expiry_mode: 'set', expiry: '2031-01-02T03:04:05.123456789+08:00' }))
  const saved = acceptKeyConfigurationSave(submitted, changedResponse(submitted, { key: 'drop-secret', owner_id: 'drop-owner' }))
  assert.equal(saved.mode, 'editing')
  assert.equal(saved.view.name, '新名称')
  assert.equal(saved.view.expires_at, '2031-01-01T19:04:05.123456Z')
  assert.equal(saved.view.spent, '11')
  assert.doesNotMatch(JSON.stringify(saved), /drop-|expected_revision|submitted/)
  for (const change of [{ id: 'other-key' }, { name: 'other' }, { scopes: [modelB, modelA] }, { allowed_regions: ['us-east', 'global'] }, { lifetime_limit: null }, { expires_at: null }, { configuration_revision: view.configuration_revision }, { configuration_revision: '9007199254740992' }, { configuration_revision: '9007199254740995' }]) assert.throws(() => acceptKeyConfigurationSave(submitted, changedResponse(submitted, change)))
  const unchanged = submitKeyConfigurationReview(initial(), draft({}))
  assert.equal(acceptKeyConfigurationSave(unchanged, { ...view, spent: '8', held: '4' }).view.configuration_revision, view.configuration_revision)
  assert.throws(() => acceptKeyConfigurationSave(unchanged, { ...view, configuration_revision: '9007199254740994' }))
  const maximum = { ...view, configuration_revision: '9223372036854775807' }
  assert.equal(acceptKeyConfigurationSave(submitKeyConfigurationReview(newKeyConfigurationReview(maximum), keyConfigurationDraft(maximum)), maximum).view.configuration_revision, maximum.configuration_revision)
})

test('budget/CAS conflicts and uncertain writes require GET and explicit review, even when the tuple matches', () => {
  const submitted = submitKeyConfigurationReview(initial(), draft({ name: '新名称' }))
  for (const failure of [new ApiError('key_budget_below_usage', 'budget', '', 409), new ApiError('conflict', 'conflict', '', 409), new ApiError('network_error', 'unknown', '', 0), new ApiError('unavailable', 'unknown', '', 503), new ApiError('request_timeout', 'unknown', '', 408), new ApiError('invalid_response', 'unknown', '', 200), new Error('mismatched success')]) {
    const failed = failKeyConfigurationReview(submitted, failure)
    assert.equal(failed.mode, failure.status === 409 ? 'conflict' : 'unknown')
    assert.throws(() => submitKeyConfigurationReview(failed, draft({})), /刷新/)
    assert.throws(() => resumeKeyConfigurationReview(failed), /刷新/)
    const observed = observeKeyConfiguration(failed, changedResponse(submitted))
    assert.equal(observed.mode, failed.mode, 'a matching current tuple does not prove attribution')
    assert.strictEqual(observed.submitted, submitted.submitted)
    assert.equal(observed.submitted.body.expected_revision, view.configuration_revision)
    assert.throws(() => submitKeyConfigurationReview(observed, draft({})), /刷新/)
    const resumed = resumeKeyConfigurationReview(observed)
    assert.equal(resumed.mode, 'editing')
    assert.equal(submitKeyConfigurationReview(resumed, keyConfigurationDraft(resumed.view)).submitted.body.expected_revision, '9007199254740994')
    assert.throws(() => observeKeyConfiguration(failed, { ...view, id: 'other-key' }), /不同/)
    assert.throws(() => observeKeyConfiguration(failed, { ...view, configuration_revision: '9007199254740992' }), /版本/)
    assert.throws(() => observeKeyConfiguration(failed, { ...view, name: 'different-without-version' }), /版本/)
    const moneyOnly = observeKeyConfiguration(failed, { ...view, spent: '9007199254740993', held: '11' })
    assert.equal(moneyOnly.mode, failed.mode)
    assert.equal(moneyOnly.observed.spent, '9007199254740993')
  }
  for (const status of [400, 401, 403, 404, 422, 429]) assert.equal(failKeyConfigurationReview(submitted, new ApiError('rejected', 'not applied', '', status)).mode, 'editing')
})

test('old lists and basic GET/save snapshots preserve all three independent revisions and current list consumption', () => {
  const previous = parseKeyConfigurationRow({ ...row, name: 'new-basic', scopes: [modelA], allowed_regions: ['cn'], lifetime_limit: '100', expires_at: null, configuration_revision: '9007199254740995', allowed_client_cidrs: [], access_policy_revision: '9007199254741003', rpm_limit: 90, limit_revision: '9007199254741013', spent: '18', held: '7' })
  const oldList = parseKeyConfigurationRow({ ...row, spent: '21', held: '8' })
  const merged = retainNewerKeySettings([oldList], [previous])[0]
  assert.equal(merged.name, 'new-basic'); assert.deepEqual(merged.scopes, [modelA]); assert.deepEqual(merged.allowed_regions, ['cn'])
  assert.equal(merged.configuration_revision, previous.configuration_revision)
  assert.deepEqual(merged.allowed_client_cidrs, []); assert.equal(merged.access_policy_revision, previous.access_policy_revision)
  assert.equal(merged.rpm_limit, 90); assert.equal(merged.limit_revision, previous.limit_revision)
  assert.equal(merged.spent, '21'); assert.equal(merged.held, '8')
  const basicView = { ...view, name: 'newest-basic', configuration_revision: '9007199254740996', spent: '1', held: '1' }
  const applied = applyKeyConfigurationToRows([merged], basicView)[0]
  assert.equal(applied.name, 'newest-basic'); assert.equal(applied.spent, '21'); assert.equal(applied.held, '8')
  assert.equal(applied.rpm_limit, 90); assert.equal(applied.limit_revision, previous.limit_revision)
  assert.deepEqual(applied.allowed_client_cidrs, []); assert.equal(applied.access_policy_revision, previous.access_policy_revision)
  assert.strictEqual(applyKeyConfigurationToRows([applied], view)[0], applied)
  const reverse = retainNewerKeySettings([previous], [parseKeyConfigurationRow({ ...row, rpm_limit: 120, limit_revision: '9007199254741014', spent: '999' })])[0]
  assert.equal(reverse.name, previous.name); assert.equal(reverse.rpm_limit, 120); assert.equal(reverse.spent, previous.spent)
  assert.equal(oldList.name, view.name)
})

test('three unknown reviews and request lanes stay independent until actor, tenant or role changes', () => {
  const unknown = new ApiError('network_error', 'unknown', '', 0)
  const basic = failKeyConfigurationReview(submitKeyConfigurationReview(initial(), draft({ name: '新名称' })), unknown)
  const source = failKeyAccessPolicyReview(submitKeyAccessPolicyReview(newKeyAccessPolicyReview(row), ''), unknown)
  const runtime = failKeyLimitsReview(submitKeyLimitsReview(newKeyLimitsReview(row), { rpm_limit: 90, burst_limit: 10, concurrency_limit: 4 }), unknown)
  const previous = structuredClone({ source, runtime })
  resumeKeyConfigurationReview(observeKeyConfiguration(basic, view))
  assert.deepEqual({ source, runtime }, previous)
  assert.equal(source.mode, 'unknown'); assert.equal(runtime.mode, 'unknown')
  const scope = createKeyAccessScope(); scope.reset(keyAccessContext('actor', 'tenant', 'owner'))
  const tickets = ['policy', 'runtime-limits', 'configuration'].map(lane => scope.ticket(lane))
  scope.invalidate('configuration')
  assert.deepEqual(tickets.map(ticket => scope.accepts(ticket)), [true, true, false])
  const oldBasic = scope.ticket('configuration'), newerBasic = scope.ticket('configuration')
  assert.equal(scope.accepts(oldBasic), false); assert.equal(scope.accepts(newerBasic), true)
  for (const context of [keyAccessContext('other', 'tenant', 'owner'), keyAccessContext('actor', 'other', 'owner'), keyAccessContext('actor', 'tenant', 'member')]) {
    const pending = ['list', 'policy', 'runtime-limits', 'configuration', 'create'].map(lane => scope.ticket(lane))
    scope.reset(context)
    assert.ok(pending.every(ticket => !scope.accepts(ticket)))
  }
})

test('directory options preserve empty, control, duplicate and unlisted historical values without altering selection', () => {
  const values = ['', 'legacy\nscope', modelB, modelB, '<img src=x onerror=alert(1)>'], before = [...values]
  const choices = [{ value: '*', label: '全部' }, { value: modelA, label: '模型 A' }]
  const options = keyConfigurationOptions(values, choices)
  assert.deepEqual(values, before)
  assert.deepEqual(options.map(option => option.value), ['*', modelA, '', 'legacy\nscope', modelB, '<img src=x onerror=alert(1)>'])
  assert.ok(options.some(option => option.label.includes('"legacy\\nscope"')))
  options[0].label = 'changed'
  assert.equal(choices[0].label, '全部')
})

test('configuration transport sends one session-CSRF PUT and an explicit GET after response loss', async () => {
  const originalFetch = globalThis.fetch, calls = []
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    if (options.method === 'PUT') throw new TypeError('lost response')
    return new Response(JSON.stringify({ data: { ...view, key: 'never-retain' }, request_id: 'configuration-request' }))
  }
  try {
    setCsrf('configuration-csrf')
    const submitted = submitKeyConfigurationReview(initial(), draft({ name: '新名称' }))
    let failed
    try { const operation = submitted.submitted; await request(operation.path, operation.method, operation.body) }
    catch (error) { failed = failKeyConfigurationReview(submitted, error) }
    assert.equal(failed.mode, 'unknown')
    assert.throws(() => submitKeyConfigurationReview(failed, draft({})), /刷新/)
    const observed = observeKeyConfiguration(failed, (await request(keyConfigurationPath(keyID))).data)
    assert.equal(observed.mode, 'unknown')
    assert.deepEqual(calls.map(call => call.options.method), ['PUT', 'GET'])
    assert.equal(calls[0].options.headers['X-CSRF-Token'], 'configuration-csrf')
    assert.equal(calls[1].options.headers['X-CSRF-Token'], undefined)
    assert.deepEqual(JSON.parse(calls[0].options.body), submitted.submitted.body)
    assert.ok(calls.every(call => call.options.credentials === 'include'))
    assert.ok(calls.every(call => !call.options.headers.Authorization && !call.options.headers['x-api-key'] && !call.options.headers['Idempotency-Key']))
    assert.ok(calls.every(call => !/\/access-policy|\/runtime-limits|\/wallet|\/credits|\/refunds|\/channels|\/models/.test(call.url)))
    assert.doesNotMatch(JSON.stringify(observed), /never-retain/)
  } finally { globalThis.fetch = originalFetch; setCsrf('') }
})

test('basic editor keeps separate unknown state, explicit expiry and complete legacy scope/region visibility', async () => {
  const [helper, component] = await Promise.all(['../src/key-configuration.ts', '../src/views/KeysView.vue'].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  for (const source of [helper, component]) assert.doesNotMatch(source, /localStorage|sessionStorage|indexedDB|stableOperationKey|console\.|v-html/)
  assert.match(component, /const unresolvedConfigurations = new Map/)
  assert.match(component, /scope\.invalidate\('configuration'\)/)
  assert.match(component, /unresolvedPolicies\.clear\(\); unresolvedLimits\.clear\(\); unresolvedConfigurations\.clear\(\)/)
  assert.match(component, /onBeforeUnmount\(reset\)/)
  assert.match(component, /applyKeyConfigurationToRows\(rows\.value, view\)/)
  assert.match(component, /retainNewerKeySettings\(parseKeyConfigurationRows/)
  assert.match(component, /v-if="canManageKeys" label="操作"/)
  assert.match(component, /!!row\.revoked_at \|\| configurationSaving/)
  for (const meaning of ['global 是精确标签', '原始秒与微秒保持不变', '保留已过期时间也可编辑其他字段', '移除期限', '已撤销密钥无法恢复', '始终与租户和所属成员授权取交集', '目录外或历史值', '最新已消费与冻结之和', '不能证明上次写入是否成功', '以当前基本配置开始新编辑', '派发准入']) assert.ok(component.includes(meaning), meaning)
})
