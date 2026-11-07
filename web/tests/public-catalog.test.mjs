import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { catalogTimestamp, fetchPublicCatalog, parsePublicCatalogPage, parsePublicModel, parsePublication, publicCatalogPath, publicFilters, publicProtocols, publicationLabel, publicationWrite, PublicCatalogError } from '../src/public-catalog.ts'
import { ratePerMillionText } from '../src/metering-wire.ts'

const id = '11111111-1111-4111-8111-111111111111', priceID = '22222222-2222-4222-8222-222222222222', schemaID = '33333333-3333-4333-8333-333333333333'
const rates = (input = '9007199254740993', output = '2000000') => { const reduce = (raw) => { let n = BigInt(raw), d = 1000000n; if (n === 0n) return ['0', '1']; const gcd = (a, b) => { while (b) { const next = a % b; a = b; b = next } return a }; const factor = gcd(n, d); return [String(n / factor), String(d / factor)] }; const [inN, inD] = reduce(input), [outN, outD] = reduce(output); return { schema_id: schemaID, version: '1', side: 'retail', currency: 'CNY', rates: [{ dimension: 'text_input_token', unit: 'token', numerator: inN, denominator: inD }, { dimension: 'text_output_token', unit: 'token', numerator: outN, denominator: outD }] } }
function model(protocol = 'openai_chat') {
  const inputOnly = ['openai_embeddings', 'jina_rerank'].includes(protocol)
  return { id, public_name: 'test-chat', protocol, capabilities: { streaming: !inputOnly, tools: !inputOnly && protocol !== 'gemini_generate_content', input_only: inputOnly, text_only: true }, limits: { context_tokens: 128000, input_token_upper_bound: 120000, max_output_tokens: inputOnly ? 0 : 8000, default_output_tokens: inputOnly ? 0 : 2000 }, pricing: { status: 'available', price_version_id: priceID, schema_id: schemaID, schema_digest: 'ab'.repeat(32), variant_key: 'default', currency: 'CNY', rates: rates('9007199254740993', inputOnly ? '0' : '2000000'), effective_at: '2026-11-01T00:00:00Z' } }
}
const page = data => ({ data, request_id: 'request-safe', next_cursor: '', as_of: '2026-11-01T01:00:00.000001Z' })

test('all six public protocols retain exact capability contracts and DTO key sets', () => {
  for (const item of publicProtocols) assert.deepEqual(parsePublicModel(model(item.id)), model(item.id))
  for (const field of ['profile', 'channel_id', 'upstream_model', 'secret', 'tenant_id', 'cost_rate']) assert.throws(() => parsePublicModel({ ...model(), [field]: 'never-public' }))
  assert.throws(() => parsePublicModel({ ...model(), capabilities: { ...model().capabilities, supplier: 'private' } }))
  for (const protocol of ['openai_embeddings', 'jina_rerank', 'gemini_generate_content']) assert.throws(() => parsePublicModel({ ...model(protocol), capabilities: { ...model(protocol).capabilities, tools: true } }))
  assert.throws(() => parsePublicModel({ ...model(), capabilities: { ...model().capabilities, streaming: false } }))
  assert.throws(() => parsePublicModel({ ...model(), capabilities: { ...model().capabilities, text_only: false } }))
})

test('zero is a known price and absence is an all-null price, never free', () => {
  const zero = { ...model(), pricing: { ...model().pricing, rates: rates('0', '0') } }
  assert.equal(parsePublicModel(zero).pricing.rates?.rates[0].numerator, '0')
  const unavailable = { ...model(), pricing: { status: 'unavailable', price_version_id: null, schema_id: schemaID, schema_digest: 'ab'.repeat(32), variant_key: 'default', currency: 'CNY', rates: null, effective_at: null } }
  assert.equal(parsePublicModel(unavailable).pricing.status, 'unavailable')
  for (const field of ['price_version_id', 'rates', 'effective_at']) assert.throws(() => parsePublicModel({ ...unavailable, pricing: { ...unavailable.pricing, [field]: field === 'rates' ? rates() : '0' } }))
  assert.throws(() => parsePublicModel({ ...model('jina_rerank'), pricing: { ...model('jina_rerank').pricing, rates: rates('1', '1') } }))
  for (const value of [0, -1, null, '01', '+1', '-1', '1e6', '9223372036854775808']) assert.throws(() => parsePublicModel({ ...model(), pricing: { ...model().pricing, rates: { ...rates(), rates: [{ ...rates().rates[0], numerator: value }, rates().rates[1]] } } }))
  assert.equal(ratePerMillionText(rates('0', '0'), 'text_input_token'), '¥0.000000')
  assert.equal(ratePerMillionText(rates('9007199254740993', '0'), 'text_input_token'), '¥9,007,199,254.740993')
  assert.equal(ratePerMillionText(rates('9223372036854775807', '0'), 'text_input_token'), '¥9,223,372,036,854.775807')
})

test('invalid limits, identities and dates fail closed without private fallback', () => {
  for (const patch of [{ context_tokens: 0 }, { input_token_upper_bound: 128001 }, { default_output_tokens: 0 }, { max_output_tokens: 200000 }, { context_tokens: 10000001 }, { context_tokens: '128000' }, { context_tokens: 1.5 }]) assert.throws(() => parsePublicModel({ ...model(), limits: { ...model().limits, ...patch } }))
  for (const value of ['', 'bad name', 'bad\nname', '中'.repeat(34)]) assert.throws(() => parsePublicModel({ ...model(), public_name: value }))
  for (const value of ['00000000-0000-0000-0000-000000000000', id.toUpperCase().replace('111', 'AAA'), 'arbitrary']) assert.throws(() => parsePublicModel({ ...model(), id: value }))
  for (const value of ['2026-02-30T00:00:00Z', '2026-11-01', '2026-11-01T00:00:00+08:00', '2026-11-01T00:00:00.1234567Z']) assert.throws(() => catalogTimestamp(value))
})

test('page boundaries require exact envelope, bounded rows, unique IDs and per-page DB time', () => {
  assert.deepEqual(parsePublicCatalogPage(page([])), page([]))
  assert.throws(() => parsePublicCatalogPage({ ...page([]), tenant: 'private' }))
  assert.throws(() => parsePublicCatalogPage({ ...page([]), as_of: undefined }))
  assert.throws(() => parsePublicCatalogPage(page([model(), model()])))
  assert.throws(() => parsePublicCatalogPage(page(Array(51).fill(model()))))
  assert.throws(() => parsePublicCatalogPage({ ...page([]), next_cursor: 'padding=' }))
  assert.throws(() => parsePublicCatalogPage({ ...page([model()]), as_of: '2026-10-31T23:59:59Z' }))
  const microFuture = { ...model(), pricing: { ...model().pricing, effective_at: '2026-11-01T01:00:00.000002Z' } }
  assert.throws(() => parsePublicCatalogPage(page([microFuture])))
})

test('filter construction preserves literal prefix characters and never sends empty cursor', () => {
  const filters = publicFilters('  a_%\\模型  ', 'openai_chat')
  const url = new URL(publicCatalogPath(filters, 'abc_DEF-12'), 'https://local.test')
  assert.equal(url.pathname, '/api/public/models')
  assert.equal(url.searchParams.get('q'), 'a_%\\模型')
  assert.equal(url.searchParams.get('protocol'), 'openai_chat')
  assert.equal(url.searchParams.get('cursor'), 'abc_DEF-12')
  assert.equal(publicCatalogPath(publicFilters(' ', '')), '/api/public/models')
  for (const value of ['a\nkey', '中'.repeat(34)]) assert.throws(() => publicFilters(value, ''))
  assert.throws(() => publicFilters('', 'unknown'))
})

test('anonymous transport omits credentials and private session/reauth side effects even for 401', async () => {
  const previousFetch = globalThis.fetch, previousWindow = globalThis.window, events = []
  globalThis.window = { dispatchEvent: event => events.push(event.type) }
  try {
    let init
    globalThis.fetch = async (path, options) => { init = options; assert.equal(path, '/api/public/models'); return { ok: true, status: 200, json: async () => page([model()]) } }
    assert.equal((await fetchPublicCatalog({ q: '', protocol: '' })).data[0].pricing.rates?.rates[0].numerator, '9007199254740993')
    assert.equal(init.credentials, 'omit'); assert.equal(init.cache, 'no-store'); assert.equal(init.redirect, 'error')
    assert.deepEqual(init.headers, { Accept: 'application/json' })
    for (const status of [401, 403, 429, 503]) {
      globalThis.fetch = async () => ({ ok: false, status, json: async () => ({ error: { code: 'reauth_required', message: 'private-secret-canary' }, request_id: 'request-safe' }) })
      await assert.rejects(fetchPublicCatalog({ q: '', protocol: '' }), error => error instanceof PublicCatalogError && !error.message.includes('private-secret-canary'))
    }
    assert.deepEqual(events, [])
  } finally { globalThis.fetch = previousFetch; globalThis.window = previousWindow }
})

test('anonymous malformed success and network failures remain safe directory errors', async () => {
  const original = globalThis.fetch
  try {
    globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => ({ ...page([]), profile: 'secret' }) })
    await assert.rejects(fetchPublicCatalog({ q: '', protocol: '' }), error => error.code === 'invalid_response')
    globalThis.fetch = async () => { throw new Error('provider-secret') }
    await assert.rejects(fetchPublicCatalog({ q: '', protocol: '' }), error => error.code === 'network_error' && !error.message.includes('provider-secret'))
  } finally { globalThis.fetch = original }
})

test('publication DTO and full CAS body keep independent int64 revision exact', () => {
  const view = { id, public_name: 'test-chat', enabled: false, public_visible: true, public_revision: '9007199254740993' }
  assert.deepEqual(parsePublication(view), view)
  const operation = publicationWrite(view, false)
  assert.deepEqual(operation, { path: `/platform/models/${id}/publication`, method: 'PUT', body: { expected_revision: '9007199254740993', public_visible: false } })
  assert.ok(Object.isFrozen(operation.body))
  assert.equal(publicationLabel(view), '已设公开 · 停用隐藏')
  assert.equal(publicationLabel({ enabled: true, public_visible: true }), '已公开')
  assert.equal(publicationLabel({ enabled: true, public_visible: false }), '私有')
  for (const revision of ['0', '01', 1, '9223372036854775808']) assert.throws(() => parsePublication({ ...view, public_revision: revision }))
  assert.throws(() => parsePublication({ ...view, profile: {} }))
  assert.throws(() => publicationWrite(view, 'true'))
})

test('public route bypass precedes session load and layout is independent of private navigation', async () => {
  const router = await readFile(new URL('../src/router.ts', import.meta.url), 'utf8'), app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8'), login = await readFile(new URL('../src/views/LoginView.vue', import.meta.url), 'utf8')
  const guard = router.slice(router.indexOf('router.beforeEach'))
  assert.ok(guard.indexOf('isAnonymousLayout(to.meta.publicLayout)') < guard.indexOf('await loadMe()'))
  assert.match(app, /v-if="isAnonymousLayout\(route.meta.publicLayout\)" class="public-shell"/)
  assert.match(login, /to="\/pricing"/)
  const publicLayout = app.slice(app.indexOf('<div v-if="isAnonymousLayout'), app.indexOf('<div v-else-if'))
  assert.doesNotMatch(publicLayout, /session\.me|platformLinks|securityOpen|tenant-switch/)
})
