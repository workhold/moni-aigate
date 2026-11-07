<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { cnyToMicro } from '../format'
import { errorText, newOperationKey } from '../api'
import { session } from '../session'
import { parsePricingEnvelope, parsePricingReceipt, parsePricingModelChoice, pricingCommand, pricingRequest } from '../pricing'
import type { PriceGroup, PricingAssignment, PricingCommand, PricingKind, PricingReceipt, PricingModelChoice } from '../pricing'
import { readConfigurationRows } from '../cost-aware-routing'
import { createPricingEpoch, pricingContext, pricingJournal } from '../pricing-state'

const props = defineProps<{ modelValue: boolean; kind: PricingKind; group?: PriceGroup | null; assignment?: PricingAssignment | null; targetGroup?: string | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; changed: [] }>()
const context = computed(() => pricingContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}`, session.me?.csrf_token || '', session.mfaPending)), gate = createPricingEpoch()
const busy = ref(false), error = ref(''), command = ref<PricingCommand | null>(null), receipt = ref<PricingReceipt | null>(null), unknown = ref(false), acceptedImpact = ref(false)
const label = ref(''), state = ref<'active'|'retired'>('active'), model = ref(''), input = ref(''), output = ref(''), effective = ref('')
const models = ref<PricingModelChoice[]>([]), modelLoading = ref(false), selectedModel = computed(() => models.value.find(row => row.id === model.value)), inputOnly = computed(() => selectedModel.value?.inputOnly ?? false)
const permitted = computed(() => session.me?.user.platform_role === 'admin' && !session.mfaPending)
const title = computed(() => ({ 'price_group.create': '创建价格组', 'price_group.update': '修改 / 退休价格组', 'group_price.publish': '追加组内售价版本', 'default_price.publish': '追加公共默认售价版本', 'cost_price.publish': '追加渠道采购报价', 'tenant_pricing.assign': '分配租户价格域' })[props.kind])
const pricePublish = computed(() => props.kind === 'group_price.publish' || props.kind === 'default_price.publish')
function clear() { gate.reset(context.value); busy.value = false; error.value = ''; command.value = null; receipt.value = null; unknown.value = false; acceptedImpact.value = false; label.value = props.kind === 'price_group.update' ? props.group?.label || '' : ''; state.value = props.kind === 'price_group.update' ? props.group?.state || 'active' : 'active'; model.value = ''; input.value = ''; output.value = ''; effective.value = ''; models.value = []; modelLoading.value = false; if (props.modelValue && pricePublish.value && permitted.value) void loadModels() }
async function loadModels() {
  const ticket = gate.ticket(); modelLoading.value = true
  try {
    const rows = await readConfigurationRows('/platform/models', () => gate.accepts(ticket))
    const choices = rows.map(parsePricingModelChoice)
    if (new Set(choices.map(row => row.id)).size !== choices.length) throw new Error('模型列表存在重复身份，请刷新核查。')
    if (gate.accepts(ticket)) models.value = choices
  } catch (cause) { if (gate.accepts(ticket)) error.value = errorText(cause) }
  finally { if (gate.accepts(ticket)) modelLoading.value = false }
}
function close() { clear(); emit('update:modelValue', false) }
function updateOpen(value: boolean) { if (!value) close() }
async function submit() {
  if (busy.value || command.value || !permitted.value || !session.me) return
  if (props.kind === 'tenant_pricing.assign' && !acceptedImpact.value) { error.value = '请明确接受缺价调用拒绝及预览可能变化的影响。'; return }
  const actor = session.me.user.id, kind = props.kind, ticket = gate.ticket()
  busy.value = true; error.value = ''
  let original: PricingCommand | null = null
  try {
    const source = props.group, assignment = props.assignment
    const body = kind === 'price_group.create' ? { label: label.value } : kind === 'price_group.update' ? { expected_revision: source?.revision, label: label.value, state: state.value } : kind === 'group_price.publish' || kind === 'default_price.publish' ? { model_id: model.value, schema_id: selectedModel.value?.schema_id, schema_digest: selectedModel.value?.schema_digest, variant_key: selectedModel.value?.variant_key, input_rate: cnyToMicro(input.value), output_rate: inputOnly.value ? '0' : cnyToMicro(output.value), effective_at: effective.value } : { expected_revision: assignment?.pricing_revision, price_group_id: props.targetGroup ?? null }
    original = await pricingCommand(kind, newOperationKey(), actor, body, kind === 'tenant_pricing.assign' ? assignment?.tenant_id : source?.id, kind === 'price_group.update' ? source || undefined : kind === 'tenant_pricing.assign' ? assignment || undefined : undefined)
    if (!gate.accepts(ticket)) return
    pricingJournal.begin(original); command.value = original; emit('changed')
    const raw = await pricingRequest(original.path, original.method, original.body, () => gate.accepts(ticket))
    if (!gate.accepts(ticket)) return
    const result = parsePricingEnvelope(raw, data => parsePricingReceipt(data, original!))
    pricingJournal.confirm(actor, original.operation_id, result); receipt.value = result; emit('changed')
  } catch (cause) {
    if (!gate.accepts(ticket)) return
    if (original && command.value) {
      try { const review = pricingJournal.fail(actor, original.operation_id, cause); unknown.value = review.state === 'unknown'; if (!unknown.value) command.value = null }
      catch { unknown.value = true }
      emit('changed')
    }
    error.value = unknown.value ? '原操作结果待核查，仅可查询原回执。404、关闭窗口或刷新页面都不证明提交失败，不能换 ID 重发。' : errorText(cause)
  } finally { if (gate.accepts(ticket)) busy.value = false }
}
async function inspect() {
  if (!command.value || busy.value) return
  const original = command.value, ticket = gate.ticket(); busy.value = true; error.value = ''
  try {
    const raw = await pricingRequest(`/platform/pricing-operations/${original.operation_id}`, 'GET', undefined, () => gate.accepts(ticket))
    if (!gate.accepts(ticket)) return
    const value = parsePricingEnvelope(raw, data => parsePricingReceipt(data, original))
    pricingJournal.confirm(original.actor, original.operation_id, value); receipt.value = value; unknown.value = false; emit('changed')
  } catch (cause) { if (gate.accepts(ticket)) error.value = `仅查询原回执，尚未确认。404不证明原提交失败。${errorText(cause)}` }
  finally { if (gate.accepts(ticket)) busy.value = false }
}
watch([() => props.modelValue, context, () => props.kind, () => props.group?.id, () => props.group?.revision, () => props.assignment?.tenant_id, () => props.assignment?.pricing_revision, () => props.targetGroup], clear, { flush: 'sync' })
watch(inputOnly, value => { if (value) output.value = '0' })
onBeforeUnmount(() => { gate.dispose() })
</script>
<template>
  <el-dialog :model-value="modelValue" :title="title" width="650px" destroy-on-close :close-on-click-modal="false" @update:model-value="updateOpen">
    <p class="dialog-description">售价是 CNY / 百万计费 Tokens 的绝对价格。当前组、模型授权与采购成本分别管理；已接受请求保留原冻结版本。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <p v-if="command" class="mono pricing-operation">原操作 ID：{{ command.operation_id }}</p>
    <el-alert v-if="receipt" type="success" title="已核对不可变原回执；当前资源可能已被后续操作修改，请刷新查看。" :closable="false" show-icon />
    <dl v-if="receipt" class="detail-list"><dt>资源</dt><dd class="mono">{{ receipt.operation.resource_id }}</dd><dt>原结果版本</dt><dd>{{ receipt.operation.revision ?? '不可变售价版本，无配置 revision' }}</dd><dt>原结果</dt><dd><pre class="code-block">{{ JSON.stringify(receipt.result, null, 2) }}</pre></dd></dl>
    <el-form v-if="!receipt && !unknown" label-position="top" :disabled="busy || !permitted" @submit.prevent="submit">
      <template v-if="kind === 'price_group.create' || kind === 'price_group.update'">
        <el-form-item label="价格组名称（最多 100 UTF-8 字节）"><el-input v-model="label" maxlength="100" /></el-form-item>
        <el-form-item v-if="kind === 'price_group.update'" label="状态"><el-select v-model="state" :disabled="group?.state === 'retired'"><el-option label="启用" value="active" /><el-option label="退休（不可恢复）" value="retired" /></el-select></el-form-item>
        <el-alert v-if="state === 'retired'" type="warning" title="退休后不能新分配租户或发布组价；已分配租户继续使用该组及其预发版本。退休不取消授权，也不改历史账单。" :closable="false" />
        <p v-if="group" class="field-hint">组 {{ group.id }} · 原版本 {{ group.revision }}。退休组仍可修改名称。完全同值不增加版本；旧版本冲突须明确重读后再编辑。</p>
      </template>
      <template v-else-if="pricePublish">
        <p v-if="kind === 'group_price.publish'" class="field-hint">组 {{ group?.label }} · {{ group?.id }}。每次追加不可变版本，同一模型在同一组的生效时刻唯一。</p>
        <p v-else class="field-hint">公共默认价适用于未分配价格组的租户。每次追加不可变版本，同一模型的默认价生效时刻唯一。</p>
        <el-form-item label="模型与已发布计量域"><el-select v-model="model" filterable :loading="modelLoading" placeholder="明确选择模型与原 schema"><el-option v-for="row in models" :key="row.id" :value="row.id" :label="`${row.public_name} · ${row.schema_id}`" /></el-select><el-button link :disabled="busy || modelLoading" @click="loadModels">刷新模型</el-button></el-form-item>
        <p v-if="selectedModel" class="field-hint">{{ selectedModel.schema_id }} · {{ selectedModel.schema_digest }} · default。{{ inputOnly ? '输入型模型：输出价固定为 0。' : '' }}</p>
        <div class="pricing-form-grid"><el-form-item label="输入价（CNY / 百万 Tokens）"><el-input v-model="input" inputmode="decimal" /></el-form-item><el-form-item label="输出价（CNY / 百万 Tokens）"><el-input v-model="output" :disabled="inputOnly" inputmode="decimal" /></el-form-item></div>
        <el-form-item label="明确生效时刻（UTC Z，最多六位微秒）"><el-input v-model="effective" placeholder="2026-11-01T00:00:00.000001Z" /><span class="field-hint">允许过去或未来时刻；新版本不重写已经冻结的请求。服务器仍按真实模型协议验证输出价。</span></el-form-item>
      </template>
      <template v-else>
        <dl class="detail-list"><dt>租户</dt><dd class="mono">{{ assignment?.tenant_id }}</dd><dt>当前价格域</dt><dd>{{ assignment?.price_group_id ?? '公共默认' }}</dd><dt>当前归属版本</dt><dd>{{ assignment?.pricing_revision }}</dd><dt>目标价格域</dt><dd>{{ targetGroup ?? '公共默认' }}</dd></dl>
        <el-alert type="warning" title="组缺价不会回落公共默认价，缺价模型的调用会拒绝。逐页预览仅是当时观察，授权、价格和组状态仍可能变化，不是分配准入令牌。" :closable="false" />
        <el-checkbox v-model="acceptedImpact" class="pricing-impact">我已逐页核查所需模型，明确接受缺价调用拒绝及预览后变化的影响</el-checkbox>
      </template>
    </el-form>
    <template #footer><el-button @click="close">关闭</el-button><el-button v-if="unknown" :loading="busy" @click="inspect">只读查询原回执</el-button><el-button v-else-if="!receipt" type="primary" :loading="busy" :disabled="!permitted || kind === 'group_price.publish' && group?.state !== 'active' || kind === 'tenant_pricing.assign' && !acceptedImpact" @click="submit">确认提交</el-button></template>
  </el-dialog>
</template>
<style scoped>.pricing-form-grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}.pricing-operation,.detail-list dd{overflow-wrap:anywhere}.pricing-impact{margin-top:16px;white-space:normal;height:auto;line-height:1.8}.el-select{width:100%}.code-block{font-size:11px;white-space:pre-wrap}@media(max-width:600px){.pricing-form-grid{grid-template-columns:1fr}}</style>
