<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, newOperationKey } from '../api'
import { session } from '../session'
import { parseGroupPriceCompile, parsePricingEnvelope, parsePricingReceipt, parsePricingModelChoice, pricingCommand, pricingRequest } from '../pricing'
import type { GroupPriceCompile, PriceGroup, PricingModelChoice, PricingReceipt } from '../pricing'
import { readConfigurationRows } from '../cost-aware-routing'

const props = defineProps<{ group: PriceGroup }>()
const emit = defineEmits<{ changed: [] }>()
const choices = ref<PricingModelChoice[]>([]), selected = ref<string[]>([]), multiplier = ref('1'), effective = ref(defaultEffective())
const preview = ref<GroupPriceCompile | null>(null), receipt = ref<PricingReceipt | null>(null), loading = ref(false), loadingModels = ref(false), error = ref('')
const canWrite = computed(() => session.me?.user.platform_role === 'admin' && !session.mfaPending && props.group.state === 'active')
const selectedModels = computed(() => new Set(selected.value))
const publishedCount = computed(() => receipt.value ? (receipt.value.result as GroupPriceCompile).items.length : 0)

function defaultEffective() {
  const now = new Date(), iso = now.toISOString().replace('Z', '')
  return `${iso.slice(0, 19)}.${(now.getMilliseconds() * 1000).toString().padStart(6, '0')}Z`
}
function reset() { selected.value = []; multiplier.value = '1'; effective.value = defaultEffective(); preview.value = null; receipt.value = null; error.value = '' }
function requestBody(expected: Record<string, string> = {}) { return { operation_id: newOperationKey(), multiplier: multiplier.value, model_ids: [...selected.value], effective_at: effective.value, expected_base_version_ids: expected } }
async function loadModels() {
  loadingModels.value = true; error.value = ''
  try {
    const rows = await readConfigurationRows('/platform/models', () => true)
    choices.value = rows.map(parsePricingModelChoice)
  } catch (cause) { error.value = errorText(cause) }
  finally { loadingModels.value = false }
}
async function compilePreview() {
  if (!selected.value.length) { error.value = '至少选择一个模型。'; return }
  loading.value = true; error.value = ''; receipt.value = null
  try {
    const body = requestBody(), raw = await pricingRequest(`/platform/price-groups/${props.group.id}/price-versions/compile-preview`, 'POST', body)
    preview.value = parsePricingEnvelope(raw, data => parseGroupPriceCompile(data, props.group.id))
  } catch (cause) { error.value = errorText(cause); preview.value = null }
  finally { loading.value = false }
}
async function publish() {
  if (!preview.value) { await compilePreview(); if (!preview.value) return }
  loading.value = true; error.value = ''
  try {
    const expected = Object.fromEntries(preview.value.items.map(item => [item.model_id, item.base_price_version_id]))
    const body = requestBody(expected), command = await pricingCommand('group_price.compile', body.operation_id, session.me!.user.id, { multiplier: body.multiplier, model_ids: body.model_ids, effective_at: body.effective_at, expected_base_version_ids: expected }, props.group.id)
    const raw = await pricingRequest(command.path, command.method, command.body), value = parsePricingEnvelope(raw, data => parsePricingReceipt(data, command))
    receipt.value = value; emit('changed')
  } catch (cause) { error.value = errorText(cause) }
  finally { loading.value = false }
}
watch(() => props.group.id, reset)
watch([selected, multiplier, effective], () => { preview.value = null; receipt.value = null }, { deep: true })
loadModels()
onBeforeUnmount(reset)
</script>

<template>
  <section v-if="canWrite" class="panel multiplier-compiler">
    <div class="panel-header"><div><div class="panel-title">分组倍率编译</div><div class="panel-subtitle">读取当前公共默认价，按精确倍率向上取整后批量发布为组内绝对售价。默认价变化后不会自动跟随。</div></div><el-button link :loading="loadingModels" @click="loadModels">刷新模型</el-button></div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <el-form label-position="top" :disabled="loading">
      <div class="compiler-grid"><el-form-item label="倍率（0 < m ≤ 100，最多 6 位小数）"><el-input v-model="multiplier" inputmode="decimal" placeholder="例如 0.85" /></el-form-item><el-form-item label="统一生效时刻（UTC，最多六位微秒）"><el-input v-model="effective" placeholder="2026-12-01T00:00:00.000000Z" /></el-form-item></div>
      <el-form-item label="模型（最多 100 个，重复项会去重）"><el-select v-model="selected" multiple filterable collapse-tags :loading="loadingModels" placeholder="选择要编译的模型"><el-option v-for="model in choices" :key="model.id" :label="`${model.public_name} · ${model.id}`" :value="model.id" /></el-select></el-form-item>
    </el-form>
    <div class="compiler-actions"><el-button :loading="loading" :disabled="!selectedModels.size" @click="compilePreview">只预览计算结果</el-button><el-button type="primary" :loading="loading" :disabled="!selectedModels.size" @click="publish">预览后发布整批</el-button></div>
    <el-alert v-if="preview" class="compiler-result" type="info" :closable="false" :title="`预览完成：${preview.items.length} 个模型；基准版本只在本次发布时用 CAS 复核。`" />
    <el-table v-if="preview" :data="preview.items" size="small"><el-table-column prop="model_id" label="模型 ID" min-width="250" /><el-table-column label="默认输入 / 输出"><template #default="{ row }">{{ row.base_input_rate }} / {{ row.base_output_rate }}</template></el-table-column><el-table-column label="组价输入 / 输出"><template #default="{ row }"><strong>{{ row.input_rate }} / {{ row.output_rate }}</strong></template></el-table-column><el-table-column prop="base_price_version_id" label="基准版本" min-width="250" /></el-table>
    <el-alert v-if="receipt" class="compiler-result" type="success" :closable="false" :title="`已发布 ${publishedCount} 个不可变组价版本；操作 ${receipt.operation.operation_id}`" />
  </section>
</template>

<style scoped>.multiplier-compiler{margin-top:22px}.compiler-grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}.compiler-grid .el-input,.multiplier-compiler .el-select{width:100%}.compiler-actions{display:flex;gap:10px;flex-wrap:wrap;margin:4px 0 18px}.compiler-result{margin:14px 0}.multiplier-compiler strong{color:#167f78}@media(max-width:650px){.compiler-grid{grid-template-columns:1fr}}</style>
