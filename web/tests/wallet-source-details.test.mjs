import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError } from '../src/api.ts'
import { commerceContext } from '../src/commerce-state.ts'
import { money } from '../src/format.ts'
import { createRequestFundingReader, createWalletSourceAuditReader, parseRequestFunding, parseRequestFundingPage, parseWalletSourceCheck, requestFundingPath, walletSourceAuditPath } from '../src/wallet-source-details.ts'

const tenant = '11111111-1111-4111-8111-111111111111', other = '22222222-2222-4222-8222-222222222222'
const id = index => `aaaaaaaa-aaaa-4aaa-8aaa-${String(index).padStart(12, '0')}`
const source = (patch = {}) => ({ id: id(1), lot_id: id(1), source_kind: 'wallet_opening', refund_class: 'nonrefundable', created_at: '2026-11-04T00:00:00.123456Z', allocation_index: '1', reserved_amount: '9007199254740993', charged_amount: '9007199254740991', released_amount: '2', refunded_amount: '1', ...patch })
const page = (start = 1) => Array.from({ length: 50 }, (_, i) => source({ id: id(start + i), lot_id: id(start + i), allocation_index: String(start + i) }))
const check = (patch = {}) => ({ tenant_id: tenant, as_of: '2026-11-04T00:00:00.123456789Z', balanced: true, source_mismatches: 0, event_mismatches: 0, allocation_mismatches: 0, wallet_mismatches: 0, ...patch })
const envelope = (data, next_cursor = '') => ({ data, next_cursor, request_id: 'safe-request' })
function deferred() { let resolve, reject; const promise = new Promise((accept, fail) => { resolve = accept; reject = fail }); return { promise, resolve, reject } }

test('request funding uses only the current request and audit requires one explicit canonical tenant', () => {
  assert.equal(requestFundingPath(id(2)), `/requests/${id(2)}/funding`)
  assert.equal(requestFundingPath(id(2), 'page-two'), `/requests/${id(2)}/funding?cursor=page-two`)
  assert.equal(walletSourceAuditPath(tenant), `/platform/wallet-sources/check?tenant_id=${tenant}`)
  for (const value of ['', undefined, 'all', '00000000-0000-0000-0000-000000000000', `${tenant}&tenant_id=${other}`]) {
    assert.throws(() => requestFundingPath(value)); assert.throws(() => walletSourceAuditPath(value))
  }
  for (const cursor of ['?tenant_id=other', ' leading', 'line\nbreak', 'x'.repeat(513)]) assert.throws(() => requestFundingPath(id(2), cursor))
})

test('funding amounts preserve exact micro-yuan and allocation sequence beyond safe integers', () => {
  const parsed = parseRequestFunding(source({ allocation_index: '9007199254740993' }))
  assert.equal(parsed.allocation_index, '9007199254740993')
  assert.equal(money(parsed.reserved_amount), '¥9,007,199,254.740993')
  assert.equal(money(parsed.charged_amount), '¥9,007,199,254.740991')
  assert.equal(money(parsed.released_amount), '¥0.000002')
  assert.throws(() => parseRequestFunding(source({ released_amount: '1' })), /不一致/)
  assert.throws(() => parseRequestFunding(source({ refunded_amount: '9007199254740992' })), /不一致/)
  for (const field of ['allocation_index', 'reserved_amount']) for (const value of ['0', '-1', '01', '1.0', 1, null, '9223372036854775808']) assert.throws(() => parseRequestFunding(source({ [field]: value })))
})

test('pending allocations keep both settlement fields null and cannot claim a refund', () => {
  const pending = source({ charged_amount: null, released_amount: null, refunded_amount: '0' })
  assert.equal(parseRequestFunding(pending).charged_amount, null)
  assert.equal(parseRequestFunding(pending).released_amount, null)
  for (const patch of [{ charged_amount: '0' }, { released_amount: '0' }, { refunded_amount: '1' }]) assert.throws(() => parseRequestFunding({ ...pending, ...patch }))
  const released = parseRequestFunding(source({ charged_amount: '0', released_amount: '9007199254740993', refunded_amount: '0' }))
  assert.equal(released.charged_amount, '0')
})

test('funding source classification, exact DTO fields and original lot identity are enforced', () => {
  for (const source_kind of ['manual_credit', 'voucher_grant', 'wallet_opening']) assert.equal(parseRequestFunding(source({ source_kind })).refund_class, 'nonrefundable')
  assert.equal(parseRequestFunding(source({ source_kind: 'sandbox_capture', refund_class: 'sandbox_original' })).refund_class, 'sandbox_original')
  for (const patch of [{ actor: 'private' }, { source_id: 'private' }, { id: id(2) }, { source_kind: 'cash' }, { source_kind: 'sandbox_capture' }, { refund_class: 'cash_original' }, { refund_class: 'sandbox_original' }]) assert.throws(() => parseRequestFunding(source(patch)))
  const missing = source(); delete missing.charged_amount; assert.throws(() => parseRequestFunding(missing))
})

test('UTC timestamps retain RFC3339 precision and reject normalized invalid dates', () => {
  for (const value of ['2026-11-04T00:00:00Z', '2026-11-04T00:00:00.1Z', '2026-11-04T00:00:00.123456789Z']) {
    assert.equal(parseRequestFunding(source({ created_at: value })).created_at, value)
    assert.equal(parseWalletSourceCheck(check({ as_of: value }), tenant).as_of, value)
  }
  for (const value of ['2026-02-30T00:00:00Z', '2026-11-04T24:00:00Z', '2026-11-04T00:00:00+08:00', '2026-11-04T00:00:00.1234567890Z']) {
    assert.throws(() => parseRequestFunding(source({ created_at: value })))
    assert.throws(() => parseWalletSourceCheck(check({ as_of: value }), tenant))
  }
})

test('funding page preserves server order and accepts empty zero-price allocations', () => {
  assert.deepEqual(parseRequestFundingPage([], ''), { rows: [], nextCursor: '' })
  const descending = [source({ id: id(2), lot_id: id(2), allocation_index: '2' }), source()]
  assert.deepEqual(parseRequestFundingPage(descending, '').rows.map(row => row.allocation_index), ['2', '1'])
  assert.equal(parseRequestFundingPage(page(), 'next-page').rows.length, 50)
  for (const data of [null, {}, page().concat(source()), [source(), source()], [source(), source({ id: id(2), lot_id: id(2) })]]) assert.throws(() => parseRequestFundingPage(data, ''))
  for (const next of [undefined, null, 'next-page']) assert.throws(() => parseRequestFundingPage([], next))
})

test('audit results bind the explicit tenant and expose only coherent safe counts', () => {
  assert.equal(parseWalletSourceCheck(check(), tenant).balanced, true)
  assert.equal(parseWalletSourceCheck(check({ balanced: false, source_mismatches: 2, event_mismatches: 3 }), tenant).event_mismatches, 3)
  assert.throws(() => parseWalletSourceCheck(check(), other), /选定客户/)
  for (const patch of [{ balanced: false }, { balanced: true, wallet_mismatches: 1 }, { balanced: 'true' }, { internal_error: 'secret' }, { source_mismatches: '1' }, { event_mismatches: -1 }, { allocation_mismatches: 0.5 }, { wallet_mismatches: Number.MAX_SAFE_INTEGER + 1 }, { source_mismatches: NaN }]) assert.throws(() => parseWalletSourceCheck(check(patch), tenant))
})

test('funding pagination advances only after a validated response and retains page sequence', async () => {
  const paths = [], responses = [envelope(page(), 'page-two'), envelope([source({ id: id(51), lot_id: id(51), allocation_index: '51' })]), envelope(page(), 'page-two')]
  const reader = createRequestFundingReader(async path => { paths.push(path); return responses.shift() })
  await reader.load(id(9)); await reader.load(id(9), 'next')
  assert.equal(reader.snapshot().cursor, 'page-two'); assert.deepEqual(reader.snapshot().history, [''])
  await reader.load(id(9), 'previous')
  assert.deepEqual(reader.snapshot().history, [])
  assert.deepEqual(paths, [`/requests/${id(9)}/funding`, `/requests/${id(9)}/funding?cursor=page-two`, `/requests/${id(9)}/funding`])
  let calls = 0
  const broken = createRequestFundingReader(async () => ++calls === 1 ? envelope(page(), 'next') : envelope([source(), source()]))
  await broken.load(id(9)); await broken.load(id(9), 'next')
  assert.equal(broken.snapshot().cursor, ''); assert.deepEqual(broken.snapshot().history, []); assert.deepEqual(broken.snapshot().rows, []); assert.notEqual(broken.snapshot().error, '')
})

test('refresh clears previous financial data while loading and failures leave no success state', async () => {
  for (const type of ['funding', 'audit']) {
    const pending = deferred(); let calls = 0
    const reader = type === 'funding' ? createRequestFundingReader(async () => ++calls === 1 ? envelope([source()]) : pending.promise) : createWalletSourceAuditReader(async () => ++calls === 1 ? envelope(check()) : pending.promise)
    const target = type === 'funding' ? id(9) : tenant
    await reader.load(target)
    const reading = reader.load(target)
    assert.equal(reader.snapshot().loading, true)
    assert.deepEqual(type === 'funding' ? reader.snapshot().rows : reader.snapshot().result, type === 'funding' ? [] : null)
    await reader.load(target); assert.equal(calls, 2)
    pending.reject(new ApiError('wallet_sources_unavailable', '资金来源暂不可用', 'safe-id', 503)); await reading
    assert.equal(reader.snapshot().loading, false); assert.match(reader.snapshot().error, /资金来源暂不可用.*safe-id/)
    assert.deepEqual(type === 'funding' ? reader.snapshot().rows : reader.snapshot().result, type === 'funding' ? [] : null)
  }
})

test('identity, role, route, CSRF and MFA resets suppress old success and authentication callbacks', async () => {
  for (const type of ['funding', 'audit']) for (const changed of ['actor', 'tenant', 'role', 'route', 'csrf', 'MFA']) {
    const old = deferred(), current = deferred(), callbacks = []
    const transport = async (_path, isCurrent) => { callbacks.push(isCurrent); return callbacks.length === 1 ? old.promise : current.promise }
    const reader = type === 'funding' ? createRequestFundingReader(transport) : createWalletSourceAuditReader(transport)
    const target = type === 'funding' ? id(9) : tenant
    reader.reset(commerceContext('actor-a', tenant, 'old'))
    const first = reader.load(target)
    reader.reset(commerceContext(changed, other, 'new'))
    assert.equal(callbacks[0](), false)
    const latest = reader.load(target)
    current.resolve(envelope(type === 'funding' ? [source({ allocation_index: '2' })] : check({ balanced: false, event_mismatches: 2 }))); await latest
    old.resolve(envelope(type === 'funding' ? [source()] : check())); await first
    assert.equal(type === 'funding' ? reader.snapshot().rows[0].allocation_index : reader.snapshot().result.event_mismatches, type === 'funding' ? '2' : 2)
    assert.equal(callbacks[1](), true)
  }
})

test('direct target switches invalidate an in-flight request even without an explicit reset', async () => {
  for (const type of ['funding', 'audit']) {
    const old = deferred(), callbacks = []
    const transport = async (_path, isCurrent) => { callbacks.push(isCurrent); return callbacks.length === 1 ? old.promise : envelope(type === 'funding' ? [] : check({ tenant_id: other })) }
    const reader = type === 'funding' ? createRequestFundingReader(transport) : createWalletSourceAuditReader(transport)
    const reading = reader.load(type === 'funding' ? id(9) : tenant)
    await reader.load(type === 'funding' ? id(10) : other)
    assert.equal(callbacks[0](), false)
    old.reject(new ApiError('unauthenticated', 'old identity', '', 401)); await reading
    assert.equal(reader.snapshot().error, '')
    if (type === 'audit') assert.equal(reader.snapshot().result.tenant_id, other)
  }
})

test('disposed readers cannot revive data and snapshots cannot mutate financial state', async () => {
  for (const type of ['funding', 'audit']) for (const fails of [false, true]) {
    const pending = deferred(); let isCurrent
    const reader = type === 'funding' ? createRequestFundingReader(async (_path, current) => { isCurrent = current; return pending.promise }) : createWalletSourceAuditReader(async (_path, current) => { isCurrent = current; return pending.promise })
    const reading = reader.load(type === 'funding' ? id(9) : tenant)
    reader.dispose(); assert.equal(isCurrent(), false)
    if (fails) pending.reject(new ApiError('unauthenticated', 'private old error', '', 401)); else pending.resolve(envelope(type === 'funding' ? [source()] : check()))
    await reading; reader.reset('new'); await reader.load(type === 'funding' ? id(9) : tenant)
    assert.equal(reader.snapshot().loading, false); assert.equal(reader.snapshot().error, '')
    assert.deepEqual(type === 'funding' ? reader.snapshot().rows : reader.snapshot().result, type === 'funding' ? [] : null)
  }
  const funding = createRequestFundingReader(async () => envelope([source()]))
  await funding.load(id(9)); const snap = funding.snapshot(); snap.rows[0].reserved_amount = '0'; snap.history.push('fake')
  assert.equal(funding.snapshot().rows[0].reserved_amount, '9007199254740993'); assert.deepEqual(funding.snapshot().history, [])
  const audit = createWalletSourceAuditReader(async () => envelope(check()))
  await audit.load(tenant); audit.snapshot().result.balanced = false; assert.equal(audit.snapshot().result.balanced, true)
})

test('real transport suppresses late unauthenticated events for both financial readers', async () => {
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window, events = []
  globalThis.window = { dispatchEvent: event => { events.push(event.type); return true } }
  try {
    for (const type of ['funding', 'audit']) {
      const body = deferred()
      globalThis.fetch = async () => ({ ok: false, status: 401, headers: new Headers(), json: () => body.promise })
      const reader = type === 'funding' ? createRequestFundingReader() : createWalletSourceAuditReader()
      reader.reset('old'); const reading = reader.load(type === 'funding' ? id(9) : tenant)
      await Promise.resolve(); reader.reset('new')
      body.resolve({ request_id: 'old-request', error: { code: 'unauthenticated', message: '旧身份失效' } }); await reading
      assert.equal(reader.snapshot().error, '')
    }
    assert.deepEqual(events, [])
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow }
})

test('invalid targets and nonadvancing pages fail without partial rows or false audit success', async () => {
  let calls = 0
  const funding = createRequestFundingReader(async () => { calls++; return envelope(page(calls * 50), 'same') })
  await funding.load('not-a-request'); assert.equal(calls, 0); assert.notEqual(funding.snapshot().error, '')
  await funding.load(id(9)); await funding.load(id(9), 'next')
  assert.match(funding.snapshot().error, /没有向前推进/); assert.deepEqual(funding.snapshot().rows, [])
  const audit = createWalletSourceAuditReader(async () => envelope(check({ tenant_id: other })))
  await audit.load(tenant); assert.equal(audit.snapshot().result, null); assert.match(audit.snapshot().error, /选定客户/)
})
