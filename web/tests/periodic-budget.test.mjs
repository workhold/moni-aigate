import test from 'node:test'
import assert from 'node:assert/strict'
import { parsePeriodicJSON } from '../src/periodic-budget-json.ts'
import { parseBudgetAllocation, parsePeriodicBudget, parsePeriodicInput, parsePeriodicPending, parsePeriodicReceipt, parsePeriodicSingle, parsePeriodicWindows, periodicAmount, periodicCanRead, periodicCanWrite, periodicFingerprint, periodicID, periodicIncompleteWindow, periodicUTC6, parsePeriodicTarget } from '../src/periodic-budget.ts'

const actor = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', tenant = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', key = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', id = 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', control = 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee'
const at = '2026-11-01T00:00:00.000000Z'
const unconfigured = () => ({ scope: 'key', scope_id: key, revision: '0', period_kind: null, enabled: false, limit_amount: null, state: 'unconfigured', effective_from: null, tracking_started_at: null, current_window: null, carry_held: '0', as_of: at })
const configured = () => ({ ...unconfigured(), revision: '1', period_kind: 'utc_day', enabled: true, limit_amount: '9223372036854775807', state: 'active', effective_from: at, tracking_started_at: '2026-10-31T23:59:59.123456Z', current_window: { id, start_at: at, end_at: '2026-11-02T00:00:00.000000Z', spent_amount: '9'.repeat(100), held_amount: '0' }, carry_held: '9007199254740993' })
const input = () => ({ operation_id: id, expected_revision: '9007199254740993', period_kind: 'utc_month', enabled: false, limit_amount: '9223372036854775807' })
const pending = () => ({ version: 1, operation_id: id, kind: 'key_periodic_budget.configure', actor, identity_scope: tenant, target: `/api/keys/${key}/periodic-budget`, input_fingerprint: 'f'.repeat(64), state: 'pending' })
const receipt = () => ({ operation: { operation_id: id, kind: 'key_periodic_budget.configure', actor, scope: tenant, target: `/api/keys/${key}/periodic-budget`, input_fingerprint: 'f'.repeat(64), resource_id: control, revision: '1', created_at: at }, result: configured() })

test('periodic values preserve BIGINT and Numeric100 and reject aliases', () => {
  assert.equal(periodicAmount('9223372036854775807'), '9223372036854775807')
  assert.equal(periodicAmount('9'.repeat(100), true), '9'.repeat(100))
  for (const value of [0, 9007199254740993n, '9223372036854775808', '-1', '+1', '01', '1e2', ' 1', '1.0', '', null]) assert.throws(() => periodicAmount(value))
  assert.throws(() => periodicAmount('1' + '0'.repeat(100), true))
  for (const value of [key.toUpperCase(), '00000000-0000-0000-0000-000000000000', `{${key}}`, key + ' ']) assert.throws(() => periodicID(value))
})
test('UTC6 validates the actual calendar without Date year 0/99 normalization', () => {
  for (const value of ['0001-01-01T00:00:00.000001Z', '0099-12-31T23:59:59.999999Z', '2000-02-29T23:59:59.999999Z', '9999-12-31T23:59:59.999999Z']) assert.equal(periodicUTC6(value), value)
  for (const value of ['0000-01-01T00:00:00.000000Z', '1900-02-29T00:00:00.000000Z', '2026-04-31T00:00:00.000000Z', '2026-11-01T24:00:00.000000Z', '2026-11-01T00:00:60.000000Z', '2026-11-01T00:00:00.000Z', '2026-11-01T00:00:00.000000+00:00', '2026-11-01T00:00:00.000000Z ']) assert.throws(() => periodicUTC6(value))
})
test('strict JSON rejects escaped duplicates, invalid Unicode and partial documents', () => {
  for (const text of ['{"a":1,"\\u0061":2}', '{"x":"\\ud800"}', '{"x":"\ud800"}', '{"x":1}{}', '{"x":null,}', '[true,]', '{"v":1.0}', '{"v":1e0}', '{"v":9007199254740993}', '"' + 'x'.repeat(131072) + '"']) assert.throws(() => parsePeriodicJSON(text))
  assert.equal(parsePeriodicJSON('{"x":"😀","amount":"9007199254740993"}').amount, '9007199254740993')
})
test('exact DTO distinguishes unconfigured, scheduled, disabled and current calendar bounds', () => {
  assert.deepEqual(parsePeriodicBudget(unconfigured(), { scope: 'key', id: key }), unconfigured())
  assert.deepEqual(parsePeriodicBudget(configured()), configured())
  const scheduled = { ...configured(), state: 'scheduled', as_of: '2026-10-31T23:59:59.999999Z', current_window: null }
  assert.equal(parsePeriodicBudget(scheduled).state, 'scheduled')
  assert.equal(periodicIncompleteWindow({ ...configured().current_window, start_at: '2026-10-31T00:00:00.000000Z' }, configured()), true)
  assert.equal(periodicIncompleteWindow(configured().current_window, configured()), false)
  assert.equal(parsePeriodicBudget({ ...configured(), enabled: false, state: 'disabled' }).state, 'disabled')
  for (const patch of [{ revision: '1' }, { enabled: true }, { limit_amount: '0' }, { carry_held: '1' }, { current_window: configured().current_window }, { as_of: undefined }, { extra: true }]) assert.throws(() => parsePeriodicBudget({ ...unconfigured(), ...patch }))
  for (const patch of [{ revision: '0' }, { state: 'scheduled' }, { effective_from: '2026-11-01T00:00:00.000001Z' }, { tracking_started_at: at }, { current_window: { ...configured().current_window, start_at: '2026-10-31T00:00:00.000000Z' } }]) assert.throws(() => parsePeriodicBudget({ ...configured(), ...patch }))
  assert.throws(() => parsePeriodicBudget(configured(), { scope: 'tenant', id: key }))
})
test('allocation unions retain supplier overruns and forbid customer disputed / legacy windows', () => {
  const allocation = { id, subject_kind: 'supplier_attempt', subject_id: key, window_id: control, upper_amount: '1', held_amount: '0', spent_amount: '9007199254740993', state: 'disputed', created_at: at }
  assert.equal(parseBudgetAllocation(allocation, 'procurement').spent_amount, '9007199254740993')
  assert.equal(parseBudgetAllocation({ ...allocation, subject_kind: 'legacy_supplier', window_id: null }).window_id, null)
  for (const patch of [{ subject_kind: 'customer_request' }, { subject_kind: 'legacy_supplier' }, { window_id: null }]) assert.throws(() => parseBudgetAllocation({ ...allocation, ...patch }))
  assert.throws(() => parseBudgetAllocation(allocation, 'tenant'))
})
test('immutable receipt binds all metadata and treats control ID independently from scope ID', () => {
  assert.equal(parsePeriodicReceipt(receipt(), pending()).operation.resource_id, control)
  for (const field of ['operation_id', 'actor', 'scope', 'target', 'input_fingerprint', 'kind', 'revision', 'created_at']) assert.throws(() => parsePeriodicReceipt({ ...receipt(), operation: { ...receipt().operation, [field]: field === 'revision' ? '2' : field === 'created_at' ? '2026-11-01T00:00:00.000001Z' : field === 'target' ? `/api/keys/${control}/periodic-budget` : control } }, pending()))
  assert.throws(() => parsePeriodicReceipt(receipt(), pending(), key))
  assert.throws(() => parsePeriodicReceipt({ ...receipt(), result: { ...configured(), scope_id: control } }, pending()))
  assert.throws(() => parsePeriodicPending({ ...pending(), body: input() }))
  assert.throws(() => parsePeriodicPending({ ...pending(), identity_scope: `tenant:${tenant}` }))
})
test('fingerprint matches the shared PostgreSQL / Python / Go golden', async () => {
  assert.equal(await periodicFingerprint('key_periodic_budget.configure', actor, tenant, `/api/keys/${key}/periodic-budget`, input()), '2a00c71a0d596018c8ae1fdba6be1c70bcb2385548b96f9b32ddd2ecb0b3d34d')
  assert.equal(await periodicFingerprint('key_periodic_budget.configure', actor, tenant, `/api/keys/${key}/periodic-budget`, { ...input(), operation_id: control }), '2a00c71a0d596018c8ae1fdba6be1c70bcb2385548b96f9b32ddd2ecb0b3d34d')
  assert.throws(() => parsePeriodicInput({ ...input(), expected_revision: 1 }))
})
test('pages require exact envelope, empty string cursor and descending UUID tie order', () => {
  const row = configured().current_window
  assert.equal(parsePeriodicWindows({ data: [row], request_id: 'req-test', next_cursor: '' }).next, '')
  for (const raw of [{ data: [], request_id: 'r', next_cursor: null }, { data: [], request_id: 'r' }, { data: [row, row], request_id: 'r', next_cursor: '' }, { data: [{ ...row, id: key }, row], request_id: 'r', next_cursor: '' }, { data: [], request_id: 'r', next_cursor: 'x'.repeat(1025) }]) assert.throws(() => parsePeriodicWindows(raw))
  assert.throws(() => parsePeriodicSingle({ data: unconfigured(), request_id: 'r', extra: true }, parsePeriodicBudget))
})
test('independent page targets reject mixed / duplicate / invalid query before transport', () => {
  assert.equal(parsePeriodicTarget('/periodic-budget', '', tenant).kind, 'wallet')
  assert.throws(() => parsePeriodicTarget('/periodic-budget', '?', tenant))
  assert.equal(parsePeriodicTarget('/periodic-budget', `key=${key}`, tenant).path, `/api/keys/${key}/periodic-budget`)
  assert.equal(parsePeriodicTarget('/platform/periodic-budget', `tenant=${tenant}`).identityScope, 'platform')
  for (const raw of [`key=${key}&key=${key}`, `key=${key}&tenant=${tenant}`, `key=${key}&x=1`, 'key=%ZZ', 'key=%ED%A0%80', 'key=', '']) assert.throws(() => parsePeriodicTarget('/platform/periodic-budget', raw, tenant))
  assert.throws(() => parsePeriodicTarget('/periodic-budget', `key=${key}&key=${key}`, tenant))
  const wallet = parsePeriodicTarget('/periodic-budget', '', tenant), platform = parsePeriodicTarget('/platform/periodic-budget', `tenant=${tenant}`)
  assert.equal(periodicCanRead(wallet, '', 'billing_viewer'), true); assert.equal(periodicCanWrite(wallet, 'admin', 'owner'), false)
  assert.equal(periodicCanRead(platform, 'finance_operator', ''), true); assert.equal(periodicCanWrite(platform, 'finance_operator', ''), false)
  assert.equal(periodicCanWrite(platform, 'platform_admin', ''), false)
})
