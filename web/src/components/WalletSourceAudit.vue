<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { CircleCheck } from '@element-plus/icons-vue'
import ErrorNotice from './ErrorNotice.vue'
import { commerceContext } from '../commerce-state'
import { integer } from '../format'
import { session } from '../session'
import { createWalletSourceAuditReader } from '../wallet-source-details'

const props = defineProps<{ tenantID: string }>()
const route = useRoute(), reader = createWalletSourceAuditReader(), state = ref(reader.snapshot())
const allowed = computed(() => !!session.me && !session.mfaPending && !session.enrollmentRequired && ['admin', 'finance_operator', 'finance_approver'].includes(session.me.user.platform_role))
const context = computed(() => commerceContext(session.me?.user.id, props.tenantID, JSON.stringify([route.fullPath, session.me?.user.platform_role, session.me?.tenant?.id, session.me?.tenant?.role, session.me?.csrf_token, session.mfaPending, session.enrollmentRequired])))
const counts = computed(() => state.value.result ? [
  { label: '来源与入账差异', count: state.value.result.source_mismatches }, { label: '事件与账本差异', count: state.value.result.event_mismatches },
  { label: '预占、结算与退款分配差异', count: state.value.result.allocation_mismatches }, { label: '钱包与来源余额差异', count: state.value.result.wallet_mismatches },
] : [])
function sync() { state.value = reader.snapshot() }
async function load() { if (!allowed.value || !props.tenantID) return; const pending = reader.load(props.tenantID); sync(); await pending; sync() }
watch(context, () => { reader.reset(context.value); sync() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { reader.dispose(); sync() })
</script>

<template>
  <section class="panel wallet-source-audit" aria-label="钱包资金来源只读核对">
    <div class="panel-header"><div><h2 class="panel-title">来源与钱包核对</h2><p class="panel-subtitle">在同一只读快照中检查来源、资金事件、消费分配与钱包余额。</p></div><el-button v-if="allowed && tenantID" :icon="CircleCheck" :loading="state.loading" @click="load">执行只读核对</el-button></div>
    <el-alert v-if="!allowed" title="核对仅向平台管理员、财务制单和财务审批角色开放。" type="info" :closable="false" />
    <div v-else class="audit-body">
      <ErrorNotice :message="state.error" @retry="load" />
      <div v-if="state.loading" role="status" aria-live="polite">正在核对当前客户资金来源…</div>
      <template v-else-if="state.result">
        <el-alert :title="state.result.balanced ? '本次快照核对平衡' : '本次快照发现差异，请按原资金记录调查'" :type="state.result.balanced ? 'success' : 'error'" :closable="false" />
        <dl class="audit-counts"><template v-for="item in counts" :key="item.label"><dt>{{ item.label }}</dt><dd class="mono">{{ integer(item.count) }}</dd></template></dl>
        <p class="muted small-text">快照时间（UTC）：<span class="mono">{{ state.result.as_of }}</span></p>
      </template>
      <p v-else class="muted">尚未核对。选择客户后点击“执行只读核对”获取结果。</p>
      <p class="muted small-text audit-note">核对只报告差异，不自动修复余额。同一异常可能出现在多个计数中，差异数不等于资金损失笔数；该结果不代表真实供应商账单或支付验收。</p>
    </div>
  </section>
</template>

<style scoped>
.wallet-source-audit{margin-top:24px}.wallet-source-audit h2{margin:0}.audit-body{padding:20px 22px}.audit-counts{display:grid;grid-template-columns:minmax(160px,1fr) auto;gap:13px 24px;max-width:620px;margin:22px 0}.audit-counts dt{color:var(--text-secondary);font-size:13px}.audit-counts dd{margin:0;font-variant-numeric:tabular-nums}.audit-note{line-height:1.8;margin-bottom:0}@media(max-width:640px){.wallet-source-audit .panel-header{gap:12px;flex-wrap:wrap}.audit-body{padding:18px 14px}}
</style>
