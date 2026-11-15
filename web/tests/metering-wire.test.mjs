import assert from 'node:assert/strict'
import test from 'node:test'
import { parseMeterSnapshotProjection, parseTextBounds, parseTextQuantityLines, parseTextQuantities, parseTextRetailQuantitiesWire, ratePerMillionText } from '../src/metering-wire.ts'
import { parseTextRetailRates } from '../src/pricing.ts'

const schema = '10000000-0000-4000-8000-000000000001'
const rates = { schema_id: schema, version: '1', side: 'retail', currency: 'CNY', rates: [{ dimension: 'text_input_token', unit: 'token', numerator: '1', denominator: '3' }, { dimension: 'text_output_token', unit: 'token', numerator: '0', denominator: '1' }] }
const bounds = { schema_id: schema, version: '1', side: 'retail', bounds: [{ dimension: 'text_input_token', unit: 'token', quantity: '9007199254740993' }, { dimension: 'text_output_token', unit: 'token', quantity: '0' }] }
const quantities = [{ dimension: 'text_input_token', unit: 'token', status: 'confirmed_final', quantity: '9007199254740993' }, { dimension: 'text_output_token', unit: 'token', status: 'missing', quantity: null }]

test('bounds and quantities keep complete ordered wires and bigint strings', () => {
  assert.equal(parseTextBounds(bounds, schema, 'retail').bounds[0].quantity, '9007199254740993')
  assert.deepEqual(parseTextQuantityLines(quantities, schema), quantities)
  assert.deepEqual(parseTextQuantities({ schema_id: schema, version: '1', side: 'retail', quantities }, schema, 'retail').quantities, quantities)
  for (const bad of [
    { ...bounds, bounds: [...bounds.bounds].reverse() },
    { ...bounds, bounds: [{ ...bounds.bounds[0], quantity: 1 }, bounds.bounds[1]] },
    { ...bounds, input_bound: '1' },
  ]) assert.throws(() => parseTextBounds(bad, schema, 'retail'))
  assert.throws(() => parseTextQuantityLines([{ ...quantities[0], quantity: null }, quantities[1]], schema))
  assert.throws(() => parseTextQuantityLines([{ ...quantities[0], status: 'confirmed_final', quantity: '01' }, quantities[1]], schema))
})

test('snapshot projection has one complete vector identity and no scalar fallback', () => {
  const projection = parseMeterSnapshotProjection({ schema_id: schema, schema_digest: 'ab'.repeat(32), variant_key: 'default', retail_rates: rates, retail_bounds: bounds, usage_status: 'confirmed_final', retail_quantities: quantities })
  assert.equal(projection.retail_rates.rates[0].denominator, '3')
  assert.throws(() => parseMeterSnapshotProjection({ schema_id: schema, schema_digest: 'ab'.repeat(32), variant_key: 'default', retail_rates: rates, retail_bounds: bounds, usage_status: 'confirmed_final', retail_quantities: null }))
  assert.throws(() => parseMeterSnapshotProjection({ schema_id: schema, schema_digest: 'ab'.repeat(32), variant_key: 'default', retail_rates: rates, retail_bounds: bounds, usage_status: 'confirmed_final', retail_quantities: quantities, input_tokens: 1 }))
})

test('rate display is exact for fractions and does not convert to Number', () => {
  const parsed = parseTextRetailRates(rates, schema)
  assert.equal(ratePerMillionText(parsed, 'text_input_token'), '1000000/3 微币 / 百万 Tokens')
  assert.equal(ratePerMillionText(parsed, 'text_output_token'), '0.000000 币 / 百万 Tokens')
})

test('manual reconciliation accepts only the complete retail QuantitiesWire for the original schema', () => {
  const wire = { schema_id: schema, version: '1', side: 'retail', quantities: [{ dimension: 'text_input_token', unit: 'token', quantity: '9007199254740993' }, { dimension: 'text_output_token', unit: 'token', quantity: '0' }] }
  assert.deepEqual(parseTextRetailQuantitiesWire(wire, schema), wire)
  for (const patch of [{ side: 'supplier' }, { schema_id: '10000000-0000-4000-8000-000000000002' }, { input_tokens: '1' }, { output_tokens: '0' }, { quantities: [{ ...wire.quantities[0], status: 'confirmed_final' }, wire.quantities[1]] }, { quantities: [...wire.quantities].reverse() }, { quantities: [wire.quantities[0]] }]) assert.throws(() => parseTextRetailQuantitiesWire({ ...wire, ...patch }, schema))
})
