import assert from 'node:assert/strict'
import test from 'node:test'
import { money, cnyToMicro } from '../src/format.ts'

test('micro-CNY conversion remains exact beyond JavaScript safe integers', () => {
  assert.equal(money('9007199254740993'), '¥9,007,199,254.740993')
  assert.equal(cnyToMicro('9007199254.740993'), '9007199254740993')
  assert.equal(cnyToMicro('9223372036854.775807'), '9223372036854775807')
  assert.equal(money('-6000'), '−¥0.006000')
  assert.equal(money('0'), '¥0.000000')
})
test('unknown balances stay unknown and input cannot silently round money', () => {
  assert.equal(money(null), '—')
  assert.equal(money(undefined), '—')
  assert.equal(money(''), '—')
  assert.equal(cnyToMicro('2.1'), '2100000')
  for (const invalid of ['-1', '1.0000001', '1e6', 'NaN', '9223372036854.775808']) assert.throws(() => cnyToMicro(invalid))
})
