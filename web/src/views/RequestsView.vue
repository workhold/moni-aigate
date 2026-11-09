<script setup lang="ts">
import { useRoute } from 'vue-router'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Refresh, Search, CopyDocument } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import RouteAttempts from '../components/RouteAttempts.vue'
import RequestFunding from '../components/RequestFunding.vue'
import { commerceContext, createCommerceScope } from '../commerce-state'
import { hasPlatform, session } from '../session'
import { errorText, request } from '../api'
import { label, money, shortId, tagType, time } from '../format'
import { quantityFor } from '../metering-wire'
import { parseRequestRows } from '../request-wire'
import type { RequestRow } from '../request-wire'

export type { RequestRow } from '../request-wire'
const route = useRoute()
const rows = ref<RequestRow[]>([]); const loading = ref(false); const error = ref(''); const cursor = ref(''); const cursorStack = ref<string[]>([])
const search = ref(''); const status = ref(''); const detail = ref<RequestRow | null>(null); const detailOpen = ref(false); const detailBusy = ref(false); const detailError = ref('')
const scope = createCommerceScope(), detailID = ref('')
const context = computed(() => commerceContext(session.me?.user.id, session.me?.tenant?.id, JSON.stringify([route.fullPath, session.me?.user.platform_role, session.me?.tenant?.role, session.me?.csrf_token, session.mfaPending, session.enrollmentRequired])))
const filtered = computed(() => rows.value.filter(row => (!status.value || row.billing_status === status.value) && `${row.id} ${row.model_name || row.model_id}`.toLowerCase().includes(search.value.toLowerCase())))
function amount(row: RequestRow, dimension: 'text_input_token' | 'text_output_token'): string {
  const line = quantityFor(row.retail_quantities, dimension)
  return line?.quantity ?? (line ? label(line.status) : '—')
}
function usageSummary(row: RequestRow): string { return row.retail_quantities ? `${amount(row, 'text_input_token')} / ${amount(row, 'text_output_token')}` : label(row.usage_status) }
async function load(pageCursor = cursorStack.value.at(-1) || '') {
  const ticket = scope.ticket('requests')
  loading.value = true; error.value = ''; rows.value = []; cursor.value = ''
  try { const response = await request<unknown[]>(`/requests${pageCursor ? `?cursor=${encodeURIComponent(pageCursor)}` : ''}`, 'GET', undefined, undefined, () => scope.accepts(ticket)); if (scope.accepts(ticket)) { rows.value = parseRequestRows(response.data); cursor.value = response.next_cursor || '' } }
  catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) } finally { if (scope.accepts(ticket)) loading.value = false }
}
async function next() { if (!cursor.value) return; cursorStack.value.push(cursor.value); await load(cursor.value) }
async function previous() { cursorStack.value.pop(); await load() }
async function showDetail(row: Pick<RequestRow, 'id'>) {
  const ticket = scope.ticket('detail')
  detailID.value = row.id; detail.value = null; detailOpen.value = true; detailBusy.value = true; detailError.value = ''
  try { const response = await request<unknown>(`/requests/${encodeURIComponent(row.id)}`, 'GET', undefined, undefined, () => scope.accepts(ticket)); if (scope.accepts(ticket)) detail.value = parseRequestRows([response.data])[0] ?? null }
  catch (e) { if (scope.accepts(ticket)) detailError.value = errorText(e) } finally { if (scope.accepts(ticket)) detailBusy.value = false }
}
function retryDetail() { if (detailID.value) void showDetail({ id: detailID.value }) }
watch(context, () => { scope.reset(context.value); rows.value = []; cursor.value = ''; cursorStack.value = []; error.value = ''; loading.value = false; detailOpen.value = false; detail.value = null; detailID.value = ''; detailError.value = ''; detailBusy.value = false; if (session.me?.tenant && !session.mfaPending && !session.enrollmentRequired) void load() }, { flush: 'sync' })
watch(detailOpen, open => { if (!open) { scope.invalidate('detail'); detail.value = null; detailID.value = ''; detailError.value = ''; detailBusy.value = false } }, { flush: 'sync' })
onBeforeUnmount(() => { scope.reset(); rows.value = []; detail.value = null })
async function copy(value: string) { try { await navigator.clipboard.writeText(value); ElMessage.success('请求 ID 已复制。') } catch { ElMessage.warning('无法复制，请手动选择。') } }
onMounted(() => {
  void load()
  const id = route.query.request_id
  if (typeof id === 'string' && id.length <= 128) void showDetail({ id })
})
</script>
<template>
  <PageHeader eyebrow="REQUEST EXPLORER" title="调用记录" description="按请求追踪生成结果、最终用量与资金处理状态。"><el-button :icon="Refresh" :loading="loading" @click="load()">刷新</el-button></PageHeader>
  <ErrorNotice :message="error" @retry="load()" />
  <section class="panel"><div class="filters"><el-input v-model="search" :prefix-icon="Search" placeholder="搜索当前页请求 ID / 模型" clearable /><el-select v-model="status" placeholder="全部收费状态" clearable style="width:150px"><el-option label="已结算" value="settled" /><el-option label="已释放" value="released" /><el-option label="待核对" value="pending_reconciliation" /><el-option label="已冻结" value="reserved" /></el-select></div><el-table v-loading="loading" :data="filtered" empty-text="暂无调用记录。使用 API Key 发起调用后，可在这里查看结果。"><el-table-column label="请求 / 模型" min-width="215"><template #default="{ row }"><el-button link type="primary" class="mono" @click="showDetail(row)">{{ shortId(row.id) }}</el-button><div class="muted small-text" style="margin-top:4px">{{ row.model_name || row.model_id }}</div></template></el-table-column><el-table-column label="生成结果" width="108"><template #default="{ row }"><el-tag :type="tagType(row.status)">{{ label(row.status) }}</el-tag></template></el-table-column><el-table-column label="收费状态" min-width="115"><template #default="{ row }"><el-tag :type="tagType(row.billing_status)">{{ label(row.billing_status) }}</el-tag></template></el-table-column><el-table-column label="完整用量（输入 / 输出）" min-width="190"><template #default="{ row }"><span class="mono">{{ usageSummary(row) }}</span></template></el-table-column><el-table-column label="实际收费" min-width="130"><template #default="{ row }"><span>{{ row.billing_status === 'pending_reconciliation' ? '待核对' : money(row.charged_amount) }}</span></template></el-table-column><el-table-column label="请求时间" min-width="170"><template #default="{ row }">{{ time(row.created_at || row.accepted_at) }}</template></el-table-column><el-table-column label="操作" width="80" fixed="right"><template #default="{ row }"><el-button type="primary" link @click="showDetail(row)">明细</el-button></template></el-table-column></el-table><div class="table-footer"><span>当前页 {{ rows.length }} 条 · 筛选仅作用于当前页</span><div><el-button size="small" :disabled="!cursorStack.length || loading" @click="previous">上一页</el-button><el-button size="small" :disabled="!cursor || loading" @click="next">下一页</el-button></div></div></section>
  <p class="section-note">生成成功与收费结算是不同状态。待核对不会按估算量或最大预占扣费；24 小时截止仍未结算时释放冻结，迟到证据不自动追扣客户。</p>
  <el-dialog v-model="detailOpen" title="请求明细" :width="hasPlatform ? '950px' : '650px'" destroy-on-close><ErrorNotice :message="detailError" @retry="retryDetail" /><div v-loading="detailBusy"><dl v-if="detail" class="detail-list"><dt>请求 ID</dt><dd class="mono">{{ detail.id }}<el-button link :icon="CopyDocument" aria-label="复制请求ID" @click="copy(detail.id)" /></dd><dt>模型</dt><dd>{{ detail.model_name || detail.model_id }}</dd><dt>生成结果</dt><dd><el-tag :type="tagType(detail.status)">{{ label(detail.status) }}</el-tag></dd><dt>收费状态</dt><dd><el-tag :type="tagType(detail.billing_status)">{{ label(detail.billing_status) }}</el-tag></dd><dt>完整用量（输入 / 输出）</dt><dd><span class="mono">{{ usageSummary(detail) }}</span></dd><dt>计量 schema</dt><dd class="mono">{{ detail.schema_id }} · {{ detail.variant_key }}</dd><dt>零售费率</dt><dd><div v-for="line in detail.retail_rates.rates" :key="line.dimension">{{ line.dimension }}：{{ line.numerator }} / {{ line.denominator }} · {{ line.unit }}</div></dd><dt>零售原界</dt><dd><div v-for="line in detail.retail_bounds.bounds" :key="line.dimension">{{ line.dimension }}：{{ line.quantity }} {{ line.unit }}</div></dd><dt>用量可信度</dt><dd>{{ label(detail.usage_status) }}</dd><dt>原始冻结</dt><dd>{{ money(detail.reserved_amount ?? detail.reservation_amount) }}</dd><dt>实际收费</dt><dd>{{ detail.billing_status === 'pending_reconciliation' ? '待核对' : money(detail.charged_amount) }}</dd><dt>协议结果</dt><dd>{{ label(detail.protocol_outcome) }}</dd><dt>终止原因</dt><dd>{{ detail.finish_reason || detail.transport_end_reason || '—' }}</dd><dt>本地交付阶段</dt><dd>{{ detail.delivery_stage || '—' }}</dd><dt>结算单 ID</dt><dd class="mono">{{ detail.settlement_id || '—' }}</dd><dt>请求时间</dt><dd>{{ time(detail.created_at || detail.accepted_at) }}</dd></dl><RequestFunding v-if="detail" :request-id="detail.id" /><RouteAttempts v-if="hasPlatform && detail" :request-id="detail.id" /></div><div class="danger-note" style="margin-top:25px">网络写入成功仅表示本地写入进展，不保证客户应用已收到全部内容。结算后的交付争议请提供请求 ID，由运营人员按原消费记录处理。</div></el-dialog>
</template>
