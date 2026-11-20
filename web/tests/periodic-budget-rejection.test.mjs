import assert from 'node:assert/strict'
import test from 'node:test'
import { parseBudgetRejection, parseRequestRows, tryBudgetRejection } from '../src/request-wire.ts'
import { inferenceBudgetRejection, inferenceRequestError } from '../src/inference.ts'

const rejection = {
  scope: 'tenant', reason: 'limit_exceeded', window_used: '120', limit_amount: '119', window_reset_at: '2026-11-21T00:00:00.000000Z',
}
const schema = '10000000-0000-4000-8000-000000000001'
const requestRow = {
  id: '11111111-1111-4111-8111-111111111111', model_id: '22222222-2222-4222-8222-222222222222',
  status: 'failed', billing_status: 'reserved', schema_id: schema, schema_digest: 'ab'.repeat(32), variant_key: 'default',
  retail_rates: { schema_id: schema, version: '1', side: 'retail', currency: 'CNY', rates: [{ dimension: 'text_input_token', unit: 'token', numerator: '1', denominator: '1' }, { dimension: 'text_output_token', unit: 'token', numerator: '0', denominator: '1' }] },
  retail_bounds: { schema_id: schema, version: '1', side: 'retail', bounds: [{ dimension: 'text_input_token', unit: 'token', quantity: '10' }, { dimension: 'text_output_token', unit: 'token', quantity: '10' }] },
  usage_status: 'missing', retail_quantities: null,
}

test('periodic budget rejection parser returns only five validated safe fields', () => {
  const parsed = parseBudgetRejection({ error: { code: 'budget_exceeded', message: 'safe public message', ...rejection, sql: 'private-sql-marker', body: 'private-body-marker', secret: 'private-secret-marker' } })
  assert.deepEqual(parsed, rejection)
  const wire = JSON.stringify(parsed)
  for (const marker of ['private-sql-marker', 'private-body-marker', 'private-secret-marker', 'message', 'sql', 'body', 'secret']) assert.equal(wire.includes(marker), false)
})

test('periodic budget rejection parser fails closed on malformed metadata', () => {
  assert.equal(parseBudgetRejection({ code: 'budget_exceeded', message: 'metadata unavailable' }), null)
  assert.equal(parseBudgetRejection({ error: { code: 'other_error', ...rejection } }), null)
  for (const [field, value] of Object.entries({ scope: 'private', reason: 'private', window_used: '01', limit_amount: 119, window_reset_at: '2026-11-21T00:00:00Z' })) {
    assert.throws(() => parseBudgetRejection({ error: { code: 'budget_exceeded', ...rejection, [field]: value } }))
    assert.equal(tryBudgetRejection({ error: { code: 'budget_exceeded', ...rejection, [field]: value } }), null)
  }
  for (const field of Object.keys(rejection)) assert.throws(() => parseBudgetRejection({ ...rejection, [field]: undefined }))
})

test('strict request wire accepts complete optional budget facts and rejects partial facts', () => {
  const [row] = parseRequestRows([{ ...requestRow, ...rejection }])
  assert.deepEqual({ scope: row.scope, reason: row.reason, window_used: row.window_used, limit_amount: row.limit_amount, window_reset_at: row.window_reset_at }, rejection)
  assert.throws(() => parseRequestRows([{ ...requestRow, scope: 'tenant' }]), /旧标量|未定义|合同/)
  assert.throws(() => parseRequestRows([{ ...requestRow, ...rejection, private_provider_error: 'never-display' }]))
})

test('playground inference errors preserve only validated budget rejection details', () => {
  const error = inferenceRequestError('safe public error', { error: { code: 'budget_exceeded', ...rejection, private: 'never-display' } })
  assert.deepEqual(inferenceBudgetRejection(error), rejection)
  assert.equal(inferenceBudgetRejection(new Error('plain')), null)
  assert.equal(inferenceBudgetRejection(inferenceRequestError('bad', { error: { code: 'budget_exceeded', ...rejection, window_used: 'not-a-number' } })), null)
})
