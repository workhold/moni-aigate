import assert from 'node:assert/strict'
import test from 'node:test'
import { outputPriceAfterProtocolChange } from '../src/model-contract.ts'
import { resetResponseForModelChange } from '../src/playground-state.ts'

test('switching retrieval pricing back to generation requires a deliberate output price', () => {
  let price = '20.500000'
  price = outputPriceAfterProtocolChange(price, false, true)
  assert.equal(price, '0')
  price = outputPriceAfterProtocolChange(price, true, true)
  assert.equal(price, '0')
  price = outputPriceAfterProtocolChange(price, true, false)
  assert.equal(price, '')
  // The required price field now prevents publication until the operator
  // chooses a value, including an explicitly chosen zero if appropriate.
  assert.equal(outputPriceAfterProtocolChange('1.250000', false, false), '1.250000')
  assert.equal(outputPriceAfterProtocolChange('0', false, false), '0')
})

function completedPageState() {
  return {
    output: 'previous generated answer', usage: { input_tokens: 10, output_tokens: 5 }, requestId: 'previous-request',
    metrics: { requestId: 'previous-request', durationMs: 10, bytes: 200 }, error: 'previous warning', status: '调用完成',
    key: 'page-only-key', prompt: 'input text', query: 'search query', documents: 'first\nsecond',
  }
}

test('selecting a different model clears all response facts while preserving key and request inputs', () => {
  const state = completedPageState()
  resetResponseForModelChange(state, 'generated-model', 'embedding-model')
  assert.deepEqual(state, {
    output: '', usage: {}, requestId: '', metrics: null, error: '', status: '',
    key: 'page-only-key', prompt: 'input text', query: 'search query', documents: 'first\nsecond',
  })
  // Losing authorization/selection also removes the previous model's result.
  const removed = completedPageState()
  resetResponseForModelChange(removed, 'generated-model', undefined)
  assert.equal(removed.output, '')
  assert.equal(removed.requestId, '')
  assert.equal(removed.metrics, null)
})

test('refreshing the same model retains its result, request ID, usage and timing', () => {
  const state = completedPageState()
  const before = structuredClone(state)
  const usage = state.usage; const metrics = state.metrics
  resetResponseForModelChange(state, 'same-model', 'same-model')
  assert.deepEqual(state, before)
  assert.equal(state.usage, usage)
  assert.equal(state.metrics, metrics)
})
