import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { ref } from 'vue'
import { ApiError, request, setCsrf } from '../src/api.ts'
import { createProcurementJournal, procurementContext } from '../src/procurement-state.ts'
import { acceptProcurementResult, parseProcurementAccount, parseProcurementBinding, parseProcurementChannel, parseProcurementEvent, parseProcurementObservation, parseProcurementPage, parseProcurementProposal, parseProcurementReceipt, parseProcurementReservation, procurementAccountCommand, procurementApprovalCommand, procurementBindingCommand, procurementCny, procurementInteger, procurementObservationCommand, procurementProposalCommand, readProcurementRows } from '../src/procurement.ts'

const operationID = 'b51784ad-ce02-4eaa-a507-1c2b5a4c34c7', approvalID = '54d89d17-90c1-4b2d-aae7-d9386859db3b'
const accountID = '365a428d-9f85-4bb5-9b43-256d1bc69ea8', channelID = 'ed0e2614-90cc-470f-865b-665a950d37d5', credentialID = '791cc620-a20c-40af-94a9-de3dcc8679e7'
const maker = '6d772865-f77e-422c-aa5b-64c2a6b3fa66', checker = '653b7cd7-d1ce-4d7b-8203-a8f2c1c9f5ad', tenantID = 'a2cbcd48-2868-4cf2-8e03-6804bad9b1dc'
const at = '2026-11-01T00:00:00Z'
const account = { id: accountID, label: '共享上游账户', provider: 'supplier-platform', state: 'active', limit_amount: '9223372036854775807', held_amount: '9007199254740993', spent_amount: '9007199254740994', revision: '9007199254740993', created_at: at }
const proposal = { id: operationID, tenant_id: tenantID, attempt_id: credentialID, amount: '9007199254740993', basis: 'invoice', evidence_ref: 'invoice:2026-001', reason: '核对供应商最终账单\n保留原报价', requester: maker, state: 'proposed', approver: null, approval_operation_id: null, created_at: at, approved_at: null, expected_revision: '9007199254740993', expected_evidence_count: '2' }
const approved = { ...proposal, state: 'approved', approver: checker, approval_operation_id: approvalID, approved_at: at }
const receipt = { operation_id: operationID, actor: maker, kind: 'account_create', target_id: accountID, status: 'applied', reason: '', before: null, after: account, created_at: at }
const proposalDraft = { tenant_id: tenantID, attempt_id: credentialID, amount: '9007199254.740993', basis: 'invoice', evidence_ref: proposal.evidence_ref, reason: proposal.reason }

test('account money and revisions preserve int64 precision while cumulative numeric facts may exceed int64', () => {
  const value = parseProcurementAccount({ ...account, held_amount: '92233720368547758080', secret: 'do-not-keep' })
  assert.equal(value.limit_amount, '9223372036854775807')
  assert.equal(value.held_amount, '92233720368547758080')
  assert.equal(procurementCny('9007199254740993'), '9007199254.740993')
  assert.doesNotMatch(JSON.stringify(value), /secret|do-not-keep/)
  for (const invalid of [9007199254740993, '-1', '01', '+1', '1.2', '1e3', '']) assert.throws(() => parseProcurementAccount({ ...account, held_amount: invalid }))
  assert.throws(() => parseProcurementAccount({ ...account, limit_amount: '9223372036854775808' }))
  assert.throws(() => parseProcurementAccount({ ...account, revision: '0' }))
  assert.throws(() => parseProcurementAccount(account, channelID), /对象不一致/)
})

test('account create/configuration commands rebuild safe complete bodies with captured string CAS', () => {
  const draft = ref({ label: account.label, provider: account.provider, state: 'paused', limit: '9223372036854.775807', held_amount: '0', secret: 'never' })
  const command = procurementAccountCommand(draft.value, operationID, account)
  assert.deepEqual(command.body, { operation_id: operationID, expected_revision: account.revision, label: account.label, provider: account.provider, state: 'paused', limit_amount: '9223372036854775807' })
  draft.value.limit = '0'; draft.value.label = 'mutated'
  assert.equal(command.body.limit_amount, '9223372036854775807')
  assert.ok(Object.isFrozen(command.body))
  assert.doesNotMatch(JSON.stringify(command), /secret|held_amount/)
  const create = procurementAccountCommand({ label: '新账户', provider: 'supplier', state: 'active', limit: '0' }, operationID)
  assert.equal(create.method, 'POST'); assert.equal(create.body.limit_amount, '0'); assert.equal(create.body.expected_revision, undefined)
  for (const limit of ['-1', '1.0000001', '9223372036854.775808']) assert.throws(() => procurementAccountCommand({ label: '账户', provider: 'supplier', state: 'active', limit }, operationID))
  assert.throws(() => procurementAccountCommand({ label: '账'.repeat(34), provider: 'supplier', state: 'active', limit: '1' }, operationID), /名称/)
})

test('permanent binding captures the exact credential/account and channel management revision without provider secrets', () => {
  const channel = parseProcurementChannel({ id: channelID, name: '渠道', management_revision: '9007199254740993', secret: 'hidden', base_url: 'https://private.invalid' })
  const command = procurementBindingCommand(channel, credentialID, accountID, operationID)
  assert.deepEqual(command.body, { operation_id: operationID, channel_id: channelID, credential_id: credentialID, account_id: accountID, expected_channel_revision: '9007199254740993' })
  channel.management_revision = '1'
  assert.equal(command.body.expected_channel_revision, '9007199254740993')
  assert.doesNotMatch(JSON.stringify(channel), /secret|base_url|private/)
  const bound = parseProcurementBinding({ id: operationID, ...command.body, operation_id: operationID, bound_at: at, channel_revision: '9007199254740994' })
  assert.equal(bound.account_id, accountID)
  assert.throws(() => procurementBindingCommand(channel, '../credential', accountID, operationID))
})

test('manual observations distinguish unknown from known free and never submit stale typed amount for nonknown status', () => {
  const draft = { status: 'unknown', amount: '99', source: '人工供应商控制台', semantic: '预付可用余额', observed_at: at, provider_as_of: '', evidence_ref: 'evidence:001' }
  const unknown = procurementObservationCommand(accountID, draft, operationID)
  assert.equal(unknown.body.amount, null); assert.equal(unknown.body.provider_as_of, null); assert.equal(unknown.body.currency, 'CNY')
  const known = procurementObservationCommand(accountID, { ...draft, status: 'known', amount: '0', provider_as_of: '2026-11-01T08:00:00+08:00' }, operationID)
  assert.equal(known.body.amount, '0'); assert.equal(known.body.provider_as_of, '2026-11-01T00:00:00.000Z')
  for (const evidence_ref of ['https://private.invalid/token', 'sk-secret', 'mg_secret', 'Bearer-secret', 'with space']) assert.throws(() => procurementObservationCommand(accountID, { ...draft, evidence_ref }, operationID), /证据/)
  const parsed = parseProcurementObservation({ ...known.body, id: channelID, account_id: accountID, actor: maker, created_at: at })
  assert.equal(parsed.amount, '0')
  assert.throws(() => parseProcurementObservation({ ...parsed, status: 'unknown' }), /状态不一致/)
})

test('unknown/disputed procurement projections and signed budget events remain exact and do not become customer money', () => {
  const raw = { id: credentialID, attempt_id: credentialID, account_id: accountID, binding_id: operationID, tenant_id: tenantID, request_id: approvalID, channel_id: channelID, input_bound: '9007199254740993', output_bound: '16', upper_amount: '100', held_amount: '92', spent_amount: '8', state: 'disputed', anomaly: true, created_at: at, secret: 'hidden' }
  const hold = parseProcurementReservation(raw)
  assert.equal(hold.state, 'disputed'); assert.equal(hold.held_amount, '92'); assert.equal(hold.tenant_id, tenantID)
  assert.doesNotMatch(JSON.stringify(hold), /secret|hidden/)
  assert.throws(() => parseProcurementReservation({ ...raw, id: channelID }), /不一致/)
  const event = parseProcurementEvent({ id: operationID, event_key: 'cost:one', account_id: accountID, attempt_id: credentialID, held_delta: '-9007199254740993', spent_delta: '9007199254740993', type: 'cost_recorded', evidence_ref: 'invoice:001', created_at: at })
  assert.equal(event.held_delta, '-9007199254740993')
  for (const invalid of ['-0', '+1', '01', 1]) assert.throws(() => procurementInteger(invalid, true))
})

test('HTTP-success rejected management receipts remain rejected and strip arbitrary before/after snapshots', () => {
  const parsed = parseProcurementReceipt({ ...receipt, status: 'rejected', reason: 'revision_conflict', before: { secret: 'hidden' }, after: { private_url: 'secret-url' } }, operationID, maker)
  assert.equal(parsed.status, 'rejected')
  assert.doesNotMatch(JSON.stringify(parsed), /before|after|hidden|private_url|secret-url/)
  assert.throws(() => parseProcurementReceipt(receipt, approvalID, maker), /原操作/)
  assert.throws(() => parseProcurementReceipt(receipt, operationID, checker), /账号/)
  assert.throws(() => parseProcurementReceipt({ ...receipt, status: 'pending' }, operationID), /终态/)
})

test('cost proposals use original operation ID as proposal ID and validate frozen content and requester attribution', () => {
  const command = procurementProposalCommand(proposalDraft, operationID)
  assert.equal(command.body.amount, '9007199254740993')
  assert.equal(command.lookupPath, `/platform/procurement-cost-proposals/${operationID}`)
  assert.equal(acceptProcurementResult(command, proposal, maker).id, operationID)
  assert.equal(acceptProcurementResult(command, approved, maker).state, 'approved', 'a later approval still proves original creation')
  for (const invalid of [{ ...proposal, requester: checker }, { ...proposal, amount: '1' }, { ...proposal, reason: 'changed' }, { ...proposal, id: approvalID }]) assert.throws(() => acceptProcurementResult(command, invalid, maker))
  assert.throws(() => parseProcurementProposal({ ...proposal, expected_revision: 2 }))
  assert.throws(() => parseProcurementProposal({ ...proposal, expected_evidence_count: 2 }))
  assert.equal(parseProcurementProposal(proposal).reason, proposal.reason)
})

test('dual approval uses a distinct operation ID and only matching approval attribution confirms the original write', () => {
  const command = procurementApprovalCommand(proposal, checker, approvalID)
  assert.deepEqual(command.body, { operation_id: approvalID })
  assert.equal(command.lookupPath, `/platform/procurement-cost-proposals/${operationID}`)
  assert.equal(acceptProcurementResult(command, approved, checker).approval_operation_id, approvalID)
  assert.throws(() => procurementApprovalCommand(proposal, maker, approvalID), /不同/)
  assert.throws(() => procurementApprovalCommand(proposal, checker, operationID), /独立/)
  assert.throws(() => acceptProcurementResult(command, proposal, checker), /不能证明/)
  assert.throws(() => acceptProcurementResult(command, { ...approved, approval_operation_id: channelID }, checker), /原审批/)
  assert.throws(() => parseProcurementProposal({ ...approved, approver: maker }), /身份/)
  assert.throws(() => parseProcurementProposal({ ...proposal, approval_operation_id: approvalID }), /状态/)
})

test('confirmed proposal may advance only from proposed to approved without changing immutable evidence or money', () => {
  const journal = createProcurementJournal(), actor = procurementContext(maker, tenantID, 'finance_operator'), command = procurementProposalCommand(proposalDraft, operationID)
  journal.begin(actor, command); journal.confirm(actor, operationID, parseProcurementProposal(proposal))
  assert.doesNotThrow(() => journal.confirm(actor, operationID, parseProcurementProposal(approved)))
  assert.throws(() => journal.confirm(actor, operationID, parseProcurementProposal(proposal)), /不一致/)
  assert.equal(journal.get(actor, operationID).result.state, 'approved')
  const another = createProcurementJournal()
  another.begin(actor, command); another.confirm(actor, operationID, parseProcurementProposal(proposal))
  assert.throws(() => another.confirm(actor, operationID, parseProcurementProposal({ ...approved, amount: '1' })), /不一致/)
})

test('stale evidence rejection retains the original code and ID so approval cannot silently become a fresh command', () => {
  const journal = createProcurementJournal(), actor = procurementContext(checker, tenantID, 'finance_approver'), command = procurementApprovalCommand(proposal, checker, approvalID)
  journal.begin(actor, command)
  journal.fail(actor, approvalID, new ApiError('procurement_evidence_stale', 'evidence changed', '', 409), '须新制单')
  assert.equal(journal.get(actor, approvalID).failureCode, 'procurement_evidence_stale')
  assert.equal(journal.get(actor, approvalID).command.lookupPath, `/platform/procurement-cost-proposals/${operationID}`)
  assert.throws(() => journal.begin(actor, command), /已使用/)
})

test('safe configuration option loading covers all 50-row pages and rejects malformed or repeating cursors', async () => {
  const original = globalThis.fetch, calls = []
  try {
    globalThis.fetch = async url => { calls.push(String(url)); return new Response(JSON.stringify({ data: [account], next_cursor: String(url).includes('cursor=') ? undefined : 'next+page' })) }
    const rows = await readProcurementRows('/platform/procurement-accounts', parseProcurementAccount, () => true)
    assert.equal(rows.length, 2); assert.ok(calls[1].endsWith('cursor=next%2Bpage'))
    for (const cursor of [12, {}, 'x'.repeat(513)]) assert.throws(() => parseProcurementPage([], cursor, parseProcurementAccount))
    assert.throws(() => parseProcurementPage(Array(51).fill(account), '', parseProcurementAccount))
    globalThis.fetch = async () => new Response(JSON.stringify({ data: [account], next_cursor: 'repeated' }))
    await assert.rejects(readProcurementRows('/platform/procurement-accounts', parseProcurementAccount, () => true), /重复/)
    let active = true
    globalThis.fetch = async () => { active = false; return new Response(JSON.stringify({ data: [account] })) }
    await assert.rejects(readProcurementRows('/platform/procurement-accounts', parseProcurementAccount, () => active), /关闭/)
  } finally { globalThis.fetch = original }
})

test('unknown command transport sends operation_id only once, uses session CSRF, and recovery performs only GET', async () => {
  const original = globalThis.fetch, calls = [], command = procurementAccountCommand({ label: '账户', provider: 'supplier', state: 'active', limit: '1' }, operationID), actor = procurementContext(maker, tenantID, 'admin'), journal = createProcurementJournal()
  setCsrf('fixture-csrf')
  try {
    globalThis.fetch = async (url, init) => { calls.push({ url: String(url), init }); if (init.method === 'POST') throw new Error('connection lost'); return new Response(JSON.stringify({ data: receipt, request_id: 'fixture' })) }
    journal.begin(actor, command)
    try { await request(command.path, command.method, command.body) } catch (error) { journal.fail(actor, operationID, error, '待确认') }
    assert.equal(journal.get(actor, operationID).state, 'unknown')
    const result = acceptProcurementResult(command, (await request(command.lookupPath)).data, maker)
    journal.confirm(actor, operationID, result)
    assert.deepEqual(calls.map(call => call.init.method), ['POST', 'GET'])
    assert.equal(JSON.parse(calls[0].init.body).operation_id, operationID)
    assert.equal(calls[0].init.headers['X-CSRF-Token'], 'fixture-csrf')
    assert.equal(calls[0].init.headers['Idempotency-Key'], undefined)
    assert.equal(calls[0].init.credentials, 'include')
    assert.equal(journal.get(actor, operationID).state, 'confirmed')
  } finally { globalThis.fetch = original; setCsrf('') }
})

test('procurement editors and independent platform route wire context/close epochs and explicit original-result inspection', async () => {
  const [writer, view, router, app, attempts] = await Promise.all(['../src/components/ProcurementWriter.vue', '../src/views/ProcurementView.vue', '../src/router.ts', '../src/App.vue', '../src/components/RouteAttempts.vue'].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  assert.match(writer, /props\.modelValue.*props\.kind.*props\.account\?\.id.*props\.proposal\?\.id.*context/)
  assert.match(writer, /onBeforeUnmount\(\(\) => scope\.reset\(\)\)/)
  assert.match(writer, /original\.lookupPath/)
  assert.match(writer, /procurement_evidence_stale/)
  assert.match(view, /watch\(\[context, tab\], resetPage/)
  assert.match(view, /scope\.invalidate\('proposal'\)/)
  assert.match(router, /path: '\/platform\/procurement'.*ProcurementView/)
  assert.match(app, /path: '\/platform\/procurement'.*hasPlatform/)
  for (const reason of ['procurement_paused', 'procurement_budget_exceeded', 'procurement_bound_invalid', 'procurement_budget_unavailable']) assert.ok(attempts.includes(reason))
})
