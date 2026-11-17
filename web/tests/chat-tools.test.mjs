import assert from 'node:assert/strict'
import test from 'node:test'
import { ChatToolCallAssembler } from '../src/chat-tools.ts'
import { frameUpdate, jsonUpdate, runInference, textRequest } from '../src/inference.ts'

const chunk = (delta, finish = null) => ({ type: '', data: JSON.stringify({ model: 'retail-model', choices: [{ index: 0, delta, finish_reason: finish }] }) })
const fragments = [
  { role: 'assistant', content: null, tool_calls: [{ index: 0, id: 'call_weather', type: 'function', function: { name: 'weather', arguments: '' } }] },
  { tool_calls: [{ index: 0, function: { arguments: '{"city":' } }] },
  { tool_calls: [{ index: 0, function: { arguments: '"Paris"}' } }] },
  { tool_calls: [{ index: 1, id: 'call_time', type: 'function', function: { name: 'time', arguments: '' } }] },
  { tool_calls: [{ index: 1, function: { arguments: '{"zone":"UTC"}' } }] },
]

test('Chat tool fragments retain order and reassemble two calls by index', () => {
  const assembler = new ChatToolCallAssembler()
  for (const delta of fragments) {
    const update = frameUpdate('openai_chat', chunk(delta))
    assert.equal(update.delta, '')
    assembler.apply(update.toolCalls)
  }
  assert.deepEqual(assembler.list(), [
    { index: 0, id: 'call_weather', type: 'function', name: 'weather', arguments: '{"city":"Paris"}' },
    { index: 1, id: 'call_time', type: 'function', name: 'time', arguments: '{"zone":"UTC"}' },
  ])
  const finish = frameUpdate('openai_chat', chunk({}, 'tool_calls'))
  assert.equal(finish.finishReason, 'tool_calls')
  assert.equal(finish.terminal, undefined, 'finish_reason is not the SSE transport DONE marker')
  assert.deepEqual(frameUpdate('openai_chat', { type: '', data: '[DONE]' }), { terminal: true })
})

test('late tool identity is accepted but conflicting identity is never overwritten', () => {
  const assembler = new ChatToolCallAssembler()
  assembler.apply([{ index: 0, name: 'weather', arguments: '{"city":' }])
  assembler.apply([{ index: 0, id: 'call_late', type: 'function', arguments: '"Paris"}' }])
  assert.deepEqual(assembler.list(), [{ index: 0, id: 'call_late', type: 'function', name: 'weather', arguments: '{"city":"Paris"}' }])
  for (const delta of [{ index: 0, id: 'different' }, { index: 0, name: 'different' }, { index: 0, type: 'other' }]) assert.throws(() => assembler.apply([delta]))
  for (const delta of [{ tool_calls: [{ index: 0, id: '' }] }, { tool_calls: [{ index: 0, function: { name: '' } }] }]) {
    const update = frameUpdate('openai_chat', chunk(delta))
    assert.throws(() => assembler.apply(update.toolCalls), /冲突/, 'empty-after-set is also an identity conflict')
  }
  assert.throws(() => assembler.apply([{ index: 64, arguments: '{}' }]), /索引/)
})

test('Chat JSON exposes structured calls and actual terminal reason without executing tools', () => {
  const update = jsonUpdate('openai_chat', { choices: [{ index: 0, message: { role: 'assistant', content: null, tool_calls: [{ id: 'call_weather', type: 'function', function: { name: 'weather', arguments: '{"city":"Paris"}' } }] }, finish_reason: 'tool_calls' }], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } })
  assert.equal(update.delta, '')
  assert.equal(update.finishReason, 'tool_calls')
  assert.deepEqual(update.toolCalls, [{ index: 0, id: 'call_weather', type: 'function', name: 'weather', arguments: '{"city":"Paris"}' }])
  assert.equal(update.terminal, true)
  for (const finish of ['stop', 'length', 'content_filter']) assert.equal(jsonUpdate('openai_chat', { choices: [{ message: { role: 'assistant', content: '' }, finish_reason: finish }] }).finishReason, finish)
  assert.throws(() => jsonUpdate('openai_chat', { choices: [{ message: { role: 'assistant', content: 'truncated' } }] }), /终态/)
  assert.throws(() => frameUpdate('openai_chat', chunk({}, 'unexpected')), /终态/)
})

test('real Chat SSE reader forwards tool updates and completes only at DONE', async () => {
  const events = [...fragments.map(delta => chunk(delta).data), chunk({}, 'tool_calls').data, JSON.stringify({ choices: [], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } })]
  const bytes = new TextEncoder()
  const previous = globalThis.fetch
  globalThis.fetch = async () => new Response(new ReadableStream({
    start(controller) {
      for (const event of events) controller.enqueue(bytes.encode(`data: ${event}\n\n`))
      controller.enqueue(bytes.encode('data: [DONE]\n\n')); controller.close()
    },
  }), { headers: { 'Content-Type': 'text/event-stream', 'X-Request-ID': 'chat-tool-request' } })
  try {
    const updates = []
    const result = await runInference('openai_chat', 'key', textRequest('openai_chat', 'retail-model', 'hello', 20, true), new AbortController().signal, update => updates.push(update))
    assert.equal(result.requestId, 'chat-tool-request')
    assert.ok(result.firstContentMs !== undefined)
    assert.equal(updates.filter(update => update.toolCalls).length, fragments.length)
    assert.equal(updates.filter(update => update.finishReason === 'tool_calls').length, 1)
    assert.equal(updates.at(-1).terminal, true)
  } finally { globalThis.fetch = previous }
})
