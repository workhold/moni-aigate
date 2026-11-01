import assert from 'node:assert/strict'
import test from 'node:test'
import { clearSession, loadMe, session } from '../src/session.ts'
import { request, setCsrf, stableOperationKey } from '../src/api.ts'

const me = id => ({ user: { id, email: id + '@example.test', platform_role: '', mfa_enabled: false }, tenant: null, tenants: [], csrf_token: 'csrf-' + id, mfa_required: true, enrollment_required: true })
function deferred() { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const response = data => new Response(JSON.stringify({ data, request_id: 'safe-request' }), { status: 200 })
const rejected = () => new Response(JSON.stringify({ error: { code: 'reauth_required', message: 'stale request' }, request_id: 'safe-request' }), { status: 401 })

test('same epoch joins in-flight reads; force refreshes a ready cache and still joins another force', async () => {
  const original = globalThis.fetch, first = deferred(), second = deferred(); let calls = 0
  globalThis.fetch = () => { calls++; return calls === 1 ? first.promise : second.promise }
  try {
    clearSession()
    const a = loadMe(true), b = loadMe(true)
    assert.equal(calls, 1); first.resolve(response(me('first')))
    assert.equal(await a, true); assert.equal(await b, true)
    assert.equal(await loadMe(), true); assert.equal(calls, 1)
    const c = loadMe(true), d = loadMe(true)
    assert.equal(calls, 2); second.resolve(response(me('second')))
    assert.equal(await c, true); assert.equal(await d, true); assert.equal(session.me.user.id, 'second')
  } finally { globalThis.fetch = original; clearSession() }
})

test('clearSession invalidates old success, CSRF, MFA flags and operation-key cache', async () => {
  const original = globalThis.fetch, old = deferred(); let captured
  globalThis.fetch = path => { assert.equal(path, '/api/me'); return old.promise }
  try {
    clearSession(); setCsrf('old-csrf')
    const key = stableOperationKey('/path', { value: 1 }), pending = loadMe(true)
    clearSession(); old.resolve(response(me('old')))
    assert.equal(await pending, false)
    assert.deepEqual({ me: session.me, ready: session.ready, mfa: session.mfaPending, enrollment: session.enrollmentRequired }, { me: null, ready: true, mfa: false, enrollment: false })
    assert.notEqual(stableOperationKey('/path', { value: 1 }), key)
    globalThis.fetch = async (_, init) => { captured = init; return response({}) }
    await request('/probe', 'POST', {})
    assert.equal(captured.headers['X-CSRF-Token'], undefined)
  } finally { globalThis.fetch = original; clearSession() }
})

test('old 401 catch/finally cannot emit private events, clear new state or remove a new in-flight read', async () => {
  const original = globalThis.fetch, originalWindow = globalThis.window, old = deferred(), next = deferred(), events = []; let calls = 0
  globalThis.window = { dispatchEvent: event => events.push(event.type) }
  globalThis.fetch = () => { calls++; return calls === 1 ? old.promise : next.promise }
  try {
    clearSession(); const stale = loadMe(true); clearSession(); const fresh = loadMe(true)
    old.resolve(rejected()); assert.equal(await stale, false)
    assert.deepEqual(events, [])
    const joined = loadMe(true); assert.equal(calls, 2)
    assert.equal(session.ready, true)
    next.resolve(response(me('new')))
    assert.equal(await fresh, true); assert.equal(await joined, true); assert.equal(session.me.user.id, 'new')
  } finally { globalThis.fetch = original; globalThis.window = originalWindow; clearSession() }
})

test('late success or network failure cannot overwrite a completed new identity', async () => {
  const original = globalThis.fetch
  try {
    for (const failure of [false, true]) {
      const old = deferred(), next = deferred(); let calls = 0
      globalThis.fetch = () => ++calls === 1 ? old.promise : next.promise
      clearSession(); const stale = loadMe(true); clearSession(); const fresh = loadMe(true)
      next.resolve(response(me('fresh'))); assert.equal(await fresh, true)
      if (failure) old.reject(new Error('network')); else old.resolve(response(me('stale')))
      assert.equal(await stale, false); assert.equal(session.me.user.id, 'fresh'); assert.equal(session.mfaPending, true); assert.equal(session.enrollmentRequired, true)
      let captured; globalThis.fetch = async (_, init) => { captured = init; return response({}) }
      await request('/probe', 'POST', {}); assert.equal(captured.headers['X-CSRF-Token'], 'csrf-fresh')
    }
  } finally { globalThis.fetch = original; clearSession() }
})

test('current-epoch 401 retains existing private event behavior', async () => {
  const original = globalThis.fetch, originalWindow = globalThis.window, events = []
  globalThis.window = { dispatchEvent: event => events.push(event.type) }
  globalThis.fetch = async () => rejected()
  try {
    clearSession(); assert.equal(await loadMe(true), false)
    assert.deepEqual(events, ['moni:unauthenticated', 'moni:reauth'])
    assert.equal(session.me, null)
  } finally { globalThis.fetch = original; globalThis.window = originalWindow; clearSession() }
})
