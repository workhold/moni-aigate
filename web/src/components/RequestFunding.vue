<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import ErrorNotice from './ErrorNotice.vue'
import { commerceContext } from '../commerce-state'
import { coin, shortId } from '../format'
import { session } from '../session'
import { walletRefundLabel, walletSourceLabel } from '../wallet-sources'
import { createRequestFundingReader } from '../wallet-source-details'

const props = defineProps<{ requestId: string }>()
const route = useRoute(), reader = createRequestFundingReader(), state = ref(reader.snapshot())
const allowed = computed(() => !!session.me?.tenant && !session.mfaPending && !session.enrollmentRequired && ['owner', 'member', 'billing_viewer'].includes(session.me.tenant.role))
const context = computed(() => commerceContext(session.me?.user.id, session.me?.tenant?.id, JSON.stringify([props.requestId, route.fullPath, session.me?.user.platform_role, session.me?.tenant?.role, session.me?.csrf_token, session.mfaPending, session.enrollmentRequired])))
function sync() { state.value = reader.snapshot() }
async function load(navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
  if (!allowed.value || !props.requestId) return
  const pending = reader.load(props.requestId, navigation); sync(); await pending; sync()
}
watch(context, () => { reader.reset(context.value); sync(); if (allowed.value) void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { reader.dispose(); sync() })
</script>

<template>
  <section v-if="allowed" class="request-funding" aria-label="请求资金来源分配">
    <div class="funding-header"><h3>资金来源分配</h3><el-button size="small" :icon="Refresh" :loading="state.loading" @click="load()">刷新分配</el-button></div>
    <p class="muted small-text">分配序号表示本次请求的 FIFO 扣费顺序；列表按来源时间倒序分页。消费退款退回原来源，金额以 COIN 展示。</p>
    <ErrorNotice :message="state.error" @retry="load()" />
    <div v-if="state.loading" role="status" aria-live="polite" class="funding-loading">正在读取资金分配…</div>
    <el-table v-else :data="state.rows" row-key="lot_id" empty-text="本请求暂无资金分配，零价请求无需分配资金。" aria-label="请求资金来源明细">
      <el-table-column label="分配序号" width="105"><template #default="{ row }"><span class="mono">{{ row.allocation_index }}</span></template></el-table-column>
      <el-table-column label="来源 / 批次" min-width="170"><template #default="{ row }"><div>{{ walletSourceLabel(row.source_kind) }}</div><span class="mono muted small-text" :title="row.lot_id">{{ shortId(row.lot_id) }}</span><div class="muted small-text">{{ walletRefundLabel(row.refund_class) }}</div></template></el-table-column>
      <el-table-column label="原始预占" min-width="165"><template #default="{ row }"><span class="funding-money">{{ coin(row.reserved_amount) }}</span></template></el-table-column>
      <el-table-column label="实际收费" min-width="165"><template #default="{ row }"><span class="funding-money">{{ row.charged_amount === null ? '未结算' : coin(row.charged_amount) }}</span></template></el-table-column>
      <el-table-column label="释放金额" min-width="165"><template #default="{ row }"><span class="funding-money">{{ row.released_amount === null ? '未结算' : coin(row.released_amount) }}</span></template></el-table-column>
      <el-table-column label="已退回钱包" min-width="165"><template #default="{ row }"><span class="funding-money">{{ coin(row.refunded_amount) }}</span></template></el-table-column>
    </el-table>
    <div class="table-footer"><span>第 {{ state.history.length + 1 }} 页 · 当前页 {{ state.rows.length }} 条 · 每页最多 50 条</span><div><el-button size="small" :disabled="state.loading || !state.history.length" @click="load('previous')">上一页</el-button><el-button size="small" :disabled="state.loading || !state.nextCursor" @click="load('next')">下一页</el-button></div></div>
  </section>
</template>

<style scoped>
.request-funding{margin-top:26px}.funding-header{display:flex;align-items:center;justify-content:space-between;gap:12px}.funding-header h3{margin:0;font-size:15px}.request-funding p{line-height:1.8}.funding-money{white-space:nowrap;font-variant-numeric:tabular-nums}.funding-loading{padding:30px;text-align:center;color:var(--text-secondary)}.request-funding .table-footer{padding-right:0;padding-left:0;gap:12px;flex-wrap:wrap}
</style>
