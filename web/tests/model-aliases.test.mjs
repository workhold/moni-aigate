import assert from 'node:assert/strict'
import test from 'node:test'
import { modelAliasCreate, modelAliasRetire, parseModelAliases, stableAliasCreate } from '../src/model-aliases.ts'

const model = '11111111-1111-4111-8111-111111111111'
const actor = '22222222-2222-4222-8222-222222222222'
const row = (patch = {}) => ({ alias: 'gpt-4o', model_id: model, state: 'active', revision: '1', operation_id: actor, created_by: actor, created_at: '2026-11-10T00:00:00Z', retired_at: null, ...patch })

test('model alias parser preserves terminal state and rejects ambiguous rows', () => {
  assert.equal(parseModelAliases([row()])[0].alias, 'gpt-4o')
  assert.equal(parseModelAliases([row({ state: 'retired', revision: '2', retired_at: '2026-11-10T01:00:00Z' })])[0].state, 'retired')
  for (const value of [row({ alias: 'bad name' }), row({ state: 'retired', retired_at: null }), row({ state: 'active', retired_at: '2026-11-10T01:00:00Z' }), row({ revision: '01' })]) assert.throws(() => parseModelAliases([value]))
  assert.throws(() => parseModelAliases([row(), row()]))
})

test('model alias commands use canonical encoded paths and exact revisions', () => {
  assert.deepEqual(modelAliasCreate(model, 'gpt-4o', actor), { path: `/platform/models/${model}/aliases`, method: 'POST', body: { operation_id: actor, alias: 'gpt-4o' } })
  assert.deepEqual(modelAliasRetire('gpt-4o', '2', actor), { path: '/platform/model-aliases/gpt-4o/retire', method: 'POST', body: { operation_id: actor, expected_revision: '2' } })
  assert.throws(() => modelAliasRetire('gpt 4o', '2', actor))
  assert.throws(() => modelAliasRetire('gpt-4o', '01', actor))
  const stable = stableAliasCreate(model, 'gpt-4o')
  assert.match(stable.body.operation_id, /^[0-9a-f-]{36}$/)
  assert.equal(stable.body.alias, 'gpt-4o')
})
