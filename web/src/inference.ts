export type Protocol = 'openai_chat' | 'openai_responses' | 'anthropic_messages' | 'openai_embeddings' | 'jina_rerank' | 'gemini_generate_content'
export interface ProtocolInfo { id: Protocol; title: string; endpoint: string; outputField: string; scope: string; inputOnly?: boolean }
export const protocols: ProtocolInfo[] = [
  { id: 'openai_chat', title: 'OpenAI Chat', endpoint: '/v1/chat/completions', outputField: 'max_tokens', scope: '文本消息、基础函数工具与 SSE' },
  { id: 'openai_responses', title: 'OpenAI Responses', endpoint: '/v1/responses', outputField: 'max_output_tokens', scope: '无状态文本、基础函数工具与 SSE' },
  { id: 'anthropic_messages', title: 'Anthropic Messages', endpoint: '/v1/messages', outputField: 'max_tokens', scope: '文本消息、基础函数工具与 SSE' },
  { id: 'openai_embeddings', title: 'OpenAI Embeddings', endpoint: '/v1/embeddings', outputField: '', scope: '文本或文本批次转向量，仅输入 Tokens 计费', inputOnly: true },
  { id: 'jina_rerank', title: 'Jina Rerank', endpoint: '/v1/rerank', outputField: '', scope: '按 query 为文本列表排序，仅输入 Tokens 计费', inputOnly: true },
  { id: 'gemini_generate_content', title: 'Gemini GenerateContent', endpoint: '/v1beta/models/{model}:generateContent', outputField: 'maxOutputTokens', scope: '无状态纯文本与 SSE；暂不支持函数工具、媒体或缓存引用' },
]
export function protocolInfo(value: unknown): ProtocolInfo | undefined {
  return protocols.find(p => p.id === (value || 'openai_chat'))
}
export function textRequest(protocol: Protocol, model: string, prompt: string, output: number, stream: boolean): Record<string, unknown> {
  if (protocol === 'gemini_generate_content') return geminiRequest(model, prompt, output, stream)
  const info = protocolInfo(protocol)
  if (!info || info.inputOnly || !model.trim() || !prompt.trim() || !Number.isSafeInteger(output) || output < 1) throw new Error('请填写有效的生成模型、消息和输出上限。')
  const body: Record<string, unknown> = { model, stream, [info.outputField]: output }
  if (protocol === 'openai_responses') { body.input = prompt; body.store = false }
  else body.messages = [{ role: 'user', content: prompt }]
  if (protocol === 'openai_chat' && stream) body.stream_options = { include_usage: true }
  return body
}

function geminiModelName(value: unknown): value is string { return typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(value) && !value.includes('..') }
export function geminiRequest(model: string, prompt: string, output: number, stream: boolean, systemInstruction = ''): Record<string, unknown> {
  if (!geminiModelName(model) || !prompt.trim() || !Number.isSafeInteger(output) || output < 1 || typeof stream !== 'boolean') throw new Error('请填写有效的 Gemini 模型、纯文本和正整数输出上限。')
  // model/stream are local routing fields; inferenceHTTP removes both from the
  // native JSON sent by fetch and shown in the reproducible curl example.
  const draft: Record<string, unknown> = { model, stream, contents: [{ role: 'user', parts: [{ text: prompt }] }], generationConfig: { maxOutputTokens: output, candidateCount: 1 } }
  if (systemInstruction.trim()) draft.systemInstruction = { parts: [{ text: systemInstruction }] }
  return draft
}

export interface InferenceHTTP { endpoint: string; body: Record<string, unknown>; stream: boolean }
export function inferenceHTTP(protocol: Protocol, draft: Record<string, unknown>): InferenceHTTP {
  const info = protocolInfo(protocol)
  if (!info) throw new Error('请选择受支持的调用协议。')
  if (protocol !== 'gemini_generate_content') return { endpoint: info.endpoint, body: draft, stream: !!draft.stream }
  if (!geminiModelName(draft.model) || typeof draft.stream !== 'boolean' || Object.keys(draft).some(field => !['model', 'stream', 'contents', 'generationConfig', 'systemInstruction'].includes(field))) throw new Error('Gemini 请求须使用有效模型和纯文本原生字段。')
  const endpoint = `/v1beta/models/${encodeURIComponent(draft.model)}:${draft.stream ? 'streamGenerateContent?alt=sse' : 'generateContent'}`
  const { model: _model, stream: _stream, ...body } = draft
  return { endpoint, body, stream: draft.stream }
}

export function inputLines(value: string): string[] { return value.split(/\r?\n/).map(line => line.trim()).filter(Boolean) }
function validTexts(values: string[]): boolean { return values.length > 0 && values.length <= 128 && values.every(value => typeof value === 'string' && !!value.trim()) }
export function embeddingsRequest(model: string, input: string | string[], options: { encodingFormat?: 'float' | 'base64'; dimensions?: number | null } = {}): Record<string, unknown> {
  if (!model.trim() || (Array.isArray(input) ? !validTexts(input) : !input.trim())) throw new Error('请填写模型和非空文本，文本批次最多 128 条。')
  const format = options.encodingFormat ?? 'float'
  if (format !== 'float' && format !== 'base64') throw new Error('向量编码格式须为 float 或 base64。')
  const body: Record<string, unknown> = { model, input, encoding_format: format }
  if (options.dimensions !== undefined && options.dimensions !== null) {
    if (!Number.isSafeInteger(options.dimensions) || options.dimensions < 1 || options.dimensions > 8192) throw new Error('向量维度须为 1 至 8192 的整数。')
    body.dimensions = options.dimensions
  }
  return body
}
export function rerankRequest(model: string, query: string, documents: string[], topN?: number | null, returnDocuments = false): Record<string, unknown> {
  if (!model.trim() || !query.trim() || !validTexts(documents)) throw new Error('请填写模型、查询和 1 至 128 条非空文档。')
  const body: Record<string, unknown> = { model, query, documents, return_documents: returnDocuments }
  if (topN !== undefined && topN !== null) {
    if (!Number.isSafeInteger(topN) || topN < 1 || topN > documents.length) throw new Error('返回排名数须为 1 至文档数量的整数，留空返回全部。')
    body.top_n = topN
  }
  return body
}

export interface SSEFrame { type: string; data: string }
// Incremental UTF-8 and CR/LF framing; EOF alone never completes a generation.
export class SSEDecoder {
  private decoder = new TextDecoder('utf-8', { fatal: true })
  private buffer = ''
  private type = ''
  private data: string[] = []
  private eventBytes = 0
  push(chunk: Uint8Array, final = false): SSEFrame[] {
    this.buffer += this.decoder.decode(chunk, { stream: !final })
    const frames: SSEFrame[] = []
    for (;;) {
      const boundary = this.buffer.search(/[\r\n]/)
      if (boundary < 0 || (!final && this.buffer[boundary] === '\r' && boundary === this.buffer.length - 1)) break
      const line = this.buffer.slice(0, boundary)
      const separator = this.buffer[boundary] === '\r' && this.buffer[boundary + 1] === '\n' ? 2 : 1
      this.buffer = this.buffer.slice(boundary + separator)
      this.eventBytes += new TextEncoder().encode(line).byteLength + separator
      if (this.eventBytes > 1 << 20) throw new Error('响应事件超过展示上限，请凭请求 ID 查询结果。')
      if (!line) {
        if (this.data.length) frames.push({ type: this.type, data: this.data.join('\n') })
        this.type = ''; this.data = []; this.eventBytes = 0
        continue
      }
      if (line.startsWith(':')) continue
      const colon = line.indexOf(':')
      const field = colon < 0 ? line : line.slice(0, colon)
      const value = colon < 0 ? '' : line.slice(colon + 1).replace(/^ /, '')
      if (field === 'event') this.type = value
      if (field === 'data') this.data.push(value)
    }
    if (new TextEncoder().encode(this.buffer).byteLength + this.eventBytes > 1 << 20) throw new Error('响应事件超过展示上限，请凭请求 ID 查询结果。')
    if (final && (this.buffer || this.data.length || this.type)) throw new Error('响应流在完整事件结束前断开，请查询账单状态。')
    return frames
  }
}

type Value = Record<string, unknown>
function record(value: unknown): Value { return value && typeof value === 'object' && !Array.isArray(value) ? value as Value : {} }
function list(value: unknown): unknown[] { return Array.isArray(value) ? value : [] }
function text(value: unknown): string { return typeof value === 'string' ? value : '' }
function safeCode(value: unknown): string { const code = text(value); return /^[a-zA-Z0-9_]{1,64}$/.test(code) ? code : 'inference_failed' }
export interface InferenceUpdate { delta?: string; usage?: Value; terminal?: boolean; error?: string }
export function frameUpdate(protocol: Protocol, frame: SSEFrame): InferenceUpdate {
  if (protocolInfo(protocol)?.inputOnly) throw new Error('检索协议只支持 JSON 响应，不支持 SSE。')
  if (frame.data.trim() === '[DONE]') {
    if (protocol !== 'openai_chat') throw new Error('收到不符合所选协议的结束标记。')
    return { terminal: true }
  }
  let value: Value
  try { value = record(JSON.parse(frame.data)) } catch { throw new Error('响应事件不是有效 JSON，请查询账单状态。') }
  const kind = frame.type || text(value.type)
  if (value.error || kind === 'error' || kind === 'response.failed') return { error: safeCode(record(value.error || record(value.response).error).code) }
  if (protocol === 'gemini_generate_content') return geminiUpdate(value, false)
  if (protocol === 'openai_chat') {
    const delta = record(record(list(value.choices)[0]).delta)
    return { delta: text(delta.content) || text(delta.refusal), usage: value.usage ? record(value.usage) : undefined }
  }
  if (protocol === 'openai_responses') {
    if (kind === 'response.completed' || kind === 'response.incomplete') return { terminal: true, usage: record(record(value.response).usage) }
    if (kind === 'response.output_text.delta' || kind === 'response.refusal.delta') return { delta: text(value.delta) }
    if (kind === 'response.function_call_arguments.delta') return { delta: text(value.delta) }
    if (kind === 'response.output_item.added' && record(value.item).type === 'function_call') return { delta: `\n[函数 ${text(record(value.item).name)}]\n` }
    return {}
  }
  if (kind === 'message_stop') return { terminal: true }
  if (kind === 'message_start') return { usage: record(record(value.message).usage) }
  if (kind === 'message_delta') return { usage: record(value.usage) }
  if (kind === 'content_block_start' && record(value.content_block).type === 'tool_use') return { delta: `\n[函数 ${text(record(value.content_block).name)}]\n` }
  if (kind === 'content_block_start') return { delta: text(record(value.content_block).text) }
  if (kind === 'content_block_delta') return { delta: text(record(value.delta).text) || text(record(value.delta).partial_json) }
  return {}
}

export function jsonUpdate(protocol: Protocol, value: unknown): InferenceUpdate {
  const body = record(value)
  if (body.error) return { error: safeCode(record(body.error).code) }
  if (protocol === 'gemini_generate_content') return geminiUpdate(body, true)
  if (protocolInfo(protocol)?.inputOnly) return retrievalUpdate(protocol, body)
  let delta = ''
  if (protocol === 'openai_chat') {
    const message = record(record(list(body.choices)[0]).message)
    delta = text(message.content) || text(message.refusal)
    for (const call of list(message.tool_calls)) delta += `\n[函数 ${text(record(record(call).function).name)}] ${text(record(record(call).function).arguments)}`
  } else if (protocol === 'openai_responses') {
    for (const item of list(body.output)) {
      const output = record(item)
      for (const part of list(output.content)) delta += text(record(part).text) || text(record(part).refusal)
      if (output.type === 'function_call') delta += `\n[函数 ${text(output.name)}] ${text(output.arguments)}`
    }
  } else {
    for (const part of list(body.content)) {
      const content = record(part)
      delta += text(content.text)
      if (content.type === 'tool_use') delta += `\n[函数 ${text(content.name)}] ${JSON.stringify(content.input || {})}`
    }
  }
  return { delta, usage: record(body.usage), terminal: true }
}

function geminiUpdate(body: Value, requireTerminal: boolean): InferenceUpdate {
  const candidates = list(body.candidates)
  if (candidates.length > 1) throw new Error('Gemini 返回了超出当前合同的候选结果，请查询账单状态。')
  const candidate = record(candidates[0])
  if (candidate.index !== undefined && candidate.index !== 0) throw new Error('Gemini 候选索引无效，请查询账单状态。')
  const finish = text(candidate.finishReason)
  if (finish && finish !== 'STOP' && finish !== 'MAX_TOKENS') throw new Error('Gemini 响应未按支持的终态结束，请查询账单状态。')
  if (requireTerminal && !finish) throw new Error('Gemini 响应缺少成功结束标记，请查询账单状态。')
  let delta = ''
  for (const value of list(record(candidate.content).parts)) {
    const part = record(value)
    if (typeof part.text !== 'string' || (part.thought !== undefined && typeof part.thought !== 'boolean') || Object.keys(part).some(key => !['text', 'thought'].includes(key))) throw new Error('Gemini 返回了当前不支持的内容，请查询账单状态。')
    if (!part.thought) delta += part.text
  }
  let usage: Value | undefined
  if (body.usageMetadata !== undefined) {
    usage = record(body.usageMetadata)
    const fields = ['promptTokenCount', 'candidatesTokenCount', 'totalTokenCount', 'thoughtsTokenCount', 'cachedContentTokenCount']
    if (Object.keys(usage).some(key => !fields.includes(key)) || Object.values(usage).some(value => !Number.isSafeInteger(value) || Number(value) < 0)) throw new Error('Gemini 用量含无法确认的计费维度，请凭请求 ID 查询账单。')
  }
  if (finish) {
    const input = usage?.promptTokenCount; const candidatesOutput = usage?.candidatesTokenCount; const total = usage?.totalTokenCount
    const thoughts = Number(usage?.thoughtsTokenCount ?? 0); const cached = Number(usage?.cachedContentTokenCount ?? 0)
    const sum = Number(input) + Number(candidatesOutput) + thoughts
    if (![input, candidatesOutput, total].every(value => Number.isSafeInteger(value) && Number(value) >= 0) || !Number.isSafeInteger(sum) || sum !== total || cached > Number(input)) throw new Error('Gemini 缺少可信最终用量，请凭请求 ID 查询账单。')
  }
  return { delta: delta || undefined, usage, ...(finish ? { terminal: true } : {}) }
}

function retrievalUpdate(protocol: Protocol, body: Value): InferenceUpdate {
  const usage = record(body.usage)
  const total = usage.total_tokens
  if (!Number.isSafeInteger(total) || Number(total) < 0 || (protocol === 'openai_embeddings' && usage.prompt_tokens === undefined) || (usage.prompt_tokens !== undefined && usage.prompt_tokens !== total) || (usage.completion_tokens !== undefined && usage.completion_tokens !== 0) || Object.keys(usage).some(key => !['total_tokens', 'prompt_tokens', 'completion_tokens'].includes(key))) throw new Error('缺少可信的仅输入用量，请凭请求 ID 查询账单。')
  if (protocol === 'openai_embeddings') {
    const data = list(body.data)
    if (body.object !== 'list' || !data.length || data.length > 128) throw new Error('向量结果格式无效，请凭请求 ID 查询结果。')
    const preview = data.slice(0, 16).map(item => {
      const row = record(item); const vector = row.embedding
      if (!Number.isSafeInteger(row.index) || Number(row.index) < 0 || row.object !== 'embedding' || (!Array.isArray(vector) && typeof vector !== 'string')) throw new Error('向量结果格式无效，请凭请求 ID 查询结果。')
      if (Array.isArray(vector)) {
        if (!vector.length || vector.length > 8192 || vector.some(value => typeof value !== 'number' || !Number.isFinite(value))) throw new Error('向量结果格式无效，请凭请求 ID 查询结果。')
        return { index: row.index, dimensions: vector.length, first_values: vector.slice(0, 8) }
      }
      if (!vector || !/^[A-Za-z0-9+/]+={0,2}$/.test(vector)) throw new Error('向量结果格式无效，请凭请求 ID 查询结果。')
      return { index: row.index, encoding_format: 'base64', base64_preview: `${vector.slice(0, 64)}${vector.length > 64 ? '…' : ''}` }
    })
    return { delta: `向量数量：${data.length}\n仅展示前 16 条、每条前 8 维；完整向量请通过 API 获取。\n${JSON.stringify(preview, null, 2)}`, usage, terminal: true }
  }
  const results = list(body.results)
  if (!results.length || results.length > 128) throw new Error('排名结果格式无效，请凭请求 ID 查询结果。')
  const preview = results.slice(0, 20).map((item, rank) => {
    const row = record(item); const score = row.relevance_score
    if (!Number.isSafeInteger(row.index) || Number(row.index) < 0 || typeof score !== 'number' || !Number.isFinite(score) || score < 0 || score > 1) throw new Error('排名结果格式无效，请凭请求 ID 查询结果。')
    const document = typeof row.document === 'string' ? row.document : text(record(row.document).text)
    return { rank: rank + 1, document_index: row.index, relevance_score: score, ...(document ? { document_preview: document.length > 500 ? `${document.slice(0, 500)}…` : document } : {}) }
  })
  return { delta: `排名数量：${results.length}\n索引从 0 开始对应原文档；仅展示前 20 条及文档摘要。\n${JSON.stringify(preview, null, 2)}`, usage, terminal: true }
}

export interface InferenceResult { requestId: string; firstByteMs?: number; firstContentMs?: number; durationMs: number; bytes: number }
export async function runInference(protocol: Protocol, key: string, body: Record<string, unknown>, signal: AbortSignal, onUpdate: (update: InferenceUpdate) => void, onHeaders?: (id: string) => void): Promise<InferenceResult> {
  const info = protocolInfo(protocol)
  if (!info || !key.trim() || /[\r\n]/.test(key)) throw new Error('请输入有效的 API 密钥。')
  if (info.inputOnly && ['stream', 'stream_options', 'messages', 'tools', 'tool_choice', 'parallel_tool_calls', 'max_tokens', 'max_completion_tokens', 'max_output_tokens'].some(field => field in body)) throw new Error('检索请求不支持生成输出、SSE 或工具字段。')
  const target = inferenceHTTP(protocol, body)
  const start = performance.now()
  const headers: Record<string, string> = { 'Content-Type': 'application/json', Accept: target.stream ? 'text/event-stream' : 'application/json' }
  if (protocol === 'anthropic_messages') { headers['x-api-key'] = key.trim(); headers['anthropic-version'] = '2023-06-01' }
  else headers.Authorization = `Bearer ${key.trim()}`
  const response = await fetch(target.endpoint, {
    method: 'POST', credentials: 'omit', redirect: 'error', signal,
    headers,
    body: JSON.stringify(target.body),
  })
  const result: InferenceResult = { requestId: response.headers.get('X-Request-ID') || '', durationMs: 0, bytes: 0 }
  onHeaders?.(result.requestId)
  const reader = response.body?.getReader()
  if (!reader) throw new Error('服务没有返回响应体，请查询请求状态。')
  const decoder = new SSEDecoder()
  const chunks: Uint8Array[] = []
  let terminal = false
  const streaming = response.ok && target.stream
  if (streaming && !response.headers.get('Content-Type')?.toLowerCase().startsWith('text/event-stream')) { await reader.cancel(); throw new Error('服务未返回 SSE 响应，请查询请求状态。') }
  function update(value: InferenceUpdate) {
    if (value.error) throw new Error(`调用未完成（${value.error}），请凭请求 ID 查询账单。`)
    if (terminal) throw new Error('结束标记后仍有响应事件，请查询请求状态。')
    if (value.delta && result.firstContentMs === undefined) result.firstContentMs = performance.now() - start
    if (value.terminal) terminal = true
    onUpdate(value)
  }
  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      if (value.byteLength && result.firstByteMs === undefined) result.firstByteMs = performance.now() - start
      result.bytes += value.byteLength
      if (result.bytes > 8 << 20) throw new Error('响应超过 8 MiB 展示上限，请查询调用记录。')
      if (streaming) for (const frame of decoder.push(value)) update(frameUpdate(protocol, frame))
      else chunks.push(value)
    }
    if (streaming) {
      for (const frame of decoder.push(new Uint8Array(), true)) update(frameUpdate(protocol, frame))
      if (!terminal) throw new Error('响应流缺少成功结束标记，请查询账单状态。')
    } else {
      const bytes = new Uint8Array(result.bytes); let offset = 0
      for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength }
      let value: unknown
      try { value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) } catch { throw new Error(`服务响应无法解析（HTTP ${response.status}），请凭请求 ID 查询结果。`) }
      if (!response.ok) throw new Error(`调用未完成（HTTP ${response.status} · ${safeCode(record(record(value).error).code)}），请查询账单状态。`)
      update(jsonUpdate(protocol, value))
    }
  } finally { await reader.cancel().catch(() => undefined); reader.releaseLock() }
  result.durationMs = performance.now() - start
  return result
}
