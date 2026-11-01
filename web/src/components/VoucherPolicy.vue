<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ApiError, errorText, request } from '../api'
import { parseVoucherPolicy } from '../commerce'
import type { VoucherPolicy } from '../commerce'
import { commerceContext, commerceDeadline, createCommerceScope, unknownCommerceWrite } from '../commerce-state'
import { session } from '../session'
const emit = defineEmits<{ changed: [] }>()
const scope = createCommerceScope(), context = computed(() => commerceContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.csrf_token}`))
const view = ref<VoucherPolicy | null>(null), wanted = ref(false), loading = ref(false), saving = ref(false), error = ref(''), locked = ref(false), observed = ref(false)
const storageKey = () => `moni-voucher-policy-v1:${session.me?.user.id}`
function markPending() { sessionStorage.setItem(storageKey(), 'pending') }
function clearPending() { sessionStorage.removeItem(storageKey()) }
async function load() {
  const ticket = scope.ticket('read'); loading.value = true; error.value = ''; observed.value = false
  try {
    const response = await request<unknown>('/platform/voucher-policy', 'GET', undefined, undefined, () => scope.accepts(ticket))
    if (!scope.accepts(ticket)) return
    view.value = parseVoucherPolicy(response.data); wanted.value = view.value.enabled; observed.value = true
  } catch (cause) { if (scope.accepts(ticket)) error.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
async function save() {
  if (!view.value || saving.value || locked.value) return
  const before = view.value, enabled = wanted.value, ticket = scope.ticket('write')
  error.value = ''
  try {
    markPending(); saving.value = true; locked.value = true; observed.value = false
    const response = await commerceDeadline(request<unknown>('/platform/voucher-policy', 'PUT', { expected_revision: before.revision, enabled }, undefined, () => scope.accepts(ticket) && saving.value))
    if (!scope.accepts(ticket) || !saving.value) return
    const result = parseVoucherPolicy(response.data), expected = BigInt(before.revision) + (enabled === before.enabled ? 0n : 1n)
    if (result.enabled !== enabled || BigInt(result.revision) !== expected || result.runtime_allowed !== before.runtime_allowed) throw new Error('保存响应版本不一致，请只读核查。')
    clearPending(); view.value = result; wanted.value = result.enabled; locked.value = false; emit('changed')
  } catch (cause) {
    if (!scope.accepts(ticket)) return
    if (!unknownCommerceWrite(cause) && cause instanceof ApiError && cause.status !== 409) { try { clearPending(); locked.value = false } catch { locked.value = true } }
    error.value = locked.value ? '设置结果待核查。只能先读取当前策略；相同值不证明原请求已提交，不会自动再次提交。' : errorText(cause)
  } finally { if (scope.accepts(ticket)) saving.value = false }
}
function restart() {
  if (!observed.value || saving.value || !view.value) return
  try { clearPending(); locked.value = false; observed.value = false; error.value = ''; wanted.value = view.value.enabled }
  catch { error.value = '无法清除安全编辑状态，请恢复会话存储后再编辑。' }
}
function reset() {
  scope.reset(context.value); view.value = null; loading.value = false; saving.value = false; error.value = ''; observed.value = false
  try { locked.value = sessionStorage.getItem(storageKey()) !== null } catch { locked.value = true; error.value = '会话存储不可用，只能读取策略。' }
  if (session.me?.user.platform_role === 'admin') void load()
}
watch(context, reset, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => scope.reset())
</script>
<template>
  <section v-if="session.me?.user.platform_role === 'admin'" class="panel voucher-policy"><div class="panel-header"><div><div class="panel-title">兑换能力开关</div><p class="panel-subtitle">部署硬开关与数据库策略共同生效；关闭不撤回旧回执，也不阻止已有批次的审批/作废。</p></div><el-button :loading="loading" :disabled="saving" @click="load">只读刷新策略</el-button></div>
    <el-alert v-if="error" :title="error" type="warning" :closable="false" />
    <template v-if="view"><p>进程硬开关：{{ view.runtime_allowed ? '允许' : '关闭，需部署配置后重启' }} · 当前有效能力：{{ view.effective_enabled ? '已开启' : '关闭' }} · 策略版本 {{ view.revision }}</p><el-switch v-model="wanted" :disabled="saving || loading || locked || !view.runtime_allowed && !view.enabled" active-text="数据库策略开启" inactive-text="关闭" /><el-button :loading="saving" :disabled="loading || locked || !view.runtime_allowed && wanted" @click="save">保存策略</el-button><el-button v-if="locked && observed" :disabled="saving" @click="restart">复核当前版本后开始新编辑</el-button><p v-if="locked" class="field-hint">原编辑仍待核查。只读刷新不会自动解锁；新编辑按当前版本发起，不证明原请求的归因。</p></template>
  </section>
</template>
<style scoped>.voucher-policy{margin-top:24px;font-size:12px;line-height:1.8}.voucher-policy .el-button{margin-left:14px}</style>
