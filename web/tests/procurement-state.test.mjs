import assert from 'node:assert/strict'
import test from 'node:test'
import { ref, reactive } from 'vue'
import { ApiError } from '../src/api.ts'
import { createProcurementJournal, createProcurementScope, procurementContext, procurementOperationID, unknownProcurementWrite } from '../src/procurement-state.ts'

const operationID = 'b51784ad-ce02-4eaa-a507-1c2b5a4c34c7', anotherID = '54d89d17-90c1-4b2d-aae7-d9386859db3b'
const actor = procurementContext('admin-one', 'tenant-one', 'admin')
function command(id = operationID, target = 'account:one') { return { operationID: id, target, path: '/platform/procurement-accounts/one/configuration', method: 'PUT', body: { operation_id: id, limit_amount: '9007199254740993', expected_revision: '9007199254740993' }, lookupPath: `/platform/procurement-operations/${id}` } }

test('procurement UUIDs are canonical and reject arbitrary lookup paths', () => {
  assert.equal(procurementOperationID(operationID), operationID)
  for (const value of ['', operationID.toUpperCase(), '../secret', '00000000-0000-0000-0000-000000000000', null]) assert.throws(() => procurementOperationID(value))
})

test('reactive commands are frozen safely and exact money survives journal snapshots', () => {
  const journal = createProcurementJournal(), value = ref(command())
  journal.begin(actor, value.value)
  value.value.body.limit_amount = '1'
  const saved = journal.get(actor, operationID)
  assert.equal(saved.command.body.limit_amount, '9007199254740993')
  saved.command.body.limit_amount = '2'
  assert.equal(journal.get(actor, operationID).command.body.limit_amount, '9007199254740993')
  journal.confirm(actor, operationID, reactive({ operation_id: operationID, revision: '9007199254740994' }))
  assert.equal(journal.get(actor, operationID).result.revision, '9007199254740994')
})

test('submitting and unknown commands block duplicate target writes until the original receipt is confirmed', () => {
  const journal = createProcurementJournal()
  journal.begin(actor, command())
  assert.throws(() => journal.begin(actor, command(anotherID)), /待确认/)
  journal.fail(actor, operationID, new ApiError('network_error', 'network', '', 0), '结果待确认')
  assert.equal(journal.get(actor, operationID).state, 'unknown')
  assert.throws(() => journal.begin(actor, command(anotherID)), /换 ID/)
  journal.confirm(actor, operationID, { operation_id: operationID })
  assert.doesNotThrow(() => journal.begin(actor, command(anotherID)))
  assert.throws(() => journal.begin(actor, command()), /已使用/)
})

test('receipt lookup errors do not resolve uncertain writes; explicit rejection remains distinct from conflict', () => {
  const journal = createProcurementJournal()
  journal.begin(actor, command())
  journal.fail(actor, operationID, new Error('malformed success'), '待核查')
  journal.fail(actor, operationID, new ApiError('not_found', 'not found', '', 404), '尚未查到')
  assert.equal(journal.get(actor, operationID).state, 'unknown')
  journal.begin(actor, command(anotherID, 'account:two'))
  journal.fail(actor, anotherID, new ApiError('revision_conflict', 'conflict', '', 409), '重新读取配置')
  assert.equal(journal.get(actor, anotherID).state, 'conflict')
  const rejected = createProcurementJournal()
  rejected.begin(actor, command())
  rejected.fail(actor, operationID, new ApiError('reauth_required', 'reauth', '', 403), '需要再认证')
  assert.equal(rejected.get(actor, operationID).state, 'rejected')
})

test('journal isolates actor, tenant, role and targets and retains old outcomes after page/context invalidation', () => {
  const journal = createProcurementJournal(), scope = createProcurementScope()
  scope.reset(actor)
  const ticket = scope.ticket('write')
  journal.begin(actor, command())
  for (const context of [procurementContext('admin-two', 'tenant-one', 'admin'), procurementContext('admin-one', 'tenant-two', 'admin'), procurementContext('admin-one', 'tenant-one', 'finance_operator')]) {
    assert.deepEqual(journal.list(context), [])
    assert.doesNotThrow(() => journal.begin(context, command()))
  }
  scope.invalidate('write')
  assert.equal(scope.accepts(ticket), false)
  scope.reset('another-context')
  journal.confirm(actor, operationID, { operation_id: operationID })
  assert.equal(journal.get(actor, operationID).state, 'confirmed')
  assert.equal(scope.accepts(ticket), false)
})

test('nondefinitive transport outcomes are unknown and conflicting durable receipts cannot overwrite history', () => {
  for (const error of [new Error('parse'), new ApiError('timeout', 'timeout', '', 408), new ApiError('server', 'server', '', 500), new ApiError('invalid_response', 'bad', '', 200)]) assert.equal(unknownProcurementWrite(error), true)
  const journal = createProcurementJournal()
  journal.begin(actor, command())
  journal.confirm(actor, operationID, { operation_id: operationID, revision: '2' })
  assert.throws(() => journal.confirm(actor, operationID, { operation_id: operationID, revision: '3' }), /不一致/)
  assert.equal(journal.get(actor, operationID).result.revision, '2')
  journal.fail(actor, operationID, new Error('late'), 'late')
  assert.equal(journal.get(actor, operationID).state, 'confirmed')
})
