import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError } from '../src/api.ts'
import { pricingCommand } from '../src/pricing.ts'
import { createPricingEpoch, createPricingJournal, createPricingPager, pricingContext, unknownPricingWrite } from '../src/pricing-state.ts'
const actor = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', group = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', tenant = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', operation = '11111111-1111-4111-8111-111111111111', other = '22222222-2222-4222-8222-222222222222', at = '2026-11-01T00:00:00.000001Z'
function memory() { const values = new Map(); return { values, getItem: key => values.get(key) || null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) } }
const command = (op = operation) => pricingCommand('price_group.create', op, actor, { label: 'safe-private-label' })
function receipt(c, patch = {}) { return { operation: { operation_id: c.operation_id, actor, kind: c.kind, scope: c.scope, target: c.target, input_fingerprint: c.input_fingerprint, resource_id: group, revision: '1', created_at: at }, result: { id: group, label: 'safe-private-label', state: 'active', revision: '1', created_at: at }, ...patch } }
test('identity, tenant, role, CSRF, page close and newer tickets suppress late results', () => {
  const scope = createPricingEpoch(), current = pricingContext(actor, tenant, 'admin:owner','csrf',false)
  scope.reset(current); const initial = scope.ticket(); assert(scope.accepts(initial))
  scope.reset(pricingContext(actor, tenant, 'finance_operator:owner','csrf',false)); assert(!scope.accepts(initial))
  const newer = scope.ticket(), latest = scope.ticket(); assert(!scope.accepts(newer)); assert(scope.accepts(latest))
  scope.reset(current); assert(!scope.accepts(latest)); const unmounted = scope.ticket(); scope.dispose(); assert(!scope.accepts(unmounted))
  assert.notEqual(current, pricingContext(actor, group, 'admin:owner','csrf',false))
  assert.notEqual(current, pricingContext(actor, tenant, 'admin:owner','new-csrf',false))
})
test('pager uses per-page observations, serializes more, blocks cursor cycles and discards old identity', () => {
  const pager = createPricingPager(); pager.reset('actor/tenant/targetA')
  const first = pager.begin(); assert.equal(pager.begin(false), null)
  pager.accept(first, { rows: [{ id: group }], next: 'next', context: { as_of: at } })
  const more = pager.begin(false); assert.equal(more.cursor, 'next')
  assert.throws(() => pager.accept(more, { rows: [], next: 'next' }), /未前进/)
  pager.fail(more, new Error('failed GET')); assert.equal(pager.snapshot().rows.length, 1)
  const retry = pager.begin(false); pager.accept(retry, { rows: [], next: '', context: { as_of: 'later' } })
  assert.equal(pager.snapshot().rows.length, 0); assert.equal(pager.snapshot().page, 2); assert.equal(pager.snapshot().context.as_of, 'later')
  assert.equal(pager.begin(false), null)
  const old = pager.begin(); pager.reset('actor/tenant/targetB'); assert.equal(pager.accept(old, { rows: [{ id: group }], next: '' }), false)
  assert.equal(pager.fail(old, new Error('late error')), false); assert.deepEqual(pager.snapshot().rows, [])
})
test('unknown submission locks target through 404, close and reload; safe storage excludes body and result', async () => {
  const storage = memory(), journal = createPricingJournal(storage), c = await command()
  journal.begin(c); journal.fail(actor, operation, new ApiError('pricing_busy','unknown','request',503))
  assert(journal.blocked(actor, c.target)); await assert.rejects(async () => journal.begin(await command(other)), /不能换 ID/)
  const saved = [...storage.values.values()].join('')
  for (const forbidden of ['safe-private-label','body','csrf','result']) assert(!saved.includes(forbidden))
  const restored = createPricingJournal(storage), pending = restored.list(actor)[0]
  assert.equal(pending.state, 'unknown'); assert.equal(pending.command.input_fingerprint, c.input_fingerprint)
  assert(!('body' in pending.command)); assert(restored.blocked(actor, c.target))
  // A failed lookup does not call confirm/fail; state and lock stay unchanged.
  const missing = new ApiError('not_found','missing','request',404); assert.equal(missing.status,404); assert.equal(restored.list(actor)[0].state,'unknown')
  restored.confirm(actor, operation, receipt(c)); assert(!restored.blocked(actor, c.target)); assert.equal(storage.values.size,0)
})
test('definite CAS rejection allows explicit new operation, schema/transport/cancel are unknown', async () => {
  const journal = createPricingJournal(), c = await command()
  journal.begin(c); assert.equal(journal.fail(actor, operation, new ApiError('pricing_revision_conflict','stale','r',409)).state, 'rejected')
  assert(!journal.blocked(actor,c.target)); journal.begin(await command(other)); assert(journal.blocked(actor,c.target))
  for (const error of [new Error('invalid success DTO'), new DOMException('aborted','AbortError'), new ApiError('network_error','network','',0), new ApiError('invalid_response','bad','',201), new ApiError('busy','unknown','',500)]) assert(unknownPricingWrite(error))
  assert(!unknownPricingWrite(new ApiError('reauth_required','reauth','',403)))
})
test('receipt mismatch never unlocks unknown target and confirmed original result is immutable', async () => {
  const journal = createPricingJournal(), c = await command(); journal.begin(c)
  const raw = receipt(c)
  assert.throws(() => journal.confirm(actor,operation,{ ...raw, operation: { ...raw.operation,input_fingerprint:'0'.repeat(64) } }))
  assert(journal.blocked(actor,c.target)); journal.confirm(actor,operation,raw)
  const confirmed = journal.list(actor)[0]
  assert(Object.isFrozen(confirmed.receipt.result)); assert.throws(() => { confirmed.receipt.result.label = 'mutation' })
  assert.throws(() => journal.confirm(actor,operation,{ ...raw,result:{ ...raw.result,label:'changed' } }))
})
test('corrupt or unavailable session storage fails before a new write and never silently clears pending', async () => {
  const storage=memory(), c=await command(); storage.setItem('moni:pricing:pending:v1:'+actor,'invalid json')
  assert.throws(()=>createPricingJournal(storage).begin(c), /存储损坏/)
  const failing=createPricingJournal({getItem:()=>null,setItem:()=>{throw new Error('quota')},removeItem:()=>{}})
  assert.throws(()=>failing.begin(c), /不允许新提交/)
  assert.equal(failing.list(actor).length,0)
})
