import assert from 'node:assert/strict'
import test from 'node:test'
import { embeddingsRequest, frameUpdate, inputLines, jsonUpdate, protocolInfo, rerankRequest, runInference, textRequest } from '../src/inference.ts'
import { modelGenerationSettings } from '../src/model-contract.ts'

const generationFields = ['stream', 'stream_options', 'max_tokens', 'max_output_tokens', 'max_completion_tokens', 'messages', 'tools', 'tool_choice', 'parallel_tool_calls']
const embeddingResult = { object: 'list', model: 'public-vector', data: [{ object: 'embedding', index: 0, embedding: [0.1, -0.2, 0.3] }], usage: { prompt_tokens: 10, total_tokens: 10 } }
const rerankResult = { model: 'public-rerank', results: [{ index: 1, relevance_score: 0.9, document: { text: 'matching document', private_metadata: 'secret' } }, { index: 0, relevance_score: 0.1 }], usage: { total_tokens: 12 } }

test('retrieval request builders use exact native JSON contracts without generation settings', () => {
  for (const [protocol, endpoint] of [['openai_embeddings', '/v1/embeddings'], ['jina_rerank', '/v1/rerank']]) {
    assert.equal(protocolInfo(protocol).endpoint, endpoint)
    assert.equal(protocolInfo(protocol).outputField, '')
    assert.equal(protocolInfo(protocol).inputOnly, true)
    assert.throws(() => textRequest(protocol, 'model', 'text', 10, false), /生成模型/)
    assert.throws(() => frameUpdate(protocol, { type: '', data: '[DONE]' }), /JSON/)
  }
  const single = embeddingsRequest('public-vector', 'a text\nwith a newline')
  assert.deepEqual(single, { model: 'public-vector', input: 'a text\nwith a newline', encoding_format: 'float' })
  const batch = embeddingsRequest('public-vector', inputLines(' first \r\n\r\nsecond\n'), { encodingFormat: 'base64', dimensions: 256 })
  assert.deepEqual(batch, { model: 'public-vector', input: ['first', 'second'], encoding_format: 'base64', dimensions: 256 })
  const rank = rerankRequest('public-rerank', 'find this', ['first', 'second'])
  assert.deepEqual(rank, { model: 'public-rerank', query: 'find this', documents: ['first', 'second'], return_documents: false })
  assert.deepEqual(rerankRequest('public-rerank', 'query', ['a', 'b'], 1, true), { model: 'public-rerank', query: 'query', documents: ['a', 'b'], top_n: 1, return_documents: true })
  for (const body of [single, batch, rank]) for (const field of generationFields) assert.equal(field in body, false, field)
})

test('retrieval form boundaries reject empty, excessive, and fractional inputs while optional fields remain absent', () => {
  for (const input of ['', ' \n ', [], ['valid', ''], Array(129).fill('text')]) assert.throws(() => embeddingsRequest('model', input))
  for (const dimensions of [0, 8193, 1.5, NaN]) assert.throws(() => embeddingsRequest('model', 'text', { dimensions }))
  assert.equal('dimensions' in embeddingsRequest('model', 'text', { dimensions: null }), false)
  assert.throws(() => embeddingsRequest('model', 'text', { encodingFormat: 'other' }))
  assert.throws(() => rerankRequest('model', ' ', ['a']))
  for (const documents of [[], [''], Array(129).fill('text')]) assert.throws(() => rerankRequest('model', 'query', documents))
  for (const top of [0, 3, 1.5, NaN]) assert.throws(() => rerankRequest('model', 'query', ['a', 'b'], top))
  assert.equal('top_n' in rerankRequest('model', 'query', ['a'], null), false)
})

test('input-only model publication overrides stale generated settings, while generation still requires positive output', () => {
  for (const protocol of ['openai_embeddings', 'jina_rerank']) {
    assert.deepEqual(modelGenerationSettings(protocol, 2048, 512, true), { output_limit: 0, default_output_limit: 0, supports_tools: false })
    assert.deepEqual(modelGenerationSettings(protocol, undefined, null, true), { output_limit: 0, default_output_limit: 0, supports_tools: false })
  }
  for (const protocol of ['openai_chat', 'openai_responses', 'anthropic_messages']) {
    assert.deepEqual(modelGenerationSettings(protocol, 2048, 512, true), { output_limit: 2048, default_output_limit: 512, supports_tools: true })
    for (const [output, initial] of [[0, 0], [1, 2], [10, null], [1.5, 1]]) assert.throws(() => modelGenerationSettings(protocol, output, initial, false))
  }
  assert.throws(() => modelGenerationSettings('unsupported', 1, 1, false))
})

test('retrieval JSON exposes bounded vector/ranking previews and trusted usage without provider metadata', () => {
  const vector = jsonUpdate('openai_embeddings', { ...embeddingResult, private_debug: 'secret' })
  assert.equal(vector.terminal, true)
  assert.deepEqual(vector.usage, embeddingResult.usage)
  assert.match(vector.delta, /向量数量：1/)
  assert.match(vector.delta, /"dimensions": 3/)
  assert.match(vector.delta, /-0.2/)
  assert.equal(vector.delta.includes('secret'), false)
  const base64 = jsonUpdate('openai_embeddings', { ...embeddingResult, data: [{ object: 'embedding', index: 0, embedding: 'AACAPwAAAEA=' }] })
  assert.match(base64.delta, /base64_preview/)
  const long = jsonUpdate('openai_embeddings', { ...embeddingResult, data: Array.from({ length: 17 }, (_, index) => ({ object: 'embedding', index, embedding: Array(20).fill(0.1) })) })
  assert.match(long.delta, /向量数量：17/)
  assert.equal((long.delta.match(/"index"/g) || []).length, 16)
  assert.equal((long.delta.match(/0\.1/g) || []).length, 16 * 8)
  const ranked = jsonUpdate('jina_rerank', rerankResult)
  assert.equal(ranked.terminal, true)
  assert.deepEqual(ranked.usage, { total_tokens: 12 })
  assert.match(ranked.delta, /"document_index": 1/)
  assert.match(ranked.delta, /matching document/)
  assert.equal(ranked.delta.includes('secret'), false)
})

test('retrieval results without trustworthy input usage or usable results cannot mark success', () => {
  for (const usage of [undefined, {}, { total_tokens: 1 }, { prompt_tokens: 1, total_tokens: 2 }, { prompt_tokens: 1, total_tokens: 1, completion_tokens: 2 }, { prompt_tokens: 1, total_tokens: 1, search_units: 1 }]) assert.throws(() => jsonUpdate('openai_embeddings', { ...embeddingResult, usage }), /可信/)
  for (const usage of [{ total_tokens: -1 }, { total_tokens: 1.5 }, { total_tokens: 1, prompt_tokens: 2 }, { total_tokens: 1, search_units: 1 }]) assert.throws(() => jsonUpdate('jina_rerank', { ...rerankResult, usage }), /可信/)
  assert.throws(() => jsonUpdate('openai_embeddings', { ...embeddingResult, data: [] }), /向量/)
  assert.throws(() => jsonUpdate('jina_rerank', { ...rerankResult, results: [{ index: 0, relevance_score: 1.1 }] }), /排名/)
  assert.deepEqual(jsonUpdate('jina_rerank', { error: { code: 'billing_pending', message: 'secret' } }), { error: 'billing_pending' })
})

test('retrieval HTTP calls use bearer JSON, preserve request identity and never replay errors', async () => {
  const originalFetch = globalThis.fetch
  try {
    for (const protocol of ['openai_embeddings', 'jina_rerank']) {
      const calls = []; const updates = []
      const body = protocol === 'openai_embeddings' ? embeddingsRequest('model', 'hello') : rerankRequest('model', 'query', ['a', 'b'])
      globalThis.fetch = async (url, options) => {
        calls.push({ url, options })
        return new Response(JSON.stringify(protocol === 'openai_embeddings' ? embeddingResult : rerankResult), { headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'retrieval-request' } })
      }
      const result = await runInference(protocol, 'test-key', body, new AbortController().signal, update => updates.push(update))
      assert.equal(result.requestId, 'retrieval-request')
      assert.equal(calls.length, 1)
      assert.equal(calls[0].url, protocolInfo(protocol).endpoint)
      assert.equal(calls[0].options.headers.Authorization, 'Bearer test-key')
      assert.equal(calls[0].options.headers.Accept, 'application/json')
      assert.equal(calls[0].options.credentials, 'omit')
      assert.deepEqual(JSON.parse(calls[0].options.body), body)
      assert.equal(updates.at(-1).terminal, true)
      assert.ok(result.firstContentMs >= result.firstByteMs)
      for (const field of generationFields) {
        await assert.rejects(runInference(protocol, 'key', { ...body, [field]: false }, new AbortController().signal, () => {}), /检索请求/)
      }
      assert.equal(calls.length, 1)
      let attempts = 0; let completed = false
      globalThis.fetch = async () => { attempts++; return new Response(JSON.stringify({ error: { code: 'billing_pending', message: 'secret' } }), { status: 503 }) }
      await assert.rejects(runInference(protocol, 'key', body, new AbortController().signal, update => { completed ||= !!update.terminal }), error => /billing_pending/.test(error.message) && !error.message.includes('secret'))
      assert.equal(attempts, 1)
      assert.equal(completed, false)
    }
  } finally { globalThis.fetch = originalFetch }
})

test('cancelling retrieval response parsing preserves request identity and never replays the POST', async () => {
  const originalFetch = globalThis.fetch
  const abort = new AbortController(); const firstRead = Promise.withResolvers(); const headersReady = Promise.withResolvers()
  let calls = 0; let requestId = ''; let completed = false
  globalThis.fetch = async (_url, options) => {
    calls++
    return new Response(new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('{"object":"list","data":['))
        options.signal.addEventListener('abort', () => controller.error(new DOMException('Aborted', 'AbortError')), { once: true })
      },
      pull() { firstRead.resolve() },
    }), { headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'cancelled-retrieval' } })
  }
  try {
    const pending = runInference('openai_embeddings', 'key', embeddingsRequest('model', 'text'), abort.signal, update => { completed ||= !!update.terminal }, id => { requestId = id; headersReady.resolve() })
    await headersReady.promise
    await firstRead.promise
    abort.abort()
    await assert.rejects(pending, error => error.name === 'AbortError')
    assert.equal(calls, 1)
    assert.equal(completed, false)
    assert.equal(requestId, 'cancelled-retrieval')
  } finally { globalThis.fetch = originalFetch }
})
