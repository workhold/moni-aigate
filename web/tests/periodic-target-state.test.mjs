import assert from 'node:assert/strict'
import test from 'node:test'
import { createPeriodicTargetLoader, periodicTargetContent, periodicTargetEmptyActions } from '../src/periodic-target-state.ts'

const tenant = { kind: 'tenant', id: '6d772865-f77e-422c-aa5b-64c2a6b3fa66', name: 'new-tenant', provider: 'workspace', state: 'active' }
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

test('late successful responses cannot replace a newer identity or generation', async () => {
  const calls = []
  const loader = createPeriodicTargetLoader(() => { const next = deferred(); calls.push(next); return next.promise })
  const first = loader.reset(), second = loader.reset()
  assert.equal(calls.length, 2)
  calls[1].resolve([tenant]); await second
  assert.deepEqual(loader.state.choices, [tenant])
  calls[0].resolve([{ ...tenant, name: 'stale-tenant' }]); await first
  assert.equal(loader.state.choices[0].name, 'new-tenant')
  const disposed = loader.reset(); loader.dispose(); calls[2].resolve([{ ...tenant, name: 'after-dispose' }]); await disposed
  assert.deepEqual(loader.state.choices, [])
  assert.equal(loader.state.loading, false)
})

test('load errors and content branches are explicit and never masquerade as empty success', async () => {
  const loader = createPeriodicTargetLoader(async () => { throw new Error('read failed') })
  await loader.reload()
  assert.equal(loader.state.error, 'read failed')
  assert.deepEqual(loader.state.choices, [])
  assert.equal(periodicTargetContent(loader.state, 0), 'error')
  assert.equal(periodicTargetContent({ choices: [], loading: true, error: '' }, 0), 'loading')
  assert.equal(periodicTargetContent({ choices: [], loading: false, error: '' }, 0), 'empty')
  assert.equal(periodicTargetContent({ choices: [tenant], loading: false, error: '' }, 0), 'search_empty')
  assert.equal(periodicTargetContent({ choices: [tenant], loading: false, error: '' }, 1), 'rows')
})

test('empty-state actions are role-correct and contain no dead finance navigation', () => {
  const admin = periodicTargetEmptyActions(true)
  assert.deepEqual(admin.actions.map(action => action.path), ['/platform/tenants', '/platform/procurement'])
  const finance = periodicTargetEmptyActions(false)
  assert.deepEqual(finance.actions, [])
  assert.match(finance.message, /联系平台管理员/)
})
