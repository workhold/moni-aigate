import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { periodicTargetChoicePath, periodicTargetPage, readPeriodicTargetChoices } from '../src/periodic-target.ts'
import { periodicTargetSelectionNeeded } from '../src/periodic-budget.ts'

const tenant = { id: '6d772865-f77e-422c-aa5b-64c2a6b3fa66', name: '体验客户', status: 'active' }
const account = { id: '10000000-0000-4000-8000-000000000001', label: '采购账户', provider: 'mock-supplier', state: 'active', limit_amount: '1000000', held_amount: '0', spent_amount: '0', revision: '1', created_at: '2026-11-18T00:00:00.000000Z' }

test('target choice path preserves exact tenant/procurement UUID identity', () => {
  assert.equal(periodicTargetChoicePath({ kind: 'tenant', id: tenant.id, name: tenant.name, provider: '', state: 'active' }), `/platform/periodic-budget?tenant=${tenant.id}`)
  assert.equal(periodicTargetChoicePath({ kind: 'procurement', id: account.id, name: account.label, provider: account.provider, state: 'active' }), `/platform/periodic-budget?procurement=${account.id}`)
  assert.throws(() => periodicTargetChoicePath({ kind: 'other', id: tenant.id }))
})

test('target loader combines only successful strict lists and follows cursors', async () => {
  const previous = globalThis.fetch, calls = []
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    if (url.includes('/tenants') && !url.includes('cursor=')) return new Response(JSON.stringify({ data: [tenant], next_cursor: 'tenant-next' }))
    if (url.includes('/tenants')) return new Response(JSON.stringify({ data: [], next_cursor: '' }))
    if (url.includes('/procurement-accounts')) return new Response(JSON.stringify({ data: [account], next_cursor: '' }))
    throw new Error(`unexpected ${url}`)
  }
  try {
    const rows = await readPeriodicTargetChoices(() => true)
    assert.deepEqual(rows.map(row => [row.kind, row.id]), [['tenant', tenant.id], ['procurement', account.id]])
    assert.ok(calls.some(call => call.url.includes('/tenants?cursor=tenant-next')))
    assert.ok(calls.every(call => call.options.method === 'GET' && !call.options.headers['X-CSRF-Token']))
  } finally { globalThis.fetch = previous }
})

test('target loader rejects partial failure, repeated cursors and stale contexts instead of inventing an empty directory', async () => {
  const previous = globalThis.fetch
  try {
    globalThis.fetch = async url => new Response(JSON.stringify(url.includes('/tenants') ? { data: [tenant], next_cursor: '' } : { data: [{ ...account, revision: 'bad' }], next_cursor: '' }))
    await assert.rejects(readPeriodicTargetChoices(() => true))
    globalThis.fetch = async url => new Response(JSON.stringify(url.includes('/tenants') ? { data: [], next_cursor: 'same' } : { data: [], next_cursor: '' }))
    await assert.rejects(readPeriodicTargetChoices(() => true), /游标重复/)
    let calls = 0
    globalThis.fetch = async () => { calls++; return new Response('{}') }
    await assert.rejects(readPeriodicTargetChoices(() => false), /关闭/)
    assert.equal(calls, 0)
  } finally { globalThis.fetch = previous }
})

test('bare-path selection and malformed-query branches are behavioral, not source-pattern checks', () => {
  assert.equal(periodicTargetSelectionNeeded('/platform/periodic-budget', '/platform/periodic-budget', false), true)
  for (const fullPath of ['/platform/periodic-budget?', '/platform/periodic-budget?tenant=bad', '/platform/periodic-budget?tenant=x&tenant=x', '/periodic-budget']) assert.equal(periodicTargetSelectionNeeded('/platform/periodic-budget', fullPath, false), false)
  assert.equal(periodicTargetSelectionNeeded('/platform/periodic-budget', '/platform/periodic-budget', true), false)
})

test('search and client pagination return real filtered rows and bounded pages', () => {
  const rows = Array.from({ length: 45 }, (_, index) => ({ kind: 'tenant', id: `00000000-0000-4000-8000-${String(index).padStart(12, '0')}`, name: `target-${index}`, provider: 'workspace', state: 'active' }))
  assert.equal(periodicTargetPage(rows, '', 2).rows.length, 20)
  assert.equal(periodicTargetPage(rows, '', 3).rows.length, 5)
  assert.equal(periodicTargetPage(rows, '', 2).total, 45)
  const filtered = periodicTargetPage(rows, 'target-44', 1)
  assert.equal(filtered.total, 1)
  assert.equal(filtered.rows[0].name, 'target-44')
  assert.equal(periodicTargetPage(rows, 'missing', 1).total, 0)
  assert.throws(() => periodicTargetPage(rows, '', 0), /分页/)
})

test('a stale 401 from the picker cannot clear a newer authenticated session', async () => {
  const previousFetch = globalThis.fetch, previousWindow = globalThis.window, events = [], resolvers = []
  let current = true
  globalThis.window = { dispatchEvent: event => { events.push(event.type); return true } }
  globalThis.fetch = async () => new Promise(resolve => { resolvers.push(resolve) })
  try {
    const pending = readPeriodicTargetChoices(() => current)
    await new Promise(resolve => setImmediate(resolve))
    current = false
    for (const resolve of resolvers) resolve(new Response(JSON.stringify({ error: { code: 'invalid_credentials', message: 'stale' } }), { status: 401 }))
    await assert.rejects(pending)
    assert.equal(events.filter(type => type === 'moni:unauthenticated').length, 0)
  } finally { globalThis.fetch = previousFetch; globalThis.window = previousWindow }
})

test('periodic view uses a selector only for an empty platform path and preserves malformed-target errors', async () => {
  const [view, picker] = await Promise.all(['../src/views/PeriodicBudgetView.vue', '../src/components/PeriodicTargetPicker.vue'].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  assert.match(view, /!target && !selectTarget/)
  assert.match(view, /<PeriodicTargetPicker/)
  assert.match(picker, /createPeriodicTargetLoader/)
  assert.match(picker, /periodicTargetChoicePath/)
  assert.match(picker, /:page-size="20"/)
  assert.match(picker, /periodicTargetEmptyActions/)
  assert.match(picker, /target-select/)
  assert.doesNotMatch(picker, /v-html|localStorage|sessionStorage|indexedDB/)
})
