<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { errorText, request } from '../api'
import { commerceReasonLabels, commerceUUID, parseCommerceReceipt } from '../commerce'
import type { CommerceReceipt } from '../commerce'
import { commerceContext, commerceJournal, createCommerceScope } from '../commerce-state'
import type { SafeCommerceCommand } from '../commerce-state'
import { coin } from '../format'
import { session } from '../session'
const props = defineProps<{ scope: string }>(), emit = defineEmits<{ settled: [receipt: CommerceReceipt] }>()
const scope = createCommerceScope(), context = computed(() => commerceContext(session.me?.user.id, props.scope, `${session.me?.user.platform_role}:${session.me?.tenant?.role}:${session.me?.csrf_token}`))
const pending = ref<SafeCommerceCommand[]>([]), id = ref(''), busy = ref(false), error = ref(''), result = ref<CommerceReceipt | null>(null)
function load() { try { pending.value = session.me ? commerceJournal.list(session.me.user.id, props.scope) : [] } catch (cause) { pending.value = []; error.value = errorText(cause) } }
function reset() { scope.reset(context.value); pending.value = []; id.value = ''; busy.value = false; error.value = ''; result.value = null; load() }
function change() { scope.invalidate('lookup'); busy.value = false; error.value = ''; result.value = null }
async function inspect(original?: SafeCommerceCommand) {
  if (!session.me || busy.value) return
  const ticket = scope.ticket('lookup'), actor = session.me.user.id, expectedScope = props.scope
  busy.value = true; error.value = ''; result.value = null
  try {
    const operation = commerceUUID(original?.operation_id ?? id.value), base = expectedScope === 'platform' ? '/platform' : '/wallet'
    id.value = operation
    const response = await request<unknown>(`${base}/commerce-operations/${operation}`, 'GET', undefined, undefined, () => scope.accepts(ticket))
    if (!scope.accepts(ticket)) return
    const saved = original ?? pending.value.find(entry => entry.operation_id === operation)
    result.value = saved ? commerceJournal.settle(actor, saved, response.data) : parseCommerceReceipt(response.data, { operation_id: operation, actor, scope: expectedScope })
    load(); emit('settled', result.value)
  } catch (cause) { if (scope.accepts(ticket)) error.value = `只读查询未确认。404、刷新、切换页签均不会解除原操作保护。${errorText(cause)}` }
  finally { if (scope.accepts(ticket)) busy.value = false }
}
watch(context, reset, { immediate: true, flush: 'sync' })
onMounted(() => window.addEventListener('moni:commerce-journal', load))
onBeforeUnmount(() => { scope.reset(); window.removeEventListener('moni:commerce-journal', load) })
</script>
<template>
  <section class="panel commerce-commands"><div class="panel-title">操作回执核查</div><p class="panel-subtitle">待核查记录只保存操作 ID、种类、作用域、资源 ID 与时间；不保存券码、请求正文或响应。未查到回执不能重复提交。</p>
    <el-alert v-if="error" :title="error" type="warning" :closable="false" />
    <div class="commerce-query"><el-input v-model="id" placeholder="原 operation UUID" aria-label="原操作 ID" @input="change" /><el-button :loading="busy" :disabled="!id" @click="inspect()">只读查询</el-button></div>
    <div v-for="entry in pending" :key="entry.operation_id" class="commerce-pending"><div><strong>结果待核查</strong><span class="mono">{{ entry.operation_id }}</span><small>{{ entry.kind }} · {{ entry.created_at }}</small></div><el-button :loading="busy" @click="inspect(entry)">核查原回执</el-button></div>
    <template v-if="result"><el-alert :type="result.status === 'accepted' ? 'success' : 'warning'" :title="commerceReasonLabels[result.reason] || result.reason" :closable="false" /><dl class="commerce-result"><dt>原操作</dt><dd class="mono">{{ result.operation_id }}</dd><dt>操作类型</dt><dd>{{ result.kind }}</dd><dt>资源</dt><dd class="mono">{{ result.resource_id || '无业务效果' }}</dd><template v-if="result.amount"><dt>已入账</dt><dd>{{ coin(result.amount) }}</dd><dt>来源 / 流水</dt><dd class="mono">{{ result.funding_receipt_id }} / {{ result.journal_id }}</dd></template><dt>提交时刻（UTC）</dt><dd>{{ result.created_at }}</dd></dl><p v-if="result.kind === 'voucher.batch.create' && result.status === 'accepted'" class="field-hint">查询不会恢复兑换券。丢失明文应按原资源作废/双审撤销后再明确新建。</p></template>
  </section>
</template>
<style scoped>.commerce-commands{margin-top:24px}.commerce-query{display:flex;gap:12px;max-width:650px;margin:16px 0}.commerce-pending{display:flex;justify-content:space-between;gap:20px;padding:12px 0;border-top:1px solid var(--border-color,#e8edf1)}.commerce-pending>div{display:grid;gap:5px;overflow-wrap:anywhere;font-size:12px}.commerce-pending small{color:var(--text-secondary)}.commerce-result{display:grid;grid-template-columns:110px minmax(0,1fr);gap:10px;font-size:12px}.commerce-result dd{margin:0;overflow-wrap:anywhere}</style>
