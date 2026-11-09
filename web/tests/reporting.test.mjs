import assert from 'node:assert/strict'
import test from 'node:test'
import { parseProviderCostRows, parseReportSummary, providerCostText } from '../src/reporting.ts'

test('unknown provider cost is never rendered as free, including populated or unsupported facts', () => {
  for (const [amount, status] of [[null, 'unknown'], ['0', 'unknown'], ['123', 'estimated'], ['123', 'future_status'], [undefined, 'invoice'], ['', 'contract']]) {
    assert.equal(providerCostText(amount, status), '待核对')
  }
  assert.equal(providerCostText('0', 'invoice'), '¥0.000000')
  assert.equal(providerCostText('9007199254740993', 'contract'), '¥9,007,199,254.740993')
})

test('summary and provider-cost responses reject scalar leakage and unknown fields', () => {
  const summary = { tenant_id: '', credited: '1', charged: '2', refunded: '0', released: '0', requests: 1, projection_pending: 0, as_of: '2026-11-02T01:00:00Z' }
  assert.equal(parseReportSummary(summary, false).charged, '2')
  assert.throws(() => parseReportSummary({ ...summary, input_tokens: 1 }, false), /旧标量|未定义/)
  const cost = { id: 'a', tenant_id: 'b', tenant_name: 'tenant', request_id: 'c', channel_id: 'd', channel_name: 'channel', upstream_model: 'model', cost_amount: '3', cost_status: 'invoice', cost_version_id: 'e', cost_basis: 'invoice', created_at: '2026-11-02T01:00:00Z' }
  assert.equal(parseProviderCostRows([cost])[0].cost_amount, '3')
  assert.throws(() => parseProviderCostRows([{ ...cost, input_rate: '1' }]), /旧标量|未定义/)
})
