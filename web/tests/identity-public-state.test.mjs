import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { createIdentityAction, createIdentityCapabilitiesReader } from '../src/identity-public-state.ts'
import { IdentityPublicError } from '../src/identity-public.ts'
import { isAnonymousLayout } from '../src/public-layout.ts'

const token = 'ic1_' + Buffer.alloc(32, 17).toString('base64url')
const proof = purpose => ({ purpose, token, path: purpose === 'registration' ? '/register/complete' : '/password/reset' })
const body = { password: 'valid-password-canary', tenant_name: 'Team', mfa_code: '' }
const capabilities = { email_delivery_configured: true, registration: { available: true, reason: 'available' }, password_reset: { available: true, reason: 'available' } }

test('mail request requires an explicit begin, blocks duplicate pending/accepted sends and resets only explicitly', () => {
  const action = createIdentityAction('registration')
  assert.equal(action.snapshot().phase, 'ready')
  assert.throws(() => action.beginRequest('a@example.test', false))
  assert.equal(action.snapshot().phase, 'ready')
  const first = action.beginRequest(' A.User+Tag@EXAMPLE.test ', true)
  assert.equal(first.email, 'a.user+tag@example.test')
  assert.equal(action.snapshot().phase, 'submitting')
  assert.throws(() => action.beginRequest('a@example.test', true)); assert.throws(() => action.newMailRequest())
  assert.equal(action.accept(first.ticket), true); assert.equal(action.snapshot().phase, 'accepted')
  assert.throws(() => action.beginRequest('a@example.test', true))
  action.newMailRequest(); assert.equal(action.snapshot().phase, 'ready')
  const second = action.beginRequest('a@example.test', true)
  assert.notEqual(second.ticket.epoch, first.ticket.epoch)
  assert.equal(action.fail(first.ticket, new Error('late')), false)
  assert.equal(action.snapshot().phase, 'submitting')
})

test('completion requires matching path/purpose and never stores proof/password in safe state', () => {
  for (const value of [undefined, proof('registration'), { ...proof('password_reset'), path: '/register/complete' }]) {
    const missing = createIdentityAction('password_reset', value, true)
    assert.equal(missing.snapshot().phase, 'missing_proof')
    assert.throws(() => missing.beginCompletion(body, true))
  }
  const action = createIdentityAction('password_reset', proof('password_reset'), true)
  assert.equal(action.snapshot().phase, 'ready')
  const submission = action.beginCompletion(body, true)
  assert.ok(Object.isFrozen(submission.body)); assert.deepEqual(submission.body, { token, password: body.password, mfa_code: '' })
  const safe = JSON.stringify(action.snapshot())
  assert.equal(safe.includes(token), false); assert.equal(safe.includes(body.password), false)
  assert.throws(() => action.beginCompletion(body, true))
})

test('known proof rejection permits manual correction, while success consumes local proof', () => {
  const action = createIdentityAction('password_reset', proof('password_reset'), true)
  const first = action.beginCompletion(body, true)
  assert.equal(action.fail(first.ticket, new IdentityPublicError('identity_proof_invalid', '证明无效或已过期。', 400)), true)
  assert.equal(action.snapshot().phase, 'proof_invalid')
  const retry = action.beginCompletion({ ...body, mfa_code: 'recovery-code' }, true)
  assert.equal(retry.body.token, token)
  assert.equal(action.accept(retry.ticket), true); assert.equal(action.snapshot().phase, 'completed')
  assert.throws(() => action.beginCompletion(body, true))
  assert.throws(() => action.newMailRequest())
  assert.equal(action.fail(retry.ticket, new Error('late')), false)
})

test('unknown completion clears proof and cannot replay, silently issue mail or auto login', () => {
  for (const error of [new Error('private-canary'), new IdentityPublicError('network_error', '结果未知。', 0), new IdentityPublicError('identity_unavailable', '暂停。', 503), new IdentityPublicError('invalid_response', '未知。', 200)]) {
    const action = createIdentityAction('registration', proof('registration'), true), submission = action.beginCompletion(body, true)
    assert.equal(action.fail(submission.ticket, error), true)
    assert.equal(action.snapshot().phase, 'unknown')
    assert.equal(JSON.stringify(action.snapshot()).includes('private-canary'), false)
    assert.throws(() => action.beginCompletion(body, true)); assert.throws(() => action.newMailRequest())
    assert.equal(action.accept(submission.ticket), false)
  }
})

test('explicit 4xx rejection is user-retryable and capability pause does not revoke an in-memory proof', () => {
  const action = createIdentityAction('password_reset', proof('password_reset'), true)
  assert.throws(() => action.beginCompletion(body, false)); assert.equal(action.snapshot().phase, 'ready')
  const first = action.beginCompletion(body, true)
  action.fail(first.ticket, new IdentityPublicError('rate_limited', '稍后提交。', 429))
  assert.equal(action.snapshot().phase, 'error')
  const second = action.beginCompletion(body, true); assert.equal(second.body.token, token)
})

test('route, identity and unmount disposal invalidate pending results and clear a proof', () => {
  for (const purpose of ['registration', 'password_reset']) {
    const old = createIdentityAction(purpose, proof(purpose), true), submission = old.beginCompletion(body, true)
    old.dispose()
    const next = createIdentityAction(purpose, undefined, true)
    assert.equal(old.accept(submission.ticket), false); assert.equal(old.fail(submission.ticket, new Error('late')), false)
    assert.throws(() => old.beginCompletion(body, true))
    assert.equal(next.snapshot().phase, 'missing_proof')
  }
})

test('capability reads ignore stale refresh, identity invalidation and unmount results', () => {
  const reader = createIdentityCapabilitiesReader(), first = reader.begin(), second = reader.begin()
  assert.equal(reader.accept(first, capabilities), false); assert.equal(reader.fail(first), false)
  assert.equal(reader.accept(second, capabilities), true)
  const third = reader.begin(); reader.invalidate()
  assert.equal(reader.accept(third, capabilities), false)
  const fourth = reader.begin(); assert.equal(reader.fail(fourth), true); assert.equal(reader.snapshot().capabilities, undefined)
  const fifth = reader.begin(); reader.dispose()
  assert.equal(reader.accept(fifth, capabilities), false); assert.equal(reader.fail(fifth), false)
})

test('anonymous layouts are narrowly defined and earliest guard preserves private MFA gates', async () => {
  assert.equal(isAnonymousLayout('catalog'), true); assert.equal(isAnonymousLayout('identity'), true)
  for (const value of [undefined, true, 'login', 'public', 'security', '']) assert.equal(isAnonymousLayout(value), false)
  const router = await readFile(new URL('../src/router.ts', import.meta.url), 'utf8')
  const guard = router.slice(router.indexOf('router.beforeEach'))
  assert.ok(guard.indexOf('isAnonymousLayout(to.meta.publicLayout)') < guard.indexOf('await loadMe()'))
  for (const path of ['/register', '/register/complete', '/password/forgot', '/password/reset']) assert.ok(router.includes(`path: '${path}'`))
  assert.match(guard, /session.enrollmentRequired && to.path !== '\/security'/)
  assert.match(guard, /session.mfaPending && !session.enrollmentRequired && to.path !== '\/login'/)
  assert.match(guard, /to.meta.platform && !hasPlatform.value/)
})

test('identity views mount only GET discovery, clear inputs on submit/unmount and explicitly invalidate session before login', async () => {
  for (const name of ['IdentityRequestView', 'IdentityCompleteView']) {
    const source = await readFile(new URL(`../src/views/${name}.vue`, import.meta.url), 'utf8')
    assert.match(source, /onMounted\(\(\) => \{ void refreshCapabilities\(\) \}\)/)
    assert.match(source, /function goToLogin\(\).*clearSession\(\); void router.push\('\/login'\)/)
    assert.match(source, /watch\(\(\) => .*session.me\?\.user.id/)
    assert.match(source, /onBeforeUnmount\(\(\) => \{ action.dispose\(\); reader.dispose\(\)/)
    assert.doesNotMatch(source, /from ['"]\.\.\/api['"]|localStorage|sessionStorage|console\.|auth\/logout|auth\/login/)
  }
  const complete = await readFile(new URL('../src/views/IdentityCompleteView.vue', import.meta.url), 'utf8')
  assert.ok(complete.indexOf('clearInputs()\n  try { await completeIdentityProof') > 0)
  assert.match(complete, /若账号已启用验证器，请填写动态验证码或一次性恢复码/)
  assert.match(complete, /所有设备的账号会话均已退出/); assert.match(complete, /已有 API Key 不会自动撤销/)
  const login = await readFile(new URL('../src/views/LoginView.vue', import.meta.url), 'utf8')
  assert.match(login, /v-if="registrationAvailable" to="\/register"/); assert.match(login, /to="\/password\/forgot"/)
  assert.match(login, /request\('\/auth\/mfa\/verify'/); assert.match(login, /to="\/pricing"/)
})
