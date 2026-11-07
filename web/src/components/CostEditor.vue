<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { errorText, newOperationKey } from '../api'
import { isAdmin, session } from '../session'
import { cnyToMicro, time } from '../format'
import { parseCostChannel, parseCostSchemaChoice, parseCostVersion, readConfigurationRows } from '../cost-aware-routing'
import type { CostChannel, CostDraft, CostSchemaChoice, CostVersion } from '../cost-aware-routing'
import { parsePricingEnvelope, parsePricingReceipt, pricingCommand, pricingRequest, pricingTime } from '../pricing'
import type { CostPrice, PricingCommand, PricingReceipt } from '../pricing'
import { ratePerMillionText } from '../metering-wire'
import { createPricingEpoch, pricingContext, pricingJournal } from '../pricing-state'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [] }>()
const context = computed(() => pricingContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}`, session.me?.csrf_token || '', session.mfaPending)), gate = createPricingEpoch()
const channels = ref<CostChannel[]>([]), selectedID = ref(''), channel = ref<CostChannel | null>(null)
const schemas = ref<CostSchemaChoice[]>([])
const blank = (): CostDraft => ({ schema_id: '', schema_digest: '', variant_key: 'default', upstream_model: '', basis: 'unknown', input_rate: '', output_rate: '', effective_at: '' })
const draft = ref<CostDraft>(blank()), observed = ref<CostVersion[]>([])
const command = ref<PricingCommand | null>(null), receipt = ref<PricingReceipt | null>(null), unknown = ref(false)
const loading = ref(false), saving = ref(false), error = ref('')
const visible = computed({ get: () => props.modelValue, set: value => { if (!value) reset(); emit('update:modelValue', value) } })
const permitted = computed(() => isAdmin.value && !session.mfaPending)
const editable = computed(() => permitted.value && !!channel.value && !command.value && !loading.value && !saving.value)
const published = computed(() => receipt.value?.result as CostPrice | undefined)
function reset() { gate.reset(context.value); channels.value = []; schemas.value = []; selectedID.value = ''; channel.value = null; draft.value = blank(); observed.value = []; command.value = null; receipt.value = null; unknown.value = false; loading.value = false; saving.value = false; error.value = '' }
function selectSchema(id: string) {
  const selected = schemas.value.find(row => row.schema_id === id)
  draft.value.schema_id = selected?.schema_id ?? ''; draft.value.schema_digest = selected?.schema_digest ?? ''; draft.value.variant_key = 'default'
}
async function loadChannels() {
  if (!props.modelValue || !permitted.value || loading.value || saving.value) return
  const ticket = gate.ticket(); loading.value = true; error.value = ''
  try {
    const [channelRows, modelRows] = await Promise.all([readConfigurationRows('/platform/channels', () => gate.accepts(ticket)), readConfigurationRows('/platform/models', () => gate.accepts(ticket))])
    const rows = channelRows.map(row => parseCostChannel(row)), choices = modelRows.map(parseCostSchemaChoice)
    if (gate.accepts(ticket)) { channels.value = rows; schemas.value = [...new Map(choices.map(row => [row.schema_id, row])).values()] }
  } catch (e) { if (gate.accepts(ticket)) error.value = errorText(e) }
  finally { if (gate.accepts(ticket)) loading.value = false }
}
function selectChannel() {
  if (saving.value || loading.value || command.value) return
  error.value = ''; observed.value = []; draft.value = blank()
  const selected = channels.value.find(row => row.id === selectedID.value)
  channel.value = selected ? { ...selected } : null
}
// Read-only observation of current versions; it never proves an old command.
async function inspectVersions() {
  if (!channel.value || !permitted.value || loading.value || saving.value) return
  const id = channel.value.id, model = (command.value?.body.upstream_model as string | undefined) ?? draft.value.upstream_model.trim(), schema = (command.value?.body.schema_id as string | undefined) ?? draft.value.schema_id, ticket = gate.ticket()
  loading.value = true; error.value = ''
  try {
    const [channelRows, costRows] = await Promise.all([readConfigurationRows('/platform/channels', () => gate.accepts(ticket)), readConfigurationRows('/platform/cost-versions', () => gate.accepts(ticket))])
    const latest = channelRows.map(row => parseCostChannel(row)).find(row => row.id === id)
    if (!latest) throw new Error('当前渠道已不存在或无读取权限。')
    const versions = costRows.map(parseCostVersion).filter(row => row.channel_id === id && (!model || row.upstream_model === model) && (!schema || row.schema_id === schema))
    if (!gate.accepts(ticket)) return
    observed.value = versions
    if (!command.value) channel.value = latest
  } catch (e) { if (gate.accepts(ticket)) error.value = errorText(e) }
  finally { if (gate.accepts(ticket)) loading.value = false }
}
function effectiveAt(value: string): string {
  if (!value) return pricingTime(new Date().toISOString(), true)
  const date = new Date(value)
  if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() < 1 || date.getUTCFullYear() > 9999) throw new Error('请选择有效的生效时间。')
  return pricingTime(date.toISOString(), true)
}
async function save() {
  if (!editable.value || !channel.value || !session.me) return
  const actor = session.me.user.id, selected = channel.value, ticket = gate.ticket()
  error.value = ''
  let original: PricingCommand | null = null
  try {
    if (draft.value.basis !== 'unknown' && (!draft.value.input_rate.trim() || !draft.value.output_rate.trim())) throw new Error('合同或账单依据须明确填写两维单价，免费请填 0。')
    const known = draft.value.basis !== 'unknown'
    original = await pricingCommand('cost_price.publish', newOperationKey(), actor, { channel_id: selected.id, upstream_model: draft.value.upstream_model.trim(), schema_id: draft.value.schema_id, schema_digest: draft.value.schema_digest, variant_key: draft.value.variant_key, basis: draft.value.basis, input_rate: known ? cnyToMicro(draft.value.input_rate) : null, output_rate: known ? cnyToMicro(draft.value.output_rate) : null, effective_at: effectiveAt(draft.value.effective_at), expected_channel_revision: selected.management_revision })
    if (!gate.accepts(ticket)) return
    pricingJournal.begin(original); command.value = original; saving.value = true
    const raw = await pricingRequest(original.path, original.method, original.body, () => gate.accepts(ticket))
    if (!gate.accepts(ticket)) return
    const result = parsePricingEnvelope(raw, data => parsePricingReceipt(data, original!))
    pricingJournal.confirm(actor, original.operation_id, result); receipt.value = result
    emit('saved'); ElMessage.success('采购报价已追加保存；已有尝试保留原报价。')
  } catch (cause) {
    if (!gate.accepts(ticket)) return
    if (original && command.value) {
      try { const review = pricingJournal.fail(actor, original.operation_id, cause); unknown.value = review.state === 'unknown'; if (!unknown.value) command.value = null }
      catch { unknown.value = true }
    }
    error.value = unknown.value ? '原发布结果待核查，仅可查询原回执。404、关闭窗口或刷新页面都不证明提交失败，不能换 ID 重发。' : errorText(cause)
  } finally { if (gate.accepts(ticket)) saving.value = false }
}
async function inspectReceipt() {
  if (!command.value || saving.value) return
  const original = command.value, ticket = gate.ticket(); saving.value = true; error.value = ''
  try {
    const raw = await pricingRequest(`/platform/pricing-operations/${original.operation_id}`, 'GET', undefined, () => gate.accepts(ticket))
    if (!gate.accepts(ticket)) return
    const value = parsePricingEnvelope(raw, data => parsePricingReceipt(data, original))
    pricingJournal.confirm(original.actor, original.operation_id, value); receipt.value = value; unknown.value = false; emit('saved')
  } catch (cause) { if (gate.accepts(ticket)) error.value = `仅查询原回执，尚未确认。404不证明原提交失败。${errorText(cause)}` }
  finally { if (gate.accepts(ticket)) saving.value = false }
}
watch(() => props.modelValue, () => { reset(); if (props.modelValue && permitted.value) void loadChannels() }, { immediate: true, flush: 'sync' })
watch(context, () => { reset(); emit('update:modelValue', false) }, { flush: 'sync' })
onBeforeUnmount(() => { gate.dispose() })
</script>

<template>
  <el-dialog v-model="visible" title="新增渠道模型采购成本版本" width="780px" destroy-on-close :close-on-click-modal="false">
    <p class="dialog-description">按渠道、精确上游模型、schema 与 default 变体追加独立采购报价。客户售价保持独立；未知成本保存为空，不代表免费。历史attempt不追溯改价，账单差异请另追加成本更正。</p>
    <el-alert title="发布会校验并增加渠道管理版本，与渠道配置及其他成本发布互斥。结果不明时只按原操作 ID 查询，不能直接重发。" type="info" :closable="false" show-icon />
    <el-alert v-if="error" :title="error" type="error" :closable="false" style="margin-top:16px" />
    <p v-if="command" class="mono pricing-operation">原操作 ID：{{ command.operation_id }}</p>
    <el-alert v-if="published" type="success" :title="`已核对不可变原回执：渠道管理版本 ${published.expected_channel_revision} → ${published.channel_revision}。`" :closable="false" style="margin-top:14px" />
    <el-form v-if="!command" label-position="top" style="margin-top:20px" @submit.prevent="save">
      <el-form-item label="采购渠道" required><el-select v-model="selectedID" filterable :disabled="loading || saving" aria-label="采购渠道" style="width:100%" placeholder="选择并冻结渠道管理版本" @change="selectChannel"><el-option v-for="row in channels" :key="row.id" :label="row.name" :value="row.id" /></el-select></el-form-item>
      <template v-if="channel">
        <p class="field-hint">本次渠道管理 revision：{{ channel.management_revision }}</p>
        <el-form-item label="计量 schema（来自已发布模型）" required><el-select v-model="draft.schema_id" filterable :disabled="!editable" aria-label="采购报价计量 schema" style="width:100%" @change="selectSchema"><el-option v-for="row in schemas" :key="row.schema_id" :label="`${row.public_name} · ${row.schema_id}`" :value="row.schema_id" /></el-select><p class="field-hint">报价只作用于所选 schema / default；不会自动套用其他模型的报价。</p></el-form-item>
        <el-form-item label="精确上游模型名称" required><el-input v-model="draft.upstream_model" :disabled="!editable" maxlength="200" aria-label="采购上游模型名称" /></el-form-item>
        <el-form-item label="成本依据" required><el-select v-model="draft.basis" :disabled="!editable" aria-label="成本依据" style="width:100%"><el-option label="成本未知" value="unknown" /><el-option label="供应商合同" value="contract" /><el-option label="供应商账单" value="invoice" /></el-select></el-form-item>
        <div class="cost-rates"><el-form-item label="输入成本（CNY / 百万 Tokens）" :required="draft.basis !== 'unknown'"><el-input v-model="draft.input_rate" :disabled="!editable || draft.basis === 'unknown'" :placeholder="draft.basis === 'unknown' ? '成本未知' : '免费请明确填 0'" inputmode="decimal" maxlength="32" aria-label="输入采购单价" /></el-form-item><el-form-item label="输出成本（CNY / 百万 Tokens）" :required="draft.basis !== 'unknown'"><el-input v-model="draft.output_rate" :disabled="!editable || draft.basis === 'unknown'" :placeholder="draft.basis === 'unknown' ? '成本未知' : '免费请明确填 0'" inputmode="decimal" maxlength="32" aria-label="输出采购单价" /></el-form-item></div>
        <el-form-item label="生效时间"><el-input v-model="draft.effective_at" type="datetime-local" step="1" :disabled="!editable" aria-label="采购报价生效时间" /><p class="field-hint">留空则以提交时刻生效。可登记历史合同或未来调价；仅影响后续尝试，同渠道、上游模型、schema、变体与生效时刻不能重复。</p></el-form-item>
      </template>
    </el-form>
    <div class="cost-check"><el-button v-if="!channel" :icon="Refresh" :loading="loading" :disabled="saving" @click="loadChannels">重新读取渠道</el-button><el-button v-else :icon="Refresh" :loading="loading" :disabled="saving" @click="inspectVersions">只读查看当前报价</el-button></div>
    <el-table v-if="observed.length" :data="observed.slice(0, 20)" style="margin-top:18px"><el-table-column prop="upstream_model" label="上游模型" min-width="140" /><el-table-column prop="schema_id" label="Schema / default" min-width="200" /><el-table-column label="依据" width="90"><template #default="{ row }">{{ row.basis === 'unknown' ? '未知' : row.basis === 'invoice' ? '账单' : '合同' }}</template></el-table-column><el-table-column label="完整供应商费率" min-width="300"><template #default="{ row }"><template v-if="row.rates"><div v-for="line in row.rates.rates" :key="line.dimension">{{ line.dimension }}：{{ line.numerator }} / {{ line.denominator }} · {{ line.unit }}</div><div class="muted small-text">输入 {{ ratePerMillionText(row.rates, 'text_input_token') }} · 输出 {{ ratePerMillionText(row.rates, 'text_output_token') }}</div></template><span v-else>未知（无 RatesWire）</span></template></el-table-column><el-table-column label="生效时间" min-width="170"><template #default="{ row }">{{ time(row.effective_at) }}</template></el-table-column></el-table>
    <p v-if="observed.length > 20" class="field-hint">已读取 {{ observed.length }} 条匹配版本，以上展示最近登记的 20 条。</p>
    <template #footer><el-button @click="visible = false">关闭</el-button><el-button v-if="unknown" :loading="saving" @click="inspectReceipt">只读查询原回执</el-button><el-button v-else-if="!command" type="primary" :loading="saving" :disabled="!editable" @click="save">校验并追加成本版本</el-button></template>
  </el-dialog>
</template>

<style scoped>
.cost-rates{display:grid;grid-template-columns:1fr 1fr;gap:18px}.cost-check{display:flex;gap:10px;flex-wrap:wrap;margin-top:18px}.cost-check .el-button{margin:0}.pricing-operation{overflow-wrap:anywhere;margin-top:12px}@media(max-width:700px){.cost-rates{grid-template-columns:1fr;gap:0}}
</style>
