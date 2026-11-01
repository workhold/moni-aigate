import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError } from '../src/api.ts'
import { commerceContext, commerceDeadline, createCommerceDeliveryGate, createCommerceJournal, createCommerceScope, unknownCommerceWrite } from '../src/commerce-state.ts'

const actor = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', other = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', tenant = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', operation = '11111111-1111-4111-8111-111111111111', nextOperation = '22222222-2222-4222-8222-222222222222', resource = '33333333-3333-4333-8333-333333333333', at = '2026-11-01T00:00:00.000001Z'
const command = (patch = {}) => ({ operation_id: operation, kind: 'voucher.redeem', scope: tenant, resource_id: null, created_at: at, ...patch })
const receipt = (patch = {}) => ({ operation_id: operation, kind: 'voucher.redeem', scope: tenant, actor, status: 'accepted', reason: 'accepted', resource_id: resource, amount: '9007199254740993', funding_receipt_id: resource, journal_id: resource, created_at: at, ...patch })
function memory() { const values = new Map(); return { values, getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) } }

test('safe journal persists only five metadata fields across reload; secret body and result are never retained', () => {
  const adapter = memory(), first = createCommerceJournal(() => adapter)
  first.begin(actor, { ...command(), body: { code: 'do-not-retain', reason: 'raw-private-body' }, result: { secret: 'never' }, actor })
  const serialized = [...adapter.values.values()].join('')
  assert.doesNotMatch(serialized, /do-not-retain|raw-private-body|secret|body|result/)
  assert.deepEqual(Object.keys(JSON.parse(serialized)[0]).sort(), ['created_at','kind','operation_id','resource_id','scope'])
  const reloaded = createCommerceJournal(() => adapter)
  assert.deepEqual(reloaded.list(actor, tenant), [command()])
  assert.throws(() => reloaded.begin(actor, command({ operation_id: nextOperation })), /待核查/)
  assert.deepEqual(reloaded.list(other, tenant), [])
  assert.deepEqual(reloaded.list(actor, 'platform'), [])
})

test('only a matching known receipt resolves unknown; 404/read failure and identical policy do not unlock', () => {
  const adapter = memory(), journal = createCommerceJournal(() => adapter), original = journal.begin(actor, command())
  for (const patch of [{ actor: other }, { scope: other }, { operation_id: nextOperation }, { kind: 'voucher.batch.approve' }, { raw_code: 'private' }]) assert.throws(() => journal.settle(actor, original, receipt(patch)))
  journal.definiteFailure(actor, original, new ApiError('commerce_outcome_unknown', 'unknown', '', 503))
  assert.equal(journal.list(actor, tenant).length, 1)
  assert.throws(() => journal.begin(actor, command({ operation_id: nextOperation })))
  assert.equal(journal.settle(actor, original, receipt()).status, 'accepted')
  assert.equal(journal.list(actor, tenant).length, 0)
  assert.doesNotThrow(() => journal.begin(actor, command({ operation_id: nextOperation })))
})

test('terminal rejected receipt releases protection without reporting money or a resource', () => {
  const journal = createCommerceJournal(() => memoryAdapter), memoryAdapter = memory(), original = journal.begin(actor, command())
  const rejected = receipt({ status: 'rejected', reason: 'voucher_unavailable', resource_id: null, amount: null, funding_receipt_id: null, journal_id: null })
  assert.equal(journal.settle(actor, original, rejected).status, 'rejected')
  assert.deepEqual(journal.list(actor, tenant), [])
})

test('target-level protection spans different batch actions and validates original receipt resource', () => {
  const adapter = memory(), journal = createCommerceJournal(() => adapter), original = journal.begin(actor, command({ kind: 'voucher.batch.approve', scope: 'platform', resource_id: resource }))
  assert.throws(() => journal.begin(actor, command({ kind: 'voucher.batch.void', scope: 'platform', resource_id: resource, operation_id: nextOperation })))
  assert.throws(() => journal.settle(actor, original, receipt({ kind: 'voucher.batch.approve', scope: 'platform', amount: null, funding_receipt_id: null, journal_id: null, resource_id: nextOperation })))
  journal.definiteFailure(actor, original, new ApiError('reauth_required', 'reauth', '', 403))
  assert.deepEqual(journal.list(actor, 'platform'), [])
})

test('storage unavailable or malformed fails closed before a new write can begin', () => {
  const blocked = createCommerceJournal(() => ({ getItem: () => null, setItem: () => { throw new Error('quota') } }))
  assert.throws(() => blocked.begin(actor, command()), /不能开始提交/)
  const adapter = memory(), journal = createCommerceJournal(() => adapter)
  journal.begin(actor, command())
  const key = [...adapter.values.keys()][0]
  adapter.values.set(key, JSON.stringify([{ ...command(), code: 'secret' }]))
  assert.throws(() => journal.list(actor, tenant))
  adapter.values.set(key, 'x'.repeat(65537))
  assert.throws(() => journal.begin(actor, command()))
})

test('deadline makes late 201 ineligible for secret display; lookup activity cannot revive the delivery gate', async () => {
  const gate = createCommerceDeliveryGate(), token = gate.begin()
  let deliver
  const response = new Promise(resolve => { deliver = resolve })
  await assert.rejects(commerceDeadline(response, 5))
  gate.unknown(token)
  assert.equal(gate.accepts(token), false)
  assert.throws(() => gate.begin())
  deliver({ status: 201, codes: ['must-discard'] })
  assert.equal(gate.accepts(token), false)
  gate.settled(token)
  assert.equal(gate.accepts(token), false)
})

test('close/reopen and identity epoch invalidate old secret deliveries and old private response events', () => {
  const gate = createCommerceDeliveryGate(), token = gate.begin()
  gate.clear(); const another = gate.begin()
  assert.equal(gate.accepts(token), false)
  assert.equal(gate.accepts(another), true)
  const scope = createCommerceScope()
  scope.reset(commerceContext(actor, tenant, 'owner'))
  const write = scope.ticket('write'), lookup = scope.ticket('lookup')
  scope.reset(commerceContext(other, tenant, 'owner'))
  assert.equal(scope.accepts(write), false)
  assert.equal(scope.accepts(lookup), false)
  gate.clear(); assert.equal(gate.accepts(another), false)
})

test('ambiguous transport/parse/timeout failures remain unknown; static 4xx are distinct', () => {
  for (const error of [new Error('parse'), new ApiError('network_error', '', '', 0), new ApiError('commerce_unavailable', '', '', 503), new ApiError('invalid_response', '', '', 200), new ApiError('timeout', '', '', 408)]) assert.equal(unknownCommerceWrite(error), true)
  for (const code of [400,401,403,404,409,429]) assert.equal(unknownCommerceWrite(new ApiError('known', '', '', code)), false)
})
