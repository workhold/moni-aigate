import assert from 'node:assert/strict'
import test from 'node:test'
import { completeIdentityProof, fetchIdentityCapabilities, identityAcceptedMessage, identityCompletionBody, identityEmail, identityErrorText, identityPassword, identityTenantName, identityUnavailableText, identityUnknownResult, IdentityPublicError, parseIdentityAccepted, parseIdentityCapabilities, parseIdentityCompleted, requestIdentityEmail } from '../src/identity-public.ts'

const token = 'ic1_' + Buffer.alloc(32, 17).toString('base64url')
const enabled = { email_delivery_configured: true, registration: { available: true, reason: 'available' }, password_reset: { available: true, reason: 'available' } }
const envelope = data => ({ data, request_id: 'request-safe' })
const response = (data, status = 200) => new Response(JSON.stringify(envelope(data)), { status })

test('capability DTO uses exact safe keys and consistent availability reasons', () => {
  assert.deepEqual(parseIdentityCapabilities(enabled), enabled)
  const disabled = { email_delivery_configured: false, registration: { available: false, reason: 'feature_disabled' }, password_reset: { available: false, reason: 'mail_not_configured' } }
  assert.deepEqual(parseIdentityCapabilities(disabled), disabled)
  for (const value of [
    { ...enabled, smtp_host: 'private-canary' }, { ...enabled, email_delivery_configured: false },
    { ...enabled, registration: { available: false, reason: 'available' } }, { ...enabled, registration: { available: true, reason: 'available', user_exists: true } },
    { ...enabled, registration: { available: false, reason: 'mail_not_configured' } },
    { ...enabled, password_reset: { available: false, reason: 'setup_required' } },
    { ...disabled, registration: { available: false, reason: 'setup_required' } },
  ]) assert.throws(() => parseIdentityCapabilities(value), error => !error.message.includes('private-canary'))
  for (const reason of ['feature_disabled', 'mail_not_configured', 'setup_required', 'recovery_mode']) assert.ok(identityUnavailableText({ available: false, reason }).length)
})

test('email normalization retains plus tags and dots while rejecting display names and controls', () => {
  assert.equal(identityEmail('  A.User+Tag@Example.COM  '), 'a.user+tag@example.com')
  assert.equal(identityEmail('a'.repeat(241) + '@example.test').length, 254)
  for (const value of ['', 'a@@example.test', 'User <a@example.test>', 'a@example.test,b@example.test', 'ü@example.test', 'a b@example.test', 'a\r\nb@example.test', 'a'.repeat(242) + '@example.test']) assert.throws(() => identityEmail(value))
})

test('password and tenant byte boundaries follow UTF-8 without trimming passwords', () => {
  assert.equal(identityPassword(' 1234567890 '), ' 1234567890 ')
  assert.equal(identityPassword('中'.repeat(24)), '中'.repeat(24))
  assert.equal(identityPassword('中'.repeat(4)), '中'.repeat(4))
  for (const value of ['a'.repeat(11), 'a'.repeat(73), '中'.repeat(25)]) assert.throws(() => identityPassword(value))
  assert.equal(identityTenantName('  工作空间  '), '工作空间')
  assert.equal(identityTenantName('中'.repeat(66) + 'ab').length, 68)
  for (const value of ['  ', 'a'.repeat(201), '中'.repeat(67), 'a\nb', 'a\u0000b']) assert.throws(() => identityTenantName(value))
})

test('completion DTOs stay fixed, immutable and include required empty mfa_code', () => {
  const registration = identityCompletionBody('registration', token, { password: 'valid-password', tenant_name: ' Team ', mfa_code: 'not-for-registration' })
  assert.deepEqual(registration, { token, password: 'valid-password', tenant_name: 'Team' }); assert.ok(Object.isFrozen(registration))
  assert.deepEqual(identityCompletionBody('password_reset', token, { password: 'valid-password' }), { token, password: 'valid-password', mfa_code: '' })
  assert.equal(identityCompletionBody('password_reset', token, { password: 'valid-password', mfa_code: '  Recovery-code_123  ' }).mfa_code, 'Recovery-code_123')
  assert.throws(() => identityCompletionBody('password_reset', token, { password: 'valid-password', mfa_code: 'a\nb' }))
  assert.throws(() => identityCompletionBody('password_reset', 'canary-secret', { password: 'valid-password' }), error => !error.message.includes('canary-secret'))
})

test('MFA/recovery input follows the frozen 128 UTF-8 byte limit before network dispatch', async () => {
  const original = globalThis.fetch; let calls = 0
  globalThis.fetch = async () => { calls++; return response({ completed: true, login_required: true }) }
  try {
    for (const code of ['a'.repeat(128), '中'.repeat(42) + 'ab', '']) {
      const body = identityCompletionBody('password_reset', token, { password: 'valid-password', mfa_code: code })
      assert.equal(body.mfa_code, code)
      await completeIdentityProof('password_reset', body)
    }
    assert.equal(calls, 3)
    for (const code of ['a'.repeat(129), '中'.repeat(43), '中'.repeat(42) + 'abc']) {
      assert.throws(() => identityCompletionBody('password_reset', token, { password: 'valid-password', mfa_code: code }))
      assert.throws(() => completeIdentityProof('password_reset', { token, password: 'valid-password', mfa_code: code }))
    }
    assert.equal(calls, 3, 'oversized input must be rejected before dispatching a proof consumption')
  } finally { globalThis.fetch = original }
})

test('accepted/completed DTOs cannot silently grant sessions or accept false success', () => {
  assert.deepEqual(parseIdentityAccepted({ accepted: true }), { accepted: true })
  assert.deepEqual(parseIdentityCompleted({ completed: true, login_required: true }), { completed: true, login_required: true })
  for (const value of [{ accepted: false }, { accepted: true, account_exists: true }, { accepted: true, token }]) assert.throws(() => parseIdentityAccepted(value))
  for (const value of [{ completed: true, login_required: false }, { completed: true }, { completed: true, login_required: true, session: 'private' }]) assert.throws(() => parseIdentityCompleted(value))
  assert.equal(identityAcceptedMessage, '请求已接受；如果该邮箱符合条件，稍后会收到邮件。')
})

test('anonymous GET/POST omit credentials, CSRF, authorization and private events; each explicit action sends once', async () => {
  const previousFetch = globalThis.fetch, previousWindow = globalThis.window, calls = [], events = []
  globalThis.window = { dispatchEvent: event => events.push(event.type) }
  globalThis.fetch = async (path, init) => { calls.push({ path, init }); return response(path.endsWith('capabilities') ? enabled : path.endsWith('request') ? { accepted: true } : { completed: true, login_required: true }, path.endsWith('request') ? 202 : 200) }
  try {
    await fetchIdentityCapabilities()
    await requestIdentityEmail('registration', ' A.User+tag@Example.TEST ')
    await requestIdentityEmail('password_reset', 'a@example.test')
    await completeIdentityProof('registration', { token, password: 'valid-password', tenant_name: 'Team' })
    await completeIdentityProof('password_reset', { token, password: 'valid-password', mfa_code: '' })
    assert.deepEqual(calls.map(call => call.path), ['/api/auth/capabilities', '/api/auth/registration/request', '/api/auth/password-reset/request', '/api/auth/registration/complete', '/api/auth/password-reset/complete'])
    for (const { init } of calls) { assert.equal(init.credentials, 'omit'); assert.equal(init.cache, 'no-store'); assert.equal(init.redirect, 'error'); assert.equal(init.headers.Authorization, undefined); assert.equal(init.headers['X-CSRF-Token'], undefined); assert.equal(init.headers['Idempotency-Key'], undefined) }
    assert.deepEqual(calls[0].init.headers, { Accept: 'application/json' }); assert.equal(calls[0].init.body, undefined)
    assert.deepEqual(JSON.parse(calls[1].init.body), { email: 'a.user+tag@example.test' })
    assert.deepEqual(JSON.parse(calls[4].init.body), { token, password: 'valid-password', mfa_code: '' })
    assert.deepEqual(events, [])
  } finally { globalThis.fetch = previousFetch; globalThis.window = previousWindow }
})

test('reset transport rejects missing/non-string mfa_code and accidental extra fields before dispatch', async () => {
  const original = globalThis.fetch; let calls = 0
  globalThis.fetch = async () => { calls++; return response({ completed: true, login_required: true }) }
  try {
    for (const body of [{ token, password: 'valid-password' }, { token, password: 'valid-password', mfa_code: null }, { token, password: 'valid-password', mfa_code: '', email: 'a@example.test' }]) assert.throws(() => completeIdentityProof('password_reset', body))
    assert.equal(calls, 0)
  } finally { globalThis.fetch = original }
})

test('error bodies never expose account/factor hints or raw messages and never fire private events', async () => {
  const original = globalThis.fetch, originalWindow = globalThis.window, events = []
  globalThis.window = { dispatchEvent: event => events.push(event.type) }
  try {
    for (const status of [400, 401, 403, 429, 503]) {
      globalThis.fetch = async () => new Response(JSON.stringify({ error: { code: 'identity_proof_invalid', message: 'account-exists-private-canary' }, request_id: 'request-safe' }), { status })
      await assert.rejects(completeIdentityProof('password_reset', { token, password: 'valid-password', mfa_code: '' }), error => error instanceof IdentityPublicError && !identityErrorText(error).includes('private-canary') && identityUnknownResult(error) === (status >= 500))
    }
    assert.deepEqual(events, [])
    assert.equal(identityErrorText(new Error('canary-secret')).includes('canary-secret'), false)
  } finally { globalThis.fetch = original; globalThis.window = originalWindow }
})

test('network, 5xx, malformed success and unexpected status are unknown with no automatic retry', async () => {
  const original = globalThis.fetch
  try {
    for (const implementation of [
      async () => { throw new Error('secret-network') },
      async () => new Response('<html>secret</html>', { status: 503 }),
      async () => response({ completed: true, login_required: true, token }),
      async () => response({ completed: true, login_required: true }, 201),
      async () => new Response(JSON.stringify({ ...envelope({ completed: true, login_required: true }), csrf_token: 'secret' })),
    ]) {
      let calls = 0; globalThis.fetch = async (...args) => { calls++; return implementation(...args) }
      await assert.rejects(completeIdentityProof('password_reset', { token, password: 'valid-password', mfa_code: '' }), error => identityUnknownResult(error) && !identityErrorText(error).includes('secret'))
      assert.equal(calls, 1)
    }
  } finally { globalThis.fetch = original }
})
