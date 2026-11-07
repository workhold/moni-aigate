<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Cpu, Refresh, CopyDocument } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import { errorText, request } from '../api'
import { integer, time } from '../format'
import { protocolInfo } from '../inference'
import { session } from '../session'
import { applyRetailQuote, parsePricedModels } from '../customer-pricing'
import type { PricedModel as Model } from '../customer-pricing'
import { parsePriceVersion, parsePricingAssignment, parsePricingEnvelope, parsePricingPage, parseRetailPrice, pricingID, pricingRequest } from '../pricing'
import type { PriceVersion, PricingAssignment } from '../pricing'
import { rateIsZero, ratePerMillionText } from '../metering-wire'
import { createPricingEpoch, pricingContext } from '../pricing-state'

const rows = ref<Model[]>([]), prices = ref<PriceVersion[]>([]), loading = ref(false), error = ref(''), priceError = ref(''), domainError = ref(''), assignment = ref<PricingAssignment | null>(null), quoteBusy = ref(''), quoteError = ref('')
const context = computed(() => pricingContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}`, session.me?.csrf_token || '', session.mfaPending)), listGate = createPricingEpoch(), quoteGate = createPricingEpoch()
const canReadDomain = computed(() => ['owner','billing_viewer'].includes(session.me?.tenant?.role || ''))
const defaultHistory = computed(() => session.me?.user.platform_role === 'admin')
function property(model: Model, key: string) { return (model as unknown as Record<string, unknown>)[key] ?? model.profile?.[key] }
function inputOnly(model: Model) { return !!protocolInfo(property(model, 'protocol'))?.inputOnly }
function modelName(id: string) { return rows.value.find(m => m.id === id)?.public_name || id }
function rate(model: Model, dimension: 'text_input_token' | 'text_output_token') { return ratePerMillionText(model.retail_price?.rates, dimension) }
function outputIsFree(model: Model) { return rateIsZero(model.retail_price?.rates, 'text_output_token') }
async function load() {
  const ticket = listGate.ticket(), tenant = session.me?.tenant?.id
  quoteGate.reset(context.value); quoteBusy.value = ''; quoteError.value = ''
  loading.value = true; error.value = ''; priceError.value = ''; domainError.value = ''; assignment.value = null
  const results = await Promise.allSettled([request<unknown>('/models', 'GET', undefined, undefined, () => listGate.accepts(ticket)), request<unknown>('/prices', 'GET', undefined, undefined, () => listGate.accepts(ticket)), canReadDomain.value ? pricingRequest('/tenant/pricing', 'GET', undefined, () => listGate.accepts(ticket)) : Promise.resolve(null)])
  if (!listGate.accepts(ticket)) return
  if (results[0].status === 'fulfilled') { try { rows.value = parsePricedModels(results[0].value.data) } catch (cause) { rows.value = []; error.value = errorText(cause) } } else { rows.value = []; error.value = errorText(results[0].reason) }
  if (results[1].status === 'fulfilled') { try { prices.value = parsePricingPage(results[1].value, value => parsePriceVersion(value), row => row.id).rows } catch (cause) { prices.value = []; priceError.value = errorText(cause) } } else { prices.value = []; priceError.value = errorText(results[1].reason) }
  if (results[2].status === 'fulfilled' && results[2].value !== null && tenant) { try { assignment.value = parsePricingEnvelope(results[2].value, value => parsePricingAssignment(value, tenant)) } catch (cause) { domainError.value = errorText(cause) } } else if (results[2].status === 'rejected') domainError.value = errorText(results[2].reason)
  loading.value = false
}
async function quote(model: Model) {
  if (quoteBusy.value || loading.value) return
  const ticket = quoteGate.ticket(); quoteBusy.value = model.id; quoteError.value = ''
  try {
    const raw = await pricingRequest(`/models/${pricingID(model.id)}/retail-price`, 'GET', undefined, () => quoteGate.accepts(ticket))
    if (!quoteGate.accepts(ticket)) return
    rows.value = applyRetailQuote(rows.value, parsePricingEnvelope(raw, value => parseRetailPrice(value, model.id)))
  } catch (cause) { if (quoteGate.accepts(ticket)) quoteError.value = errorText(cause) }
  finally { if (quoteGate.accepts(ticket)) quoteBusy.value = '' }
}
async function copy(value: string) { try { await navigator.clipboard.writeText(value); ElMessage.success('模型名称已复制。') } catch { ElMessage.warning('无法复制，请手动选择模型名称。') } }
watch(context, () => { listGate.reset(context.value); quoteGate.reset(context.value); rows.value = []; prices.value = []; assignment.value = null; quoteBusy.value = ''; loading.value = false; error.value = ''; priceError.value = ''; quoteError.value = ''; domainError.value = ''; if (session.me?.tenant) void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { listGate.dispose(); quoteGate.dispose() })
</script>
<template>
  <PageHeader eyebrow="MODEL CATALOG" title="模型与价格" description="本租户当前报价、模型能力与不可变售价版本；实际调用按服务端冻结的价格结算。"><el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button></PageHeader>
  <ErrorNotice :message="error" @retry="load" />
  <ErrorNotice :message="domainError" @retry="load" />
  <el-alert v-if="assignment" type="info" :closable="false" :title="`当前租户价格域：${assignment.price_group_id ?? '公共默认'} · 归属版本 ${assignment.pricing_revision}`" />
  <p v-if="!canReadDomain" class="section-note">当前成员仅查看自己获授权模型的报价；报价来源和时刻逐模型显示。</p>
  <el-alert v-if="quoteError" :title="quoteError" type="error" :closable="false" />
  <el-skeleton v-if="loading && !rows.length" :rows="5" animated class="panel skeleton-card" />
  <div v-else-if="rows.length" class="model-card-grid">
    <article v-for="model in rows" :key="model.id" class="model-card">
      <div style="display:flex;justify-content:space-between;align-items:flex-start"><div class="model-icon"><el-icon><Cpu /></el-icon></div><el-tag type="success">{{ protocolInfo(property(model, 'protocol'))?.title || '未知协议' }}</el-tag></div>
      <h3>{{ model.public_name }}<el-button link :icon="CopyDocument" aria-label="复制模型名称" @click="copy(model.public_name)" /></h3><div class="model-id mono">{{ model.id }}</div>
      <div class="model-properties">
        <div class="model-property"><span>{{ inputOnly(model) ? '整个请求输入容量' : '上下文上限' }}</span><strong>{{ integer(property(model, 'context_limit')) }} Tokens</strong></div>
        <div class="model-property"><span>{{ inputOnly(model) ? '计费方式' : '最大输出' }}</span><strong>{{ inputOnly(model) ? '仅输入计费' : `${integer(property(model, 'output_limit'))} Tokens` }}</strong></div>
        <div v-if="inputOnly(model)" class="model-property"><span>结果类型</span><strong>{{ property(model, 'protocol') === 'openai_embeddings' ? '向量 · JSON' : '相关性排名 · JSON' }}</strong></div>
        <div v-else class="model-property"><span>函数工具转发</span><strong>{{ property(model, 'protocol') === 'gemini_generate_content' ? '当前纯文本合同不支持' : property(model, 'supports_tools') ? '支持' : '以接口合同为准' }}</strong></div>
        <div class="model-property"><span>协议</span><strong>{{ protocolInfo(property(model, 'protocol'))?.endpoint || '联系运营确认' }}</strong></div>
      </div>
      <p v-if="inputOnly(model)" class="field-hint">输入硬上界覆盖整个文本批次或 query 与全部文档。无生成输出、SSE 或函数工具。</p>
      <p v-if="property(model, 'protocol') === 'gemini_generate_content'" class="field-hint">当前支持无状态纯文本 JSON / SSE，模型名位于调用路径。暂不支持媒体、函数工具、缓存引用和 thinking 配置；计费输出包含候选与思考 Tokens。</p>
      <el-alert v-if="model.retail_price?.status === 'unavailable'" title="本租户价格域暂无有效价；调用将拒绝，组缺价不回落公共默认。" type="warning" :closable="false" />
      <div v-if="model.retail_price?.status === 'available'" class="model-price"><div><span>本租户输入 / 百万 Tokens</span><strong>{{ rate(model, 'text_input_token') }}</strong></div><div><span>本租户输出 / 百万 Tokens</span><strong>{{ inputOnly(model) && outputIsFree(model) ? '不计费（固定为 0）' : rate(model, 'text_output_token') }}</strong></div></div>
      <div v-else class="model-price"><div><span>本租户完整报价</span><strong>暂无有效报价</strong></div></div>
      <p v-if="model.retail_price" class="field-hint">{{ model.retail_price.source === 'default' ? '公共默认域' : `租户组 ${model.retail_price.price_group_id}` }} · 观察 {{ model.retail_price.as_of }}<br v-if="model.retail_price.effective_at" /><span v-if="model.retail_price.effective_at">生效 {{ model.retail_price.effective_at }}</span></p>
      <p v-else class="field-hint">点击读取单模型报价，核查本租户当前价格域、缺价状态与数据库观察时刻。</p>
      <el-button link :loading="quoteBusy === model.id" :disabled="!!quoteBusy || loading" @click="quote(model)">读取当前单模型报价</el-button>
      <router-link :to="{ path: '/playground', query: { model: model.id } }" class="model-playground-link">测试调用 →</router-link>
    </article>
  </div>
  <section v-else-if="!error" class="panel empty-state"><div class="empty-icon"><el-icon><Cpu /></el-icon></div><h3>暂无获授权的模型</h3><p>模型目录仅显示当前租户与成员可以调用的模型。请联系运营人员确认授权及渠道状态。</p></section>
  <p class="section-note">调用前按模型公布的输入硬上界冻结费用，生成模型另计受控输出上限；Embeddings 与 Rerank 仅输入计费。最终按可信用量结算，多余冻结自动释放。渠道切换不改变已锁定客户售价；网关不执行函数工具，也不自动续写。</p>
  <section class="panel"><div class="panel-header"><div><div class="panel-title">{{ defaultHistory ? '公共默认售价历史（平台管理员接口）' : '本租户当前域的已生效售价版本' }}</div><div class="panel-subtitle">{{ defaultHistory ? '平台管理员读取完整费率向量，历史 schema 身份随版本保留。' : '仅含当前价格域与当前授权范围；过去请求保留原冻结快照。' }} 时间按本地时区显示。</div></div></div><ErrorNotice :message="priceError" @retry="load" /><el-table :data="prices" empty-text="暂无售价版本"><el-table-column label="模型" min-width="180"><template #default="{ row }">{{ row.model_name || modelName(row.model_id) }}</template></el-table-column><el-table-column prop="schema_id" label="Schema / default" min-width="280" /><el-table-column label="完整费率（微元 / token）" min-width="260"><template #default="{ row }"><div v-for="line in row.rates.rates" :key="line.dimension">{{ line.dimension }}：{{ line.numerator }} / {{ line.denominator }} · {{ line.unit }}</div></template></el-table-column><el-table-column label="输入 / 百万 Tokens" min-width="180"><template #default="{ row }">{{ ratePerMillionText(row.rates, 'text_input_token') }}</template></el-table-column><el-table-column label="输出 / 百万 Tokens" min-width="180"><template #default="{ row }">{{ ratePerMillionText(row.rates, 'text_output_token') }}</template></el-table-column><el-table-column label="生效时间" min-width="180"><template #default="{ row }">{{ time(row.effective_at) }}</template></el-table-column></el-table></section>
</template>

<style scoped>
.model-playground-link{display:inline-block;font-size:11px;color:#087e83;margin-top:18px;line-height:1.8}
</style>
