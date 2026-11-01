import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError, request, setCsrf } from '../src/api.ts'
import { createDiscoveryScope, discoveryDetailPath, discoveryErrorID, discoveryFormat, discoveryFormats, discoveryHistoryPath, discoveryRequest, discoveryStatusText, parseDiscoveryMetadata, parseDiscoveryView } from '../src/discovery.ts'

const binding = { model_id: 'model-one', public_name: 'public-one', upstream_model: 'vendor/model-1', revision: 3, channel_version_id: 'old-channel-version', enabled: true }
const metadata = { id: 'discovery-one', channel_id: 'channel-one', channel_version_id: 'channel-version', format: 'openai', status: 'completed', created_at: '2026-10-31T00:00:00Z', completed_at: '2026-10-31T00:00:01Z', pages: 2, stale: false }
const result = { ...metadata, models: ['new-model', 'vendor/model-1'], unbound: ['new-model'], present: [binding, { ...binding, model_id: 'model-two', public_name: 'public-two', enabled: false }], missing: [{ ...binding, model_id: 'model-three', public_name: 'public-three', upstream_model: 'missing-model' }] }

test('discovery only constructs the fixed observation and history endpoints with encoded identifiers', () => {
  assert.deepEqual(discoveryFormats.map(format => format.id), ['openai', 'anthropic', 'gemini'])
  for (const format of ['openai', 'anthropic', 'gemini']) {
    assert.equal(discoveryFormat(format), format)
    assert.deepEqual(discoveryRequest('channel/one?key=x', 'current-version', format), { path: '/platform/channels/channel%2Fone%3Fkey%3Dx/discover-models', method: 'POST', body: { format, expected_version: 'current-version' } })
  }
  assert.equal(discoveryHistoryPath('channel-one', 'cursor?x=1&x=2'), '/platform/channels/channel-one/model-discoveries?cursor=cursor%3Fx%3D1%26x%3D2')
  assert.equal(discoveryDetailPath('channel-one', 'record/one'), '/platform/channels/channel-one/model-discoveries/record%2Fone')
  for (const format of ['', 'openai_chat', 'https://upstream/models', {}, null]) assert.throws(() => discoveryRequest('channel', 'version', format), /格式/)
  for (const version of ['', ' ', '\r\nversion', null]) assert.throws(() => discoveryRequest('channel', version, 'openai'), /标识/)
  assert.throws(() => discoveryHistoryPath('channel', 'a'.repeat(513)), /游标/)
})

test('discovery projection preserves all public bindings and directory-missing mappings without altering source data', () => {
  const source = structuredClone(result), original = structuredClone(source)
  const parsed = parseDiscoveryView(source)
  assert.deepEqual(parsed.models, ['new-model', 'vendor/model-1'])
  assert.deepEqual(parsed.unbound, ['new-model'])
  assert.equal(parsed.present.length, 2)
  assert.equal(parsed.present[0].upstream_model, parsed.present[1].upstream_model)
  assert.notEqual(parsed.present[0].model_id, parsed.present[1].model_id)
  assert.equal(parsed.present[1].enabled, false)
  assert.equal(parsed.missing.length, 1)
  assert.equal(parsed.missing[0].upstream_model, 'missing-model')
  assert.deepEqual(source, original)
  assert.equal('delete' in parsed, false)
  assert.equal('bindings_to_publish' in parsed, false)
})

test('discovery treats stale observations and empty complete directories as evidence for manual review', () => {
  const stale = parseDiscoveryView({ ...result, stale: true, secret: 'must-not-be-projected', provider_url: 'https://private.example' })
  assert.equal(stale.stale, true)
  assert.equal(stale.status, 'completed')
  assert.equal('secret' in stale, false)
  assert.equal('provider_url' in stale, false)
  const empty = parseDiscoveryView({ ...metadata, models: [], unbound: [], present: [], missing: [binding], pages: 1 })
  assert.deepEqual(empty.models, [])
  assert.deepEqual(empty.missing, [binding])
  const legacy = { ...binding, upstream_model: '供应商模型 / older model' }
  assert.deepEqual(parseDiscoveryView({ ...metadata, models: [], unbound: [], present: [], missing: [legacy] }).missing, [legacy])
})

test('incomplete or invalid discovery evidence cannot be displayed as a successful partial directory', () => {
  for (const status of ['running', 'interrupted', 'failed']) {
    const parsed = parseDiscoveryView({ ...metadata, status, completed_at: undefined, models: [], unbound: [], present: [], missing: [], error_code: status === 'failed' ? 'discovery_upstream_rejected' : undefined, pages: 0 })
    assert.equal(parsed.status, status)
    assert.notEqual(discoveryStatusText(status), '已完成')
    assert.throws(() => parseDiscoveryView({ ...result, status }), /未完成/)
  }
  for (const change of [{ models: ['https://private.example/model'] }, { models: ['../private'] }, { models: ['model%2fname'] }, { models: ['secret\nkey'] }, { models: ['same', 'same'] }, { models: Array.from({ length: 1001 }, (_, i) => `model-${i}`) }, { unbound: ['absent'] }, { present: [{ ...binding, upstream_model: 'absent' }] }, { missing: [binding] }, { stale: 'false' }, { pages: 11 }, { status: 'success' }]) assert.throws(() => parseDiscoveryView({ ...result, ...change }))
})

test('history metadata stays bounded and omits directory contents and supplier text', () => {
  const parsed = parseDiscoveryMetadata({ ...result, raw_error: 'private-error', APIKey: 'private-key' })
  assert.deepEqual(parsed, metadata)
  assert.equal('models' in parsed, false)
  assert.equal('raw_error' in parsed, false)
  assert.throws(() => parseDiscoveryMetadata({ ...metadata, error_code: '<script>private</script>' }))
  assert.throws(() => parseDiscoveryMetadata({ ...metadata, created_at: 'not-a-date' }))
})

test('closing or changing channels and newer selections reject previous asynchronous responses', () => {
  const scope = createDiscoveryScope()
  const firstHistory = scope.ticket('history'), firstDetail = scope.ticket('detail')
  assert.equal(scope.accepts(firstHistory), true)
  const newDetail = scope.ticket('detail')
  assert.equal(scope.accepts(firstDetail), false)
  assert.equal(scope.accepts(newDetail), true)
  assert.equal(scope.accepts(firstHistory), true)
  scope.reset() // close, channel change or new discovery
  assert.equal(scope.accepts(firstHistory), false)
  assert.equal(scope.accepts(newDetail), false)
  const reopened = scope.ticket('detail')
  assert.equal(scope.accepts(reopened), true)
  scope.reset()
  assert.equal(scope.accepts(reopened), false)
})

test('a late history refresh cannot restore A after a newer detail selection B, while an unchanged selection can refresh', async () => {
  const scope = createDiscoveryScope()
  let selected = 'A'
  scope.ticket('detail')
  let release
  const pendingHistory = new Promise(resolve => { release = resolve })
  const historyTicket = scope.ticket('history'), capturedSelection = scope.snapshot('detail'), capturedID = selected
  const lateRestore = (async () => {
    await pendingHistory
    if (scope.accepts(historyTicket) && scope.accepts(capturedSelection)) {
      scope.ticket('detail')
      selected = capturedID
    }
  })()
  selected = 'B'
  const detailB = scope.ticket('detail')
  release()
  await lateRestore
  assert.equal(selected, 'B')
  assert.equal(scope.accepts(detailB), true, 'history completion must not create a new detail request that invalidates B')
  const unchangedSelection = scope.snapshot('detail')
  scope.ticket('history')
  assert.equal(scope.accepts(unchangedSelection), true, 'history-only refresh may reload the still-selected record')
  scope.reset()
  const initialSelection = scope.snapshot('detail')
  assert.equal(scope.accepts(initialSelection), true, 'initial history may auto-select its latest record')
  scope.ticket('detail')
  assert.equal(scope.accepts(initialSelection), false, 'auto-latest must not override an explicit newer selection')
})

test('discovery uses the existing session and CSRF transport once, then read-only history without model or money writes', async () => {
  const originalFetch = globalThis.fetch, calls = []
  globalThis.fetch = async (url, options) => { calls.push({ url, options }); return new Response(JSON.stringify({ data: url.includes('discover-models') ? result : url.endsWith('/discovery-one') ? result : [metadata], request_id: 'discovery-request' })) }
  try {
    setCsrf('csrf-discovery')
    const operation = discoveryRequest('channel-one', 'channel-version', 'openai')
    const response = await request(operation.path, operation.method, operation.body)
    assert.equal(parseDiscoveryView(response.data).id, 'discovery-one')
    await request(discoveryHistoryPath('channel-one'))
    await request(discoveryDetailPath('channel-one', 'discovery-one'))
    assert.equal(calls.length, 3)
    assert.equal(calls[0].url, '/api/platform/channels/channel-one/discover-models')
    assert.equal(calls[0].options.method, 'POST')
    assert.equal(calls[0].options.headers['X-CSRF-Token'], 'csrf-discovery')
    assert.equal(calls[0].options.credentials, 'include')
    assert.deepEqual(JSON.parse(calls[0].options.body), { format: 'openai', expected_version: 'channel-version' })
    assert.equal(calls[0].options.headers.Authorization, undefined)
    assert.equal(calls[0].options.headers['x-api-key'], undefined)
    assert.deepEqual(calls.slice(1).map(call => call.options.method), ['GET', 'GET'])
    assert.ok(calls.every(call => !/\/bindings|\/models|\/credits|\/price-versions|\/refunds/.test(call.url)))
  } finally { globalThis.fetch = originalFetch; setCsrf('') }
})

test('failed discovery preserves its record identifier and never retries automatically', async () => {
  const originalFetch = globalThis.fetch; let calls = 0
  globalThis.fetch = async () => { calls++; return new Response(JSON.stringify({ error: { code: 'discovery_failed', message: '目录读取未完成' }, discovery_id: 'failed-discovery', request_id: 'failed-request' }), { status: 503 }) }
  try {
    await assert.rejects(request('/platform/channels/channel-one/discover-models', 'POST', { format: 'openai', expected_version: 'v1' }), error => error instanceof ApiError && discoveryErrorID(error) === 'failed-discovery' && error.requestId === 'failed-request')
    assert.equal(calls, 1)
    assert.equal(discoveryErrorID(new Error('network disconnected')), '')
  } finally { globalThis.fetch = originalFetch }
})
