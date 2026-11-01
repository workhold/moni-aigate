<script setup lang="ts">
import { useRoute } from 'vue-router'
import { computed, onBeforeUnmount, onMounted, reactive, ref, toRefs, watch } from 'vue'
import { Connection, Delete, Refresh, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import { errorText, request } from '../api'
import { embeddingsRequest, geminiRequest, inferenceHTTP, inputLines, protocolInfo, rerankRequest, runInference, textRequest } from '../inference'
import type { Protocol } from '../inference'
import { resetResponseForModelChange } from '../playground-state'
import type { PlaygroundResponseState } from '../playground-state'
import { integer } from '../format'
import { isAdmin, session } from '../session'

interface Model { id: string; public_name: string; protocol?: string; output_limit?: number; default_output_limit?: number; profile?: Record<string, unknown> }
const route = useRoute()
const models = ref<Model[]>([]); const modelId = ref(''); const loading = ref(false); const loadError = ref('')
const key = ref(''); const prompt = ref('用一句话介绍你自己。'); const outputLimit = ref(128); const streaming = ref(true)
const systemInstruction = ref('')
const embeddingBatch = ref(false); const encodingFormat = ref<'float' | 'base64'>('float'); const dimensions = ref<number | undefined>()
const query = ref('什么是 AI 网关？'); const documents = ref('AI 网关统一管理模型接口、密钥和用量。\n向量数据库用于相似度检索。'); const topN = ref<number | undefined>(); const returnDocuments = ref(false)
const busy = ref(false)
const responseState = reactive<PlaygroundResponseState>({ output: '', usage: {}, requestId: '', metrics: null, error: '', status: '' })
const { status, error, output, usage, requestId, metrics } = toRefs(responseState)
let controller: AbortController | undefined
const selected = computed(() => models.value.find(model => model.id === modelId.value))
function property(model: Model | undefined, field: string) { return model?.[field as keyof Model] ?? model?.profile?.[field] }
const info = computed(() => protocolInfo(property(selected.value, 'protocol')))
const inputOnly = computed(() => !!info.value?.inputOnly)
const documentList = computed(() => inputLines(documents.value))
const maximum = computed(() => { const n = Number(property(selected.value, 'output_limit')); return Number.isSafeInteger(n) && n > 0 ? n : 2048 })
const requestDraft = computed(() => {
  if (!selected.value || !info.value) return { body: null, error: '' }
  try {
    const model = selected.value.public_name
    const body = info.value.id === 'openai_embeddings'
      ? embeddingsRequest(model, embeddingBatch.value ? inputLines(prompt.value) : prompt.value, { encodingFormat: encodingFormat.value, dimensions: dimensions.value })
      : info.value.id === 'jina_rerank'
        ? rerankRequest(model, query.value, documentList.value, topN.value, returnDocuments.value)
        : info.value.id === 'gemini_generate_content'
          ? geminiRequest(model, prompt.value, outputLimit.value, streaming.value, systemInstruction.value)
          : textRequest(info.value.id, model, prompt.value, outputLimit.value, streaming.value)
    inferenceHTTP(info.value.id, body)
    return { body, error: '' }
  } catch (e) { return { body: null, error: errorText(e) } }
})
const payload = computed(() => requestDraft.value.body)
const endpoint = computed(() => payload.value && info.value ? inferenceHTTP(info.value.id, payload.value).endpoint : info.value?.endpoint)
const snippet = computed(() => {
  if (!payload.value || !info.value) return ''
  const target = inferenceHTTP(info.value.id, payload.value)
  const auth = info.value.id === 'anthropic_messages'
    ? `  -H 'x-api-key: '"$MONI_API_KEY" \\\n  -H 'anthropic-version: 2023-06-01'`
    : `  -H 'Authorization: Bearer '"$MONI_API_KEY"`
  return `curl${target.stream ? ' -N' : ''} '${window.location.origin}${target.endpoint}' \\\n${auth} \\\n  -H 'Content-Type: application/json' \\\n  --data-binary @request.json\n\n# request.json\n${JSON.stringify(target.body, null, 2)}`
})
const usageText = computed(() => Object.keys(usage.value).length ? JSON.stringify(usage.value, null, 2) : '')
async function load() {
  if (!session.me?.tenant) return
  loading.value = true; loadError.value = ''
  try {
    models.value = (await request<Model[]>('/models')).data || []
    if (!models.value.some(model => model.id === modelId.value)) modelId.value = models.value.find(model => model.id === route.query.model)?.id || models.value[0]?.id || ''
  } catch (e) { loadError.value = errorText(e) } finally { loading.value = false }
}
watch(selected, (model, previous) => {
  const n = Number(property(model, 'default_output_limit'))
  outputLimit.value = Math.min(Number.isSafeInteger(n) && n > 0 ? n : 128, maximum.value)
  resetResponseForModelChange(responseState, previous?.id, model?.id)
})
function clear() { key.value = ''; output.value = ''; usage.value = {}; requestId.value = ''; metrics.value = null; error.value = ''; status.value = '' }
async function run() {
  if (busy.value) return
  error.value = ''
  if (!payload.value || !info.value || !key.value.trim()) { error.value = requestDraft.value.error || '请选择模型并填写 API 密钥和测试输入。'; return }
  if (!inputOnly.value && outputLimit.value > maximum.value) { error.value = `输出上限不能超过模型允许的 ${maximum.value} Tokens。`; return }
  const protocol: Protocol = info.value.id
  const body = payload.value
  busy.value = true; output.value = ''; usage.value = {}; requestId.value = ''; metrics.value = null; status.value = '等待响应'
  controller = new AbortController()
  try {
    metrics.value = await runInference(protocol, key.value, body, controller.signal, update => {
      if (update.delta) { output.value += update.delta; status.value = '正在接收' }
      if (update.usage) usage.value = { ...usage.value, ...update.usage }
    }, id => { requestId.value = id })
    status.value = '调用完成'
  } catch (e) {
    if (controller.signal.aborted) { status.value = '已停止接收'; error.value = '已取消本次调用。供应商可能已产生用量，请凭请求 ID 查询最终账单。' }
    else { status.value = '调用未完成'; error.value = errorText(e) }
  } finally { busy.value = false; controller = undefined }
}
onMounted(load)
onBeforeUnmount(() => { controller?.abort(); key.value = ''; prompt.value = ''; systemInstruction.value = ''; query.value = ''; documents.value = ''; output.value = ''; usage.value = {} })
</script>

<template>
  <PageHeader eyebrow="DEVELOPER PLAYGROUND" title="API 联调" description="使用真实 API 密钥验证生成、向量或排名结果和用量，按模型公布价格正常计费。"><el-button :icon="Refresh" :loading="loading" :disabled="busy" @click="load">刷新模型</el-button></PageHeader>
  <ErrorNotice :message="loadError" @retry="load" />
  <section v-if="!session.me?.tenant" class="panel"><div class="panel-header"><div><div class="panel-title">先准备客户工作空间</div><p class="panel-subtitle">API 调用、模型授权、密钥与钱包都归属于客户工作空间。</p></div></div><div class="panel-body"><div class="instruction-step"><span class="step-number">01</span><div><h3>接入渠道并发布模型</h3><p>选择生成、Embeddings 或 Rerank 协议，配置渠道、模型映射与售价。</p></div></div><div class="instruction-step"><span class="step-number">02</span><div><h3>创建客户并完成授权与核款</h3><p>为客户授权模型，按人工核款流程由不同财务账号审批入账。</p></div></div><div class="instruction-step"><span class="step-number">03</span><div><h3>使用客户账号创建密钥并测试</h3><p>登录客户工作空间，创建受限 API 密钥后进入本页，验证真实调用和账单。</p></div></div><div v-if="isAdmin" class="list-action" style="margin-top:22px"><router-link to="/platform/catalog"><el-button>配置模型与路由</el-button></router-link><router-link to="/platform/tenants"><el-button type="primary">管理客户</el-button></router-link></div><p v-else class="field-hint" style="margin-top:22px">请联系平台管理员，为当前账号分配客户工作空间；已加入其他工作空间时，可在顶部切换。</p></div></section>
  <div v-else class="playground-grid">
    <section class="panel playground-input"><div class="panel-header"><div><div class="panel-title"><el-icon><Connection /></el-icon>请求配置</div><p class="panel-subtitle">密钥仅保留在当前页面，离开或清空时移除。</p></div></div><div class="panel-body">
      <el-form label-position="top" @submit.prevent="run">
        <el-form-item label="模型"><el-select v-model="modelId" filterable :loading="loading" :disabled="busy" placeholder="选择获授权模型" style="width:100%"><el-option v-for="model in models" :key="model.id" :value="model.id" :label="`${model.public_name} · ${protocolInfo(property(model, 'protocol'))?.title || '未知协议'}`" /></el-select></el-form-item>
        <div v-if="selected" class="playground-protocol"><el-tag>{{ info?.title || '未知协议' }}</el-tag><code class="mono">{{ endpoint }}</code><p class="field-hint">{{ info?.scope }}。{{ inputOnly ? '仅返回 JSON；不使用生成输出、SSE 或工具。' : '此页发送单轮文本，不自动执行工具或续写。' }}</p></div>
        <el-form-item label="API 密钥"><el-input v-model="key" type="password" show-password autocomplete="off" :disabled="busy" placeholder="粘贴当前租户的 API 密钥" maxlength="256" /><div class="field-hint">可在 <router-link to="/keys" class="text-success">API 密钥</router-link> 页面创建。最终权限以该密钥为准。</div></el-form-item>
        <template v-if="info?.id === 'jina_rerank'">
          <el-form-item label="查询 query"><el-input v-model="query" type="textarea" :rows="2" maxlength="16000" :disabled="busy" placeholder="输入要匹配的查询" /></el-form-item>
          <el-form-item label="待排名文档 documents"><el-input v-model="documents" type="textarea" :rows="6" maxlength="128000" :disabled="busy" placeholder="每行一条非空文档，最多 128 条" /><p class="field-hint">每行一条，空行忽略。当前 {{ documentList.length }} 条；整个请求按输入 Tokens 计费。</p></el-form-item>
          <div class="form-grid"><el-form-item label="返回前 N 条 top_n（可选）"><el-input-number v-model="topN" :min="1" :max="Math.max(1, documentList.length)" :precision="0" :disabled="busy" placeholder="留空返回全部" controls-position="right" style="width:100%" /></el-form-item><el-form-item label="返回原文档"><el-switch v-model="returnDocuments" :disabled="busy" /></el-form-item></div>
        </template>
        <template v-else>
          <el-form-item v-if="info?.id === 'gemini_generate_content'" label="系统指令 systemInstruction（可选）"><el-input v-model="systemInstruction" type="textarea" :rows="2" maxlength="16000" :disabled="busy" placeholder="可留空；仅发送纯文本系统指令" /></el-form-item>
          <el-form-item v-if="info?.id === 'openai_embeddings'" label="文本输入方式"><el-switch v-model="embeddingBatch" :disabled="busy" active-text="每行一条" inactive-text="整段文本" /></el-form-item>
          <el-form-item :label="info?.id === 'openai_embeddings' ? '待编码文本 input' : '测试消息'"><el-input v-model="prompt" type="textarea" :rows="5" maxlength="16000" show-word-limit :disabled="busy" :placeholder="info?.id === 'openai_embeddings' && embeddingBatch ? '每行一条文本，最多 128 条；空行忽略' : '输入一条测试文本'" /></el-form-item>
          <div v-if="info?.id === 'openai_embeddings'" class="form-grid"><el-form-item label="向量编码"><el-select v-model="encodingFormat" :disabled="busy" style="width:100%"><el-option value="float" label="float 数字数组" /><el-option value="base64" label="base64 float32" /></el-select></el-form-item><el-form-item label="向量维度（可选）"><el-input-number v-model="dimensions" :min="1" :max="8192" :precision="0" :disabled="busy" placeholder="供应商默认" controls-position="right" style="width:100%" /></el-form-item></div>
        </template>
        <div v-if="!inputOnly" class="form-grid"><el-form-item label="最大输出 Tokens"><el-input-number v-model="outputLimit" :min="1" :max="maximum" :precision="0" controls-position="right" :disabled="busy" style="width:100%" /></el-form-item><el-form-item label="流式输出 SSE"><el-switch v-model="streaming" :disabled="busy" /></el-form-item></div>
        <p v-if="inputOnly" class="field-hint">仅输入计费：整个文本批次或 query 与全部 documents 共用模型公布的输入硬上界。排名数量和向量维度不是生成输出 Tokens。</p>
        <p v-if="info?.id === 'gemini_generate_content'" class="field-hint">Gemini 纯文本调用暂不支持媒体、工具、缓存引用和 thinking 配置。最终输出计费包含供应商返回的候选与思考 Tokens，缓存量是输入量的子集。</p>
        <p v-if="requestDraft.error" class="field-hint">{{ requestDraft.error }}</p>
        <div class="list-action"><el-button type="primary" :icon="VideoPlay" :disabled="busy || !selected || !info || !key.trim() || !payload" @click="run">发送请求</el-button><el-button v-if="busy" :icon="VideoPause" @click="controller?.abort()">停止接收</el-button><el-button v-else :icon="Delete" @click="clear">清空结果与密钥</el-button></div>
      </el-form>
    </div></section>
    <section class="panel playground-output"><div class="panel-header"><div><div class="panel-title">模型响应</div><p class="panel-subtitle">{{ status || '发送请求后，在这里查看实际结果。' }}</p></div><el-tag v-if="busy" type="warning">调用中</el-tag><el-tag v-else-if="metrics" type="success">已完成</el-tag></div><div class="panel-body">
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon style="margin-bottom:18px" />
      <div v-if="requestId" class="playground-request"><span>请求 ID</span><code class="mono">{{ requestId }}</code><router-link :to="{ path: '/requests', query: { request_id: requestId } }" class="text-success">查询调用记录</router-link></div>
      <pre v-if="output" class="playground-response">{{ output }}</pre><div v-else class="playground-placeholder"><el-icon><Connection /></el-icon><span>{{ busy ? '等待有效内容…' : '暂无响应' }}</span></div>
      <div v-if="metrics" class="playground-timing"><span>首字节 <strong>{{ metrics.firstByteMs?.toFixed(1) ?? '—' }} ms</strong></span><span>首有效内容 <strong>{{ metrics.firstContentMs?.toFixed(1) ?? '—' }} ms</strong></span><span>总耗时 <strong>{{ metrics.durationMs.toFixed(1) }} ms</strong></span><span>响应大小 <strong>{{ integer(metrics.bytes) }} B</strong></span></div>
      <p v-if="metrics" class="field-hint">本次浏览器端测量包含网络、网关和供应商等待；单次耗时不代表网关额外开销或 P95。非流式的首有效内容在完整响应解析后记录。</p>
      <div v-if="usageText" class="playground-usage"><h3>{{ inputOnly ? '可信输入用量' : '供应商用量' }}</h3><pre class="code-block">{{ usageText }}</pre><p class="field-hint">{{ inputOnly ? '仅按整个请求的可信输入 Tokens 计费，输出用量为 0。' : '流中用量可能分步到达。' }}最终收费以调用记录中的已结算账单为准。</p></div>
    </div></section>
  </div>
  <section v-if="snippet" class="panel"><div class="panel-header"><div><div class="panel-title">在你的应用中复现</div><p class="panel-subtitle">将请求 JSON 保存为 request.json，并在本地设置环境变量 MONI_API_KEY。示例不包含你的密钥。</p></div></div><div class="panel-body"><pre class="code-block">{{ snippet }}</pre></div></section>
</template>

<style scoped>
.playground-grid{display:grid;grid-template-columns:minmax(300px,.85fr) minmax(0,1.2fr);gap:22px;align-items:start}.playground-input .panel-body{padding-bottom:26px}.playground-protocol{margin:-6px 0 22px;display:flex;gap:9px;align-items:center;flex-wrap:wrap}.playground-protocol p{width:100%;margin-top:0}.playground-output{min-height:580px}.playground-request{display:flex;gap:10px;flex-wrap:wrap;font-size:11px;line-height:1.8;color:#8493a5;margin-bottom:20px}.playground-request code{overflow-wrap:anywhere}.playground-response{white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.95;font-family:inherit;font-size:13px;color:#334e64;margin:0;min-height:220px}.playground-placeholder{display:flex;align-items:center;justify-content:center;flex-direction:column;gap:15px;color:#a5b3c3;font-size:12px;min-height:300px}.playground-placeholder .el-icon{font-size:32px}.playground-timing{display:grid;grid-template-columns:1fr 1fr;gap:15px;background:#f6f9fb;padding:17px;border-radius:8px;margin-top:23px;font-size:10px;color:#8191a4}.playground-timing strong{display:block;color:#3e6276;font-size:12px;font-weight:500;margin-top:7px}.playground-usage{margin-top:26px}.playground-usage h3{font-size:12px;font-weight:500;margin-bottom:12px}.playground-usage .code-block{margin:0}@media(max-width:1100px){.playground-grid{grid-template-columns:1fr}.playground-output{min-height:400px}}
</style>
