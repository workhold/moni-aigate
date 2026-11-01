import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { canonicalIdentityToken, createIdentityProofHandoff } from '../src/identity-proof.ts'

const token = 'ic1_' + Buffer.alloc(32, 17).toString('base64url')
function browser(path, hash, search = '') {
  const location = { pathname: path, search, hash }
  const initial = path + search + hash
  const entries = [{ url: initial, state: { current: initial, back: initial, forward: initial } }]
  let index = 0
  function visit(url) { const parsed = new URL(url, 'https://gateway.test'); Object.assign(location, { pathname: parsed.pathname, search: parsed.search, hash: parsed.hash }) }
  const history = {
    get state() { return entries[index].state },
    replaceState(state, _, url) { entries[index] = { state, url }; visit(url) },
    push(url) { const from = entries[index].url; entries[++index] = { url, state: { current: url, back: from } }; visit(url) },
    back() { if (index) index--; visit(entries[index].url) },
  }
  return { location, history, entries }
}

test('canonical identity proof requires exactly 32 bytes and canonical base64url bits', () => {
  assert.equal(canonicalIdentityToken(token), true)
  assert.equal(canonicalIdentityToken('ic1_' + 'A'.repeat(43)), true)
  for (const value of ['', undefined, token + '=', token.slice(1), 'ic1_' + 'A'.repeat(42) + 'B', 'ic1_' + '+'.repeat(43), '#token=' + token]) assert.equal(canonicalIdentityToken(value), false)
})

test('proof capture cleans URL and history state before router history snapshots, including later push/back', () => {
  for (const [path, purpose] of [['/register/complete', 'registration'], ['/password/reset', 'password_reset']]) {
    const b = browser(path, '#token=' + token, '?token=ignored&redirect=/')
    const handoff = createIdentityProofHandoff(b.location, b.history)
    assert.equal(b.location.hash, ''); assert.equal(b.location.search, ''); assert.equal(b.history.state, null)
    assert.equal(JSON.stringify(b.entries).includes(token), false)
    // This is the cleaned location/history that createWebHistory now receives.
    b.history.replaceState({ current: b.location.pathname, back: null }, '', b.location.pathname)
    b.history.push('/pricing'); b.history.back()
    assert.equal(b.location.hash, ''); assert.equal(JSON.stringify(b.entries).includes(token), false)
    assert.deepEqual(handoff.take(path), { path, purpose, token })
    assert.equal(handoff.take(path), undefined)
    assert.equal(JSON.stringify(handoff).includes(token), false)
  }
})

test('invalid, duplicate, encoded and query-only proof are cleaned without being accepted', () => {
  for (const hash of ['#token=invalid', '#token=' + token + '&token=' + token, '#token=%69' + token.slice(1), '#other=' + token, '#token=' + token + '&extra=1', '']) {
    const b = browser('/register/complete', hash, '?token=' + token)
    const handoff = createIdentityProofHandoff(b.location, b.history)
    assert.equal(handoff.take('/register/complete'), undefined)
    assert.equal(b.location.hash, ''); assert.equal(b.location.search, ''); assert.equal(b.history.state, null)
  }
})

test('mail fragments on non-target paths are removed, and wrong-path claim destroys a proof', () => {
  for (const hash of ['#token=' + token, '#token=invalid', '#%74oken=%69' + token.slice(1), '#other=' + token]) {
    const b = browser('/pricing', hash, '?q=model')
    assert.equal(createIdentityProofHandoff(b.location, b.history).take('/pricing'), undefined)
    assert.equal(b.location.hash, ''); assert.equal(b.location.search, '?q=model'); assert.equal(b.history.state, null)
  }
  const b = browser('/password/reset', '#token=' + token), handoff = createIdentityProofHandoff(b.location, b.history)
  assert.equal(handoff.take('/register/complete'), undefined)
  assert.equal(handoff.take('/password/reset'), undefined)
})

test('unclaimed proof is discarded on leaving its path; unrelated public anchors are preserved', () => {
  const b = browser('/register/complete', '#token=' + token), handoff = createIdentityProofHandoff(b.location, b.history)
  handoff.discardExcept('/pricing'); assert.equal(handoff.take('/register/complete'), undefined)
  const plain = browser('/pricing', '#models'), original = plain.history.state
  createIdentityProofHandoff(plain.location, plain.history)
  assert.equal(plain.location.hash, '#models'); assert.equal(plain.history.state, original)
})

test('router initializes proof handoff before createWebHistory invocation and never reads token from query', async () => {
  const source = await readFile(new URL('../src/router.ts', import.meta.url), 'utf8')
  assert.ok(source.indexOf('captureIdentityProofBeforeHistory()') < source.indexOf('history: createWebHistory()'))
  assert.match(source, /router\.afterEach\(to => \{ discardUnclaimedIdentityProof\(to.path\) \}\)/)
  const view = await readFile(new URL('../src/views/IdentityCompleteView.vue', import.meta.url), 'utf8')
  assert.match(view, /takeIdentityProof\(route.path\)/)
  assert.doesNotMatch(view, /route\.(query|hash)|localStorage|sessionStorage|console\.|location\.hash/)
})
