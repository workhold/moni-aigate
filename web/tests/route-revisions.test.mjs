import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { ApiError, setCsrf } from '../src/api.ts'
import { acceptRouteActivation, createRouteActivationJournal, parseRouteRevision, parseRouteRevisionList, readRouteRevisions, routeActivation, routeRevisionPath } from '../src/route-revisions.ts'

const modelID = '6d772865-f77e-422c-aa5b-64c2a6b3fa66', routeID = '10000000-0000-4000-8000-000000000001', channelID = '653b7cd7-d1ce-4d7b-8203-a8f2c1c9f5ad', versionID = '20000000-0000-4000-8000-000000000001'
const revision = (number, active = false) => ({ id: routeID, revision: number, created_at: '2026-11-17T01:02:03.123456Z', profile_version: versionID, release_intent: 'test_only', active, current_profile: true, bindings: [{ channel_id: channelID, channel_name: '渠道', channel_kind: number === 1 ? 'new_api' : 'direct', integration_mode: number === 1 ? 'external_relay' : 'embedded_adapter', channel_version_id: versionID, upstream_model: number === 1 ? 'relay-model' : 'provider-model', priority: 1, weight: 1, max_inflight: 4 }] })

test('route history parser has a strict safe projection and unique active/revisions', () => {
  assert.deepEqual(parseRouteRevision(revision(2, true)), revision(2, true))
  assert.equal(parseRouteRevisionList([revision(2, true), revision(1)], '').rows[1].revision, 1)
  const serverValid = parseRouteRevision({ ...revision(1), bindings: [{ ...revision(1).bindings[0], channel_name: ' relay ', upstream_model: ' supplier/model ' }] })
  assert.equal(serverValid.bindings[0].channel_name, ' relay ')
  assert.equal(serverValid.bindings[0].upstream_model, ' supplier/model ')
  for (const invalid of [{ ...revision(1), secret: 'never' }, { ...revision(1), revision: 1.5 }, { ...revision(1), created_at: '2026-11-17' }, { ...revision(1), release_intent: 'qualified' }, { ...revision(1), bindings: [{ ...revision(1).bindings[0], base_url: 'https://private.invalid' }] }]) assert.throws(() => parseRouteRevision(invalid))
  assert.throws(() => parseRouteRevisionList([revision(1, true), { ...revision(1), id: routeID }], ''))
  assert.throws(() => parseRouteRevisionList([revision(1, true), { ...revision(2), active: true }], ''))
  assert.throws(() => parseRouteRevisionList([], 12))
  assert.equal(routeRevisionPath(modelID, 'a+b'), `/platform/models/${modelID}/route-revisions?cursor=a%2Bb`)
})

test('activation accepts a lower target revision and validates the exact returned target', () => {
  const operation = routeActivation(modelID, 2, 1)
  assert.deepEqual(operation.body, { expected_revision: 2 })
  assert.equal(operation.path, `/platform/models/${modelID}/route-revisions/1/activate`)
  assert.doesNotThrow(() => acceptRouteActivation(operation, { revision: 1 }))
  for (const response of [{ revision: 2 }, { revision: '1' }, { revision: 1, extra: true }]) assert.throws(() => acceptRouteActivation(operation, response))
  assert.throws(() => routeActivation(modelID, 1, 1), /已经/)
})

test('unknown and conflicting activation require read-only observation without monotonic assumptions', () => {
  for (const failure of [new ApiError('network_error', 'unknown', '', 0), new ApiError('invalid_response', 'unknown', '', 200), new ApiError('revision_conflict', 'conflict', '', 409)]) {
    const journal = createRouteActivationJournal(), operation = routeActivation(modelID, 2, 1)
    journal.begin('admin-a', modelID, operation)
    assert.throws(() => journal.begin('admin-a', modelID, operation), /已有/)
    journal.fail('admin-a', modelID, failure)
    const expectedMode = failure.status === 409 ? 'conflict' : 'unknown'
    assert.equal(journal.get('admin-a', modelID).mode, expectedMode)
    assert.throws(() => journal.resume('admin-a', modelID), /只读/)
    const observed = journal.observe('admin-a', modelID, [revision(2), revision(1, true)])
    assert.equal(observed.observedActive, 1, 'rollback to a lower revision is a valid observation')
    journal.resume('admin-a', modelID)
    assert.equal(journal.get('admin-a', modelID), null)
    assert.equal(journal.get('admin-b', modelID), null)
  }
  for (const failure of [new ApiError('forbidden', 'no', '', 403), new ApiError('invalid_request', 'no', '', 400)]) {
    const journal = createRouteActivationJournal(); journal.begin('admin-a', modelID, routeActivation(modelID, 2, 1)); journal.fail('admin-a', modelID, failure)
    assert.equal(journal.get('admin-a', modelID), null)
  }
})

test('history reads use only GET, follow bounded cursors and accept active revision on a later page', async () => {
  const previous = globalThis.fetch, calls = []
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    const second = url.includes('cursor=')
    return new Response(JSON.stringify({ data: second ? [revision(1, true)] : [revision(2)], request_id: 'history', next_cursor: second ? '' : 'next+page' }))
  }
  try {
    setCsrf('history-csrf')
    const rows = await readRouteRevisions(modelID)
    assert.deepEqual(rows.map(row => row.revision), [2, 1])
    assert.deepEqual(calls.map(call => call.options.method), ['GET', 'GET'])
    assert.ok(calls.every(call => call.options.headers['X-CSRF-Token'] === undefined))
  } finally { globalThis.fetch = previous; setCsrf('') }
  globalThis.fetch = async () => new Response(JSON.stringify({ data: [], next_cursor: 'same' }))
  try { await assert.rejects(readRouteRevisions(modelID), /游标/) } finally { globalThis.fetch = previous }
})

test('route history component is context-scoped and never retries activation implicitly', async () => {
  const [component, platform] = await Promise.all(['../src/components/RouteRevisions.vue', '../src/views/PlatformView.vue'].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  assert.match(component, /routeActivationJournal\.begin/)
  assert.match(component, /routeActivationJournal\.observe/)
  assert.match(component, /acceptRouteActivation/)
  assert.match(component, /watch\(context.*reset\(\).*false/)
  assert.doesNotMatch(component, /setInterval|setTimeout|localStorage|sessionStorage|indexedDB|v-html|console\./)
  assert.match(platform, /<RouteRevisions/)
  assert.match(platform, /showRouteHistory/)
})
