import assert from 'node:assert/strict'
import test from 'node:test'
import { frameUpdate, geminiRequest, inferenceHTTP, jsonUpdate, protocolInfo, runInference, textRequest } from '../src/inference.ts'
import { modelGenerationSettings } from '../src/model-contract.ts'
import { resetResponseForModelChange } from '../src/playground-state.ts'

const protocol = 'gemini_generate_content'
const usage = { promptTokenCount: 10, candidatesTokenCount: 3, thoughtsTokenCount: 2, totalTokenCount: 15, cachedContentTokenCount: 4 }
const result = { modelVersion: 'public-gemini', candidates: [{ index: 0, content: { role: 'model', parts: [{ text: 'private-thought', thought: true }, { text: '你好' }, { text: ' world' }] }, finishReason: 'STOP' }], usageMetadata: usage, private_debug: 'secret' }
const bytes = value => new TextEncoder().encode(value)
function streamResponse(raw, id = 'gemini-request') {
  const data = bytes(raw)
  return new Response(new ReadableStream({ start(controller) { for (let index = 0; index < data.length; index += 3) controller.enqueue(data.slice(index, index + 3)); controller.close() } }), { headers: { 'Content-Type': 'text/event-stream', 'X-Request-ID': id } })
}

test('Gemini uses model and action in the URL while curl and fetch receive exact native JSON', () => {
  const info = protocolInfo(protocol)
  assert.equal(info.outputField, 'maxOutputTokens')
  assert.equal(info.inputOnly, undefined)
  for (const stream of [false, true]) {
    const draft = geminiRequest('public-gemini_2.5', 'hello', 123, stream, 'Be concise.')
    const target = inferenceHTTP(protocol, draft)
    assert.equal(target.endpoint, `/v1beta/models/public-gemini_2.5:${stream ? 'streamGenerateContent?alt=sse' : 'generateContent'}`)
    assert.equal(target.stream, stream)
    assert.deepEqual(target.body, { contents: [{ role: 'user', parts: [{ text: 'hello' }] }], generationConfig: { maxOutputTokens: 123, candidateCount: 1 }, systemInstruction: { parts: [{ text: 'Be concise.' }] } })
    for (const field of ['model', 'stream', 'max_tokens', 'max_output_tokens', 'maxOutputTokens', 'messages', 'tools', 'cachedContent']) assert.equal(field in target.body, false, field)
    assert.equal(draft.model, 'public-gemini_2.5')
    assert.equal(draft.stream, stream)
  }
  assert.deepEqual(textRequest(protocol, 'model', 'hello', 8, true), geminiRequest('model', 'hello', 8, true))
  assert.equal('systemInstruction' in geminiRequest('model', 'hello', 8, false, ' \n '), false)
  for (const model of ['', 'model..name', '../model', 'a/model', 'a%2fmodel', 'a:model', '?key=secret', '模型', '-name', 'a'.repeat(101)]) assert.throws(() => geminiRequest(model, 'hello', 8, false), /Gemini/)
  for (const output of [0, -1, 1.5, NaN]) assert.throws(() => geminiRequest('model', 'hello', output, false), /正整数/)
  assert.throws(() => geminiRequest('model', ' \n ', 1, false), /纯文本/)
  const draft = geminiRequest('model', 'hello', 8, false)
  assert.throws(() => inferenceHTTP(protocol, { ...draft, tools: [] }), /原生字段/)
  assert.throws(() => inferenceHTTP(protocol, { ...draft, model: 'models/not-safe' }), /原生字段/)
})

test('Gemini model publication forces tools off and preserves generation output limits', () => {
  assert.deepEqual(modelGenerationSettings(protocol, 2048, 512, true), { output_limit: 2048, default_output_limit: 512, supports_tools: false })
  assert.deepEqual(modelGenerationSettings(protocol, 1, 1, false), { output_limit: 1, default_output_limit: 1, supports_tools: false })
  for (const [maximum, initial] of [[0, 0], [10, 0], [1, 2], [10, null]]) assert.throws(() => modelGenerationSettings(protocol, maximum, initial, true))
  assert.equal(modelGenerationSettings('openai_chat', 10, 5, true).supports_tools, true)
})

test('Gemini JSON hides thought text and confirms candidate plus thought usage without counting cache twice', () => {
  const update = jsonUpdate(protocol, result)
  assert.equal(update.delta, '你好 world')
  assert.equal(update.terminal, true)
  assert.deepEqual(update.usage, usage)
  assert.equal(update.delta.includes('private-thought'), false)
  assert.equal(update.delta.includes('secret'), false)
  const zero = jsonUpdate(protocol, { candidates: [{ index: 0, finishReason: 'MAX_TOKENS' }], usageMetadata: { promptTokenCount: 0, candidatesTokenCount: 0, totalTokenCount: 0 } })
  assert.equal(zero.terminal, true)
  for (const metadata of [undefined, {}, { promptTokenCount: 1, candidatesTokenCount: 1 }, { promptTokenCount: 1, candidatesTokenCount: 1, totalTokenCount: 2, cachedContentTokenCount: 2 }, { promptTokenCount: 1, candidatesTokenCount: 1, totalTokenCount: 3 }, { ...usage, thoughtsTokenCount: -1 }, { ...usage, totalTokenCount: 15.5 }, { ...usage, searchUnits: 1 }, { ...usage, totalTokenCount: Number.MAX_SAFE_INTEGER + 1 }]) assert.throws(() => jsonUpdate(protocol, { ...result, usageMetadata: metadata }), /用量|计费维度/)
  assert.throws(() => jsonUpdate(protocol, { ...result, candidates: [{ index: 0, content: { parts: [{ text: 'text' }] } }] }), /结束标记/)
  assert.throws(() => jsonUpdate(protocol, { ...result, candidates: [{ index: 0, finishReason: 'SAFETY' }] }), /终态/)
  assert.throws(() => jsonUpdate(protocol, { ...result, candidates: [result.candidates[0], result.candidates[0]] }), /候选/)
  assert.throws(() => jsonUpdate(protocol, { ...result, candidates: [{ ...result.candidates[0], content: { parts: [{ functionCall: { name: 'unsupported' } }] } }] }), /不支持的内容/)
})

test('Gemini SSE only confirms a post-billing finish with final trusted native usage', () => {
  const content = frameUpdate(protocol, { type: '', data: JSON.stringify({ candidates: [{ index: 0, content: { parts: [{ text: 'hello' }] } }], usageMetadata: { promptTokenCount: 10 } }) })
  assert.equal(content.delta, 'hello')
  assert.equal(content.terminal, undefined)
  assert.deepEqual(content.usage, { promptTokenCount: 10 })
  const final = frameUpdate(protocol, { type: '', data: JSON.stringify({ candidates: [{ index: 0, finishReason: 'STOP' }], usageMetadata: usage }) })
  assert.equal(final.delta, undefined)
  assert.equal(final.terminal, true)
  assert.deepEqual(final.usage, usage)
  assert.throws(() => frameUpdate(protocol, { type: '', data: '[DONE]' }), /结束标记/)
  assert.throws(() => frameUpdate(protocol, { type: '', data: JSON.stringify({ candidates: [{ finishReason: 'STOP' }] }) }), /可信最终用量/)
  assert.deepEqual(frameUpdate(protocol, { type: 'error', data: JSON.stringify({ error: { code: 'billing_pending', message: 'private-detail' } }) }), { error: 'billing_pending' })
})

test('Gemini JSON and split UTF8 SSE use one bearer POST with no query credential or native model field', async () => {
  const previousFetch = globalThis.fetch
  try {
    for (const stream of [false, true]) {
      const calls = []; const updates = []
      globalThis.fetch = async (url, options) => {
        calls.push({ url, options })
        return stream
          ? streamResponse(`data: {"candidates":[{"index":0,"content":{"parts":[{"text":"你好 world"}]}}]}\r\n\r\ndata: ${JSON.stringify({ candidates: [{ index: 0, finishReason: 'STOP' }], usageMetadata: usage })}\r\n\r\n`)
          : new Response(JSON.stringify(result), { headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'gemini-request' } })
      }
      const draft = geminiRequest('public-gemini', 'hello', 20, stream, 'Be concise.')
      const response = await runInference(protocol, 'page-key', draft, new AbortController().signal, update => updates.push(update))
      assert.equal(calls.length, 1)
      assert.equal(calls[0].url, `/v1beta/models/public-gemini:${stream ? 'streamGenerateContent?alt=sse' : 'generateContent'}`)
      assert.equal(calls[0].url.includes('key='), false)
      assert.equal(calls[0].options.headers.Authorization, 'Bearer page-key')
      assert.equal(calls[0].options.headers['x-goog-api-key'], undefined)
      assert.equal(calls[0].options.headers.Accept, stream ? 'text/event-stream' : 'application/json')
      assert.equal(calls[0].options.credentials, 'omit')
      assert.equal(calls[0].options.redirect, 'error')
      assert.deepEqual(JSON.parse(calls[0].options.body), inferenceHTTP(protocol, draft).body)
      assert.equal(response.requestId, 'gemini-request')
      assert.equal(updates.filter(update => update.terminal).length, 1)
      assert.equal(updates.map(update => update.delta || '').join(''), '你好 world')
      assert.deepEqual(updates.at(-1).usage, usage)
    }
  } finally { globalThis.fetch = previousFetch }
})

test('Gemini stream EOF, uncertain usage, billing_pending and error after finish reject the call without replay', async () => {
  const previousFetch = globalThis.fetch
  try {
    for (const scenario of ['eof', 'usage', 'billing', 'after_finish', 'http']) {
      let calls = 0; let finished = false; let id = ''
      globalThis.fetch = async () => {
        calls++
        if (scenario === 'http') return new Response(JSON.stringify({ error: { code: 'model_forbidden', message: 'private-detail' } }), { status: 403, headers: { 'X-Request-ID': 'failed-gemini' } })
        let raw = 'data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}\n\n'
        if (scenario === 'usage') raw += 'data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1}}\n\n'
        if (scenario === 'billing') raw += 'event: error\ndata: {"error":{"code":"billing_pending","message":"private-detail"}}\n\n'
        if (scenario === 'after_finish') raw += `data: ${JSON.stringify({ candidates: [{ finishReason: 'STOP' }], usageMetadata: usage })}\n\ndata: {"error":{"code":"stream_failed","message":"private-detail"}}\n\n`
        return streamResponse(raw, 'failed-gemini')
      }
      await assert.rejects(runInference(protocol, 'key', geminiRequest('model', 'hello', 20, true), new AbortController().signal, update => { finished ||= !!update.terminal }, value => { id = value }), error => !error.message.includes('private-detail'))
      assert.equal(calls, 1)
      assert.equal(id, 'failed-gemini')
      if (scenario !== 'after_finish') assert.equal(finished, false)
    }
  } finally { globalThis.fetch = previousFetch }
})

test('cancelling Gemini closes the reader, preserves request ID and does not replay', async () => {
  const previousFetch = globalThis.fetch; const abort = new AbortController()
  let calls = 0; let cancelled = false; let finished = false; let requestId = ''
  globalThis.fetch = async (_url, options) => {
    calls++
    return new Response(new ReadableStream({
      start(controller) {
        controller.enqueue(bytes('data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}\n\n'))
        options.signal.addEventListener('abort', () => { cancelled = true; controller.error(new DOMException('Aborted', 'AbortError')) }, { once: true })
      },
    }), { headers: { 'Content-Type': 'text/event-stream', 'X-Request-ID': 'cancelled-gemini' } })
  }
  try {
    await assert.rejects(runInference(protocol, 'key', geminiRequest('model', 'hello', 20, true), abort.signal, update => { finished ||= !!update.terminal; if (update.delta) abort.abort() }, id => { requestId = id }), error => error.name === 'AbortError')
    assert.equal(calls, 1)
    assert.equal(cancelled, true)
    assert.equal(finished, false)
    assert.equal(requestId, 'cancelled-gemini')
  } finally { globalThis.fetch = previousFetch }
})

test('switching from another model to Gemini clears prior results without clearing request inputs', () => {
  const state = { output: 'old answer', usage: { total_tokens: 10 }, requestId: 'old-request', metrics: { requestId: 'old-request', durationMs: 1, bytes: 10 }, status: '调用完成', error: '', key: 'page-key', prompt: 'hello', systemInstruction: 'Be concise.' }
  resetResponseForModelChange(state, 'other-model', 'gemini-model')
  assert.equal(state.output, '')
  assert.equal(state.requestId, '')
  assert.deepEqual(state.usage, {})
  assert.equal(state.metrics, null)
  assert.equal(state.key, 'page-key')
  assert.equal(state.prompt, 'hello')
  assert.equal(state.systemInstruction, 'Be concise.')
})
