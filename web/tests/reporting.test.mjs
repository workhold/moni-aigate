import assert from 'node:assert/strict'
import test from 'node:test'
import { providerCostText } from '../src/reporting.ts'

test('unknown provider cost is never rendered as free, including populated or unsupported facts', () => {
  for (const [amount, status] of [[null, 'unknown'], ['0', 'unknown'], ['123', 'estimated'], ['123', 'future_status'], [undefined, 'invoice'], ['', 'contract']]) {
    assert.equal(providerCostText(amount, status), '待核对')
  }
  assert.equal(providerCostText('0', 'invoice'), '¥0.000000')
  assert.equal(providerCostText('9007199254740993', 'contract'), '¥9,007,199,254.740993')
})
