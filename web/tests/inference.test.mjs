import assert from 'node:assert/strict'
import test from 'node:test'
import { frameUpdate, protocolInfo, runInference, SSEDecoder, textRequest } from '../src/inference.ts'

const bytes = value => new TextEncoder().encode(value)
function sseResponse(frames, id = 'request-test') {
  // Split inside UTF-8 codepoints and CRLF, as a real TCP connection can.
  const all = bytes(frames)
  return new Response(new ReadableStream({ start(controller) { for (let i = 0; i < all.length; i += 3) controller.enqueue(all.slice(i, i + 3)); controller.close() } }), { headers: { 'Content-Type': 'text/event-stream', 'X-Request-ID': id } })
}

test('native text requests choose exact endpoints, output fields and stateless Responses', () => {
  assert.equal(protocolInfo(undefined).endpoint, '/v1/chat/completions')
  assert.equal(protocolInfo('unknown_protocol'), undefined)
  const responses = textRequest('openai_responses', 'public-model', '你好', 123, true)
  assert.equal(responses.store, false)
  assert.equal(responses.input, '你好')
  assert.equal(responses.max_output_tokens, 123)
  assert.equal(responses.messages, undefined)
  const messages = textRequest('anthropic_messages', 'public-model', '你好', 123, true)
  assert.equal(messages.max_tokens, 123)
  assert.deepEqual(messages.messages, [{ role: 'user', content: '你好' }])
  assert.equal(messages.stream_options, undefined)
  assert.deepEqual(textRequest('openai_chat', 'public-model', '你好', 123, true).stream_options, { include_usage: true })
  assert.throws(() => textRequest('openai_chat', 'public-model', '你好', 1.5, false))
})

test('SSE parser handles CR/LF, comments, multiline data, split UTF8 and bounded/incomplete events', () => {
  const raw = bytes(': heartbeat\r\nevent: response.output_text.delta\rdata: {"type":"response.output_text.delta",\r\ndata: "delta":"你好"}\r\n\r\n')
  const parser = new SSEDecoder(); const frames = []
  for (const byte of raw) frames.push(...parser.push(Uint8Array.of(byte)))
  frames.push(...parser.push(new Uint8Array(), true))
  assert.equal(frames.length, 1)
  assert.equal(frameUpdate('openai_responses', frames[0]).delta, '你好')
  assert.throws(() => { const partial = new SSEDecoder(); partial.push(bytes('data: {"type":"message_stop"}\n')); partial.push(new Uint8Array(), true) }, /完整事件/)
  assert.throws(() => new SSEDecoder().push(bytes('data: ' + 'x'.repeat((1 << 20) + 1))), /展示上限/)
  assert.throws(() => new SSEDecoder().push(Uint8Array.from([0xff])), /encoded data/)
})

test('Chat SSE uses real bearer key without session cookies, completes once and never retries', async () => {
  const oldFetch = globalThis.fetch; const calls = []; const updates = []
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    return sseResponse('data: {"choices":[{"delta":{"role":"assistant"}}]}\r\n\r\ndata: {"choices":[{"delta":{"content":"你好"}}]}\r\n\r\ndata: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}\r\n\r\ndata: [DONE]\r\n\r\n')
  }
  try {
    const result = await runInference('openai_chat', 'test-secret', textRequest('openai_chat', 'public-model', 'test', 8, true), new AbortController().signal, update => updates.push(update))
    assert.equal(calls.length, 1)
    assert.equal(calls[0].url, '/v1/chat/completions')
    assert.equal(calls[0].options.credentials, 'omit')
    assert.equal(calls[0].options.redirect, 'error')
    assert.equal(calls[0].options.headers.Authorization, 'Bearer test-secret')
    assert.equal(calls[0].options.headers['X-CSRF-Token'], undefined)
    assert.equal(result.requestId, 'request-test')
    assert.ok(result.firstContentMs >= result.firstByteMs)
    assert.ok(result.durationMs >= result.firstContentMs)
    assert.equal(updates.filter(update => update.terminal).length, 1)
    assert.equal(updates.map(update => update.delta || '').join(''), '你好')
    assert.deepEqual(updates.find(update => update.usage).usage, { prompt_tokens: 5, completion_tokens: 2 })
  } finally { globalThis.fetch = oldFetch }
})

test('Responses and Messages keep native usage, named events and post-billing terminals', async () => {
  const oldFetch = globalThis.fetch
  try {
    for (const protocol of ['openai_responses', 'anthropic_messages']) {
      const raw = protocol === 'openai_responses'
        ? 'event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"hello"}\n\nevent: response.completed\ndata: {"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":2}}}\n\n'
        : 'event: message_start\ndata: {"type":"message_start","message":{"usage":{"input_tokens":7,"cache_read_input_tokens":3}}}\n\nevent: content_block_delta\ndata: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}\n\nevent: message_delta\ndata: {"type":"message_delta","usage":{"output_tokens":2}}\n\nevent: message_stop\ndata: {"type":"message_stop"}\n\n'
      let endpoint; let headers; const updates = []
      globalThis.fetch = async (url, options) => { endpoint = url; headers = options.headers; return sseResponse(raw) }
      await runInference(protocol, 'test-key', textRequest(protocol, 'model', 'hello', 8, true), new AbortController().signal, update => updates.push(update))
      assert.equal(endpoint, protocolInfo(protocol).endpoint)
      if (protocol === 'anthropic_messages') {
        assert.equal(headers['x-api-key'], 'test-key')
        assert.equal(headers['anthropic-version'], '2023-06-01')
        assert.equal(headers.Authorization, undefined)
      } else {
        assert.equal(headers.Authorization, 'Bearer test-key')
        assert.equal(headers['x-api-key'], undefined)
      }
      assert.equal(updates.map(update => update.delta || '').join(''), 'hello')
      assert.equal(updates.at(-1).terminal, true)
      const usage = Object.assign({}, ...updates.filter(update => update.usage).map(update => update.usage))
      assert.equal(usage.input_tokens, 7)
      assert.equal(usage.output_tokens, 2)
      if (protocol === 'anthropic_messages') assert.equal(usage.cache_read_input_tokens, 3)
    }
  } finally { globalThis.fetch = oldFetch }
})

test('missing terminal, billing_pending, upstream HTML and network errors never imply success or retry', async () => {
  const oldFetch = globalThis.fetch
  try {
    for (const scenario of ['eof', 'billing', 'html', 'network']) {
      let calls = 0; let completed = false; let requestId = ''
      globalThis.fetch = async () => {
        calls++
        if (scenario === 'network') throw new TypeError('network unavailable')
        if (scenario === 'html') return new Response('<html>private token</html>', { status: 502, headers: { 'X-Request-ID': 'failed-request' } })
        return sseResponse(`data: {"choices":[{"delta":{"content":"partial"}}]}\n\n${scenario === 'billing' ? 'data: {"error":{"code":"billing_pending","message":"private token"}}\n\n' : ''}`, 'failed-request')
      }
      await assert.rejects(runInference('openai_chat', 'test-key', textRequest('openai_chat', 'model', 'hello', 8, true), new AbortController().signal, update => { completed ||= !!update.terminal }, id => { requestId = id }), error => !error.message.includes('private token'))
      assert.equal(calls, 1)
      assert.equal(completed, false)
      if (scenario !== 'network') assert.equal(requestId, 'failed-request')
    }
  } finally { globalThis.fetch = oldFetch }
})

test('JSON text extraction and HTTP rejection use sanitized messages and preserve request ID', async () => {
  const oldFetch = globalThis.fetch
  try {
    for (const protocol of ['openai_chat', 'openai_responses', 'anthropic_messages']) {
      const body = protocol === 'openai_chat' ? { choices: [{ message: { content: 'hello' } }], usage: { prompt_tokens: 3, completion_tokens: 1 } }
        : protocol === 'openai_responses' ? { output: [{ type: 'message', content: [{ type: 'output_text', text: 'hello' }] }], usage: { input_tokens: 3, output_tokens: 1 } }
          : { content: [{ type: 'text', text: 'hello' }], usage: { input_tokens: 3, output_tokens: 1 } }
      globalThis.fetch = async () => new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } })
      const updates = []
      await runInference(protocol, 'key', textRequest(protocol, 'model', 'hello', 8, false), new AbortController().signal, update => updates.push(update))
      assert.equal(updates[0].delta, 'hello')
      assert.equal(updates[0].terminal, true)
    }
    globalThis.fetch = async () => new Response(JSON.stringify({ error: { code: 'insufficient_balance', message: 'private details' } }), { status: 402, headers: { 'X-Request-ID': 'funding-request' } })
    let id
    await assert.rejects(runInference('openai_chat', 'key', textRequest('openai_chat', 'model', 'hello', 8, false), new AbortController().signal, () => {}, value => { id = value }), /HTTP 402 · insufficient_balance/)
    assert.equal(id, 'funding-request')
  } finally { globalThis.fetch = oldFetch }
})

test('cancellation reaches the body reader, keeps request identity and performs no replay', async () => {
  const oldFetch = globalThis.fetch; const abort = new AbortController()
  let calls = 0; let closed = false; let id = ''; let terminal = false
  globalThis.fetch = async (_url, options) => {
    calls++
    return new Response(new ReadableStream({
      start(controller) {
        controller.enqueue(bytes('data: {"choices":[{"delta":{"content":"partial"}}]}\n\n'))
        options.signal.addEventListener('abort', () => { closed = true; controller.error(new DOMException('Aborted', 'AbortError')) }, { once: true })
      },
    }), { headers: { 'Content-Type': 'text/event-stream', 'X-Request-ID': 'cancelled-request' } })
  }
  try {
    await assert.rejects(runInference('openai_chat', 'key', textRequest('openai_chat', 'model', 'hello', 8, true), abort.signal, update => {
      terminal ||= !!update.terminal
      if (update.delta) abort.abort()
    }, value => { id = value }), error => error.name === 'AbortError')
    assert.equal(calls, 1)
    assert.equal(closed, true)
    assert.equal(terminal, false)
    assert.equal(id, 'cancelled-request')
  } finally { globalThis.fetch = oldFetch }
})
