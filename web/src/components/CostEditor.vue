<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { errorText, request } from '../api'
import { isAdmin, session } from '../session'
import { money, time } from '../format'
import { createKeyAccessScope, keyAccessContext } from '../key-access-policy'
import { acceptCostWrite, costRateToCny, costWrite, costWriteJournal, parseCostChannel, parseCostVersion, readConfigurationRows } from '../cost-aware-routing'
import type { CostChannel, CostDraft, CostVersion, CostWrite, WriteReview } from '../cost-aware-routing'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [] }>()
const context = computed(() => keyAccessContext(session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role))
const scope = createKeyAccessScope()
const channels = ref<CostChannel[]>([]), selectedID = ref(''), channel = ref<CostChannel | null>(null)
const blank = (): CostDraft => ({ upstream_model: '', basis: 'unknown', input_rate: '', output_rate: '', effective_at: '' })
const draft = ref<CostDraft>(blank()), review = ref<WriteReview<CostChannel, CostWrite> | null>(null), observed = ref<CostVersion[]>([])
const loading = ref(false), saving = ref(false), error = ref(''), previousUnknown = ref(false)
const visible = computed({ get: () => props.modelValue, set: value => { if (!value) reset(); emit('update:modelValue', value) } })
const blocked = computed(() => !!review.value && review.value.mode !== 'saved')
const editable = computed(() => isAdmin.value && !!channel.value && !blocked.value && !loading.value && !saving.value)
function reset() { scope.reset(context.value); channels.value = []; selectedID.value = ''; channel.value = null; draft.value = blank(); review.value = null; observed.value = []; loading.value = false; saving.value = false; error.value = ''; previousUnknown.value = false }
async function loadChannels() {
  if (!props.modelValue || !isAdmin.value || loading.value || saving.value) return
  const ticket = scope.ticket('read'); loading.value = true; error.value = ''
  try {
    const rows = (await readConfigurationRows('/platform/channels', () => scope.accepts(ticket))).map(row => parseCostChannel(row))
    if (scope.accepts(ticket)) channels.value = rows
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
function selectChannel() {
  if (saving.value || loading.value) return
  scope.invalidate('read'); error.value = ''; observed.value = []; draft.value = blank()
  const selected = channels.value.find(row => row.id === selectedID.value)
  channel.value = selected ? { ...selected } : null; review.value = null; previousUnknown.value = false
  if (!selected) return
  previousUnknown.value = costWriteJournal.hadUnknown(context.value, selected.id)
  const pending = costWriteJournal.get(context.value, selected.id)
  if (pending && pending.mode !== 'saved') {
    review.value = pending; channel.value = pending.view
    const original = pending.submitted.body
    draft.value = { upstream_model: original.upstream_model, basis: original.basis, input_rate: costRateToCny(original.input_rate), output_rate: costRateToCny(original.output_rate), effective_at: '' }
  }
}
async function inspect() {
  if (!channel.value || !props.modelValue || !isAdmin.value || loading.value || saving.value) return
  const id = channel.value.id, actor = context.value, current = costWriteJournal.get(actor, id)
  review.value = current?.mode === 'saved' ? null : current
  if (current?.mode === 'submitting') { error.value = '原提交仍在等待响应。可稍后刷新提交状态，不会再次发布。'; return }
  const model = current?.submitted.body.upstream_model ?? draft.value.upstream_model.trim(), ticket = scope.ticket('read')
  loading.value = true; error.value = ''
  try {
    const [channelRows, costRows] = await Promise.all([readConfigurationRows('/platform/channels', () => scope.accepts(ticket)), readConfigurationRows('/platform/cost-versions', () => scope.accepts(ticket))])
    const choices = channelRows.map(row => parseCostChannel(row)), latest = choices.find(row => row.id === id)
    if (!latest) throw new Error('当前渠道已不存在或无读取权限。')
    const versions = costRows.map(parseCostVersion).filter(row => row.channel_id === id && (!model || row.upstream_model === model))
    if (!scope.accepts(ticket)) return
    channels.value = choices; observed.value = versions
    if (review.value) review.value = costWriteJournal.observe(actor, id, latest)
    else channel.value = latest
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
function resume() {
  if (!channel.value || loading.value || saving.value) return
  try {
    const next = costWriteJournal.resume(context.value, channel.value.id)
    channel.value = next.view; previousUnknown.value = next.previousUnknown; review.value = null; error.value = ''; draft.value = blank()
  } catch (e) { error.value = errorText(e) }
}
async function save() {
  if (!editable.value || !channel.value) return
  let operation: CostWrite
  error.value = ''
  try { operation = costWrite(channel.value, draft.value) } catch (e) { error.value = errorText(e); return }
  const actor = context.value, id = channel.value.id, ticket = scope.ticket('write')
  try { review.value = costWriteJournal.begin(actor, id, channel.value, operation, previousUnknown.value) } catch (e) { error.value = errorText(e); return }
  saving.value = true
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    acceptCostWrite(operation, response.data); costWriteJournal.saved(actor, id)
    if (!scope.accepts(ticket)) return
    visible.value = false; emit('saved'); ElMessage.success('成本版本已追加保存；已有尝试保留原报价。')
  } catch (e) {
    costWriteJournal.fail(actor, id, e)
    if (scope.accepts(ticket)) { review.value = costWriteJournal.get(actor, id); error.value = errorText(e) }
  } finally { if (scope.accepts(ticket)) saving.value = false }
}
watch(() => props.modelValue, () => { reset(); if (props.modelValue && isAdmin.value) void loadChannels() }, { immediate: true, flush: 'sync' })
watch(context, () => { reset(); emit('update:modelValue', false) }, { flush: 'sync' })
onBeforeUnmount(reset)
</script>

<template>
  <el-dialog v-model="visible" title="新增渠道模型采购成本版本" width="780px" destroy-on-close :close-on-click-modal="false">
    <p class="dialog-description">按渠道与精确上游模型追加独立采购报价。客户售价保持独立；未知成本保存为空，不代表免费。历史attempt不追溯改价，账单差异请另追加成本更正。</p>
    <el-alert title="发布会校验并增加渠道管理版本，与渠道配置及其他成本发布互斥。结果不明时先只读核查，不能直接重发。" type="info" :closable="false" show-icon />
    <el-alert v-if="previousUnknown" title="此渠道曾有发布结果不明。读回相同报价不能证明原请求归因；新编辑不会重放原命令。" type="warning" :closable="false" style="margin-top:14px" />
    <el-alert v-if="review?.mode === 'unknown'" title="原发布结果不明，已锁定重发。请只读读取渠道版本与报价，再审核并显式开始新编辑。" type="warning" :closable="false" style="margin-top:14px" />
    <el-alert v-else-if="review?.mode === 'conflict'" title="渠道管理版本冲突。请先只读核查渠道与当前报价，再重新编辑。" type="warning" :closable="false" style="margin-top:14px" />
    <el-alert v-else-if="review?.mode === 'submitting'" title="原提交等待响应中，不能重复发布；关闭后仍可回来刷新提交状态。" type="info" :closable="false" style="margin-top:14px" />
    <el-alert v-if="error" :title="error" type="error" :closable="false" style="margin-top:16px" />
    <el-form label-position="top" style="margin-top:20px" @submit.prevent="save">
      <el-form-item label="采购渠道" required><el-select v-model="selectedID" filterable :disabled="loading || saving" aria-label="采购渠道" style="width:100%" placeholder="选择并冻结渠道管理版本" @change="selectChannel"><el-option v-for="row in channels" :key="row.id" :label="row.name" :value="row.id" /></el-select></el-form-item>
      <template v-if="channel">
        <p class="field-hint">本次渠道管理 revision：{{ channel.management_revision }}</p>
        <el-form-item label="精确上游模型名称" required><el-input v-model="draft.upstream_model" :disabled="!editable" maxlength="200" aria-label="采购上游模型名称" /></el-form-item>
        <el-form-item label="成本依据" required><el-select v-model="draft.basis" :disabled="!editable" aria-label="成本依据" style="width:100%"><el-option label="成本未知" value="unknown" /><el-option label="供应商合同" value="contract" /><el-option label="供应商账单" value="invoice" /></el-select></el-form-item>
        <div class="cost-rates"><el-form-item label="输入成本（CNY / 百万 Tokens）" :required="draft.basis !== 'unknown'"><el-input v-model="draft.input_rate" :disabled="!editable || draft.basis === 'unknown'" :placeholder="draft.basis === 'unknown' ? '成本未知' : '免费请明确填 0'" inputmode="decimal" maxlength="32" aria-label="输入采购单价" /></el-form-item><el-form-item label="输出成本（CNY / 百万 Tokens）" :required="draft.basis !== 'unknown'"><el-input v-model="draft.output_rate" :disabled="!editable || draft.basis === 'unknown'" :placeholder="draft.basis === 'unknown' ? '成本未知' : '免费请明确填 0'" inputmode="decimal" maxlength="32" aria-label="输出采购单价" /></el-form-item></div>
        <el-form-item label="生效时间"><el-input v-model="draft.effective_at" type="datetime-local" step="1" :disabled="!editable" aria-label="采购报价生效时间" /><p class="field-hint">留空按数据库锁后时间立即生效。可登记历史合同或未来调价；仅影响后续尝试，同渠道、模型与生效时刻不能重复。</p></el-form-item>
      </template>
    </el-form>
    <div class="cost-check"><el-button v-if="!channel" :icon="Refresh" :loading="loading" :disabled="saving" @click="loadChannels">重新读取渠道</el-button><el-button v-else :icon="Refresh" :loading="loading" :disabled="saving" @click="inspect">{{ review?.mode === 'submitting' ? '刷新提交状态' : '只读核查渠道与报价' }}</el-button><el-button v-if="blocked" :disabled="!review?.observed || loading || saving" @click="resume">审核后开始新编辑</el-button></div>
    <template v-if="review?.observed"><p class="field-hint">已读取当前渠道 revision {{ review.observed.management_revision }}。原提交结果仍保留；核查不会发布。</p><pre class="code-block cost-command">{{ JSON.stringify(review.submitted.body, null, 2) }}</pre></template>
    <el-table v-if="observed.length" :data="observed.slice(0, 20)" style="margin-top:18px"><el-table-column prop="upstream_model" label="上游模型" min-width="140" /><el-table-column label="依据" width="90"><template #default="{ row }">{{ row.basis === 'unknown' ? '未知' : row.basis === 'invoice' ? '账单' : '合同' }}</template></el-table-column><el-table-column label="输入 / 百万" min-width="140"><template #default="{ row }">{{ money(row.input_rate) }}</template></el-table-column><el-table-column label="输出 / 百万" min-width="140"><template #default="{ row }">{{ money(row.output_rate) }}</template></el-table-column><el-table-column label="生效时间" min-width="170"><template #default="{ row }">{{ time(row.effective_at) }}</template></el-table-column></el-table>
    <p v-if="observed.length > 20" class="field-hint">已读取 {{ observed.length }} 条匹配版本，以上展示最近登记的 20 条。</p>
    <template #footer><el-button @click="visible = false">关闭</el-button><el-button type="primary" :loading="saving" :disabled="!editable" @click="save">校验并追加成本版本</el-button></template>
  </el-dialog>
</template>

<style scoped>
.cost-rates{display:grid;grid-template-columns:1fr 1fr;gap:18px}.cost-check{display:flex;gap:10px;flex-wrap:wrap;margin-top:18px}.cost-check .el-button{margin:0}.cost-command{max-height:220px;overflow:auto;margin-top:12px}@media(max-width:700px){.cost-rates{grid-template-columns:1fr;gap:0}}
</style>
