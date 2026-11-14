<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import CommerceWriter from './CommerceWriter.vue'
import CommerceCommands from './CommerceCommands.vue'
import ErrorNotice from './ErrorNotice.vue'
import MetricMoney from './MetricMoney.vue'
import { errorText, request } from '../api'
import { commerceUUID, fundingReceiptsPath, fundingSourceKinds, fundingSourceLabel, parseCommerceCapabilities, parseFundingReceiptsPage, parseFundingSummary, parseVoucherRedemption } from '../commerce'
import type { CommerceCapabilities, CommerceReceipt, FundingReceipt, FundingSummary, VoucherRedemption } from '../commerce'
import { commerceContext, createCommerceScope } from '../commerce-state'
import { coin, shortId } from '../format'
import { canViewWallet, session } from '../session'

const emit = defineEmits<{ changed: [] }>(), scope = createCommerceScope()
const tenantID = computed(() => session.me?.tenant?.id || ''), owner = computed(() => session.me?.tenant?.role === 'owner')
const context = computed(() => commerceContext(session.me?.user.id, tenantID.value, `${session.me?.user.platform_role}:${session.me?.tenant?.role}:${session.me?.csrf_token}`))
const summary = ref<FundingSummary | null>(null), caps = ref<CommerceCapabilities | null>(null), error = ref(''), capsError = ref(''), loading = ref(false)
const rows = ref<FundingReceipt[]>([]), filter = ref(''), cursor = ref(''), next = ref(''), trail = ref<string[]>([]), listBusy = ref(false), listError = ref(''), writerOpen = ref(false)
const redemptionID = ref(''), redemption = ref<VoucherRedemption | null>(null), redemptionBusy = ref(false), redemptionError = ref('')
async function loadSummary() {
  const ticket = scope.ticket('summary'); loading.value = true; error.value = ''; capsError.value = ''; caps.value = null
  const values = await Promise.allSettled([request<unknown>('/wallet/funding-summary', 'GET', undefined, undefined, () => scope.accepts(ticket)), request<unknown>('/wallet/commerce-capabilities', 'GET', undefined, undefined, () => scope.accepts(ticket))])
  if (!scope.accepts(ticket)) return
  if (values[0].status === 'fulfilled') { try { summary.value = parseFundingSummary(values[0].value.data) } catch (cause) { summary.value = null; error.value = errorText(cause) } } else { summary.value = null; error.value = errorText(values[0].reason) }
  if (values[1].status === 'fulfilled') { try { caps.value = parseCommerceCapabilities(values[1].value.data) } catch (cause) { capsError.value = errorText(cause) } } else capsError.value = errorText(values[1].reason)
  loading.value = false
}
async function load(page = cursor.value, navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
  const ticket = scope.ticket('receipts'), expectedTenant = tenantID.value, sourceKind = filter.value, previous = cursor.value
  listBusy.value = true; listError.value = ''
  try {
    const response = await request<unknown>(fundingReceiptsPath(sourceKind, page), 'GET', undefined, undefined, () => scope.accepts(ticket))
    if (!scope.accepts(ticket)) return
    const parsed = parseFundingReceiptsPage(response.data, response.next_cursor, expectedTenant, sourceKind, page)
    rows.value = parsed.rows; next.value = parsed.next; cursor.value = page
    if (navigation === 'next') trail.value.push(previous); else if (navigation === 'previous') trail.value.pop()
  } catch (cause) { if (scope.accepts(ticket)) { listError.value = errorText(cause); rows.value = []; next.value = '' } }
  finally { if (scope.accepts(ticket)) listBusy.value = false }
}
async function inspectRedemption() {
  const ticket = scope.ticket('redemption'), expectedTenant = tenantID.value; redemptionBusy.value = true; redemption.value = null; redemptionError.value = ''
  try {
    const id = commerceUUID(redemptionID.value), response = await request<unknown>(`/wallet/voucher-redemptions/${id}`, 'GET', undefined, undefined, () => scope.accepts(ticket))
    if (scope.accepts(ticket)) redemption.value = parseVoucherRedemption(response.data, expectedTenant, id)
  } catch (cause) { if (scope.accepts(ticket)) redemptionError.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) redemptionBusy.value = false }
}
function settled(receipt: CommerceReceipt) { void loadSummary(); void load(); emit('changed'); if (receipt.status === 'accepted' && receipt.kind === 'voucher.redeem' && receipt.resource_id) { redemptionID.value = receipt.resource_id; void inspectRedemption() } }
function reset() { scope.reset(context.value); summary.value = null; caps.value = null; error.value = ''; capsError.value = ''; loading.value = false; rows.value = []; filter.value = ''; cursor.value = ''; next.value = ''; trail.value = []; listBusy.value = false; listError.value = ''; writerOpen.value = false; redemptionID.value = ''; redemption.value = null; redemptionBusy.value = false; redemptionError.value = ''; if (canViewWallet.value) { void loadSummary(); void load('') } }
watch(context, reset, { immediate: true, flush: 'sync' })
watch(filter, () => { scope.invalidate('receipts'); rows.value = []; cursor.value = ''; next.value = ''; trail.value = []; if (canViewWallet.value) void load('') })
onBeforeUnmount(() => scope.reset())
</script>
<template>
  <section class="panel funding-panel"><div class="panel-header"><div><div class="panel-title">钱包入账来源</div><p class="panel-subtitle">按原始回执展示人工核款、运营赠额、本地模拟入账和初始余额导入。累计入账不等于可用余额或现金收入；当前余额分配见上方来源分桶。</p></div><div><el-button :loading="loading" @click="loadSummary(); load()">刷新来源</el-button><el-button v-if="owner" type="primary" :disabled="caps?.vouchers_enabled !== true" @click="writerOpen = true">兑换运营赠额</el-button></div></div>
    <ErrorNotice :message="error" @retry="loadSummary" /><ErrorNotice :message="capsError" @retry="loadSummary" /><el-alert v-if="caps && !caps.vouchers_enabled" title="当前兑换能力关闭，请联系平台运营。旧兑换回执与来源仍可查询。" type="info" :closable="false" />
    <div class="funding-metrics"><div><span>累计钱包入账</span><MetricMoney unit="COIN" :value="summary?.total_amount" /></div><div><span>人工核款</span><MetricMoney unit="COIN" :value="summary?.manual_credit_amount" /></div><div><span>随单赠送</span><MetricMoney unit="COIN" :value="summary?.manual_bonus_amount" /></div><div><span>未知付款基准</span><MetricMoney unit="COIN" :value="summary?.manual_unknown_amount" /></div><div><span>运营赠额</span><MetricMoney unit="COIN" :value="summary?.voucher_grant_amount" /></div><div><span>本地模拟入账</span><MetricMoney unit="COIN" :value="summary?.sandbox_capture_amount" /></div><div><span>初始余额导入（非现金）</span><MetricMoney unit="COIN" :value="summary?.wallet_opening_amount" /></div></div>
    <p class="field-hint">同步来源观察时刻（UTC）：{{ summary?.as_of || '尚未取得' }}。报表的 credited 是异步投影，可能晚于本摘要，差值不能用于自动补账。赠额不赋予现金退款权。本地模拟入账无真实收款。初始余额导入为非现金开账，不赋予原路现金退款权。</p>
    <div class="funding-filter"><el-select v-model="filter" aria-label="资金来源筛选" style="width:250px"><el-option label="全部入账来源" value="" /><el-option v-for="kind in fundingSourceKinds" :key="kind" :label="fundingSourceLabel(kind)" :value="kind" /></el-select></div><ErrorNotice :message="listError" @retry="load()" />
    <el-table v-loading="listBusy" :data="rows" empty-text="当前筛选下暂无入账来源"><el-table-column label="来源回执" min-width="165"><template #default="{ row }"><span class="mono" :title="row.id">{{ shortId(row.id) }}</span></template></el-table-column><el-table-column label="来源类型 / 原记录" min-width="200"><template #default="{ row }">{{ fundingSourceLabel(row.source_kind) }}<div class="mono" :title="row.source_id">{{ row.source_id }}</div></template></el-table-column><el-table-column label="入账金额" min-width="170"><template #default="{ row }">{{ coin(row.amount) }}</template></el-table-column><el-table-column label="账本流水" min-width="170"><template #default="{ row }"><span class="mono" :title="row.journal_id">{{ shortId(row.journal_id) }}</span></template></el-table-column><el-table-column label="提交时刻（UTC）" min-width="235"><template #default="{ row }">{{ row.created_at }}</template></el-table-column></el-table><div class="table-footer"><span>固定每页最多 50 条</span><div><el-button :disabled="listBusy || !trail.length" @click="load(trail.at(-1) || '', 'previous')">上一页</el-button><el-button :disabled="listBusy || !next" @click="load(next, 'next')">下一页</el-button></div></div>
  </section>
  <CommerceCommands v-if="tenantID" :scope="tenantID" @settled="settled" />
  <section class="panel funding-panel"><div class="panel-title">兑换明细核查</div><p class="panel-subtitle">使用 accepted 回执中的 resource_id（兑换记录 ID），不是券 ID 或操作 ID。</p><div class="funding-lookup"><el-input v-model="redemptionID" placeholder="兑换记录 UUID" @input="scope.invalidate('redemption'); redemption = null; redemptionBusy = false" /><el-button :loading="redemptionBusy" @click="inspectRedemption">只读读取</el-button></div><ErrorNotice :message="redemptionError" /><template v-if="redemption"><p>已入账 {{ coin(redemption.amount) }} · {{ redemption.created_at }}</p><p class="mono funding-reference">来源回执 {{ redemption.funding_receipt_id }} · 流水 {{ redemption.journal_id }}</p></template></section>
  <CommerceWriter v-if="tenantID" v-model:open="writerOpen" kind="voucher.redeem" :scope="tenantID" :enabled="caps?.vouchers_enabled === true" @settled="settled" />
</template>
<style scoped>.funding-panel{margin-top:24px}.funding-metrics{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:22px;margin:24px 0}.funding-metrics>div>span{font-size:12px;color:var(--text-secondary)}.funding-filter{margin:20px 0}.funding-lookup{display:flex;gap:12px;max-width:650px;margin:16px 0}.funding-reference{font-size:12px;overflow-wrap:anywhere}@media(max-width:700px){.funding-metrics{grid-template-columns:1fr}}</style>
