import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError, clearOperationKeys, request, setCsrf, stableOperationKey } from '../src/api.ts'

test('funding writes send same-origin session, CSRF and stable idempotency; conflicts do not retry', async () => {
  const originalFetch = globalThis.fetch
  const calls = []
  globalThis.fetch = async (url, options) => { calls.push({ url, options }); return new Response(JSON.stringify({ error: { code: 'idempotency_conflict', message: '业务内容冲突' }, request_id: 'req-test' }), { status: 409 }) }
  try {
    setCsrf('csrf-test')
    const body = { tenant_id: 'tenant-test', amount: '9007199254740993', business_ref: 'bank-unique' }
    const key = stableOperationKey('/platform/credits', body)
    assert.equal(stableOperationKey('/platform/credits', body), key)
    await assert.rejects(request('/platform/credits', 'POST', body, key), error => error instanceof ApiError && error.code === 'idempotency_conflict' && error.requestId === 'req-test')
    assert.equal(calls.length, 1)
    assert.equal(calls[0].url, '/api/platform/credits')
    assert.equal(calls[0].options.credentials, 'include')
    assert.equal(calls[0].options.headers['X-CSRF-Token'], 'csrf-test')
    assert.equal(calls[0].options.headers['Idempotency-Key'], key)
    assert.equal(JSON.parse(calls[0].options.body).amount, body.amount)
    clearOperationKeys()
    assert.notEqual(stableOperationKey('/platform/credits', body), key, 'a different signed-in actor must not inherit a prior actor operation key')
  } finally { globalThis.fetch = originalFetch; setCsrf('') }
})
test('invalid upstream response is not exposed as raw HTML', async () => {
  const originalFetch = globalThis.fetch
  globalThis.fetch = async () => new Response('<html>private endpoint or database error</html>', { status: 502 })
  try { await assert.rejects(request('/wallet'), error => error instanceof ApiError && error.code === 'invalid_response' && !error.message.includes('private')) }
  finally { globalThis.fetch = originalFetch }
})
