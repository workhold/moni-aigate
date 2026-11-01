import assert from 'node:assert/strict'
import test from 'node:test'
import { request, setCsrf } from '../src/api.ts'
import { commerceWrite, parseCommerceWriteResult } from '../src/commerce.ts'
import { createCommerceJournal } from '../src/commerce-state.ts'

const actor = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', tenant = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', operation = '11111111-1111-4111-8111-111111111111', voucherID = '33333333-3333-4333-8333-333333333333', at = '2026-11-01T00:00:00.000001Z'
const code = `mav1.${voucherID}.${Buffer.alloc(32, 17).toString('base64url')}`
function fixture() {
  const values = new Map(), adapter = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }
  const journal = createCommerceJournal(() => adapter), command = journal.begin(actor, { operation_id: operation, kind: 'voucher.redeem', scope: tenant, resource_id: null, created_at: at })
  const write = commerceWrite('voucher.redeem', operation, { code }, null, null), expected = { operation_id: operation, kind: 'voucher.redeem', scope: tenant, actor }
  return { values, journal, command, write, expected }
}
function receipt(status = 'rejected') { const accepted = status === 'accepted'; return { operation_id: operation, kind: 'voucher.redeem', scope: tenant, actor, status, reason: accepted ? 'accepted' : 'voucher_unavailable', resource_id: accepted ? operation : null, amount: accepted ? '1000000' : null, funding_receipt_id: accepted ? voucherID : null, journal_id: accepted ? voucherID : null, created_at: at } }

test('session/CSRF POST sends the precise redeem body once and 200 rejected remains no-credit terminal', async () => {
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window
  globalThis.window = new EventTarget()
  const calls = [], data = receipt(), f = fixture(); setCsrf('csrf-current')
  globalThis.fetch = async (path, options) => { calls.push({ path, options }); return new Response(JSON.stringify({ data, request_id: 'safe' }), { status: 200, headers: { 'Content-Type': 'application/json' } }) }
  try {
    const result = parseCommerceWriteResult(await request(f.write.path, 'POST', f.write.body), f.expected)
    assert.equal(f.journal.settle(actor, f.command, result.receipt).status, 'rejected')
    assert.equal(calls.length, 1)
    assert.equal(calls[0].path, '/api/wallet/voucher-redemptions')
    assert.equal(calls[0].options.credentials, 'include')
    assert.equal(calls[0].options.headers['X-CSRF-Token'], 'csrf-current')
    assert.equal(calls[0].options.headers['Idempotency-Key'], undefined)
    assert.deepEqual(JSON.parse(calls[0].options.body), { operation_id: operation, code })
    assert.deepEqual(f.journal.list(actor, tenant), [])
    assert.doesNotMatch([...f.values.values()].join(''), /mav1\.|code|fingerprint/)
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow; setCsrf('') }
})

test('network unknown followed by GET404 cannot replay; later GET receipt alone resolves the same original ID', async () => {
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window
  globalThis.window = new EventTarget()
  const f = fixture(), calls = []; let stage = 0
  globalThis.fetch = async (path, options) => {
    calls.push({ path, method: options.method })
    if (stage++ === 0) throw new Error('lost response after commit')
    if (stage === 2) return new Response(JSON.stringify({ error: { code: 'not_found', message: '尚未查到' }, request_id: 'safe' }), { status: 404 })
    return new Response(JSON.stringify({ data: receipt('accepted'), request_id: 'safe' }), { status: 200 })
  }
  try {
    await assert.rejects(request(f.write.path, 'POST', f.write.body))
    const path = `/wallet/commerce-operations/${operation}`
    await assert.rejects(request(path))
    assert.equal(f.journal.list(actor, tenant).length, 1)
    assert.throws(() => f.journal.begin(actor, { ...f.command, operation_id: voucherID }))
    assert.equal(f.journal.settle(actor, f.command, (await request(path)).data).status, 'accepted')
    assert.deepEqual(calls.map(call => call.method), ['POST', 'GET', 'GET'])
    assert.equal(calls[1].path, `/api${path}`)
    assert.equal(calls[2].path, calls[1].path)
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow }
})
