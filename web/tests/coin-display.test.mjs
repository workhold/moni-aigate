import assert from 'node:assert/strict'
import test from 'node:test'
import { applyCoinDisplay, coinDisplayCommand, coinDisplayConfirmation, coinDisplayState, loadCoinDisplay, parseCoinDisplayPublic, parseCoinDisplaySetting, parseCoinDisplaySettings, publicCoinDisplay, resetCoinDisplay } from '../src/coin-display.ts'
import { coin, money } from '../src/format.ts'

const operation = '11111111-1111-4111-8111-111111111111', actor = '22222222-2222-4222-8222-222222222222', at = '2026-11-15T00:00:00.000000Z'
const publicValue = { version: '2', display_name: '点数', fiat_currency: 'CNY', rate_num: '7', rate_den: '10' }
const setting = { ...publicValue, operation_id: operation, actor, reason: 'display review', created_at: at }
test.afterEach(() => resetCoinDisplay())

test('display genesis is label-only and public settings expose no administration fields', () => {
  assert.equal(coin('1000000'), '1.000000 币')
  const genesis = parseCoinDisplaySetting({ ...setting, version: '1', display_name: '币', fiat_currency: null, rate_num: null, rate_den: null, actor: null })
  assert.deepEqual(publicCoinDisplay(genesis), { version: '1', display_name: '币', fiat_currency: null, rate_num: null, rate_den: null })
  assert.deepEqual(Object.keys(publicCoinDisplay(genesis)), ['version', 'display_name', 'fiat_currency', 'rate_num', 'rate_den'])
  const history = parseCoinDisplaySettings({ current: genesis, versions: [genesis] })
  assert.equal(history.current.version, '1')
  assert.throws(() => parseCoinDisplaySettings({ current: genesis, versions: [] }))
})

test('approximate display uses exact BigInt rational conversion with version and disclaimer', () => {
  applyCoinDisplay(publicValue)
  assert.equal(coin('3000000'), '3.000000 点数（约合 ¥2.100000 · v2，仅供参考）')
  assert.equal(coin('-1500000'), '−1.500000 点数（约合 −¥1.050000 · v2，仅供参考）')
  assert.equal(coinDisplayState.displayName, '点数')
  applyCoinDisplay({ ...publicValue, fiat_currency: 'USD', rate_num: '2', rate_den: '1' })
  assert.equal(coin('1500000'), '1.500000 点数（约合 $3.000000 · v2，仅供参考）')
  const max = '9223372036854775807'
  applyCoinDisplay({ ...publicValue, rate_num: max, rate_den: '1' })
  assert.equal(coin(max), `9,223,372,036,854.775807 点数（约合 ${money((BigInt(max) * BigInt(max)).toString())} · v2，仅供参考）`)
  applyCoinDisplay({ ...publicValue, fiat_currency: null, rate_num: null, rate_den: null })
  assert.equal(coin('1000000'), '1.000000 点数')
})

test('display parsers reject invalid rate combinations, unsafe names and stale histories', () => {
  for (const patch of [
    { fiat_currency: 'EUR' }, { fiat_currency: null }, { rate_num: '0' }, { rate_num: '-1' }, { rate_num: 7 }, { rate_num: '01' },
    { display_name: '' }, { display_name: ' 点数' }, { display_name: '点\n' }, { display_name: '点'.repeat(33) }, { extra: true },
  ]) assert.throws(() => parseCoinDisplayPublic({ ...publicValue, ...patch }))
  for (const patch of [{ operation_id: 'not-a-uuid' }, { actor: 'not-a-uuid' }, { actor: null }, { version: '1', actor }, { created_at: '2026-11-15' }]) assert.throws(() => parseCoinDisplaySetting({ ...setting, ...patch }))
  assert.throws(() => parseCoinDisplaySettings({ current: setting, versions: [{ ...setting, version: '3' }] }))
  assert.throws(() => parseCoinDisplaySettings({ current: { ...setting, version: '3' }, versions: [setting] }))
})

test('display command requires confirmation and complete enabled or disabled fields', () => {
  const command = coinDisplayCommand({ operation_id: operation, expected_revision: '2', display_name: '点数', fiat_currency: 'CNY', rate_num: '7', rate_den: '10', reason: 'display review', confirmation: coinDisplayConfirmation })
  assert.deepEqual(command, { operation_id: operation, expected_revision: '2', display_name: '点数', fiat_currency: 'CNY', rate_num: '7', rate_den: '10', reason: 'display review', confirmation: coinDisplayConfirmation })
  const disabled = coinDisplayCommand({ operation_id: operation, expected_revision: '2', display_name: '币', fiat_currency: null, rate_num: null, rate_den: null, reason: 'disable approximate', confirmation: coinDisplayConfirmation })
  assert.equal(disabled.fiat_currency, null)
  for (const patch of [{ confirmation: 'no' }, { rate_num: null }, { rate_num: '0' }, { display_name: '' }]) assert.throws(() => coinDisplayCommand({ ...command, ...patch }))
})

test('failed public reload removes a previously enabled approximate display', async () => {
  applyCoinDisplay(publicValue)
  assert.match(coin('1000000'), /约合/)
  const original = globalThis.fetch
  globalThis.fetch = async () => new Response(JSON.stringify({ error: { code: 'unavailable', message: 'unavailable' } }), { status: 503, headers: { 'Content-Type': 'application/json' } })
  try {
    await assert.rejects(loadCoinDisplay())
    assert.equal(coin('1000000'), '1.000000 币')
    assert.equal(coinDisplayState.loaded, false)
  } finally { globalThis.fetch = original }
})
