import assert from 'node:assert/strict'
import test from 'node:test'
import { commerceKinds, commerceTime, commerceWrite, fundingReceiptsPath, fundingSourceKinds, fundingSourceLabel, parseCommerceCapabilities, parseCommercePage, parseCommerceReceipt, parseCommerceWriteResult, parseFundingReceipt, parseFundingReceiptsPage, parseFundingSummary, parseVoucherBatch, parseVoucherPolicy, parseVoucherRedemption, parseVoucherRevocation, voucherCode } from '../src/commerce.ts'
import { label, money } from '../src/format.ts'

const actor = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', checker = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', tenant = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', otherTenant = 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', operation = '11111111-1111-4111-8111-111111111111', batchID = '22222222-2222-4222-8222-222222222222', voucherID = '33333333-3333-4333-8333-333333333333', proposalID = '44444444-4444-4444-8444-444444444444'
const at = '2026-11-01T00:00:00.000001Z', expiry = '2026-11-08T00:00:00.000001Z'
const code = `mav1.${voucherID}.${Buffer.alloc(32, 17).toString('base64url')}`
function receipt(kind = 'voucher.batch.create', patch = {}) { const redeem = kind === 'voucher.redeem'; return { operation_id: operation, kind, scope: redeem ? tenant : 'platform', actor, status: 'accepted', reason: 'accepted', resource_id: batchID, amount: redeem ? '9007199254740993' : null, funding_receipt_id: redeem ? voucherID : null, journal_id: redeem ? proposalID : null, created_at: at, ...patch } }
function batch(patch = {}) { return { id: batchID, state: 'proposed', face_amount: '9007199254740993', count: 2, expires_at: expiry, purpose_ref: 'campaign:2026-11', reason: '限量试用', maker: actor, approver: null, activated_at: null, terminal_actor: null, terminal_at: null, revision: '9007199254740993', redeemed_count: '0', remaining_count: '2', created_at: at, expired: false, observed_at: at, ...patch } }
function proposal(patch = {}) { return { id: proposalID, batch_id: batchID, expected_revision: '9007199254740993', expected_redeemed_count: '1', reason: '停止发放', maker: actor, created_at: at, decision: null, ...patch } }
const draft = () => ({ face: '0.000001', count: '2', expires_at: expiry, purpose_ref: 'campaign:2026-11', reason: '测试赠额', code })

test('receipt matrix preserves exact >2^53 amounts and verifies identity, scope, kind and operation', () => {
  for (const kind of commerceKinds) {
    const value = receipt(kind)
    assert.deepEqual(parseCommerceReceipt(value, { operation_id: operation, actor, kind, scope: value.scope }), value)
    for (const patch of [{ actor: checker }, { operation_id: proposalID }, { kind: 'voucher.batch.void' }, { scope: 'platform' }]) {
      const expected = { operation_id: operation, actor, kind, scope: value.scope, ...patch }
      if (Object.entries(expected).some(([key, item]) => value[key] !== item)) assert.throws(() => parseCommerceReceipt(value, expected))
    }
  }
  for (const patch of [{ reason: 'ok' }, { amount: '1' }, { journal_id: voucherID }, { resource_id: null }, { created_at: '2026-11-01T00:00:00Z' }, { raw_code: code }]) assert.throws(() => parseCommerceReceipt(receipt('voucher.batch.create', patch)))
  for (const patch of [{ amount: 9007199254740992 }, { amount: '01' }, { amount: null }, { funding_receipt_id: null }]) assert.throws(() => parseCommerceReceipt(receipt('voucher.redeem', patch)))
})

test('HTTP200 rejected is a terminal rejection with all associations null and kind-specific reason', () => {
  const reasons = { 'voucher.batch.create': 'vouchers_disabled', 'voucher.batch.approve': 'voucher_revision_conflict', 'voucher.batch.void': 'voucher_state_conflict', 'voucher.revocation.create': 'voucher_evidence_stale', 'voucher.revocation.approve': 'voucher_evidence_stale', 'voucher.redeem': 'voucher_unavailable' }
  for (const kind of commerceKinds) {
    const value = receipt(kind, { status: 'rejected', reason: reasons[kind], resource_id: null, amount: null, funding_receipt_id: null, journal_id: null })
    assert.equal(parseCommerceReceipt(value).status, 'rejected')
    for (const patch of [{ resource_id: batchID }, { amount: '1' }, { reason: 'accepted' }, { reason: 'database_failed' }]) assert.throws(() => parseCommerceReceipt({ ...value, ...patch }))
  }
})

test('UTC microseconds remain strings and reject offsets, impossible dates and precision loss', () => {
  assert.equal(commerceTime(at), at)
  assert.equal(commerceTime('2026-11-01T00:00:00Z', false), '2026-11-01T00:00:00.000000Z')
  assert.equal(commerceTime('2026-11-01T00:00:00.000001Z', false), at)
  for (const value of ['2026-02-30T00:00:00.000000Z', '2026-11-01T00:00:00+08:00', '2026-11-01T00:00:00.0000001Z', '2026-11-01T00:00:60.000000Z', '2026-11-01T24:00:00.000000Z']) assert.throws(() => commerceTime(value, false))
})

test('voucher codes reject trimming, nil IDs and non-canonical 32-byte encoding', () => {
  assert.equal(voucherCode(code), code)
  for (const value of [` ${code}`, `${code}\n`, code + '=', code.toUpperCase(), code.replace(voucherID, '00000000-0000-0000-0000-000000000000'), code.slice(0, -1) + 'R']) assert.throws(() => voucherCode(value))
})

test('batch lifecycle checks nullable fields without turning expiry into a different state', () => {
  assert.equal(parseVoucherBatch(batch()).revision, '9007199254740993')
  const active = batch({ state: 'active', approver: checker, activated_at: at, expired: true, observed_at: expiry })
  assert.equal(parseVoucherBatch(active).state, 'active')
  assert.equal(parseVoucherBatch({ ...active, state: 'revoked', terminal_actor: checker, terminal_at: expiry }).state, 'revoked')
  assert.equal(parseVoucherBatch(batch({ state: 'voided', terminal_actor: actor, terminal_at: at })).state, 'voided')
  for (const patch of [{ approver: checker }, { terminal_at: at }, { remaining_count: '3' }, { expired: true }, { count: '2' }, { state: 'active' }, { state: 'voided', terminal_actor: checker, terminal_at: at }]) assert.throws(() => parseVoucherBatch(batch(patch)))
})

test('revocation uses immutable decision status and rejects maker as checker or invented proposal state', () => {
  assert.deepEqual(parseVoucherRevocation(proposal(), batchID), proposal())
  const decision = { id: voucherID, operation_id: operation, status: 'rejected', reason: 'voucher_evidence_stale', approver: checker, decided_at: at }
  assert.equal(parseVoucherRevocation(proposal({ decision })).decision.status, 'rejected')
  for (const patch of [{ state: 'pending' }, { batch_id: tenant }, { decision: { ...decision, approver: actor } }, { decision: { ...decision, reason: 'approved' } }, { decision: { ...decision, created_at: at } }]) assert.throws(() => parseVoucherRevocation(proposal(patch), batchID))
})

test('all six writes emit only frozen-schema fields and count/amount/expiry keep exact semantics', () => {
  const b = parseVoucherBatch(batch()), p = parseVoucherRevocation(proposal())
  assert.deepEqual(commerceWrite('voucher.batch.create', operation, draft(), null, null).body, { operation_id: operation, face_amount: '1', count: 2, expires_at: expiry, purpose_ref: 'campaign:2026-11', reason: '测试赠额' })
  assert.deepEqual(commerceWrite('voucher.batch.approve', operation, draft(), b, null).body, { operation_id: operation, expected_revision: '9007199254740993' })
  assert.deepEqual(commerceWrite('voucher.batch.void', operation, draft(), b, null).body, { operation_id: operation, expected_revision: '9007199254740993', reason: '测试赠额' })
  assert.deepEqual(commerceWrite('voucher.revocation.create', operation, draft(), b, null).body, { operation_id: operation, expected_revision: '9007199254740993', expected_redeemed_count: '0', reason: '测试赠额' })
  assert.deepEqual(commerceWrite('voucher.revocation.approve', operation, draft(), null, p).body, { operation_id: operation })
  assert.deepEqual(commerceWrite('voucher.redeem', operation, draft(), null, null).body, { operation_id: operation, code })
  for (const patch of [{ face: '0' }, { face: '9223372036854.775807' }, { count: '101' }, { count: '1e1' }, { purpose_ref: 'https://example.com' }, { reason: 'x\n' }, { reason: '中'.repeat(67) }]) assert.throws(() => commerceWrite('voucher.batch.create', operation, { ...draft(), ...patch }, null, null))
})

test('one-time code envelopes are validated; safe GET/replay never invents codes', () => {
  const expected = { operation_id: operation, kind: 'voucher.batch.create', actor, scope: 'platform' }, data = receipt(), b = batch({ count: 1, remaining_count: '1' })
  const issued = { data, batch: b, secrets_available: true, codes: [{ voucher_id: voucherID, code }], request_id: 'safe-request' }
  assert.equal(parseCommerceWriteResult(issued, expected).codes[0].code, code)
  assert.deepEqual(parseCommerceWriteResult({ data, batch: b, secrets_available: false, request_id: 'safe-request' }, expected).codes, [])
  for (const patch of [{ secrets_available: false }, { codes: [] }, { batch: batch() }, { codes: [{ voucher_id: batchID, code }] }, { code_digest: 'not-public' }]) assert.throws(() => parseCommerceWriteResult({ ...issued, ...patch }, expected))
  assert.throws(() => parseCommerceWriteResult({ data: receipt('voucher.redeem'), request_id: 'safe', codes: [code] }, { ...expected, kind: 'voucher.redeem', scope: tenant }))
})

test('funding sources preserve legacy TEXT identity and reject cross-tenant data', () => {
  const value = { id: voucherID, tenant_id: tenant, source_kind: 'manual_credit', source_id: 'legacy-credit:text-id', amount: '9007199254740993', currency: 'CNY', journal_id: proposalID, created_at: at }
  assert.equal(parseFundingReceipt(value, tenant).source_id, value.source_id)
  for (const patch of [{ tenant_id: otherTenant }, { source_kind: 'sandbox_capture' }, { currency: 'USD' }, { source_kind: 'voucher_grant' }, { source_kind: 'wallet_opening' }, { proof_ref: 'not-public' }]) assert.throws(() => parseFundingReceipt({ ...value, ...patch }, tenant))
})

test('synchronous funding summary validates NUMERIC totals without treating it as wallet availability', () => {
  const amount = '9'.repeat(90), opening = '9223372036854775807', value = { currency: 'CNY', total_amount: (BigInt(amount) + 9007199254740994n + BigInt(opening)).toString(), manual_credit_amount: amount, voucher_grant_amount: '1', sandbox_capture_amount: '9007199254740993', wallet_opening_amount: opening, as_of: at }
  assert.deepEqual(parseFundingSummary(value), value)
  for (const patch of [{ total_amount: amount }, { total_amount: 12 }, { total_amount: (BigInt(value.total_amount) - BigInt(opening)).toString() }, { voucher_grant_amount: '-1' }, { sandbox_capture_amount: '-1' }, { sandbox_capture_amount: 9007199254740993 }, { wallet_opening_amount: '-1' }, { wallet_opening_amount: '01' }, { wallet_opening_amount: 9007199254740993 }, { available: amount }, { currency: 'USD' }]) assert.throws(() => parseFundingSummary({ ...value, ...patch }))
  for (const field of ['sandbox_capture_amount', 'wallet_opening_amount']) {
    const incomplete = { ...value }; delete incomplete[field]
    assert.throws(() => parseFundingSummary(incomplete))
  }
  const zero = { ...value, total_amount: '0', manual_credit_amount: '0', voucher_grant_amount: '0', sandbox_capture_amount: '0', wallet_opening_amount: '0' }
  assert.deepEqual(parseFundingSummary(zero), zero)
})

test('opening receipts retain exact principal and identify a noncash opening rather than cash collection', () => {
  const value = { id: voucherID, tenant_id: tenant, source_kind: 'wallet_opening', source_id: batchID, amount: '9223372036854775807', currency: 'CNY', journal_id: proposalID, created_at: at }
  assert.deepEqual(parseFundingReceipt(value, tenant), value)
  assert.equal(money(parseFundingReceipt(value, tenant).amount), '¥9,223,372,036,854.775807')
  assert.equal(fundingSourceLabel(value.source_kind), '初始余额导入（非现金）')
  assert.equal(label('wallet_opening'), fundingSourceLabel(value.source_kind))
  for (const patch of [{ source_id: 'legacy:text-id' }, { source_id: '00000000-0000-0000-0000-000000000000' }, { amount: '9223372036854775808' }, { amount: 9007199254740993 }, { amount: '0' }, { external_ref: 'private-opening-reference' }, { refund_class: 'cash_original' }]) assert.throws(() => parseFundingReceipt({ ...value, ...patch }, tenant))
  assert.throws(() => parseFundingReceipt(value, otherTenant))
})

test('the existing receipt endpoint filters all four sources and refuses rows outside the chosen source or tenant', () => {
  assert.deepEqual(fundingSourceKinds, ['manual_credit', 'voucher_grant', 'sandbox_capture', 'wallet_opening'])
  const value = { id: voucherID, tenant_id: tenant, source_kind: 'wallet_opening', source_id: batchID, amount: '9007199254740993', currency: 'CNY', journal_id: proposalID, created_at: at }
  assert.equal(fundingReceiptsPath(), '/wallet/funding-receipts')
  assert.equal(fundingReceiptsPath('wallet_opening', 'a_B-c'), '/wallet/funding-receipts?source_kind=wallet_opening&cursor=a_B-c')
  assert.deepEqual(parseFundingReceiptsPage([value], null, tenant, 'wallet_opening'), { rows: [value], next: '' })
  assert.deepEqual(parseFundingReceiptsPage([value], null, tenant), { rows: [value], next: '' })
  for (const source_kind of fundingSourceKinds) {
    const row = { ...value, source_kind }
    assert.equal(new URL(fundingReceiptsPath(source_kind), 'https://console.test').searchParams.get('source_kind'), source_kind)
    assert.deepEqual(parseFundingReceiptsPage([row], null, tenant, source_kind).rows, [row])
    if (source_kind !== 'wallet_opening') assert.throws(() => parseFundingReceiptsPage([row], null, tenant, 'wallet_opening'))
  }
  assert.throws(() => parseFundingReceiptsPage([value], null, otherTenant, 'wallet_opening'))
  assert.throws(() => parseFundingReceiptsPage([value], 'same-cursor', tenant, 'wallet_opening', 'same-cursor'))
  for (const kind of ['cash', 'future_source', 'wallet_opening&tenant_id=private']) {
    assert.throws(() => fundingReceiptsPath(kind))
    assert.throws(() => parseFundingReceiptsPage([value], null, tenant, kind))
  }
  for (const cursor of ['bad?tenant_id=private', 'x'.repeat(1025)]) assert.throws(() => fundingReceiptsPath('wallet_opening', cursor))
})

test('sandbox sources have canonical capture identity and an explicit simulation label', () => {
  const value = { id: voucherID, tenant_id: tenant, source_kind: 'sandbox_capture', source_id: batchID, amount: '9007199254740993', currency: 'CNY', journal_id: proposalID, created_at: at }
  assert.deepEqual(parseFundingReceipt(value, tenant), value)
  assert.equal(fundingSourceLabel(value.source_kind), '本地模拟入账')
  assert.equal(fundingSourceLabel('manual_credit'), '人工核款')
  assert.equal(fundingSourceLabel('voucher_grant'), '运营赠额')
  for (const source_id of ['legacy:text-id', '00000000-0000-0000-0000-000000000000', 'AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA']) assert.throws(() => parseFundingReceipt({ ...value, source_id }, tenant))
  for (const source_kind of ['payment', 'cash', 'future_source']) {
    assert.throws(() => parseFundingReceipt({ ...value, source_kind }, tenant))
    assert.throws(() => fundingSourceLabel(source_kind))
  }
  assert.throws(() => parseFundingReceipt(value, otherTenant))
})

test('redemption detail requires redemption identity and tenant, never voucher or operation identity', () => {
  const value = { id: proposalID, operation_id: operation, tenant_id: tenant, voucher_id: voucherID, batch_id: batchID, actor, amount: '100', currency: 'CNY', funding_receipt_id: batchID, journal_id: voucherID, created_at: at }
  assert.deepEqual(parseVoucherRedemption(value, tenant, proposalID), value)
  assert.throws(() => parseVoucherRedemption(value, tenant, voucherID))
  assert.throws(() => parseVoucherRedemption(value, otherTenant, proposalID))
  assert.throws(() => parseVoucherRedemption({ ...value, code }, tenant, proposalID))
})

test('capabilities and policy fail closed on missing data without inventing off on DB errors', () => {
  assert.deepEqual(parseCommerceCapabilities({ vouchers_enabled: false, payments_mode: 'off' }), { vouchers_enabled: false, payments_mode: 'off' })
  assert.deepEqual(parseCommerceCapabilities({ vouchers_enabled: false, payments_mode: 'sandbox' }), { vouchers_enabled: false, payments_mode: 'sandbox' })
  assert.throws(() => parseCommerceCapabilities({ vouchers_enabled: false, payments_mode: 'live' }))
  assert.throws(() => parseCommerceCapabilities(null))
  assert.throws(() => parseVoucherPolicy({ enabled: true, revision: '1', effective_enabled: true, runtime_allowed: false }))
  assert.equal(parseVoucherPolicy({ enabled: false, revision: '9007199254740993', effective_enabled: false, runtime_allowed: false }).revision, '9007199254740993')
})

test('fixed 50-item pages require explicit null cursor and safe response identities', () => {
  assert.deepEqual(parseCommercePage([], null, parseVoucherBatch), { rows: [], next: '' })
  for (const next of [undefined, '', 0, 'url?private=1']) assert.throws(() => parseCommercePage([], next, parseVoucherBatch))
  assert.throws(() => parseCommercePage([batch(), batch()], null, parseVoucherBatch))
  assert.throws(() => parseCommercePage(Array.from({ length: 51 }, batch), null, parseVoucherBatch))
})
