import assert from 'node:assert/strict'
import test from 'node:test'
import { createHash } from 'node:crypto'
import { applyRetailQuote, parsePricedModels } from '../src/customer-pricing.ts'
import { parseCostPrice, parseDefaultPrice, parseGroupPrice, parsePriceGroup, parsePricingAssignment, parsePricingEnvelope, parsePricingPage, parsePricingPreview, parsePricingReceipt, parseRetailPrice, parseTextRetailRates, parsePricingModelChoice, pricingInputRates, pricingCommand, pricingCursor, pricingDecimal, pricingFingerprint, pricingFrame, pricingID, pricingLabel, pricingPreviewPath, pricingQuery, pricingTime } from '../src/pricing.ts'

const actor = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', group = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', tenant = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', model = 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', version = 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', operation = '11111111-1111-4111-8111-111111111111'
const domain = { schema_id: '10000000-0000-4000-8000-000000000001', schema_digest: 'ab'.repeat(32), variant_key: 'default' }
const rates = () => ({ schema_id: domain.schema_id, version: '1', side: 'retail', currency: 'CNY', rates: [{ dimension: 'text_input_token', unit: 'token', numerator: huge, denominator: '1000000' }, { dimension: 'text_output_token', unit: 'token', numerator: '0', denominator: '1' }] })
const at = '2026-11-01T00:00:00.000001Z', before = '2026-10-01T00:00:00.123456Z', huge = '9007199254740993'
const groupView = patch => ({ id: group, label: '团队组', state: 'active', revision: huge, created_at: before, ...patch })
const assignment = patch => ({ tenant_id: tenant, price_group_id: null, pricing_revision: huge, ...patch })
const price = patch => ({ id: version, model_id: model, price_group_id: group, ...domain, currency: 'CNY', rates: rates(), effective_at: before, created_at: at, ...patch })
const quote = patch => ({ model_id: model, source: 'group', status: 'available', price_group_id: group, price_version_id: version, ...domain, currency: 'CNY', rates: rates(), effective_at: before, as_of: at, ...patch })
const envelope = data => ({ data, request_id: 'safe-request' })
const cursor = value => Buffer.from(value).toString('base64url')

test('group vector fingerprint has the frozen Go/SQL golden value and binds the full domain', async () => {
  const body = { model_id: model, ...domain, input_rate: huge, output_rate: '0', effective_at: before }
  const command = await pricingCommand('group_price.publish', operation, actor, body, group)
  assert.equal(command.input_fingerprint, '14aac71c989772d94efbda8faf1a39a758a618b645a71539a55de7664de26564')
  for (const patch of [{ schema_id: tenant }, { schema_digest: '00'.repeat(32) }, { input_rate: '9007199254740994' }, { output_rate: '1' }, { effective_at: at }]) assert.notEqual((await pricingCommand('group_price.publish', operation, actor, { ...body, ...patch }, group)).input_fingerprint, command.input_fingerprint)
  for (const patch of [{ schema_id: undefined }, { schema_digest: 'AB'.repeat(32) }, { variant_key: '' }, { rates: rates() }]) await assert.rejects(pricingCommand('group_price.publish', operation, actor, { ...body, ...patch }, group))
})
test('group rates preserve exact reduced int64 values and reject legacy or malformed arrays without sorting', () => {
  assert.deepEqual(pricingInputRates(domain.schema_id, huge, '0'), rates())
  const good = rates(), swappedKeys = Object.fromEntries(Object.entries(good).reverse())
  assert.deepEqual(parseTextRetailRates(swappedKeys, domain.schema_id), good)
  for (const patch of [{ side: 'supplier' }, { version: 1 }, { schema_id: tenant }, { currency: 'USD' }, { rates: [...good.rates].reverse() }, { rates: [good.rates[0]] }, { rates: [good.rates[0], good.rates[0]] }, { extra: true }]) assert.throws(() => parseTextRetailRates({ ...good, ...patch }, domain.schema_id))
  for (const patch of [{ numerator: 0 }, { numerator: '01' }, { numerator: '9223372036854775808' }, { denominator: '1000001' }, { denominator: '0' }, { denominator: '01' }, { numerator: '0', denominator: '2' }, { unit: 'image' }, { extra: true }]) assert.throws(() => parseTextRetailRates({ ...good, rates: [{ ...good.rates[0], ...patch }, good.rates[1]] }, domain.schema_id))
  const { rates: _, ...rest } = price()
  assert.throws(() => parseGroupPrice({ ...rest, input_rate: huge, output_rate: '0' }))
  assert.equal(parsePricingModelChoice({ id: model, public_name: 'embedding', protocol: 'openai_embeddings', ...domain }).inputOnly, true)
  assert.throws(() => parsePricingModelChoice({ id: model, public_name: 'bad', protocol: 'audio', ...domain }))
})
function receipt(command, result, revision = 'revision' in result ? result.revision : 'pricing_revision' in result ? result.pricing_revision : null) { return { operation: { operation_id: command.operation_id, kind: command.kind, actor, scope: command.scope, target: command.target, input_fingerprint: command.input_fingerprint, resource_id: result.id || tenant, revision, created_at: command.kind === 'price_group.create' ? result.created_at : at }, result } }

test('strict IDs, int64 strings and UTF-8 labels never coerce numbers, nil IDs or surrogate input', () => {
  assert.equal(pricingDecimal(huge, true), huge)
  assert.equal(pricingDecimal('9223372036854775807'), '9223372036854775807')
  assert.equal(pricingLabel(' 团队组 ', true), '团队组')
  assert.equal(pricingLabel('中'.repeat(33)), '中'.repeat(33))
  for (const value of [0, 1, null, '01','-0','+1','1e3','1.0','9223372036854775808']) assert.throws(() => pricingDecimal(value))
  assert.throws(() => pricingDecimal('0', true))
  for (const value of ['00000000-0000-0000-0000-000000000000', actor.toUpperCase(), actor + ' ', null]) assert.throws(() => pricingID(value))
  for (const value of ['', ' ', 'x\n', '\ud800', '中'.repeat(34), ' leading']) assert.throws(() => pricingLabel(value))
})
test('UTC6 normalization preserves microseconds and rejects invalid calendars, offsets and zero time', () => {
  assert.equal(pricingTime(at), at)
  assert.equal(pricingTime('2026-11-01T00:00:00Z', true), '2026-11-01T00:00:00.000000Z')
  assert.equal(pricingTime('2024-02-29T23:59:59.000001Z', true), '2024-02-29T23:59:59.000001Z')
  for (const value of ['2026-02-29T00:00:00.000000Z','2026-02-30T00:00:00.000000Z','2026-11-01T24:00:00.000000Z','2026-11-01T00:00:60Z','2026-11-01T00:00:00.1234567Z','0001-01-01T00:00:00Z','0000-11-01T00:00:00Z','2026-11-01T00:00:00+00:00']) assert.throws(() => pricingTime(value, true))
  assert.throws(() => pricingTime('2026-11-01T00:00:00Z'))
})
test('framing matches an independent byte construction and distinguishes NULL, empty, Unicode and domains', async () => {
  const parts = ['tenant_pricing.assign', actor, tenant, `/api/platform/tenants/${tenant}/pricing`, huge, null]
  const chunks = [Buffer.from('moni-aigate/pricing-command/v1\0'), Buffer.alloc(4)]
  chunks[1].writeInt32BE(parts.length)
  for (const part of parts) { const raw = part === null ? null : Buffer.from(part), length = Buffer.alloc(4); length.writeInt32BE(raw === null ? -1 : raw.length); chunks.push(length); if (raw) chunks.push(raw) }
  const frame = Buffer.concat(chunks)
  assert.deepEqual(Buffer.from(pricingFrame(parts)), frame)
  assert.equal(await pricingFingerprint(parts), createHash('sha256').update(frame).digest('hex'))
  assert.notEqual(await pricingFingerprint(['中',null]), await pricingFingerprint(['中','']))
  assert.notEqual(await pricingFingerprint(['a','bc']), await pricingFingerprint(['ab','c']))
  assert.notDeepEqual(pricingFrame(parts), pricingFrame(parts, 'moni-aigate/pricing-cursor/v1'))
})
test('all four requests freeze exact safe input, target, scope, normalized time and operation-independent fingerprint', async () => {
  const create = await pricingCommand('price_group.create', operation, actor, { label: ' 团队组 ' })
  assert.deepEqual(create.body, { operation_id: operation, label: '团队组' }); assert.equal(create.target, '/api/platform/price-groups')
  const update = await pricingCommand('price_group.update', operation, actor, { expected_revision: huge, label: '团队组', state: 'retired' }, group)
  assert.equal(update.method, 'PUT'); assert.equal(update.scope, 'platform')
  const publish = await pricingCommand('group_price.publish', operation, actor, { model_id: model, ...domain, input_rate: huge, output_rate: '0', effective_at: '2026-11-01T00:00:00Z' }, group)
  assert.equal(publish.body.effective_at, '2026-11-01T00:00:00.000000Z'); assert.equal(publish.target, `/api/platform/price-groups/${group}/price-versions`)
  const assign = await pricingCommand('tenant_pricing.assign', operation, actor, { expected_revision: huge, price_group_id: null }, tenant)
  assert.equal(assign.scope, tenant); assert.equal(assign.body.price_group_id, null)
  assert.equal(assign.input_fingerprint, (await pricingCommand('tenant_pricing.assign', version, actor, { expected_revision: huge, price_group_id: null }, tenant)).input_fingerprint)
  assert.notEqual(assign.input_fingerprint, (await pricingCommand('tenant_pricing.assign', operation, actor, { expected_revision: huge, price_group_id: group }, tenant)).input_fingerprint)
  assert(Object.isFrozen(assign.body)); assert(Object.isFrozen(assign))
  for (const body of [{ expected_revision: huge }, { expected_revision: huge, price_group_id: '' }, { expected_revision: huge, price_group_id: null, reset: true }]) await assert.rejects(pricingCommand('tenant_pricing.assign', operation, actor, body, tenant))
})
test('strict group, assignment and price DTOs reject secrets, omitted NULL and numeric precision loss', () => {
  assert.equal(parsePriceGroup(groupView()).revision, huge)
  assert.equal(parsePricingAssignment(assignment(), tenant).price_group_id, null)
  assert.equal(parseGroupPrice(price(), group).rates.rates[0].numerator, huge)
  for (const value of [{ ...groupView(), endpoint: 'private' }, groupView({ revision: 2 }), groupView({ state: 'enabled' }), groupView({ created_at: '2026-11-01T00:00:00Z' })]) assert.throws(() => parsePriceGroup(value))
  assert.throws(() => parsePricingAssignment({ tenant_id: tenant, pricing_revision: huge }))
  assert.throws(() => parseGroupPrice(price({ price_group_id: null })))
  assert.throws(() => parseGroupPrice(price({ input_rate: 9007199254740992 })))
})
test('available/unavailable is a complete vector union; no free fallback, future quote or cross-model acceptance', () => {
  assert.deepEqual(parseRetailPrice(quote(), model), quote())
  const missing = quote({ status: 'unavailable', price_version_id: null, rates: null, effective_at: null })
  assert.equal(parseRetailPrice(missing).source, 'group')
  for (const patch of [{ source: 'default' }, { status: 'unavailable' }, { price_version_id: null }, { rates: null }, { effective_at: '2027-01-01T00:00:00.000000Z' }, { supplier_cost: '0' }, { input_rate: huge }]) assert.throws(() => parseRetailPrice(quote(patch), model))
  assert.throws(() => parseRetailPrice(quote(), group))
  const models = parsePricedModels([{ id: model, public_name: 'test', retail_price: quote() }])
  assert.equal(applyRetailQuote(models, missing)[0].retail_price?.rates, null)
  assert.throws(() => parsePricedModels([{ ...models[0], input_rate: '0' }]))
  const inputOnlyRates = rates(); inputOnlyRates.rates[1] = { ...inputOnlyRates.rates[1], numerator: '1' }
  assert.throws(() => parsePricedModels([{ ...models[0], protocol: 'openai_embeddings', retail_price: quote({ rates: inputOnlyRates }) }]))
})
test('receipts match every identity field and original body/CAS, and never reconstruct current values', async () => {
  const original = groupView(), command = await pricingCommand('price_group.update', operation, actor, { expected_revision: huge, label: '新名称', state: 'active' }, group, original)
  const result = groupView({ label: '新名称', revision: '9007199254740994' }), raw = receipt(command, result)
  assert.deepEqual(parsePricingReceipt(raw, command), raw)
  for (const field of ['operation_id','actor','scope','target','kind','input_fingerprint']) { const changed = { ...raw, operation: { ...raw.operation, [field]: field === 'kind' ? 'price_group.create' : field === 'input_fingerprint' ? '0'.repeat(64) : field === 'scope' ? tenant : field === 'target' ? `/api/platform/price-groups/${tenant}` : version } }; assert.throws(() => parsePricingReceipt(changed, command)) }
  for (const patch of [{ label: '当前新值' }, { revision: huge }, { revision: '9007199254740995' }]) assert.throws(() => parsePricingReceipt(receipt(command, { ...result, ...patch }), command))
  const noop = await pricingCommand('price_group.update', operation, actor, { expected_revision: huge, label: original.label, state: original.state }, group, original)
  assert.equal(parsePricingReceipt(receipt(noop, original), noop).result.revision, huge)
  assert.throws(() => parsePricingReceipt(receipt(noop, groupView({ revision: '9007199254740994' })), noop))
  assert.throws(() => parsePricingReceipt({ ...raw, replayed: true }, command))
})
test('create, publish and assignment receipts enforce resource/type/domain and stored timestamps', async () => {
  const create = await pricingCommand('price_group.create', operation, actor, { label: '团队组' }), created = groupView({ revision: '1', created_at: at })
  assert.equal(parsePricingReceipt(receipt(create, created), create).result.state, 'active')
  const publish = await pricingCommand('group_price.publish', operation, actor, { model_id: model, ...domain, input_rate: huge, output_rate: '0', effective_at: before }, group)
  assert.equal(parsePricingReceipt(receipt(publish, price()), publish).operation.revision, null)
  assert.throws(() => parsePricingReceipt(receipt(publish, price({ price_group_id: tenant })), publish))
  assert.throws(() => parsePricingReceipt(receipt(publish, price(), '1'), publish))
  const assign = await pricingCommand('tenant_pricing.assign', operation, actor, { expected_revision: huge, price_group_id: group }, tenant, assignment())
  assert.equal(parsePricingReceipt(receipt(assign, assignment({ price_group_id: group, pricing_revision: '9007199254740994' })), assign).result.price_group_id, group)
})
test('opaque cursor consumes canonical base64url, preserves empty-string terminal and rejects 022 null', () => {
  const next = cursor('{"v":1,"opaque":"value"}')
  assert.equal(pricingCursor(next), next); assert.equal(pricingCursor(''), '')
  for (const value of [null, undefined, next + '=', 'a', 'AB', 'https://bad', cursor('x'.repeat(769))]) assert.throws(() => pricingCursor(value))
  assert.equal(pricingPreviewPath(tenant, null, next), `/platform/tenants/${tenant}/pricing-preview?price_group_id=default&cursor=${next}`)
  assert.equal(pricingQuery('/platform/price-groups', next), `/platform/price-groups?cursor=${next}`)
  assert.deepEqual(parsePricingPage({ ...envelope([]), next_cursor: '' }, parsePriceGroup, r => r.id), { rows: [], next: '' })
  for (const patch of [{ next_cursor: null }, { limit: 50 }, { data: [groupView(),groupView()] }, { data: Array.from({ length: 51 }, groupView) }]) assert.throws(() => parsePricingPage({ ...envelope([]), next_cursor: '', ...patch }, parsePriceGroup, r => r.id))
  assert.throws(() => parsePricingEnvelope({ ...envelope(groupView()), next_cursor: '' }, parsePriceGroup))
})
test('preview keeps page context for empty/suspended/retired and prevents partial-page totals or mixed observations', () => {
  const context = { tenant_id: tenant, tenant_status: 'suspended', current_price_group_id: null, current_pricing_revision: huge, target_price_group_id: group, target_group_state: 'retired', assignable: false, as_of: at }
  const value = { ...envelope([]), next_cursor: '', context }
  assert.equal(parsePricingPreview(value, tenant, group).context.assignable, false)
  const row = { model_id: model, public_name: 'model', protocol: 'openai_chat', retail_price: quote() }
  assert.equal(parsePricingPreview({ ...value, data: [row] }, tenant, group).rows.length, 1)
  for (const patch of [{ context: { ...context, assignable: true } }, { context: { ...context, target_group_state: null } }, { context: { ...context, as_of: before } }, { total_missing: 0 }, { data: [{ ...row, retail_price: quote({ price_group_id: tenant }) }] }]) assert.throws(() => parsePricingPreview({ ...value, data: [row], ...patch }, tenant, group))
})

const channel = '22222222-2222-4222-8222-222222222222'
const supplierRates = () => ({ ...rates(), side: 'supplier' })
const defaultPrice = patch => price({ price_group_id: null, ...patch })
const costPrice = patch => ({ id: version, channel_id: channel, upstream_model: 'supplier/exact-model', ...domain, currency: 'CNY', basis: 'contract', rates: supplierRates(), effective_at: before, created_at: at, expected_channel_revision: huge, channel_revision: '9007199254740994', ...patch })
const costBody = patch => ({ channel_id: channel, upstream_model: 'supplier/exact-model', ...domain, basis: 'contract', input_rate: huge, output_rate: '0', effective_at: before, expected_channel_revision: huge, ...patch })

test('default price publication has a fixed target, frozen golden fingerprint and null group receipt', async () => {
  const body = { model_id: model, ...domain, input_rate: huge, output_rate: '0', effective_at: before }
  const command = await pricingCommand('default_price.publish', operation, actor, body)
  assert.equal(command.target, '/api/platform/price-versions'); assert.equal(command.path, '/platform/price-versions')
  assert.equal(command.input_fingerprint, '5fafc4301d437fa9222ee44c47afae8ad92996b0bdc5545227fc827c73a8b3e3')
  assert.notEqual(command.input_fingerprint, (await pricingCommand('group_price.publish', operation, actor, body, group)).input_fingerprint)
  for (const patch of [{ schema_id: tenant }, { schema_digest: '00'.repeat(32) }, { input_rate: '9007199254740994' }, { output_rate: '1' }, { effective_at: at }]) assert.notEqual((await pricingCommand('default_price.publish', operation, actor, { ...body, ...patch })).input_fingerprint, command.input_fingerprint)
  assert.equal(parsePricingReceipt(receipt(command, defaultPrice()), command).result.price_group_id, null)
  assert.throws(() => parseDefaultPrice(price()))
  const changed = rates(); changed.rates[1] = { ...changed.rates[1], numerator: '1' }
  for (const result of [defaultPrice({ price_group_id: group }), defaultPrice({ rates: changed }), defaultPrice({ effective_at: at }), defaultPrice({ model_id: tenant })]) assert.throws(() => parsePricingReceipt(receipt(command, result), command))
  assert.throws(() => parsePricingReceipt(receipt(command, defaultPrice(), '1'), command))
})
test('cost publication keeps the known/unknown union, channel CAS and exact supplier rates', async () => {
  const command = await pricingCommand('cost_price.publish', operation, actor, costBody())
  assert.equal(command.target, '/api/platform/cost-versions'); assert.equal(command.path, '/platform/cost-versions')
  assert.equal(command.input_fingerprint, '117495aaf9e9de42449ed56a6a7e14ff08792513237cdeab4858135069378594')
  assert.deepEqual({ ...command.body }, { operation_id: operation, ...costBody() })
  const unknown = await pricingCommand('cost_price.publish', operation, actor, costBody({ basis: 'unknown', input_rate: null, output_rate: null }))
  assert.equal(unknown.input_fingerprint, '914b01ccb186b92559320ceccdd7f5052c3d0f65a59498e98ae9a7a1ccc03bbd')
  for (const patch of [{ basis: 'unknown' }, { input_rate: null }, { basis: 'guess' }, { upstream_model: ' padded' }, { upstream_model: '' }, { expected_channel_revision: '0' }, { effective_at: null }, { schema_digest: 'AB'.repeat(32) }, { variant_key: 'other' }]) await assert.rejects(pricingCommand('cost_price.publish', operation, actor, costBody(patch)))
  for (const patch of [{ channel_id: group }, { upstream_model: 'other' }, { basis: 'invoice' }, { input_rate: '1' }, { effective_at: at }, { expected_channel_revision: '9007199254740994' }]) assert.notEqual((await pricingCommand('cost_price.publish', operation, actor, costBody(patch))).input_fingerprint, command.input_fingerprint)
  assert.equal(parsePricingReceipt(receipt(command, costPrice()), command).result.channel_revision, '9007199254740994')
  assert.equal(parsePricingReceipt(receipt(unknown, costPrice({ basis: 'unknown', rates: null })), unknown).result.rates, null)
  assert.equal(parseCostPrice(costPrice({ channel_revision: '9223372036854775807', expected_channel_revision: '9223372036854775806' })).channel_revision, '9223372036854775807')
  for (const result of [costPrice({ channel_revision: huge }), costPrice({ rates: rates() }), costPrice({ basis: 'unknown' }), costPrice({ basis: 'invoice' }), costPrice({ upstream_model: 'other' }), costPrice({ input_rate: huge })]) assert.throws(() => parsePricingReceipt(receipt(command, result), command))
})
