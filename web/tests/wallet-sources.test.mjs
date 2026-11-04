import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError } from '../src/api.ts'
import { commerceContext } from '../src/commerce-state.ts'
import { money } from '../src/format.ts'
import { canReadWalletSources, createWalletSourcesReader, parseWalletSource, parseWalletSourcesPage, walletRefundLabel, walletSourceLabel, walletSourcesPath } from '../src/wallet-sources.ts'

const tenant = '11111111-1111-4111-8111-111111111111', other = '22222222-2222-4222-8222-222222222222'
const id = index => `aaaaaaaa-aaaa-4aaa-8aaa-${String(index).padStart(12, '0')}`
const source = (patch = {}) => ({ id: id(1), lot_id: id(1), source_kind: 'wallet_opening', refund_class: 'nonrefundable', issued_amount: '9007199254740993', available_amount: '9007199254740991', held_amount: '1', consumed_amount: '1', created_at: '2026-11-04T00:00:00.123456Z', ...patch })
const page = (start = 1) => Array.from({ length: 50 }, (_, i) => source({ id: id(start + i), lot_id: id(start + i) }))
const envelope = (data, next_cursor = '') => ({ data, next_cursor, request_id: 'safe-request' })
function deferred() { let resolve, reject; const promise = new Promise((accept, fail) => { resolve = accept; reject = fail }); return { promise, resolve, reject } }

test('wallet sources require tenant billing access; platform role does not grant member access', () => {
  for (const tenantRole of ['owner', 'billing_viewer']) assert.equal(canReadWalletSources({ authenticated: true, tenantID: tenant, tenantRole }), true)
  for (const tenantRole of ['member', '', 'admin']) assert.equal(canReadWalletSources({ authenticated: true, tenantID: tenant, tenantRole, platformRole: 'admin' }), false)
  for (const platformRole of ['admin', 'finance_operator', 'finance_approver']) assert.equal(canReadWalletSources({ authenticated: true, platform: true, platformRole }), true)
  for (const platformRole of ['member', 'owner', 'platform_admin', 'finance', '']) assert.equal(canReadWalletSources({ authenticated: true, platform: true, platformRole }), false)
  for (const patch of [{ authenticated: false }, { blocked: true }, { tenantID: '' }]) assert.equal(canReadWalletSources({ authenticated: true, tenantID: tenant, tenantRole: 'owner', ...patch }), false)
})

test('current tenant requests never accept a tenant query override; platform requires an explicit UUID', () => {
  assert.equal(walletSourcesPath({ tenantID: other }), '/wallet/sources')
  assert.equal(walletSourcesPath({ platform: true, tenantID: tenant }), `/platform/wallet-sources?tenant_id=${tenant}`)
  const opaque = 'a_B-c123', url = new URL(walletSourcesPath({ platform: true, tenantID: tenant }, opaque), 'https://console.test')
  assert.deepEqual([...url.searchParams], [['tenant_id', tenant], ['cursor', opaque]])
  for (const tenantID of ['', undefined, 'all', `${tenant}&tenant_id=${other}`]) assert.throws(() => walletSourcesPath({ platform: true, tenantID }))
  for (const cursor of [' leading', 'line\nbreak', 'a+/b=?tenant_id=other', 'x'.repeat(513)]) assert.throws(() => walletSourcesPath({}, cursor))
})

test('exact source amounts and buckets retain micro-yuan beyond JavaScript safe integers', () => {
  const parsed = parseWalletSource(source())
  assert.equal(parsed.issued_amount, '9007199254740993')
  assert.equal(money(parsed.issued_amount), '¥9,007,199,254.740993')
  assert.equal(money(parsed.available_amount), '¥9,007,199,254.740991')
  assert.equal(money(parsed.held_amount), '¥0.000001')
  assert.equal(money(parsed.consumed_amount), '¥0.000001')
  assert.throws(() => parseWalletSource(source({ available_amount: '9007199254740992' })), /不一致/)
  assert.throws(() => parseWalletSource(source({ issued_amount: '9223372036854775808' })))
  for (const issued_amount of [0, '0', '-1', '01', '1.0', null]) assert.throws(() => parseWalletSource(source({ issued_amount })))
})

test('source parsing rejects extra private metadata and enforces all four funding classifications', () => {
  for (const source_kind of ['manual_credit', 'voucher_grant', 'wallet_opening']) assert.equal(parseWalletSource(source({ source_kind })).refund_class, 'nonrefundable')
  assert.equal(parseWalletSource(source({ source_kind: 'sandbox_capture', refund_class: 'sandbox_original' })).refund_class, 'sandbox_original')
  for (const patch of [{ actor: 'private' }, { business_ref: 'private' }, { id: id(2) }, { source_kind: 'cash' }, { refund_class: 'cash_original' }, { source_kind: 'wallet_opening', refund_class: 'sandbox_original' }, { source_kind: 'sandbox_capture' }]) assert.throws(() => parseWalletSource(source(patch)))
  const incomplete = source(); delete incomplete.held_amount
  assert.throws(() => parseWalletSource(incomplete))
  assert.equal(walletSourceLabel('wallet_opening'), '初始余额导入')
  assert.equal(walletRefundLabel('nonrefundable'), '不可原路退款')
  assert.equal(walletRefundLabel('sandbox_original'), '仅本地支付沙箱')
})

test('UTC RFC3339 timestamps preserve supplier-independent database precision and reject normalized impossible dates', () => {
  for (const created_at of ['2026-11-04T00:00:00Z', '2026-11-04T00:00:00.1Z', '2026-11-04T00:00:00.123456789Z']) assert.equal(parseWalletSource(source({ created_at })).created_at, created_at)
  for (const created_at of ['2026-02-30T00:00:00Z', '2026-11-04T24:00:00Z', '2026-11-04T00:00:00+08:00', '2026-11-04', '2026-11-04T00:00:00.1234567890Z']) assert.throws(() => parseWalletSource(source({ created_at })))
})

test('pagination follows the current page envelope and rejects duplicates, overlong pages and inconsistent continuations', () => {
  assert.deepEqual(parseWalletSourcesPage([], ''), { rows: [], nextCursor: '' })
  for (const next of [undefined, null]) assert.throws(() => parseWalletSourcesPage([], next))
  assert.equal(parseWalletSourcesPage(page(), 'next-page').rows.length, 50)
  for (const data of [null, {}, page().concat(source()), [source(), source()], [source(), source({ id: id(2) })]]) assert.throws(() => parseWalletSourcesPage(data, ''))
  assert.throws(() => parseWalletSourcesPage([source()], 'next-page'))
})

test('next and previous commit cursor history only after a validated response', async () => {
  const paths = [], responses = [envelope(page(), 'page-two'), envelope([source({ id: id(51), lot_id: id(51) })]), envelope(page(), 'page-two')]
  const reader = createWalletSourcesReader(async path => { paths.push(path); return responses.shift() })
  reader.reset('owner')
  await reader.load({})
  assert.deepEqual(reader.snapshot().history, [])
  await reader.load({}, 'next')
  assert.equal(reader.snapshot().cursor, 'page-two')
  assert.deepEqual(reader.snapshot().history, [''])
  assert.equal(reader.snapshot().rows[0].id, id(51))
  await reader.load({}, 'previous')
  assert.equal(reader.snapshot().cursor, '')
  assert.deepEqual(reader.snapshot().history, [])
  assert.deepEqual(paths, ['/wallet/sources', '/wallet/sources?cursor=page-two', '/wallet/sources'])
})

test('a refresh clears old amounts while loading and a failure leaves no stale rows or paging data', async () => {
  const pending = deferred(); let calls = 0
  const reader = createWalletSourcesReader(async () => ++calls === 1 ? envelope([source()]) : pending.promise)
  reader.reset('current')
  await reader.load({})
  assert.equal(reader.snapshot().rows.length, 1)
  const reading = reader.load({})
  assert.equal(reader.snapshot().loading, true)
  assert.deepEqual(reader.snapshot().rows, [])
  await reader.load({}) // No duplicate read while this page is loading.
  assert.equal(calls, 2)
  pending.reject(new ApiError('wallet_sources_unavailable', '来源暂不可用', 'safe-id', 503))
  await reading
  assert.deepEqual(reader.snapshot().rows, [])
  assert.equal(reader.snapshot().nextCursor, '')
  assert.equal(reader.snapshot().loading, false)
  assert.match(reader.snapshot().error, /来源暂不可用.*safe-id/)
})

test('an invalid next page never advances history or displays partial rows', async () => {
  let calls = 0
  const reader = createWalletSourcesReader(async () => ++calls === 1 ? envelope(page(), 'page-two') : envelope([source(), source()]))
  await reader.load({})
  await reader.load({}, 'next')
  assert.deepEqual(reader.snapshot().history, [])
  assert.equal(reader.snapshot().cursor, '')
  assert.deepEqual(reader.snapshot().rows, [])
  assert.equal(reader.snapshot().nextCursor, '')
  assert.notEqual(reader.snapshot().error, '')
})

test('identity, tenant, role and route resets invalidate late values and late authentication events', async () => {
  for (const changed of ['actor', 'tenant', 'role', 'route', 'csrf', 'MFA']) {
    const old = deferred(), current = deferred(), callbacks = []
    const reader = createWalletSourcesReader(async (_path, isCurrent) => { callbacks.push(isCurrent); return callbacks.length === 1 ? old.promise : current.promise })
    reader.reset(commerceContext('actor-a', tenant, 'owner:wallet:csrf-a'))
    const first = reader.load({})
    reader.reset(commerceContext(changed, other, 'billing_viewer:new-route:csrf-b'))
    assert.equal(callbacks[0](), false, changed)
    assert.deepEqual(reader.snapshot().rows, [])
    const latest = reader.load({})
    current.resolve(envelope([source({ id: id(2), lot_id: id(2) })]))
    await latest
    old.resolve(envelope([source()]))
    await first
    assert.equal(reader.snapshot().rows[0].id, id(2), changed)
    assert.equal(callbacks[1](), true)
  }
})

test('unmount disposes pending values and errors permanently without reviving page state', async () => {
  for (const fails of [false, true]) {
    const pending = deferred(); let isCurrent
    const reader = createWalletSourcesReader(async (_path, current) => { isCurrent = current; return pending.promise })
    const reading = reader.load({})
    reader.dispose()
    assert.equal(isCurrent(), false)
    if (fails) pending.reject(new ApiError('unauthenticated', 'late private error', '', 401))
    else pending.resolve(envelope([source()]))
    await reading
    reader.reset('new-context')
    await reader.load({})
    assert.deepEqual(reader.snapshot(), { rows: [], loading: false, error: '', cursor: '', nextCursor: '', history: [] })
  }
})

test('the real request transport suppresses a late 401 event after identity changes', async () => {
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window, body = deferred(), events = []
  globalThis.window = { dispatchEvent: event => { events.push(event.type); return true } }
  globalThis.fetch = async () => ({ ok: false, status: 401, headers: new Headers(), json: () => body.promise })
  try {
    const reader = createWalletSourcesReader()
    reader.reset('actor-a')
    const reading = reader.load({})
    await Promise.resolve()
    reader.reset('actor-b')
    body.resolve({ request_id: 'old-request', error: { code: 'unauthenticated', message: '旧身份失效' } })
    await reading
    assert.deepEqual(events, [])
    assert.deepEqual(reader.snapshot().rows, [])
    assert.equal(reader.snapshot().error, '')
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow }
})

test('snapshots cannot mutate the reader and repeated cursor pages fail closed', async () => {
  let calls = 0
  const reader = createWalletSourcesReader(async () => { calls++; return envelope(page(calls * 50), 'same-cursor') })
  await reader.load({})
  const snapshot = reader.snapshot(); snapshot.rows[0].issued_amount = '0'; snapshot.history.push('bad')
  assert.equal(reader.snapshot().rows[0].issued_amount, '9007199254740993')
  assert.deepEqual(reader.snapshot().history, [])
  await reader.load({}, 'next')
  assert.match(reader.snapshot().error, /没有向前推进/)
  assert.deepEqual(reader.snapshot().rows, [])
})
