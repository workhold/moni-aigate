import assert from 'node:assert/strict'
import test from 'node:test'
import { parseRequestRows } from '../src/request-wire.ts'

const schema = '10000000-0000-4000-8000-000000000001'
const base = {
  id: '11111111-1111-4111-8111-111111111111', model_id: '22222222-2222-4222-8222-222222222222', model_name: 'model',
  status: 'completed', billing_status: 'settled', schema_id: schema, schema_digest: 'ab'.repeat(32), variant_key: 'default',
  retail_rates: { schema_id: schema, version: '1', side: 'retail', currency: 'CNY', rates: [{ dimension: 'text_input_token', unit: 'token', numerator: '1', denominator: '1' }, { dimension: 'text_output_token', unit: 'token', numerator: '0', denominator: '1' }] },
  retail_bounds: { schema_id: schema, version: '1', side: 'retail', bounds: [{ dimension: 'text_input_token', unit: 'token', quantity: '10' }, { dimension: 'text_output_token', unit: 'token', quantity: '10' }] },
  usage_status: 'confirmed_final', retail_quantities: [{ dimension: 'text_input_token', unit: 'token', status: 'confirmed_final', quantity: '9' }, { dimension: 'text_output_token', unit: 'token', status: 'confirmed_final', quantity: '0' }],
}

test('request rows project only the current vector contract and preserve complete RatesWire', () => {
  const [row] = parseRequestRows([{ ...base, final_local_delivery: 'content', private_provider_error: 'never-return' }].map(({ private_provider_error, ...value }) => value))
  assert.equal(row.delivery_stage, 'content')
  assert.equal(row.retail_rates.rates[0].numerator, '1')
  assert.equal('private_provider_error' in row, false)
})

test('request rows reject old usage, scalar rates and unknown response fields before projection', () => {
  for (const field of ['input_tokens', 'output_tokens', 'input_rate', 'output_rate', 'retail_snapshot_status', 'legacy', 'supplier_rates']) {
    assert.throws(() => parseRequestRows([{ ...base, [field]: '1' }]), /旧标量|未定义/)
  }
  assert.throws(() => parseRequestRows([{ ...base, retail_rates: { ...base.retail_rates, rates: [{ ...base.retail_rates.rates[0], numerator: '01' }, base.retail_rates.rates[1]] } }]))
})
