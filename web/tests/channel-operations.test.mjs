import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError, request, setCsrf } from '../src/api.ts'
import { channelManagementRevision, channelOperationApplyRequest, channelOperationCommand, channelOperationDetailPath, channelOperationHistoryPath, channelOperationPreflightRequest, createChannelOperationJournal, createChannelOperationsScope, parseChannelOperationPreview, parseChannelOperationReceipt, parseChannelOperationSummary, selectedChannelOperationTargets, unknownChannelOperationResult } from '../src/channel-operations.ts'

const channelA = '00000000-0000-4000-8000-000000000001', channelB = '00000000-0000-4000-8000-000000000002'
const versionA = '10000000-0000-4000-8000-000000000001', versionB = '10000000-0000-4000-8000-000000000002'
const actor = '20000000-0000-4000-8000-000000000001', otherActor = '20000000-0000-4000-8000-000000000002'
const operationA = '30000000-0000-4000-8000-000000000001', operationB = '30000000-0000-4000-8000-000000000002'
const revision = '9007199254740993'
const targetA = { channel_id: channelA, expected_version: versionA, expected_revision: revision }
const targetB = { channel_id: channelB, expected_version: versionB, expected_revision: '2' }
const stateA = { current_version: versionA, management_revision: revision, enabled: true, quarantined: false }
const stateB = { current_version: versionB, management_revision: '2', enabled: true, quarantined: false }
const itemA = { ...targetA, before: stateA, live_credentials: 2, published_bindings: 3, older_version_bindings: 1, status: 'ready', reason: '' }
const itemB = { ...targetB, before: stateB, live_credentials: 1, published_bindings: 1, older_version_bindings: 0, status: 'ready', reason: '' }
const command = { action: 'disable', targets: [targetA, targetB] }
const preview = { action: 'disable', can_apply: true, changed_count: 2, items: [itemA, itemB] }
const applied = { operation_id: operationA, actor, action: 'disable', status: 'applied', target_count: 2, changed_count: 2, created_at: '2026-10-31T00:00:00Z', items: [{ ...itemA, status: 'changed', after: { ...stateA, enabled: false, management_revision: '9007199254740994' } }, { ...itemB, status: 'changed', after: { ...stateB, enabled: false, management_revision: '3' } }] }
const rejected = { ...applied, status: 'rejected', changed_count: 0, items: [{ ...itemA, status: 'not_applied' }, { ...itemB, before: { ...stateB, management_revision: '3' }, status: 'blocked', reason: 'stale_revision' }] }

test('selection is confined to the visible current page and retains revision precision', () => {
  const rows = [{ id: channelA, name: 'A', current_version: versionA, management_revision: revision }, { id: channelB, name: 'B', current_version: versionB, management_revision: '2' }]
  assert.deepEqual(selectedChannelOperationTargets(rows, [channelB, channelA]), [targetA, targetB])
  assert.equal(selectedChannelOperationTargets(rows, [channelA])[0].expected_revision, '9007199254740993')
  assert.throws(() => selectedChannelOperationTargets(rows.slice(0, 1), [channelB]), /当前显示页/)
  assert.throws(() => selectedChannelOperationTargets(rows, [channelA, channelA]), /不同渠道/)
  assert.throws(() => selectedChannelOperationTargets(rows, []), /1 至 50/)
  assert.throws(() => selectedChannelOperationTargets(Array.from({ length: 51 }, () => rows[0]), [channelA]), /1 至 50/)
  assert.throws(() => selectedChannelOperationTargets([{ ...rows[0], management_revision: Number(revision) }], [channelA]), /revision/)
})

test('command builders canonicalize order and use only the fixed preflight and apply contracts', () => {
  const input = { action: 'disable', targets: [targetB, targetA], secret: 'never-send', endpoint: 'https://private.example' }, original = structuredClone(input)
  const parsed = channelOperationCommand(input)
  assert.deepEqual(parsed, command)
  assert.deepEqual(input, original)
  assert.deepEqual(channelOperationPreflightRequest(parsed), { path: '/platform/channel-operations/preflight', method: 'POST', body: command })
  assert.deepEqual(channelOperationApplyRequest(parsed, operationA), { path: '/platform/channel-operations', method: 'POST', body: { operation_id: operationA, ...command } })
  assert.equal(channelOperationDetailPath(operationA), `/platform/channel-operations/${operationA}`)
  assert.equal(channelOperationHistoryPath('cursor?a=b&x=y'), '/platform/channel-operations?cursor=cursor%3Fa%3Db%26x%3Dy')
  assert.throws(() => channelOperationHistoryPath('a'.repeat(513)), /游标/)
  for (const action of ['', 'quarantine', 'rotate', true]) assert.throws(() => channelOperationCommand({ ...command, action }))
  assert.throws(() => channelOperationApplyRequest(command, 'operation/one?x=secret'), /UUID/)
})

test('UUID, target count and canonical int64 strings reject ambiguous or rounded inputs', () => {
  assert.equal(channelManagementRevision('9223372036854775807'), '9223372036854775807')
  for (const value of [0, 1, 9007199254740993, '', '0', '-1', '+1', '01', '1.0', '1e3', '9223372036854775808', ' 1']) assert.throws(() => channelManagementRevision(value), /revision/)
  for (const targets of [[], Array.from({ length: 101 }, () => targetA), [targetA, targetA]]) assert.throws(() => channelOperationCommand({ action: 'disable', targets }))
  assert.throws(() => channelOperationCommand({ action: 'disable', targets: [{ ...targetA, channel_id: channelA.replace('4000', 'ABCD') }] }), /UUID/)
  const hundred = Array.from({ length: 100 }, (_, index) => ({ ...targetA, channel_id: `00000000-0000-4000-8000-${String(index).padStart(12, '0')}` }))
  assert.equal(channelOperationCommand({ action: 'disable', targets: hundred }).targets.length, 100)
})

test('preflight is only an observation, and any blocked target makes the whole batch unconfirmable', () => {
  assert.deepEqual(parseChannelOperationPreview(preview, command), preview)
  const partiallyBlocked = { ...preview, can_apply: false, changed_count: 0, items: [itemA, { ...itemB, before: null, status: 'blocked', reason: 'not_found', live_credentials: 0, published_bindings: 0 }] }
  assert.equal(parseChannelOperationPreview(partiallyBlocked, command).can_apply, false)
  assert.throws(() => parseChannelOperationPreview({ ...partiallyBlocked, changed_count: 1 }, command), /整批/)
  assert.throws(() => parseChannelOperationPreview({ ...partiallyBlocked, can_apply: true }, command), /整批/)
  assert.throws(() => parseChannelOperationPreview(preview, { ...command, targets: [targetA] }), /冻结命令/)
  assert.throws(() => parseChannelOperationPreview(preview, { ...command, targets: [targetA, { ...targetB, expected_revision: '3' }] }), /冻结渠道快照/)
  assert.equal('operation_id' in parseChannelOperationPreview(preview), false)
})

test('enable requires a live unquarantined pool even when a channel is already enabled', () => {
  const enabled = { action: 'enable', can_apply: true, changed_count: 0, items: [{ ...itemA, status: 'unchanged' }] }
  assert.equal(parseChannelOperationPreview(enabled).changed_count, 0)
  assert.throws(() => parseChannelOperationPreview({ ...enabled, items: [{ ...itemA, status: 'unchanged', live_credentials: 0 }] }), /整批/)
  assert.throws(() => parseChannelOperationPreview({ ...enabled, items: [{ ...itemA, status: 'unchanged', before: { ...stateA, quarantined: true } }] }), /整批/)
  const disabling = { ...preview, changed_count: 2, items: [{ ...itemA, live_credentials: 0, before: { ...stateA, quarantined: true } }, itemB] }
  assert.equal(parseChannelOperationPreview(disabling).can_apply, true, 'disable must preserve quarantine and need no live credentials')
})

test('applied receipts prove enabled-only changes while no-op application remains a valid terminal outcome', () => {
  const parsed = parseChannelOperationReceipt(applied, command)
  assert.equal(parsed.status, 'applied')
  assert.equal(parsed.items[0].after.management_revision, '9007199254740994')
  const noOp = { ...applied, target_count: 1, changed_count: 0, items: [{ ...itemA, before: { ...stateA, enabled: false }, after: { ...stateA, enabled: false }, status: 'unchanged' }] }
  assert.equal(parseChannelOperationReceipt(noOp).status, 'applied')
  assert.equal(parseChannelOperationReceipt(noOp).changed_count, 0)
  for (const changed of [{ current_version: versionB }, { quarantined: true }, { management_revision: revision }, { enabled: true }]) {
    const invalid = { ...applied, items: [{ ...applied.items[0], after: { ...applied.items[0].after, ...changed } }, applied.items[1]] }
    assert.throws(() => parseChannelOperationReceipt(invalid), /启用状态/)
  }
})

test('HTTP-200 rejected receipts stay rejected with zero changed targets and never imply partial success', () => {
  const parsed = parseChannelOperationReceipt(rejected, command)
  assert.equal(parsed.status, 'rejected')
  assert.equal(parsed.changed_count, 0)
  assert.deepEqual(parsed.items.map(item => item.status), ['not_applied', 'blocked'])
  for (const invalid of [{ ...rejected, status: 'applied' }, { ...rejected, changed_count: 1 }, { ...rejected, items: [itemA, rejected.items[1]] }, { ...rejected, items: [rejected.items[0], { ...rejected.items[1], after: stateB }] }]) assert.throws(() => parseChannelOperationReceipt(invalid))
  assert.throws(() => parseChannelOperationReceipt({ ...applied, target_count: 1, changed_count: 1 }), /明细/)
})

test('public receipt projections reject unsafe diagnostics and do not expose unexpected provider fields', () => {
  const enriched = { ...applied, raw_error: 'private-provider-error', secret: 'private', items: applied.items.map(item => ({ ...item, secret: 'private', before: { ...item.before, url: 'https://private.example' } })) }
  const parsed = parseChannelOperationReceipt(enriched)
  assert.ok(!JSON.stringify(parsed).includes('private'))
  const summary = parseChannelOperationSummary(enriched)
  assert.equal('items' in summary, false)
  assert.equal('secret' in summary, false)
  for (const invalid of [{ ...rejected, items: [rejected.items[0], { ...rejected.items[1], reason: '<script>private</script>' }] }, { ...applied, created_at: 'not-a-date' }, { ...applied, items: [applied.items[0], applied.items[0]] }, { ...applied, items: [{ ...applied.items[0], live_credentials: 33 }, applied.items[1]] }]) assert.throws(() => parseChannelOperationReceipt(invalid))
})

test('the journal allocates once, survives UI epoch resets and isolates actors without exposing mutable commands', () => {
  const journal = createChannelOperationJournal(), scope = createChannelOperationsScope(); let generated = 0
  const operation = journal.begin(actor, command, () => { generated++; return operationA })
  operation.command.targets[0].expected_revision = '99'
  const read = journal.list(actor); read[0].command.targets[0].expected_revision = '88'
  scope.reset() // close or route change must not erase the journal
  assert.equal(journal.list(actor)[0].command.targets[0].expected_revision, revision)
  assert.equal(journal.list(actor)[0].state, 'unknown')
  assert.deepEqual(journal.list(otherActor), [])
  assert.throws(() => journal.begin(actor, command, () => { generated++; return operationB }), /原操作 ID/)
  assert.equal(generated, 1, 'unknown results must not allocate a new operation ID')
  journal.begin(otherActor, command, () => operationB)
  assert.equal(journal.list(otherActor)[0].operation_id, operationB)
  const receipt = journal.resolve(actor, operationA, applied)
  receipt.items[0].after.enabled = true
  assert.equal(journal.list(actor)[0].receipt.items[0].after.enabled, false)
  assert.equal(journal.list(actor)[0].state, 'resolved')
  journal.begin(actor, command, () => { generated++; return operationB })
  assert.equal(generated, 2, 'a later explicitly confirmed operation gets a fresh ID')
})

test('a receipt for another command, operation ID or submitting actor cannot resolve an uncertain write', () => {
  const journal = createChannelOperationJournal()
  journal.begin(actor, command, () => operationA)
  for (const invalid of [{ ...applied, operation_id: operationB }, { ...applied, actor: otherActor }, { ...applied, items: [{ ...applied.items[0], expected_revision: '4' }, applied.items[1]] }]) assert.throws(() => journal.resolve(actor, operationA, invalid))
  assert.equal(journal.list(actor)[0].state, 'unknown')
  assert.equal(journal.resolve(otherActor, operationA, applied).actor, actor, 'an admin may read another actor receipt without claiming their submission')
  assert.deepEqual(journal.list(otherActor), [])
})

test('missing applied after states or rejected after states leave the original write uncertain', () => {
  const journal = createChannelOperationJournal()
  journal.begin(actor, command, () => operationA)
  const appliedNoOp = { ...applied, target_count: 1, changed_count: 0, items: [{ ...itemA, before: { ...stateA, enabled: false }, status: 'unchanged' }] }
  assert.throws(() => parseChannelOperationReceipt(appliedNoOp), /每个渠道.*后置状态/)
  for (const invalid of [{ ...applied, items: [{ ...applied.items[0], after: undefined }, applied.items[1]] }, { ...rejected, items: [{ ...rejected.items[0], after: stateA }, rejected.items[1]] }, { ...rejected, items: [{ ...rejected.items[0], after: null }, rejected.items[1]] }]) assert.throws(() => journal.resolve(actor, operationA, invalid))
  assert.equal(journal.list(actor)[0].state, 'unknown')
  const unchangedInRejected = { ...rejected, items: [{ ...itemA, before: { ...stateA, enabled: false }, after: { ...stateA, enabled: false }, status: 'unchanged' }, rejected.items[1]] }
  assert.throws(() => parseChannelOperationReceipt(unchangedInRejected), /不能有后置状态/)
  assert.throws(() => parseChannelOperationReceipt({ ...rejected, items: [rejected.items[0], { ...rejected.items[1], before: stateB }] }), /阻断原因/)
})

test('list, preflight and receipt lanes reject late responses without invalidating unrelated receipts', async () => {
  const scope = createChannelOperationsScope(), oldList = scope.ticket('list'), preflight = scope.ticket('preview'), detail = scope.ticket('detail')
  let release
  const pending = new Promise(resolve => { release = resolve })
  let page = 'new-page'
  const old = (async () => { await pending; if (scope.accepts(oldList)) page = 'old-page' })()
  const currentList = scope.ticket('list')
  scope.ticket('preview') // selection/filter refresh invalidates the old confirmation
  release(); await old
  assert.equal(page, 'new-page')
  assert.equal(scope.accepts(currentList), true)
  assert.equal(scope.accepts(preflight), false)
  assert.equal(scope.accepts(detail), true)
  scope.reset() // resource change or component unmount
  assert.equal(scope.accepts(currentList), false)
  assert.equal(scope.accepts(detail), false)
})

test('network or invalid responses retain one operation ID and use GET-only recovery including a non-final 404', async () => {
  const originalFetch = globalThis.fetch, calls = [], journal = createChannelOperationJournal()
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    if (options.method === 'POST') throw new Error('connection lost after server may commit')
    if (calls.length === 2) return new Response(JSON.stringify({ error: { code: 'not_found', message: '暂未找到' }, request_id: 'pending' }), { status: 404 })
    return new Response(JSON.stringify({ data: applied, request_id: 'resolved' }))
  }
  try {
    setCsrf('batch-csrf')
    const submitted = journal.begin(actor, command, () => operationA), apply = channelOperationApplyRequest(submitted.command, submitted.operation_id)
    await assert.rejects(request(apply.path, apply.method, apply.body), error => unknownChannelOperationResult(error))
    assert.equal(journal.list(actor)[0].state, 'unknown')
    await assert.rejects(request(channelOperationDetailPath(operationA)), error => error instanceof ApiError && error.status === 404)
    assert.equal(journal.list(actor)[0].state, 'unknown', 'GET 404 does not prove rollback')
    assert.throws(() => journal.begin(actor, command, () => operationB), /原操作 ID/)
    journal.resolve(actor, operationA, (await request(channelOperationDetailPath(operationA))).data)
    assert.deepEqual(calls.map(call => call.options.method), ['POST', 'GET', 'GET'])
    assert.ok(calls.slice(1).every(call => call.url.endsWith(operationA)))
    assert.equal(calls[0].options.headers['X-CSRF-Token'], 'batch-csrf')
    assert.equal(calls[0].options.credentials, 'include')
    assert.equal(calls[0].options.headers['Idempotency-Key'], undefined)
    assert.equal(JSON.parse(calls[0].options.body).operation_id, operationA)
  } finally { globalThis.fetch = originalFetch; setCsrf('') }
})

test('a late apply receipt cannot replace a newer historical selection or pair it with the wrong operation ID', async () => {
  const journal = createChannelOperationJournal(), scope = createChannelOperationsScope()
  const submitted = journal.begin(actor, command, () => operationA)
  const applyTicket = scope.ticket('apply'), applyDisplay = scope.ticket('display')
  let release
  const post = new Promise(resolve => { release = resolve })
  let selectedID = operationA, displayedReceipt = null
  const latePost = (async () => {
    const result = journal.resolve(actor, submitted.operation_id, await post)
    if (scope.accepts(applyTicket) && scope.accepts(applyDisplay)) { selectedID = result.operation_id; displayedReceipt = result }
  })()
  const historyTicket = scope.ticket('detail'), historyDisplay = scope.ticket('display')
  const historical = parseChannelOperationReceipt({ ...rejected, operation_id: operationB })
  if (scope.accepts(historyTicket) && scope.accepts(historyDisplay)) { selectedID = historical.operation_id; displayedReceipt = historical }
  release(applied); await latePost
  assert.equal(selectedID, operationB)
  assert.equal(displayedReceipt.operation_id, operationB)
  assert.equal(displayedReceipt.status, 'rejected')
  assert.equal(journal.list(actor)[0].state, 'resolved', 'late write still updates the actor journal without replacing the selected view')
  assert.equal(journal.list(actor)[0].receipt.operation_id, operationA)
})

test('the transport returns a durable rejected receipt and history without provider, route or financial writes', async () => {
  const originalFetch = globalThis.fetch, calls = [], journal = createChannelOperationJournal()
  globalThis.fetch = async (url, options) => { calls.push({ url, options }); return new Response(JSON.stringify({ data: url.endsWith('/preflight') ? preview : options.method === 'POST' ? rejected : url.endsWith(operationA) ? rejected : [rejected], request_id: 'batch-rejected' })) }
  try {
    setCsrf('batch-csrf')
    const check = channelOperationPreflightRequest(command)
    assert.equal(parseChannelOperationPreview((await request(check.path, check.method, check.body)).data, command).can_apply, true)
    const submitted = journal.begin(actor, command, () => operationA), apply = channelOperationApplyRequest(submitted.command, submitted.operation_id)
    assert.equal(journal.resolve(actor, operationA, (await request(apply.path, apply.method, apply.body)).data).status, 'rejected')
    assert.equal(journal.list(actor)[0].state, 'resolved', 'HTTP 200 is terminal but must not mean applied')
    assert.equal(parseChannelOperationSummary((await request(channelOperationHistoryPath())).data[0]).status, 'rejected')
    assert.equal(parseChannelOperationReceipt((await request(channelOperationDetailPath(operationA))).data).changed_count, 0)
    assert.deepEqual(calls.map(call => call.options.method), ['POST', 'POST', 'GET', 'GET'])
    assert.ok(calls.every(call => call.url.startsWith('/api/platform/channel-operations')))
    assert.ok(calls.every(call => !/\/probe|\/discover|\/bindings|\/models|\/credits|\/refunds|\/credentials|\/price/.test(call.url)))
    assert.ok(calls.every(call => !call.options.headers.Authorization && !call.options.headers['x-api-key']))
  } finally { globalThis.fetch = originalFetch; setCsrf('') }
})

test('definite authorization rejection is distinct from an uncertain write and does not trigger automatic resubmission', () => {
  for (const error of [new ApiError('network_error', '', '', 0), new ApiError('system_unavailable', '', '', 503), new ApiError('invalid_response', '', '', 200), new ApiError('timeout', '', '', 408), new Error('receipt schema is invalid')]) assert.equal(unknownChannelOperationResult(error), true)
  assert.equal(unknownChannelOperationResult(new ApiError('reauth_required', '', '', 403)), false)
  const journal = createChannelOperationJournal()
  journal.begin(actor, command, () => operationA)
  journal.notSubmitted(actor, operationA)
  assert.equal(journal.list(actor)[0].state, 'not_submitted')
  assert.equal(journal.begin(actor, command, () => operationB).operation_id, operationB)
})
