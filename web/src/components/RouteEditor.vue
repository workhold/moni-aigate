<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Delete, Plus, Refresh } from '@element-plus/icons-vue'
import { errorText, request } from '../api'
import { session, isAdmin } from '../session'
import { createKeyAccessScope, keyAccessContext } from '../key-access-policy'
import { acceptRouteWrite, parseCostChannel, parseRouteView, readConfigurationRows, routeDrafts, routeWrite, routeWriteJournal } from '../cost-aware-routing'
import type { BindingDraft, CostChannel, CostPolicy, RouteView, RouteWrite, WriteReview } from '../cost-aware-routing'

const props = defineProps<{ modelValue: boolean; model: Record<string, unknown> | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [] }>()
const context = computed(() => keyAccessContext(session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role))
const scope = createKeyAccessScope()
const view = ref<RouteView | null>(null), channels = ref<CostChannel[]>([]), drafts = ref<BindingDraft[]>([])
const policy = ref<CostPolicy>({ mode: 'manual', unknown_cost_policy: 'allow' })
const review = ref<WriteReview<RouteView, RouteWrite> | null>(null)
const error = ref(''), loading = ref(false), saving = ref(false), previousUnknown = ref(false)
const visible = computed({ get: () => props.modelValue, set: value => { if (!value) reset(); emit('update:modelValue', value) } })
const blocked = computed(() => !!review.value && review.value.mode !== 'saved')
const editable = computed(() => isAdmin.value && !!view.value && !blocked.value && !loading.value && !saving.value)
function reset() { scope.reset(context.value); view.value = null; channels.value = []; drafts.value = []; review.value = null; error.value = ''; loading.value = false; saving.value = false; previousUnknown.value = false }
function apply(current: RouteView) { view.value = current; drafts.value = routeDrafts(current); policy.value = { ...current.cost_policy } }
async function read(inspection = false) {
  if (!props.modelValue || !props.model || !isAdmin.value || saving.value || loading.value) return
  const id = String(props.model.id), actor = context.value, ticket = scope.ticket('read')
  const pending = routeWriteJournal.get(actor, id)
  review.value = pending?.mode === 'saved' ? null : pending
  if (pending?.mode === 'submitting') { error.value = '原提交仍在等待响应。可稍后刷新提交状态，不会再次发布。'; return }
  loading.value = true; error.value = ''
  try {
    const [models, allChannels] = await Promise.all([readConfigurationRows('/platform/models', () => scope.accepts(ticket)), readConfigurationRows('/platform/channels', () => scope.accepts(ticket))])
    const raw = models.find(row => row && typeof row === 'object' && (row as Record<string, unknown>).id === id)
    if (!raw) throw new Error('当前模型已不存在或无读取权限。')
    const current = parseRouteView(raw, id), choices = allChannels.map(row => parseCostChannel(row))
    if (!scope.accepts(ticket)) return
    channels.value = choices
    if (review.value && inspection) review.value = routeWriteJournal.observe(actor, id, current)
    else if (!review.value) apply(current)
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
function resume() {
  if (!view.value || loading.value || saving.value) return
  try {
    const next = routeWriteJournal.resume(context.value, view.value.id)
    previousUnknown.value = next.previousUnknown; review.value = null; apply(next.view); error.value = ''
  } catch (e) { error.value = errorText(e) }
}
async function save() {
  if (!editable.value || !view.value) return
  error.value = ''
  let operation: RouteWrite
  try { operation = routeWrite(view.value, policy.value, drafts.value) } catch (e) { error.value = errorText(e); return }
  const actor = context.value, id = view.value.id, ticket = scope.ticket('write')
  try { review.value = routeWriteJournal.begin(actor, id, view.value, operation, previousUnknown.value) } catch (e) { error.value = errorText(e); return }
  saving.value = true
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    acceptRouteWrite(operation, response.data); routeWriteJournal.saved(actor, id)
    if (!scope.accepts(ticket)) return
    visible.value = false; emit('saved'); ElMessage.success('完整路由及成本策略已发布。')
  } catch (e) {
    routeWriteJournal.fail(actor, id, e)
    if (scope.accepts(ticket)) { review.value = routeWriteJournal.get(actor, id); error.value = errorText(e) }
  } finally { if (scope.accepts(ticket)) saving.value = false }
}
function modeChanged() { if (policy.value.mode === 'cost_first') policy.value.unknown_cost_policy = 'deny' }
function add() { if (editable.value && drafts.value.length < 1000) drafts.value.push({ channel_id: '', upstream_model: '', priority: 0, weight: 1, max_inflight: 10, max_input_cost_rate: '', max_output_cost_rate: '' }) }
watch(() => [props.modelValue, props.model?.id], () => {
  reset()
  if (!props.modelValue || !props.model || !isAdmin.value) return
  const id = String(props.model.id), pending = routeWriteJournal.get(context.value, id)
  previousUnknown.value = routeWriteJournal.hadUnknown(context.value, id)
  if (pending && pending.mode !== 'saved') { review.value = pending; apply(pending.view) }
  else void read()
}, { immediate: true, flush: 'sync' })
watch(context, () => { reset(); emit('update:modelValue', false) }, { flush: 'sync' })
onBeforeUnmount(reset)
</script>

<template>
  <el-dialog v-model="visible" :title="`${view?.public_name || model?.public_name || ''} · 路由配置`" width="1060px" destroy-on-close :close-on-click-modal="false">
    <p class="dialog-description">先按授权、地域、健康、容量与单价上限过滤，保留最高优先级，再按策略分配。成本优先比较当前请求输入硬上界与输出上限对应的采购分数，同价仍按权重选择。</p>
    <el-alert title="此处控制登记采购单价，不代表供应商余额、累计采购预算或实际最低账单；客户售价独立。需要全渠道比价时，请把优先级设为相同值。" type="info" :closable="false" show-icon />
    <el-alert v-if="previousUnknown" title="本页曾有发布结果不明。只读结果不能证明原请求归因；以下新编辑独立发布，不会自动重放原命令。" type="warning" :closable="false" style="margin-top:14px" />
    <el-alert v-if="review?.mode === 'unknown'" title="原发布结果不明，已禁止重发。请先只读核查当前路由，再审核并显式开始新编辑。" type="warning" :closable="false" style="margin-top:14px" />
    <el-alert v-else-if="review?.mode === 'conflict'" title="路由版本冲突，原编辑已锁定。请只读核查当前完整配置，再重新编辑。" type="warning" :closable="false" style="margin-top:14px" />
    <el-alert v-else-if="review?.mode === 'submitting'" title="原提交正在等待响应，不能重复发布；关闭后仍可回来核查提交状态。" type="info" :closable="false" style="margin-top:14px" />
    <div v-if="blocked" class="route-check"><el-button :icon="Refresh" :disabled="saving || loading" @click="read(true)">{{ review?.mode === 'submitting' ? '刷新提交状态' : '只读核查当前路由' }}</el-button><el-button :disabled="!review?.observed || saving || loading" @click="resume">审核后开始新编辑</el-button><span v-if="review?.observed" class="muted">已读取路由版本 {{ review.observed.active_revision }}；原提交结果仍保留。</span></div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" style="margin:16px 0" />
    <div v-loading="loading">
      <el-form v-if="view" label-position="top" :disabled="!editable">
        <div class="route-policy"><el-form-item label="选择策略"><el-select v-model="policy.mode" aria-label="路由成本策略" @change="modeChanged"><el-option label="手动优先级 / 权重" value="manual" /><el-option label="最高优先级内成本优先" value="cost_first" /></el-select></el-form-item><el-form-item label="未知采购报价"><el-select v-model="policy.unknown_cost_policy" :disabled="!editable || policy.mode === 'cost_first'" aria-label="未知采购报价策略"><el-option label="允许（仅无单价上限的手动路由）" value="allow" /><el-option label="排除未知报价" value="deny" /></el-select></el-form-item></div>
        <article v-for="(binding, index) in drafts" :key="index" class="cost-binding">
          <div class="binding-heading"><strong>绑定 {{ index + 1 }}</strong><el-button link type="danger" :icon="Delete" :disabled="!editable" :aria-label="`移除绑定 ${index + 1}`" @click="drafts.splice(index, 1)">移除</el-button></div>
          <div class="binding-main"><el-form-item label="渠道"><el-select v-model="binding.channel_id" filterable aria-label="绑定渠道"><el-option v-for="channel in channels" :key="channel.id" :label="channel.name" :value="channel.id" :disabled="!channel.enabled" /></el-select></el-form-item><el-form-item label="精确上游模型名称"><el-input v-model="binding.upstream_model" aria-label="精确上游模型名称" maxlength="200" /></el-form-item></div>
          <div class="binding-numbers"><el-form-item label="优先级"><el-input-number v-model="binding.priority" :min="-2147483648" :max="2147483647" :precision="0" :controls="false" aria-label="路由优先级" /></el-form-item><el-form-item label="权重"><el-input-number v-model="binding.weight" :min="1" :max="1000000" :precision="0" :controls="false" aria-label="路由权重" /></el-form-item><el-form-item label="渠道并发"><el-input-number v-model="binding.max_inflight" :min="1" :max="1000" :precision="0" :controls="false" aria-label="渠道并发" /></el-form-item></div>
          <div class="binding-main"><el-form-item label="输入采购单价上限（CNY / 百万 Tokens）"><el-input v-model="binding.max_input_cost_rate" aria-label="输入采购单价上限" placeholder="留空不限；0 要求已知免费" inputmode="decimal" maxlength="32" /></el-form-item><el-form-item label="输出采购单价上限（CNY / 百万 Tokens）"><el-input v-model="binding.max_output_cost_rate" aria-label="输出采购单价上限" placeholder="留空不限；0 要求已知免费" inputmode="decimal" maxlength="32" /></el-form-item></div>
        </article>
        <el-button :icon="Plus" :disabled="!editable || drafts.length >= 1000" @click="add">增加绑定</el-button>
        <p class="field-hint">当前路由版本 {{ view.active_revision }} · 两维上限留空表示不限；任一上限或排除未知策略均要求两维已知报价。仅输入计费渠道建议显式登记输出采购价 0。保存将完整替换策略与全部绑定。</p>
      </el-form>
      <div v-if="review?.observed" class="observed-route"><strong>只读核查 · 版本 {{ review.observed.active_revision }}</strong><p>{{ review.observed.cost_policy.mode === 'cost_first' ? '成本优先' : '手动权重' }} / 未知报价{{ review.observed.cost_policy.unknown_cost_policy === 'deny' ? '排除' : '允许' }} · {{ review.observed.bindings.length }} 个绑定</p><pre class="code-block">{{ JSON.stringify(review.observed.bindings, null, 2) }}</pre></div>
      <el-button v-if="!view && !loading" :icon="Refresh" @click="read()">重新读取路由</el-button>
    </div>
    <template #footer><el-button @click="visible = false">关闭</el-button><el-button type="primary" :loading="saving" :disabled="!editable" @click="save">校验并发布完整路由</el-button></template>
  </el-dialog>
</template>

<style scoped>
.route-policy,.binding-main{display:grid;grid-template-columns:1fr 1fr;gap:18px}.route-policy{margin-top:20px}.binding-numbers{display:grid;grid-template-columns:repeat(3,1fr);gap:18px}.cost-binding{border:1px solid #e3eaf0;border-radius:8px;padding:18px;margin-bottom:16px}.binding-heading{display:flex;align-items:center;justify-content:space-between;margin-bottom:14px}.binding-numbers .el-input-number,.route-policy .el-select,.binding-main .el-select{width:100%}.route-check{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin:16px 0}.route-check .el-button{margin:0}.observed-route{margin-top:20px;overflow-wrap:anywhere}.observed-route pre{max-height:260px;overflow:auto}@media(max-width:700px){.route-policy,.binding-main,.binding-numbers{grid-template-columns:1fr;gap:0}.cost-binding{padding:14px}}
</style>
