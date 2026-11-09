import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError } from '../src/api.ts'
import { money } from '../src/format.ts'
import { createOperatingReportState, defaultOperatingFilter, fetchOperatingCSV, operatingFilter, operatingMarginKnown, operatingMarginText, operatingReportPath, parseOperatingReport, saveOperatingCSV } from '../src/operating-report.ts'

const tenant = '11111111-1111-4111-8111-111111111111', model = '22222222-2222-4222-8222-222222222222', schema = '33333333-3333-4333-8333-333333333333'
const draft = { from: '2026-11-01T08:00:00+08:00', to: '2026-11-02T08:00:00+08:00', tenant_id: '', model_id: '', protocol: '', execution_kind: '' }
const totals = { request_count: 1, unsettled_requests: 0, attempt_count: 2, unknown_provider_costs: 0, charged: '9007199254740993', refunded: '1', net_revenue: '9007199254740992', known_provider_cost: '2', gross_margin: '9007199254740990' }
const report = (amount = totals.charged) => ({ from: '2026-11-01T00:00:00Z', to: '2026-11-02T00:00:00Z', as_of: '2026-11-02T01:00:00Z', currency: 'CNY', amount_unit: 'micro_cny', scope: 'request_created_window', summary: { ...totals, charged: amount }, models: [], channels: [], requests: [] })

test('operating report parser rejects old token/rate projections and preserves exact report totals', () => {
  const value = parseOperatingReport(report())
  assert.equal(value.summary.charged, totals.charged)
  for (const field of ['input_tokens', 'output_tokens', 'input_rate', 'output_rate', 'retail_rates']) assert.throws(() => parseOperatingReport({ ...report(), [field]: '1' }), /旧标量|未定义/)
  assert.throws(() => parseOperatingReport({ ...report(), summary: { ...totals, output_tokens: 1 } }), /旧标量|未定义/)
})

test('operating report parser keeps current media protocols and asynchronous tasks', () => {
  const value = parseOperatingReport({ ...report(), requests: [{ request_id: 'task', tenant_id: tenant, tenant_name: 'tenant', model_id: 'mock-video-generation', model_name: 'video', status: 'succeeded', billing_status: 'settled', created_at: '2026-11-02T00:00:00Z', protocol: 'video_generation', execution_kind: 'asynchronous', profile_version: '10000000-0000-4000-8000-000000000001', schema_id: schema, schema_digest: 'ab'.repeat(32), variant_key: 'default', ...totals }] })
  assert.equal(value.requests[0].protocol, 'video_generation')
  assert.equal(value.requests[0].execution_kind, 'asynchronous')
})

test('default seven day window and explicit timezone/UUID filters produce one exact GET query', () => {
  const initial = defaultOperatingFilter(new Date('2026-11-04T12:34:56.123Z'))
  assert.equal(initial.from, '2026-10-28T12:34:56.123Z')
  assert.equal(initial.to, '2026-11-04T12:34:56.123Z')
  assert.deepEqual(operatingFilter(draft), { from: '2026-11-01T00:00:00.000Z', to: '2026-11-02T00:00:00.000Z' })
  const filter = operatingFilter({ ...draft, tenant_id: ` ${tenant} `, model_id: model })
  for (const csv of [false, true]) {
    const url = new URL(operatingReportPath(filter, csv), 'https://console.test')
    assert.equal(url.pathname, `/platform/reports/operations${csv ? '.csv' : ''}`)
    assert.deepEqual([...url.searchParams], [['from', filter.from], ['to', filter.to], ['tenant_id', tenant], ['model_id', model]])
  }
  assert.equal(operatingFilter({ ...draft, from: '2026-10-02T00:00:00Z', to: '2026-11-02T00:00:00Z' }).to, '2026-11-02T00:00:00.000Z')
  const selected = operatingFilter({ ...draft, model_id: 'mock-video-generation', protocol: 'video_generation', execution_kind: 'asynchronous' })
  assert.deepEqual([...new URLSearchParams(operatingReportPath(selected)).entries()].slice(2), [['model_id', 'mock-video-generation'], ['protocol', 'video_generation'], ['execution_kind', 'asynchronous']])
})


test('invalid filters cannot silently broaden the range or normalize impossible dates', () => {
  for (const patch of [
    { from: null }, { to: '' }, { from: '2026-11-01T00:00:00' }, { from: '2026-02-30T00:00:00Z' },
    { from: '2026-02-29T00:00:00Z' }, { from: '2026-11-01T24:00:00Z' }, { from: '0000-11-01T00:00:00Z' },
    { from: draft.to }, { to: draft.from }, { from: '2026-09-01T00:00:00Z' },
    { tenant_id: 'all-customers' }, { model_id: 'model-name' },
  ]) assert.throws(() => operatingFilter({ ...draft, ...patch }))
})

test('editing filters and changing route or identity discard reads, exports and prior amounts immediately', () => {
  const state = createOperatingReportState()
  let read = state.beginRead(); state.accept(read, report())
  assert.equal(state.snapshot().report.summary.charged, totals.charged)
  for (const contextChange of ['filter', 'route', 'actor', 'tenant', 'role', 'csrf']) {
    read = state.beginRead()
    const download = state.beginDownload()
    state.invalidate()
    assert.deepEqual(state.snapshot(), { report: null, loading: false, downloading: false, error: '', downloadError: '' }, contextChange)
    assert.equal(state.current(read), false)
    assert.equal(state.current(download), false)
    assert.equal(state.accept(read, report('999999')), false)
    assert.equal(state.fail(read, new Error('late private response')), false)
    assert.equal(state.finishDownload(download, new Error('late error')), false)
  }
  const current = state.beginRead(); state.accept(current, report('7'))
  assert.equal(state.snapshot().report.summary.charged, '7')
})

test('latest read wins and an error or oversized range never leaves old totals displayed', () => {
  const state = createOperatingReportState(), old = state.beginRead(), latest = state.beginRead()
  assert.equal(state.accept(old, report('old')), false)
  state.accept(latest, report('42'))
  const failed = state.beginRead()
  assert.equal(state.snapshot().report, null)
  state.fail(failed, new ApiError('report_range_too_large', 'range', 'safe-id', 422))
  assert.equal(state.snapshot().report, null)
  assert.match(state.snapshot().error, /5,000.*缩短/)
  const retry = state.beginRead()
  assert.equal(state.snapshot().error, '')
  state.fail(retry, new ApiError('report_unavailable', '报表暂不可用', 'request-safe', 503))
  assert.match(state.snapshot().error, /报表暂不可用.*request-safe/)
  state.invalidate('请检查时间窗口。')
  assert.equal(state.snapshot().error, '请检查时间窗口。')
})

test('download has an independent slot and unmount permanently invalidates every pending action', () => {
  const state = createOperatingReportState(), read = state.beginRead(), download = state.beginDownload()
  assert.equal(state.beginDownload(), null)
  state.accept(read, report())
  assert.equal(state.current(download), true)
  state.finishDownload(download, new Error('下载未完成'))
  assert.equal(state.snapshot().report.summary.charged, totals.charged)
  assert.equal(state.snapshot().downloadError, '下载未完成')
  const pending = state.beginDownload(), refresh = state.beginRead()
  state.dispose()
  assert.equal(state.current(pending), false)
  assert.equal(state.accept(refresh, report()), false)
  assert.equal(state.finishDownload(pending), false)
  assert.equal(state.beginRead(), null)
  assert.equal(state.beginDownload(), null)
  assert.equal(state.snapshot().report, null)
})

test('money remains exact beyond safe integers and unresolved costs or settlement never imply zero margin', () => {
  const huge = '9'.repeat(100)
  assert.equal(money(huge), `¥${'9'.repeat(94).replace(/\B(?=(\d{3})+(?!\d))/g, ',')}.999999`)
  assert.equal(operatingMarginText(totals), '¥9,007,199,254.740990')
  assert.equal(operatingMarginText({ ...totals, gross_margin: '-1000001' }), '−¥1.000001')
  assert.equal(operatingMarginText({ ...totals, request_count: 0, attempt_count: 0, charged: '0', gross_margin: '0' }), '¥0.000000')
  for (const patch of [{ gross_margin: null }, { unsettled_requests: 1 }, { unknown_provider_costs: 1 }]) {
    assert.equal(operatingMarginKnown({ ...totals, ...patch }), false)
    assert.equal(operatingMarginText({ ...totals, ...patch }), '待核对')
  }
})

test('CSV transport uses the same-origin cookie and exact filter while preserving large amount bytes', async () => {
  const original = globalThis.fetch, calls = [], content = `request_id,charged\nrequest-one,${'9'.repeat(100)}\n`
  globalThis.fetch = async (url, options) => { calls.push({ url, options }); return new Response(content, { headers: { 'Content-Type': 'text/csv; charset=utf-8' } }) }
  try {
    const filter = operatingFilter({ ...draft, tenant_id: tenant })
    const blob = await fetchOperatingCSV(filter, () => true)
    assert.equal(await blob.text(), content)
    assert.equal(calls.length, 1)
    assert.equal(calls[0].url, `/api${operatingReportPath(filter, true)}`)
    assert.deepEqual(calls[0].options, { method: 'GET', credentials: 'include', headers: { Accept: 'text/csv' } })
  } finally { globalThis.fetch = original }
})

test('JSON errors, HTML errors, bad success content type and network failure never become CSV files', async () => {
  const original = globalThis.fetch
  try {
    for (const [response, code] of [
      [new Response(JSON.stringify({ error: { code: 'report_range_too_large', message: '缩短窗口' }, request_id: 'safe' }), { status: 422, headers: { 'Content-Type': 'application/json' } }), 'report_range_too_large'],
      [new Response('<html>private upstream detail</html>', { status: 503 }), 'request_failed'],
      [new Response('null', { status: 503, headers: { 'Content-Type': 'application/json' } }), 'request_failed'],
      [new Response(JSON.stringify({ data: 'error is not csv' }), { headers: { 'Content-Type': 'application/json' } }), 'invalid_response'],
    ]) {
      globalThis.fetch = async () => response
      await assert.rejects(fetchOperatingCSV(operatingFilter(draft), () => true), error => error instanceof ApiError && error.code === code && !error.message.includes('private'))
    }
    globalThis.fetch = async () => { throw new Error('private network configuration') }
    await assert.rejects(fetchOperatingCSV(operatingFilter(draft), () => true), error => error instanceof ApiError && error.code === 'network_error' && !error.message.includes('private'))
  } finally { globalThis.fetch = original }
})

test('only current CSV authentication failures dispatch existing session events', async () => {
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window
  const events = []
  globalThis.window = { dispatchEvent: event => { events.push(event.type); return true } }
  try {
    for (const [status, code, expected] of [[401, 'unauthenticated', 'moni:unauthenticated'], [403, 'reauth_required', 'moni:reauth']]) {
      globalThis.fetch = async () => new Response(JSON.stringify({ error: { code } }), { status })
      await assert.rejects(fetchOperatingCSV(operatingFilter(draft), () => true), ApiError)
      assert.equal(events.at(-1), expected)
      const count = events.length
      assert.equal(await fetchOperatingCSV(operatingFilter(draft), () => false), null)
      assert.equal(events.length, count)
    }
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow }
})

test('filter change during body transfer discards a successful CSV before it can be saved', async () => {
  const original = globalThis.fetch, state = createOperatingReportState(), ticket = state.beginDownload()
  let finish
  const pending = new Promise(resolve => { finish = resolve })
  globalThis.fetch = async () => ({ ok: true, status: 200, headers: new Headers({ 'Content-Type': 'text/csv' }), blob: () => pending })
  try {
    const download = fetchOperatingCSV(operatingFilter(draft), () => state.current(ticket))
    await Promise.resolve()
    state.invalidate()
    finish(new Blob(['request_id,charged\na,1\n'], { type: 'text/csv' }))
    assert.equal(await download, null)
  } finally { globalThis.fetch = original }
})

test('a body transfer failure does not yield a partial CSV or expose the stream cause', async () => {
  const original = globalThis.fetch
  globalThis.fetch = async () => ({ ok: true, status: 200, headers: new Headers({ 'Content-Type': 'text/csv' }), blob: async () => { throw new Error('private stream failure') } })
  try {
    await assert.rejects(fetchOperatingCSV(operatingFilter(draft), () => true), error => error instanceof ApiError && error.code === 'network_error' && !error.message.includes('private'))
  } finally { globalThis.fetch = original }
})

test('an identity change while reading a CSV error body suppresses late authentication events', async () => {
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window, events = []
  let current = true, finish
  const body = new Promise(resolve => { finish = resolve })
  globalThis.window = { dispatchEvent: event => { events.push(event.type); return true } }
  globalThis.fetch = async () => ({ ok: false, status: 401, headers: new Headers(), json: () => body })
  try {
    const pending = fetchOperatingCSV(operatingFilter(draft), () => current)
    await Promise.resolve()
    current = false
    finish({ error: { code: 'unauthenticated' } })
    await assert.rejects(pending, ApiError)
    assert.deepEqual(events, [])
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow }
})

test('saved CSV uses a fixed filename and always revokes the object URL, including click failures', () => {
  for (const fail of [false, true]) {
    const calls = [], blob = new Blob(['request_id,charged\na,1\n'], { type: 'text/csv' })
    const target = {
      createObjectURL(value) { assert.equal(value, blob); calls.push('create'); return 'blob:owned' },
      click(url, filename) { calls.push('click'); assert.equal(url, 'blob:owned'); assert.equal(filename, 'moni-aigate-operations.csv'); if (fail) throw new Error('browser download unavailable') },
      revokeObjectURL(url) { assert.equal(url, 'blob:owned'); calls.push('revoke') },
    }
    if (fail) assert.throws(() => saveOperatingCSV(blob, target), /download unavailable/)
    else saveOperatingCSV(blob, target)
    assert.deepEqual(calls, ['create', 'click', 'revoke'])
  }
})
