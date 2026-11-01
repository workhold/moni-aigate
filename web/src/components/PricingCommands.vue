<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText } from '../api'
import { session } from '../session'
import { parsePricingEnvelope, parsePricingReceipt, pricingID, pricingRequest } from '../pricing'
import type { PricingReceipt } from '../pricing'
import { createPricingEpoch, pricingContext, pricingJournal } from '../pricing-state'
import type { PricingReview } from '../pricing-state'
const props = defineProps<{ refresh: number }>(), emit = defineEmits<{ changed: [] }>()
const context = computed(() => pricingContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}`, session.me?.csrf_token || '', session.mfaPending)), gate = createPricingEpoch()
const pending = ref<PricingReview[]>([]), operation = ref(''), busy = ref(false), error = ref(''), receipt = ref<PricingReceipt | null>(null)
function reload() { try { pending.value = session.me ? pricingJournal.list(session.me.user.id).filter(row => ['unknown','submitting'].includes(row.state)) : [] } catch (cause) { error.value = errorText(cause) } }
function reset() { gate.reset(context.value); operation.value = ''; error.value = ''; busy.value = false; receipt.value = null; pending.value = []; reload() }
async function inspect(id = operation.value) {
  if (busy.value) return
  const ticket = gate.ticket(); busy.value = true; error.value = ''; receipt.value = null
  try {
    const original = pending.value.find(row => row.command.operation_id === id), actor = session.me?.user.id
    const raw = await pricingRequest(`/platform/pricing-operations/${pricingID(id)}`, 'GET', undefined, () => gate.accepts(ticket))
    if (!gate.accepts(ticket)) return
    const result = parsePricingEnvelope(raw, data => parsePricingReceipt(data, original?.command))
    if (original && actor) pricingJournal.confirm(actor, id, result)
    receipt.value = result; operation.value = id; reload(); emit('changed')
  } catch (cause) { if (gate.accepts(ticket)) error.value = `只读查询未确认；404不证明旧提交失败，也不会解除待核查目标。${errorText(cause)}` }
  finally { if (gate.accepts(ticket)) busy.value = false }
}
watch(context, reset, { immediate: true, flush: 'sync' })
watch(() => props.refresh, reload)
onBeforeUnmount(() => gate.dispose())
</script>
<template>
  <section class="panel pricing-commands"><div class="panel-header"><div><div class="panel-title">原操作核查</div><div class="panel-subtitle">待核查命令刷新后仍保留安全标识；只查询原回执，不能换 ID 重发。原回执描述当时结果。</div></div></div>
    <div class="filters"><el-input v-model="operation" placeholder="原操作 UUID" aria-label="售价操作 ID" /><el-button :loading="busy" @click="inspect()">只读查询</el-button></div>
    <el-alert v-if="error" :title="error" type="warning" :closable="false" show-icon />
    <el-table v-if="pending.length" :data="pending"><el-table-column prop="command.operation_id" label="待核查操作" min-width="290" /><el-table-column prop="command.kind" label="动作" min-width="180" /><el-table-column prop="command.target" label="原目标" min-width="250" /><el-table-column width="100"><template #default="{ row }"><el-button link :disabled="busy" @click="inspect(row.command.operation_id)">核查</el-button></template></el-table-column></el-table>
    <div v-if="receipt" class="pricing-receipt"><p>已核对 {{ receipt.operation.kind }} · 原操作人 {{ receipt.operation.actor }} · 原版本 {{ receipt.operation.revision ?? '无配置 revision' }}</p><pre class="code-block">{{ JSON.stringify(receipt.result, null, 2) }}</pre></div>
  </section>
</template>
<style scoped>.pricing-commands{margin-top:22px}.pricing-receipt{padding:16px;font-size:12px;overflow-wrap:anywhere}.code-block{font-size:11px;white-space:pre-wrap}.filters .el-input{max-width:440px}</style>
