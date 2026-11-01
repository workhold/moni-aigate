import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { ref, reactive } from 'vue'
import { ApiError, request, setCsrf } from '../src/api.ts'
import { createKeyAccessScope, keyAccessContext } from '../src/key-access-policy.ts'
import { acceptCostWrite, acceptRouteWrite, costPolicy, costRateToCny, costWrite, createCostWriteJournal, parseCostChannel, parseCostVersion, parseRouteView, readConfigurationRows, routeDrafts, routeWrite, unknownCostWriteResult } from '../src/cost-aware-routing.ts'

const modelID = '6d772865-f77e-422c-aa5b-64c2a6b3fa66', channelID = '653b7cd7-d1ce-4d7b-8203-a8f2c1c9f5ad'
const channel = { id: channelID, name: '采购渠道', management_revision: '9007199254740993', enabled: true }
const binding = { channel_id: channelID, upstream_model: 'supplier/exact-model', priority: 4, weight: 2, max_inflight: 10, max_input_cost_rate: null, max_output_cost_rate: '0' }
const model = { id: modelID, public_name: '公开模型', active_revision: 3, cost_policy: { mode: 'manual', unknown_cost_policy: 'deny' }, bindings: [binding] }
const draft = { upstream_model: 'supplier/exact-model', basis: 'contract', input_rate: '9007199254.740993', output_rate: '0', effective_at: '' }
const actor = keyAccessContext('admin-one', 'tenant-one', 'admin')
const journal = () => createCostWriteJournal(v => BigInt(v.management_revision), (a, b) => JSON.stringify(parseCostChannel(a)) === JSON.stringify(parseCostChannel(b)))

test('route projection keeps full cost policy, exact strings and safe fields only', () => {
  const view = parseRouteView({ ...model, secret: 'never-retain', bindings: [{ ...binding, secret: 'private', max_input_cost_rate: '9007199254740993' }] }, modelID)
  assert.equal(view.bindings[0].max_input_cost_rate, '9007199254740993')
  assert.deepEqual(view.cost_policy, model.cost_policy)
  assert.doesNotMatch(JSON.stringify(view), /secret|private|never-retain/)
  assert.throws(() => parseRouteView(model, 'another-model'), /不同/)
  assert.deepEqual(parseRouteView({ ...model, cost_policy: undefined }).cost_policy, { mode: 'manual', unknown_cost_policy: 'allow' })
  assert.throws(() => parseRouteView({ ...model, active_revision: 9007199254740992 }))
  assert.throws(() => parseRouteView({ ...model, bindings: [binding, binding] }), /重复/)
})

test('CNY conversion distinguishes unlimited null, known free zero and int64 precision', () => {
  assert.equal(costRateToCny(null), '')
  assert.equal(costRateToCny('0'), '0.000000')
  assert.equal(costRateToCny('9007199254740993'), '9007199254.740993')
  assert.equal(costRateToCny('9223372036854775807'), '9223372036854.775807')
  const view = parseRouteView(model), drafts = routeDrafts(view)
  assert.equal(drafts[0].max_input_cost_rate, '')
  assert.equal(drafts[0].max_output_cost_rate, '0.000000')
  drafts[0].max_input_cost_rate = '9223372036854.775807'
  const operation = routeWrite(view, view.cost_policy, drafts)
  assert.equal(operation.body.bindings[0].max_input_cost_rate, '9223372036854775807')
  assert.equal(operation.body.bindings[0].max_output_cost_rate, '0')
  for (const value of [0, '01', '-1', '+1', '1e6', '9223372036854775808']) assert.throws(() => costRateToCny(value))
  for (const value of ['-1', '1.0000001', '9223372036854.775808']) assert.throws(() => routeWrite(view, view.cost_policy, [{ ...drafts[0], max_input_cost_rate: value }]))
})

test('complete route writes freeze all bindings and policy under the existing numeric CAS', () => {
  const view = parseRouteView(model), drafts = routeDrafts(view), policy = { mode: 'cost_first', unknown_cost_policy: 'deny' }
  const operation = routeWrite(view, policy, drafts)
  assert.deepEqual(operation, { path: `/platform/models/${modelID}/bindings`, method: 'PUT', body: { expected_revision: 3, cost_policy: policy, bindings: [binding] } })
  policy.mode = 'manual'; drafts[0].upstream_model = 'changed'; view.active_revision = 99
  assert.equal(operation.body.cost_policy.mode, 'cost_first')
  assert.equal(operation.body.bindings[0].upstream_model, binding.upstream_model)
  assert.equal(operation.body.expected_revision, 3)
  assert.ok(Object.isFrozen(operation.body.cost_policy) && Object.isFrozen(operation.body.bindings[0]))
  assert.throws(() => operation.body.bindings.push(binding), TypeError)
  assert.throws(() => costPolicy({ mode: 'cost_first', unknown_cost_policy: 'allow' }), /未知/)
  assert.throws(() => routeWrite(parseRouteView(model), model.cost_policy, []), /完整路由/)
  assert.throws(() => routeWrite(parseRouteView(model), model.cost_policy, [routeDrafts(parseRouteView(model))[0], routeDrafts(parseRouteView(model))[0]]), /只能绑定一次/)
  for (const invalid of [{ priority: 1.5 }, { weight: 0 }, { weight: 1000001 }, { max_inflight: 1001 }, { upstream_model: 'bad\nmodel' }]) assert.throws(() => routeWrite(parseRouteView(model), model.cost_policy, [{ ...routeDrafts(parseRouteView(model))[0], ...invalid }]))
})

test('route success validates precisely the next revision and treats malformed success as unknown', () => {
  const operation = routeWrite(parseRouteView(model), model.cost_policy, routeDrafts(parseRouteView(model)))
  assert.doesNotThrow(() => acceptRouteWrite(operation, { revision: 4 }))
  for (const value of [{}, { revision: 3 }, { revision: 5 }, { revision: '4' }]) assert.throws(() => acceptRouteWrite(operation, value))
  assert.equal(unknownCostWriteResult(new Error('malformed success')), true)
})

test('cost publish captures channel CAS as int64 string and uses database immediate time', () => {
  const operation = costWrite(channel, draft)
  assert.deepEqual(operation.body, { channel_id: channelID, expected_channel_revision: channel.management_revision, upstream_model: draft.upstream_model, basis: 'contract', input_rate: '9007199254740993', output_rate: '0', effective_at: null })
  assert.ok(Object.isFrozen(operation.body))
  const result = acceptCostWrite(operation, { id: 'cost-one', channel_revision: '9007199254740994' })
  assert.equal(result.channel_revision, '9007199254740994')
  for (const value of [{ id: 'cost-one', channel_revision: '9007199254740993' }, { id: 'cost-one', channel_revision: 9007199254740994 }, { channel_revision: '9007199254740994' }]) assert.throws(() => acceptCostWrite(operation, value))
  for (const value of [1, '0', '01', '9223372036854775808']) assert.throws(() => costWrite({ ...channel, management_revision: value }, draft))
  for (const invalid of [{ input_rate: '' }, { output_rate: '' }, { basis: 'guess' }, { effective_at: 'tomorrow' }, { effective_at: '10000-01-01T00:00:00Z' }]) assert.throws(() => costWrite(channel, { ...draft, ...invalid }))
  assert.equal(costWrite(channel, { ...draft, effective_at: '2026-11-01T00:00:00Z' }).body.effective_at, '2026-11-01T00:00:00.000Z')
})

test('unknown quote publishes null even after known prices were typed; metadata never retains secrets', () => {
  const operation = costWrite(channel, { ...draft, basis: 'unknown' })
  assert.equal(operation.body.input_rate, null); assert.equal(operation.body.output_rate, null)
  const parsed = parseCostChannel({ ...channel, secret: 'secret-one', base_url: 'private-url' })
  assert.deepEqual(parsed, channel)
  const cost = parseCostVersion({ id: 'version-one', channel_id: channelID, channel_revision: '9007199254740994', upstream_model: draft.upstream_model, basis: 'unknown', input_rate: null, output_rate: null, effective_at: '2026-11-01T00:00:00Z', secret: 'secret-two' })
  assert.equal(cost.input_rate, null)
  assert.equal(parseCostVersion({ ...cost, basis: 'contract', input_rate: '1', output_rate: null }).output_rate, null, 'legacy incomplete quote remains readable, never a known-free output price')
  assert.doesNotMatch(JSON.stringify([parsed, cost]), /secret|private-url/)
})

test('actual Vue ref and nested reactive metadata can enter the journal without DataCloneError', () => {
  const view = ref(parseCostChannel(channel)), operation = reactive(costWrite(view.value, draft)), entries = journal()
  assert.doesNotThrow(() => entries.begin(actor, channelID, view.value, operation))
  view.value.name = 'late mutation'
  assert.equal(entries.get(actor, channelID).view.name, channel.name)
  const routeEntries = createCostWriteJournal(v => BigInt(v.active_revision), (a, b) => JSON.stringify(a) === JSON.stringify(b)), route = ref(parseRouteView(model))
  const routeOperation = routeWrite(route.value, route.value.cost_policy, routeDrafts(route.value))
  routeEntries.begin(actor, modelID, route.value, routeOperation)
  route.value.bindings[0].weight = 99
  assert.equal(routeEntries.get(actor, modelID).view.bindings[0].weight, 2)
})

test('journal blocks duplicate submit, conflict and unknown until read-only observation plus explicit resume', () => {
  for (const failure of [new ApiError('revision_conflict', 'conflict', '', 409), new ApiError('network_error', 'unknown', '', 0), new ApiError('invalid_response', 'unknown', '', 200), new ApiError('unavailable', 'unknown', '', 503), new ApiError('timeout', 'unknown', '', 408), new Error('bad receipt')]) {
    const entries = journal(), operation = costWrite(channel, draft)
    entries.begin(actor, channelID, channel, operation)
    assert.throws(() => entries.begin(actor, channelID, channel, operation), /已有/)
    assert.throws(() => entries.resume(actor, channelID), /只读/)
    entries.fail(actor, channelID, failure)
    assert.equal(entries.get(actor, channelID).mode, failure.status === 409 ? 'conflict' : 'unknown')
    assert.throws(() => entries.begin(actor, channelID, channel, operation), /核查/)
    const latest = { ...channel, management_revision: '9007199254740995' }
    const observed = entries.observe(actor, channelID, latest)
    assert.equal(observed.mode, failure.status === 409 ? 'conflict' : 'unknown')
    assert.equal(observed.submitted.body.expected_channel_revision, channel.management_revision)
    assert.throws(() => entries.begin(actor, channelID, latest, operation), /已有/)
    const resumed = entries.resume(actor, channelID)
    assert.equal(resumed.view.management_revision, latest.management_revision)
    assert.equal(resumed.previousUnknown, failure.status !== 409)
    assert.equal(entries.hadUnknown(actor, channelID), failure.status !== 409)
    assert.equal(costWrite(resumed.view, draft).body.expected_channel_revision, latest.management_revision)
  }
})

test('observation cannot regress or alter a snapshot at the same revision and reads cannot resolve writes', () => {
  const entries = journal(), operation = costWrite(channel, draft)
  entries.begin(actor, channelID, channel, operation); entries.fail(actor, channelID, new Error('unknown'))
  assert.throws(() => entries.observe(actor, channelID, { ...channel, management_revision: '9007199254740992' }), /版本/)
  assert.throws(() => entries.observe(actor, channelID, { ...channel, name: 'different' }), /版本/)
  const unchanged = entries.observe(actor, channelID, channel)
  assert.equal(unchanged.mode, 'unknown')
  assert.deepEqual(unchanged.observed, channel)
  entries.resume(actor, channelID)
  assert.equal(entries.hadUnknown(actor, channelID), true, 'uncertainty notice survives closing and beginning a new edit')
})

test('known 4xx rejection permits corrected edit; identities and targets isolate the tab-local journal', () => {
  for (const status of [400, 401, 403, 404, 422, 429]) {
    const entries = journal(), operation = costWrite(channel, draft)
    entries.begin(actor, channelID, channel, operation)
    entries.fail(actor, channelID, new ApiError('rejected', 'rejected', '', status))
    assert.equal(entries.get(actor, channelID), null)
    entries.begin(actor, channelID, channel, operation)
    entries.saved(actor, channelID)
    assert.equal(entries.get(actor, channelID).mode, 'saved')
  }
  const entries = journal(); entries.begin(actor, channelID, channel, costWrite(channel, draft))
  assert.equal(entries.get(keyAccessContext('admin-two', 'tenant-one', 'admin'), channelID), null)
  assert.equal(entries.get(keyAccessContext('admin-one', 'tenant-two', 'admin'), channelID), null)
  assert.equal(entries.get(keyAccessContext('admin-one', 'tenant-one', 'finance_operator'), channelID), null)
  assert.equal(entries.get(actor, 'another-channel'), null)
})

test('close, page and identity epochs reject late observations and mutations; journal still records original outcome', async () => {
  const scope = createKeyAccessScope(), entries = journal()
  scope.reset(actor); const write = scope.ticket('write'), operation = costWrite(channel, draft)
  entries.begin(actor, channelID, channel, operation)
  let release; const response = new Promise(resolve => { release = resolve }); let displayed = ''
  const late = (async () => { await response; entries.fail(actor, channelID, new Error('lost response')); if (scope.accepts(write)) displayed = 'must-not-restore' })()
  scope.reset(actor); release(); await late
  assert.equal(displayed, '')
  assert.equal(entries.get(actor, channelID).mode, 'unknown')
  for (const next of [actor, keyAccessContext('admin-two', '', 'admin'), keyAccessContext('admin-one', '', 'finance_operator')]) { const read = scope.ticket('read'); scope.reset(next); assert.equal(scope.accepts(read), false) }
})

test('configuration pagination supports 1000 models and all 50-row channel/cost pages', async () => {
  const original = globalThis.fetch, calls = []
  globalThis.fetch = async url => {
    calls.push(url)
    const data = url.includes('/models') ? Array.from({ length: 1000 }, (_, i) => ({ id: `model-${i}` })) : url.includes('cursor=') ? [{ ...channel, id: 'last-channel' }] : Array.from({ length: 50 }, (_, i) => ({ ...channel, id: `channel-${i}` }))
    return new Response(JSON.stringify({ data, request_id: 'read', next_cursor: url.includes('/models') || url.includes('cursor=') ? undefined : 'next+page' }))
  }
  try {
    assert.equal((await readConfigurationRows('/platform/models')).length, 1000)
    assert.equal((await readConfigurationRows('/platform/channels')).length, 51)
    assert.ok(calls.includes('/api/platform/channels?cursor=next%2Bpage'))
    await assert.rejects(readConfigurationRows('/platform/channels', () => false), /关闭/)
  } finally { globalThis.fetch = original }
})

test('paginated inspection rejects malformed lists and repeated cursors without partial success', async () => {
  const original = globalThis.fetch
  try {
    globalThis.fetch = async () => new Response(JSON.stringify({ data: {}, request_id: 'bad' }))
    await assert.rejects(readConfigurationRows('/platform/cost-versions'), /响应/)
    globalThis.fetch = async () => new Response(JSON.stringify({ data: [], request_id: 'bad', next_cursor: 'same' }))
    await assert.rejects(readConfigurationRows('/platform/cost-versions'), /游标/)
    globalThis.fetch = async () => new Response(JSON.stringify({ data: [], request_id: 'bad', next_cursor: 12 }))
    await assert.rejects(readConfigurationRows('/platform/cost-versions'), /游标/)
  } finally { globalThis.fetch = original }
})

test('uncertain publish performs no automatic replay and explicit inspection uses only safe GETs', async () => {
  const original = globalThis.fetch, calls = [], entries = journal(), operation = costWrite(channel, draft)
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    if (options.method === 'POST') throw new TypeError('lost connection')
    return new Response(JSON.stringify({ data: url.includes('/channels') ? [channel] : [], request_id: 'safe' }))
  }
  try {
    setCsrf('cost-csrf'); entries.begin(actor, channelID, channel, operation)
    try { await request(operation.path, operation.method, operation.body) } catch (e) { entries.fail(actor, channelID, e) }
    const current = (await readConfigurationRows('/platform/channels')).map(parseCostChannel)[0]
    await readConfigurationRows('/platform/cost-versions'); entries.observe(actor, channelID, current)
    assert.equal(entries.get(actor, channelID).mode, 'unknown')
    assert.throws(() => entries.begin(actor, channelID, current, operation))
    assert.deepEqual(calls.map(call => call.options.method), ['POST', 'GET', 'GET'])
    assert.equal(calls[0].options.headers['X-CSRF-Token'], 'cost-csrf')
    assert.equal(calls[1].options.headers['X-CSRF-Token'], undefined)
    assert.ok(calls.every(call => call.options.credentials === 'include' && !call.options.headers.Authorization && !call.options.headers['Idempotency-Key']))
  } finally { globalThis.fetch = original; setCsrf('') }
})

test('editors invalidate context and unmount while platform routes use only the dedicated cost editors', async () => {
  const [route, cost, platform] = await Promise.all(['../src/components/RouteEditor.vue', '../src/components/CostEditor.vue', '../src/views/PlatformView.vue'].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  for (const source of [route, cost]) {
    assert.match(source, /watch\(context,.*reset\(\).*false/)
    assert.match(source, /onBeforeUnmount\(reset\)/)
    assert.match(source, /scope\.accepts\(ticket\)/)
    assert.doesNotMatch(source, /localStorage|sessionStorage|indexedDB|stableOperationKey|v-html|console\./)
  }
  assert.match(platform, /watch\(\[resource, platformContext\]/)
  assert.match(platform, /<RouteEditor/); assert.match(platform, /<CostEditor/)
  assert.doesNotMatch(platform, /async function saveRoute|routeBusy|routeError/)
})
