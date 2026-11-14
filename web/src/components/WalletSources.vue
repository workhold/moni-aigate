<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import ErrorNotice from './ErrorNotice.vue'
import { commerceContext } from '../commerce-state'
import { coin, shortId } from '../format'
import { session } from '../session'
import { canReadWalletSources, createWalletSourcesReader, walletRefundLabel, walletSourceLabel } from '../wallet-sources'

const props = withDefaults(defineProps<{ platform?: boolean; tenantID?: string }>(), { platform: false, tenantID: '' })
const route = useRoute(), reader = createWalletSourcesReader(), state = ref(reader.snapshot())
const tenantID = computed(() => props.platform ? props.tenantID : session.me?.tenant?.id || '')
const allowed = computed(() => canReadWalletSources({ platform: props.platform, tenantID: tenantID.value, authenticated: !!session.me, blocked: session.mfaPending || session.enrollmentRequired, tenantRole: session.me?.tenant?.role, platformRole: session.me?.user.platform_role }))
const context = computed(() => commerceContext(session.me?.user.id, tenantID.value, JSON.stringify([route.fullPath, props.platform, session.me?.user.platform_role, session.me?.tenant?.role, session.me?.csrf_token, session.mfaPending, session.enrollmentRequired])))
function sync() { state.value = reader.snapshot() }
async function load(navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
  if (!allowed.value || !tenantID.value) return
  const pending = reader.load({ platform: props.platform, tenantID: tenantID.value }, navigation)
  sync()
  await pending
  sync()
}
function refresh() { return load() }
watch(context, () => { reader.reset(context.value); sync(); if (allowed.value && tenantID.value) void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { reader.dispose(); sync() })
defineExpose({ refresh })
</script>

<template>
  <section class="panel wallet-sources" aria-label="钱包资金来源余额">
    <div class="panel-header"><div><h2 class="panel-title">资金来源余额</h2><p class="panel-subtitle">查看每笔入账的本金及可用、冻结、已消费金额；新的预占按来源时间顺序分配。</p></div><el-button v-if="allowed && tenantID" :icon="Refresh" :loading="state.loading" @click="refresh">刷新余额来源</el-button></div>
    <el-alert v-if="!allowed" :title="platform ? '资金来源仅向平台管理员、财务制单和财务审批角色开放。' : '资金来源仅向租户所有者和账单查看者开放。'" type="info" :closable="false" />
    <el-alert v-else-if="!tenantID" title="选择客户后可查看该客户的资金来源余额。" type="info" :closable="false" />
    <template v-else>
      <p class="source-note">金额为 COIN，保留 6 位小数（1 币 = 1,000,000 微币）。初始余额导入为非现金开账；赠送、未知基准和开账不赋予原路现金退款资格。人工核款标记现金来源，但 034.4 前不表示已开放现金退款。消费退款退回原来源的钱包余额。</p>
      <p class="source-note">本地模拟入账只具有支付沙箱语义，不代表真实收款或真实原路退款。以下金额按来源展示，不等于现金收入。</p>
      <ErrorNotice :message="state.error" @retry="refresh" />
      <div v-if="state.loading" role="status" aria-live="polite" class="source-loading">正在读取资金来源余额…</div>
      <el-table v-else :data="state.rows" row-key="lot_id" empty-text="当前页暂无资金来源" aria-label="资金来源余额明细">
        <el-table-column label="来源 / 批次" min-width="200"><template #default="{ row }"><div>{{ walletSourceLabel(row.source_kind) }}</div><span class="mono muted small-text" :title="row.lot_id">{{ shortId(row.lot_id) }}</span></template></el-table-column>
        <el-table-column label="退款类别" min-width="160"><template #default="{ row }"><el-tag :type="row.refund_class === 'sandbox_original' ? 'warning' : 'info'">{{ walletRefundLabel(row.refund_class) }}</el-tag><div v-if="row.source_kind === 'wallet_opening'" class="muted small-text">非现金初始余额</div></template></el-table-column>
        <el-table-column label="来源本金" min-width="175"><template #default="{ row }"><span class="source-money">{{ coin(row.issued_amount) }}</span></template></el-table-column>
        <el-table-column label="可用金额" min-width="175"><template #default="{ row }"><span class="source-money">{{ coin(row.available_amount) }}</span></template></el-table-column>
        <el-table-column label="冻结金额" min-width="175"><template #default="{ row }"><span class="source-money">{{ coin(row.held_amount) }}</span></template></el-table-column>
        <el-table-column label="退款冻结" min-width="175"><template #default="{ row }"><span class="source-money">{{ coin(row.refund_held_amount) }}</span></template></el-table-column>
        <el-table-column label="已消费金额" min-width="175"><template #default="{ row }"><span class="source-money">{{ coin(row.consumed_amount) }}</span></template></el-table-column>
        <el-table-column label="已退出金额" min-width="175"><template #default="{ row }"><span class="source-money">{{ coin(row.retired_amount) }}</span></template></el-table-column>
        <el-table-column label="入账时间（UTC）" min-width="245"><template #default="{ row }"><span class="mono small-text">{{ row.created_at }}</span></template></el-table-column>
      </el-table>
      <div class="table-footer"><span>第 {{ state.history.length + 1 }} 页 · 当前页 {{ state.rows.length }} 条 · 每页最多 50 条</span><div><el-button size="small" :disabled="state.loading || !state.history.length" @click="load('previous')">上一页</el-button><el-button size="small" :disabled="state.loading || !state.nextCursor" @click="load('next')">下一页</el-button></div></div>
    </template>
  </section>
</template>

<style scoped>
.wallet-sources{margin-top:24px}.wallet-sources h2{margin:0}.source-note{margin:12px 22px;color:var(--text-secondary);font-size:12px;line-height:1.8}.source-money{white-space:nowrap;font-variant-numeric:tabular-nums}.source-loading{padding:32px;text-align:center;color:var(--text-secondary)}.wallet-sources .table-footer{gap:12px;flex-wrap:wrap}@media(max-width:640px){.source-note{margin-left:14px;margin-right:14px}.wallet-sources .panel-header{gap:12px;flex-wrap:wrap}}
</style>
