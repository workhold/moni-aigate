import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError } from '../src/api.ts'
import { acceptPublication, createPublicCatalogPager, createPublicationJournal, failPublication, newPublicationReview, observePublication, resumePublication, submitPublication } from '../src/public-catalog-state.ts'

const id = '11111111-1111-4111-8111-111111111111', secondID = '33333333-3333-4333-8333-333333333333', priceID = '22222222-2222-4222-8222-222222222222'
const model = (modelID = id, rate = '1000000') => ({ id: modelID, public_name: modelID === id ? 'a-model' : 'b-model', protocol: 'openai_chat', capabilities: { streaming: true, tools: false, input_only: false, text_only: true }, limits: { context_tokens: 100, input_token_upper_bound: 100, max_output_tokens: 10, default_output_tokens: 10 }, pricing: { status: 'available', price_version_id: priceID, schema_id: id, schema_digest: 'ab'.repeat(32), variant_key: 'default', currency: 'CNY', rates: { schema_id: id, version: '1', side: 'retail', currency: 'CNY', rates: [{ dimension: 'text_input_token', unit: 'token', numerator: rate === '1000000' ? '1' : rate === '2000000' ? '2' : rate, denominator: '1' }, { dimension: 'text_output_token', unit: 'token', numerator: '0', denominator: '1' }] }, effective_at: '2026-11-01T00:00:00Z' } })
const page = (data, next_cursor = '', as_of = '2026-11-01T01:00:00Z') => ({ data, next_cursor, as_of, request_id: 'request-safe' })
const filters = { q: '', protocol: '' }
const view = { id, public_name: 'a-model', enabled: false, public_visible: false, public_revision: '9007199254740993' }

test('more is serialized, IDs deduplicate across pages and original price observation stays with row', () => {
  const pager = createPublicCatalogPager(), first = pager.begin(filters)
  assert.equal(pager.begin(filters, false), null)
  pager.accept(first, page([model()], 'next'))
  const more = pager.begin(filters, false)
  assert.equal(more.cursor, 'next')
  assert.equal(pager.begin(filters, false), null)
  pager.accept(more, page([model(id, '2000000'), model(secondID)], '', '2026-11-01T02:00:00Z'))
  const state = pager.snapshot()
  assert.equal(state.rows.length, 2)
  assert.equal(state.rows[0].model.pricing.rates?.rates[0].numerator, '1')
  assert.equal(state.rows[0].as_of, '2026-11-01T01:00:00Z')
  assert.equal(state.rows[1].as_of, '2026-11-01T02:00:00Z')
  assert.equal(pager.begin(filters, false), null)
})

test('filter/reset/unmount epochs prevent stale pages, errors and cursor reuse', () => {
  const pager = createPublicCatalogPager(), old = pager.begin(filters), current = pager.begin({ q: 'b', protocol: '' })
  assert.equal(pager.accept(old, page([model()], 'stale')), false)
  assert.equal(pager.fail(old, new Error('late error')), false)
  pager.accept(current, page([model(secondID)]))
  assert.equal(pager.snapshot().rows[0].model.id, secondID)
  const pending = pager.begin(filters)
  pager.invalidate(new Error('invalid filter'))
  assert.equal(pager.accept(pending, page([model()])), false)
  assert.equal(pager.snapshot().rows.length, 0)
  assert.equal(pager.snapshot().error, 'invalid filter')
  const final = pager.begin(filters); pager.dispose()
  assert.equal(pager.accept(final, page([model()])), false)
  assert.equal(pager.begin(filters), null)
})

test('failed more can retry safe GET without losing rows, cursor cycles fail rather than looping', () => {
  const pager = createPublicCatalogPager(), first = pager.begin(filters)
  pager.accept(first, page([model()], 'next'))
  const failed = pager.begin(filters, false); pager.fail(failed, new Error('temporary'))
  assert.equal(pager.snapshot().rows.length, 1)
  const retry = pager.begin(filters, false)
  assert.equal(retry.cursor, 'next')
  assert.throws(() => pager.accept(retry, page([model(secondID)], 'next')), /没有前进/)
  pager.fail(retry, new Error('分页未前进'))
  assert.equal(pager.snapshot().loading, false)
})

test('publication no-op and changed success require exactly matching full CAS result', () => {
  const first = newPublicationReview(view), change = submitPublication(first, true)
  assert.equal(change.submitted.body.expected_revision, '9007199254740993')
  const result = { ...view, public_visible: true, public_revision: '9007199254740994' }
  assert.equal(acceptPublication(change, result).mode, 'editing')
  const noop = submitPublication(first, false)
  assert.equal(acceptPublication(noop, view).view.public_revision, view.public_revision)
  for (const patch of [{ public_revision: view.public_revision }, { public_revision: '9007199254740995' }, { public_visible: false }, { id: secondID }]) assert.throws(() => acceptPublication(change, { ...result, ...patch }))
  assert.equal(change.submitted.body.public_visible, true)
})

test('uncertain PUT and conflict only GET-observe then explicit new edit, never resubmit', () => {
  const submitted = submitPublication(newPublicationReview(view), true)
  for (const error of [new ApiError('network_error', 'network', '', 0), new ApiError('server', 'server', '', 503), new Error('malformed success')]) {
    const unknown = failPublication(submitted, error)
    assert.equal(unknown.mode, 'unknown')
    assert.throws(() => submitPublication(unknown, true))
    assert.throws(() => resumePublication(unknown))
    const observed = observePublication(unknown, view)
    assert.equal(observed.mode, 'unknown')
    assert.throws(() => submitPublication(observed, true))
    assert.equal(resumePublication(observed).mode, 'editing')
  }
  const conflict = failPublication(submitted, new ApiError('public_catalog_revision_conflict', 'stale', '', 409))
  assert.equal(conflict.mode, 'conflict')
  assert.throws(() => observePublication(conflict, { ...view, public_revision: '9007199254740992' }))
  assert.throws(() => observePublication(conflict, { ...view, public_visible: true }))
  assert.equal(failPublication(submitted, new ApiError('reauth_required', 'reauth', '', 403)).mode, 'editing')
})

test('tab journal retains closed uncertain edit scoped by actor/tenant/role and rejects implicit replay', () => {
  const journal = createPublicationJournal(), submitted = submitPublication(newPublicationReview(view), true)
  const ticket = journal.begin('actor-one/tenant-one/admin', submitted)
  const reopened = journal.read('actor-one/tenant-one/admin', id)
  assert.equal(reopened.mode, 'unknown')
  assert.equal(journal.read('actor-two/tenant-one/admin', id), undefined)
  assert.throws(() => journal.begin('actor-one/tenant-one/admin', submitted), /仍待核查/)
  journal.finish('actor-one/tenant-one/admin', id, ticket, failPublication(submitted, new Error('timeout')))
  const verified = observePublication(journal.read('actor-one/tenant-one/admin', id), view)
  assert.equal(journal.restart('actor-one/tenant-one/admin', verified).mode, 'editing')
  const newTicket = journal.begin('actor-one/tenant-one/admin', submitted)
  journal.finish('actor-one/tenant-one/admin', id, ticket)
  assert.equal(journal.read('actor-one/tenant-one/admin', id).mode, 'unknown')
  journal.finish('actor-one/tenant-one/admin', id, newTicket)
  assert.equal(journal.read('actor-one/tenant-one/admin', id), undefined)
})
