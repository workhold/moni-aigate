import assert from 'node:assert/strict'
import test from 'node:test'
import { parseWalletOperatorSetting, parseWalletOperatorSettings, walletOperatorCommand, walletOperatorConfirmation } from '../src/wallet-operator-settings.ts'

const actor = '11111111-1111-4111-8111-111111111111'
const operation = index => `aaaaaaaa-aaaa-4aaa-8aaa-${String(index).padStart(12, '0')}`
const setting = (patch = {}) => ({ operation_id: operation(1), version: '2', single_operator: false, actor, reason: 'restore dual control', created_at: '2026-11-14T01:02:03.123456Z', ...patch })
const genesis = (patch = {}) => setting({ operation_id: operation(2), version: '1', single_operator: true, actor: null, reason: '034 single-operator genesis', ...patch })
const view = (patch = {}) => ({ current: setting(), versions: [setting(), genesis()], ...patch })

test('operator settings expose only append-only versions and the current snapshot', () => {
  const parsed = parseWalletOperatorSettings(view())
  assert.equal(parsed.current.version, '2')
  assert.equal(parsed.current.single_operator, false)
  assert.equal(parsed.versions[1].actor, null)
  assert.equal(parseWalletOperatorSetting(setting({ created_at: '2026-11-14T01:02:03Z' })).created_at, '2026-11-14T01:02:03Z')
})

test('operator settings reject private fields, stale current snapshots and broken history', () => {
  for (const value of [null, {}, { ...view(), extra: 'private' }, { current: setting(), versions: [] }, { current: setting({ version: '3' }), versions: [setting(), genesis()] }, { current: setting(), versions: [setting(), setting()] }, { current: setting(), versions: [setting(), genesis({ version: '0' })] }]) assert.throws(() => parseWalletOperatorSettings(value))
  for (const patch of [{ operation_id: 'not-a-uuid' }, { version: 2 }, { version: '02' }, { single_operator: 'false' }, { actor: 'private' }, { reason: '' }, { reason: 'line\nbreak' }, { created_at: '2026-02-30T00:00:00Z' }, { private: 'metadata' }]) assert.throws(() => parseWalletOperatorSetting(setting(patch)))
})

test('operator commands require canonical CAS identity and explicit risk confirmation', () => {
  const input = { operation_id: operation(3), expected_revision: '2', single_operator: true, reason: 'single operator maintenance', confirmation: walletOperatorConfirmation }
  assert.deepEqual(walletOperatorCommand(input), input)
  for (const patch of [{ operation_id: '' }, { expected_revision: 2 }, { expected_revision: '0' }, { single_operator: 'true' }, { reason: '' }, { confirmation: 'yes' }]) assert.throws(() => walletOperatorCommand({ ...input, ...patch }))
})
