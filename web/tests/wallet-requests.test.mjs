import assert from 'node:assert/strict'
import test from 'node:test'
import { parseWalletRequest, parseWalletRequestPage, walletRequestLabel, walletRequestStateLabel, walletRequestsPath } from '../src/wallet-requests.ts'

const tenant = '11111111-1111-4111-8111-111111111111', other = '22222222-2222-4222-8222-222222222222'
const id = index => `aaaaaaaa-aaaa-4aaa-8aaa-${String(index).padStart(12, '0')}`
const request = (patch = {}) => ({ id: id(1), tenant_id: tenant, kind: 'topup', requested_by: id(2), note: 'bank reference', state: 'open', linked_order_id: null, created_at: '2026-11-14T01:02:03.123456Z', ...patch })
const page = () => Array.from({ length: 50 }, (_, index) => request({ id: id(index + 1) }))

test('wallet request paths accept only an opaque cursor', () => {
  assert.equal(walletRequestsPath(), '/wallet/requests')
  assert.equal(walletRequestsPath('a_B-c'), '/wallet/requests?cursor=a_B-c')
  for (const value of ['?tenant_id=other', 'x'.repeat(513), 'bad+cursor']) assert.throws(() => walletRequestsPath(value))
})

test('wallet request DTO binds tenant, note, state and linked order without private metadata', () => {
  assert.equal(parseWalletRequest(request(), tenant).state, 'open')
  assert.equal(parseWalletRequest(request({ state: 'linked', linked_order_id: id(3) }), tenant).linked_order_id, id(3))
  assert.equal(parseWalletRequest(request({ state: 'closed' }), tenant).linked_order_id, null)
  assert.equal(parseWalletRequest(request({ created_at: '2026-11-14T01:02:03Z' }), tenant).created_at, '2026-11-14T01:02:03Z')
  assert.equal(walletRequestLabel('cash_refund'), '现金退款申请')
  assert.equal(walletRequestStateLabel('linked'), '已关联，待审批')
  for (const patch of [{ tenant_id: other }, { kind: 'refund' }, { state: 'approved' }, { state: 'linked' }, { state: 'open', linked_order_id: id(3) }, { note: '' }, { note: ' padded ' }, { note: 'line\nbreak' }, { requested_by: 'private' }, { created_at: '2026-02-30T00:00:00Z' }, { secret: 'private' }]) assert.throws(() => parseWalletRequest(request(patch), tenant))
})

test('wallet request pages reject duplicates, invalid cursors and incomplete next pages', () => {
  assert.deepEqual(parseWalletRequestPage([], '', tenant), { rows: [], next: '' })
  assert.equal(parseWalletRequestPage(page(), 'next', tenant).rows.length, 50)
  for (const data of [null, {}, page().concat(request()), [request(), request()]]) assert.throws(() => parseWalletRequestPage(data, '', tenant))
  assert.throws(() => parseWalletRequestPage([request()], 'next', tenant))
  assert.throws(() => parseWalletRequestPage([], null, tenant))
})
