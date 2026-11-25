import assert from 'node:assert/strict'
import test from 'node:test'
import { parseOperatingReport } from '../src/operating-report.ts'

const uuid = '11111111-1111-4111-8111-111111111111', schema = '33333333-3333-4333-8333-333333333333', digest = 'ab'.repeat(32)
const totals = { request_count: 1, unsettled_requests: 0, attempt_count: 1, unknown_provider_costs: 0, charged: '1', refunded: '0', net_revenue: '1', known_provider_cost: '0', gross_margin: '1', payment_basis: { CNY: '1' }, cash_refunded: {}, provider_cost: { CNY: '0' }, provisional_gross_margin: '1', bill_confirmed_gross_margin: '1', gross_margin_currency: 'CNY', margin_valuation: 'original_currency', cost_as_of: '2026-11-01T01:00:00Z', cost_revision: 'cost-revision', fx_policy_versions: [] }
const base = { from: '2026-11-01T00:00:00Z', to: '2026-11-02T00:00:00Z', as_of: '2026-11-02T00:00:00Z', currency: 'COIN', amount_unit: 'micro_coin', scope: 'request_created_window', summary: totals, models: [], channels: [], offer_reports: [] }
const row = { request_id: 'r', tenant_id: uuid, tenant_name: 't', model_id: uuid, model_name: 'm', status: 'succeeded', billing_status: 'settled', created_at: '2026-11-01T00:00:00Z', offer_version_id: '', task_class: '', ...totals }
const validUUID = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000'

test('operating response metadata matches the exhaustive 042 three-state matrix', () => {
  const profiles = ['', uuid, 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA', '00000000-0000-0000-0000-000000000000', 'x', null]
  const schemas = ['', schema, 'not-a-uuid', null], digests = ['', digest, 'AB'.repeat(32), null], variants = ['', 'default', 'other', null]
  const protocols = ['', 'openai_chat', 'openai_responses', 'anthropic_messages', 'openai_embeddings', 'jina_rerank', 'gemini_generate_content', 'image_generation', 'tts', 'video_generation', 'realtime_turn', 'unknown', null, ['openai_chat']]
  const executions = ['', 'synchronous', 'asynchronous', 'unknown', null, ['synchronous']]
  const knownProtocols = new Set(protocols.filter(value => typeof value === 'string' && value !== 'unknown'))
  let checked = 0
  for (const profile_version of profiles) for (const schema_id of schemas) for (const schema_digest of digests) for (const variant_key of variants) for (const protocol of protocols) for (const execution_kind of executions) {
    const absent = schema_id === '' && schema_digest === '' && variant_key === ''
    const media = ['image_generation', 'tts', 'video_generation', 'realtime_turn'].includes(protocol)
    const validEnums = (protocol === '' || knownProtocols.has(protocol)) && (execution_kind === '' || execution_kind === 'synchronous' || execution_kind === 'asynchronous')
    const profileValid = profile_version === '' || validUUID(profile_version)
    const full = validUUID(schema_id) && schema_digest === digest && variant_key === 'default'
    const expected = validEnums && profileValid && (media ? absent && validUUID(profile_version) : absent ? profile_version === '' : full && validUUID(profile_version))
    let actual = true
    try { parseOperatingReport({ ...base, requests: [{ ...row, profile_version, schema_id, schema_digest, variant_key, protocol, execution_kind }] }) } catch { actual = false }
    assert.equal(actual, expected, JSON.stringify({ profile_version, schema_id, schema_digest, variant_key, protocol, execution_kind, expected, actual }))
    checked++
  }
  assert.equal(checked, 37632)
})
