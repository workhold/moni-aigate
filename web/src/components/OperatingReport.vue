<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Download, Refresh } from '@element-plus/icons-vue'
import MetricMoney from './MetricMoney.vue'
import RouteAttempts from './RouteAttempts.vue'
import { request } from '../api'
import { integer, label, money, shortId, time } from '../format'
import { session } from '../session'
import { createOperatingReportState, defaultOperatingFilter, fetchOperatingCSV, operatingFilter, operatingMarginKnown, operatingMarginText, operatingReportPath, parseOperatingReport, saveOperatingCSV } from '../operating-report'
import type { OperatingReport } from '../operating-report'

const route = useRoute(), flow = createOperatingReportState()
const filters = ref(defaultOperatingFilter()), state = ref(flow.snapshot()), page = ref(1), pageSize = 50
const attemptsOpen = ref(false), attemptsRequest = ref('')
const allowed = computed(() => route.path === '/platform/reports' && ['admin', 'finance_operator', 'finance_approver'].includes(session.me?.user.platform_role || '') && !session.mfaPending && !session.enrollmentRequired)
const context = computed(() => JSON.stringify([route.fullPath, session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role, session.me?.tenant?.role, session.me?.csrf_token, session.mfaPending, session.enrollmentRequired]))
const report = computed(() => state.value.report)
const requests = computed(() => report.value?.requests.slice((page.value - 1) * pageSize, page.value * pageSize) || [])
function sync() { state.value = flow.snapshot() }
function invalidate() { flow.invalidate(); page.value = 1; attemptsOpen.value = false; attemptsRequest.value = ''; sync() }
function showAttempts(id: string) { attemptsRequest.value = id; attemptsOpen.value = true }
async function load() {
  if (!allowed.value) return
  let filter
  try { filter = operatingFilter(filters.value) }
  catch (cause) { flow.invalidate(cause instanceof Error ? cause.message : '请检查筛选条件。'); sync(); return }
  const ticket = flow.beginRead()
  if (!ticket) return
  page.value = 1; attemptsOpen.value = false; attemptsRequest.value = ''; sync()
  try {
    const result = await request<OperatingReport>(operatingReportPath(filter), 'GET', undefined, undefined, () => flow.current(ticket))
    flow.accept(ticket, parseOperatingReport(result.data))
  } catch (cause) { flow.fail(ticket, cause) }
  finally { sync() }
}
async function download() {
  if (!allowed.value) return
  let filter
  try { filter = operatingFilter(filters.value) }
  catch (cause) { flow.invalidate(cause instanceof Error ? cause.message : '请检查筛选条件。'); sync(); return }
  const ticket = flow.beginDownload()
  if (!ticket) return
  sync()
  try {
    const blob = await fetchOperatingCSV(filter, () => flow.current(ticket))
    if (blob && flow.current(ticket)) { saveOperatingCSV(blob); flow.finishDownload(ticket) }
  } catch (cause) { flow.finishDownload(ticket, cause) }
  finally { sync() }
}
watch(() => [filters.value.from, filters.value.to, filters.value.tenant_id, filters.value.model_id, filters.value.protocol, filters.value.execution_kind], invalidate, { flush: 'sync' })
watch(context, () => { invalidate(); if (allowed.value) void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { flow.dispose(); attemptsOpen.value = false; attemptsRequest.value = ''; sync() })
</script>

<template>
  <section class="operating-report" aria-label="经营报表与对账导出">
    <section class="panel">
      <div class="panel-header"><div><h2 class="panel-title">经营报表与对账导出</h2><p class="panel-subtitle">按请求创建时间选取范围，核对消费收入、退款及所有上游尝试的采购成本。</p></div></div>
      <el-alert v-if="!allowed" title="经营报表仅向平台管理员、财务制单和财务审批角色开放。" type="info" :closable="false" />
      <template v-else>
        <el-form class="report-filters" label-position="top" @submit.prevent="load">
          <el-form-item label="开始时间（含，本地时间）"><el-date-picker v-model="filters.from" type="datetime" value-format="YYYY-MM-DDTHH:mm:ss.SSSZ" format="YYYY-MM-DD HH:mm:ss" placeholder="选择开始时间" aria-label="经营报表开始时间" /></el-form-item>
          <el-form-item label="结束时间（不含，本地时间）"><el-date-picker v-model="filters.to" type="datetime" value-format="YYYY-MM-DDTHH:mm:ss.SSSZ" format="YYYY-MM-DD HH:mm:ss" placeholder="选择结束时间" aria-label="经营报表结束时间" /></el-form-item>
          <el-form-item label="客户 ID（可选）"><el-input v-model="filters.tenant_id" clearable placeholder="客户 UUID；留空为全部客户" aria-label="经营报表客户 ID" /></el-form-item>
          <el-form-item label="模型 ID（可选）"><el-input v-model="filters.model_id" clearable placeholder="模型 UUID；留空为全部模型" aria-label="经营报表模型 ID" /></el-form-item>
          <el-form-item label="协议（可选）"><el-select v-model="filters.protocol" clearable placeholder="全部协议" aria-label="经营报表协议"><el-option label="OpenAI Chat" value="openai_chat" /><el-option label="OpenAI Responses" value="openai_responses" /><el-option label="Anthropic Messages" value="anthropic_messages" /><el-option label="OpenAI Embeddings" value="openai_embeddings" /><el-option label="Jina Rerank" value="jina_rerank" /><el-option label="Gemini Generate Content" value="gemini_generate_content" /><el-option label="图片生成" value="image_generation" /><el-option label="TTS" value="tts" /><el-option label="异步视频生成" value="video_generation" /><el-option label="Realtime 单轮" value="realtime_turn" /></el-select></el-form-item>
          <el-form-item label="执行类型（可选）"><el-select v-model="filters.execution_kind" clearable placeholder="全部执行类型" aria-label="经营报表执行类型"><el-option label="同步" value="synchronous" /><el-option label="异步" value="asynchronous" /></el-select></el-form-item>
          <div class="report-actions"><el-button native-type="submit" type="primary" :icon="Refresh" :loading="state.loading">查询报表</el-button><el-button :icon="Download" :loading="state.downloading" @click="download">下载 CSV</el-button></div>
        </el-form>
        <p class="report-note">默认最近 7 天，单次最多 31 天、5,000 个请求。修改筛选会清除旧结果，点击查询后读取新范围。</p>
        <el-alert v-if="state.error" :title="state.error" type="error" :closable="false" show-icon />
        <el-alert v-if="state.downloadError" :title="state.downloadError" type="error" :closable="false" show-icon />
        <p class="report-note">收入为客户已结算消费，非现金收款。仅当范围内无未结算请求、无未知尝试成本时展示模型调用毛利；不含充值、探测、手续费、税费及其他经营费用。</p>
        <p class="report-note">CSV 金额列单位为 CNY 微元整数（1 元 = 1,000,000 微元），未知毛利留空。每次导出读取独立快照，期间新结算或成本更正可能使下载内容与屏幕不同。</p>
      </template>
    </section>

    <template v-if="allowed">
      <div v-if="state.loading" class="report-loading" role="status" aria-live="polite">正在读取同一快照内的请求与经营事实…</div>
      <p v-else-if="!report && !state.error" class="report-note">尚未查询当前筛选范围。</p>
      <template v-if="report">
        <p class="report-snapshot">请求创建窗口：<span :title="report.from">{{ time(report.from) }}</span> ≤ 创建时间 &lt; <span :title="report.to">{{ time(report.to) }}</span><br>统计快照：<span :title="report.as_of">{{ time(report.as_of) }}</span> · CNY · 以下分页均来自该次快照</p>
        <el-alert v-if="report.summary.unsettled_requests || report.summary.unknown_provider_costs" type="warning" :closable="false" show-icon :title="`有 ${integer(report.summary.unsettled_requests)} 个未结算请求、${integer(report.summary.unknown_provider_costs)} 次未知采购成本，毛利待核对；未知成本不会按零计入。`" />
        <div class="metric-grid report-metrics">
          <div class="metric-card"><div class="metric-label">客户消费收入</div><MetricMoney :value="report.summary.charged" /><div class="metric-note">已结算收费，非现金收款</div></div>
          <div class="metric-card"><div class="metric-label">消费退款</div><MetricMoney :value="report.summary.refunded" /><div class="metric-note">所选请求截至快照的退款</div></div>
          <div class="metric-card"><div class="metric-label">净消费收入</div><MetricMoney :value="report.summary.net_revenue" /><div class="metric-note">消费收入减消费退款</div></div>
          <div class="metric-card"><div class="metric-label">已知采购成本</div><MetricMoney :value="report.summary.known_provider_cost" /><div class="metric-note">所有尝试的合同或账单成本</div></div>
          <div class="metric-card"><div class="metric-label">模型调用毛利</div><MetricMoney v-if="operatingMarginKnown(report.summary)" :value="report.summary.gross_margin" /><div v-else class="metric-value text-warning">待核对</div><div class="metric-note">净消费收入减完整采购成本</div></div>
          <div class="metric-card"><div class="metric-label">请求 / 上游尝试</div><div class="metric-value">{{ integer(report.summary.request_count) }} / {{ integer(report.summary.attempt_count) }}</div><div class="metric-note">一次收入，所有尝试成本均计入</div></div>
          <div class="metric-card"><div class="metric-label">未结算请求</div><div class="metric-value" :class="{ 'text-warning': report.summary.unsettled_requests }">{{ integer(report.summary.unsettled_requests) }}</div><div class="metric-note">尚无结算事实的请求</div></div>
          <div class="metric-card"><div class="metric-label">未知采购成本尝试</div><div class="metric-value" :class="{ 'text-warning': report.summary.unknown_provider_costs }">{{ integer(report.summary.unknown_provider_costs) }}</div><div class="metric-note">待取得合同或账单成本事实</div></div>
        </div>
        <el-empty v-if="report.summary.request_count === 0" description="当前创建时间窗口及筛选范围没有请求" />

        <section class="panel"><div class="panel-header"><div><h3 class="panel-title">按模型汇总</h3><p class="panel-subtitle">每个客户请求仅计一次收入，采购成本包含该请求的全部尝试。</p></div></div>
          <el-table :data="report.models" empty-text="当前范围没有模型请求" aria-label="经营报表模型汇总">
            <el-table-column label="模型" min-width="190"><template #default="{ row }"><div>{{ row.model_name }}</div><div class="mono muted small-text" :title="row.model_id">{{ shortId(row.model_id) }}</div></template></el-table-column>
            <el-table-column label="请求 / 尝试" min-width="125"><template #default="{ row }">{{ integer(row.request_count) }} / {{ integer(row.attempt_count) }}</template></el-table-column>
            <el-table-column label="消费收入" min-width="165"><template #default="{ row }">{{ money(row.charged) }}</template></el-table-column>
            <el-table-column label="消费退款" min-width="165"><template #default="{ row }">{{ money(row.refunded) }}</template></el-table-column>
            <el-table-column label="净消费收入" min-width="165"><template #default="{ row }">{{ money(row.net_revenue) }}</template></el-table-column>
            <el-table-column label="已知采购成本" min-width="165"><template #default="{ row }">{{ money(row.known_provider_cost) }}</template></el-table-column>
            <el-table-column label="未结算 / 未知成本" min-width="155"><template #default="{ row }">{{ integer(row.unsettled_requests) }} / {{ integer(row.unknown_provider_costs) }}</template></el-table-column>
            <el-table-column label="模型调用毛利" min-width="165"><template #default="{ row }"><span :class="{ 'text-warning': !operatingMarginKnown(row) }">{{ operatingMarginText(row) }}</span></template></el-table-column>
          </el-table>
        </section>

        <section class="panel"><div class="panel-header"><div><h3 class="panel-title">按渠道汇总采购成本</h3><p class="panel-subtitle">渠道仅汇总采购成本，不分摊客户收入；失败和重试尝试也纳入。</p></div></div>
          <el-table :data="report.channels" empty-text="当前范围尚无上游尝试" aria-label="经营报表渠道成本汇总">
            <el-table-column label="渠道" min-width="210"><template #default="{ row }"><div>{{ row.channel_name }}</div><div class="mono muted small-text" :title="row.channel_id">{{ shortId(row.channel_id) }}</div></template></el-table-column>
            <el-table-column label="关联请求" min-width="110"><template #default="{ row }">{{ integer(row.request_count) }}</template></el-table-column>
            <el-table-column label="尝试次数" min-width="110"><template #default="{ row }">{{ integer(row.attempt_count) }}</template></el-table-column>
            <el-table-column label="已知采购成本" min-width="190"><template #default="{ row }">{{ money(row.known_provider_cost) }}</template></el-table-column>
            <el-table-column label="未知成本尝试" min-width="140"><template #default="{ row }"><span :class="{ 'text-warning': row.unknown_provider_costs }">{{ integer(row.unknown_provider_costs) }}</span></template></el-table-column>
          </el-table>
        </section>

        <section class="panel"><div class="panel-header"><div><h3 class="panel-title">请求级对账明细</h3><p class="panel-subtitle">按创建时间倒序，点击请求 ID 查看渠道选择和全部尝试。模型与渠道显示当前标签，ID 为准确关联身份。</p></div></div>
          <el-table :data="requests" empty-text="当前范围没有请求明细" aria-label="经营报表请求明细">
            <el-table-column label="请求 / 创建时间" min-width="195"><template #default="{ row }"><el-button link type="primary" class="mono" :title="row.request_id" @click="showAttempts(row.request_id)">{{ shortId(row.request_id) }}</el-button><div class="muted small-text">{{ time(row.created_at) }}</div></template></el-table-column>
            <el-table-column label="客户 / 模型" min-width="195"><template #default="{ row }"><div :title="row.tenant_id">{{ row.tenant_name || shortId(row.tenant_id) }}</div><div class="muted small-text" :title="row.model_id">{{ row.model_name || shortId(row.model_id) }}</div></template></el-table-column>
            <el-table-column label="请求 / 收费状态" min-width="140"><template #default="{ row }"><div>{{ label(row.status) }}</div><div class="muted small-text">{{ label(row.billing_status) }}</div></template></el-table-column>
            <el-table-column label="消费收入" min-width="165"><template #default="{ row }">{{ money(row.charged) }}</template></el-table-column>
            <el-table-column label="消费退款" min-width="165"><template #default="{ row }">{{ money(row.refunded) }}</template></el-table-column>
            <el-table-column label="净消费收入" min-width="165"><template #default="{ row }">{{ money(row.net_revenue) }}</template></el-table-column>
            <el-table-column label="尝试 / 未知成本" min-width="140"><template #default="{ row }">{{ integer(row.attempt_count) }} / {{ integer(row.unknown_provider_costs) }}</template></el-table-column>
            <el-table-column label="已知采购成本" min-width="165"><template #default="{ row }">{{ money(row.known_provider_cost) }}</template></el-table-column>
            <el-table-column label="模型调用毛利" min-width="165"><template #default="{ row }"><span :class="{ 'text-warning': !operatingMarginKnown(row) }">{{ operatingMarginText(row) }}</span></template></el-table-column>
          </el-table>
          <div class="table-footer"><span>共 {{ integer(report.requests.length) }} 个请求 · 当前快照内每页 {{ pageSize }} 条</span><el-pagination v-model:current-page="page" :page-size="pageSize" :total="report.requests.length" layout="prev, pager, next" small /></div>
        </section>
      </template>
    </template>
    <el-dialog v-model="attemptsOpen" title="路由与尝试" width="min(950px, 94vw)" destroy-on-close><div class="muted small-text">请求 ID <code class="mono">{{ attemptsRequest }}</code></div><RouteAttempts v-if="allowed && attemptsOpen" :request-id="attemptsRequest" /></el-dialog>
  </section>
</template>

<style scoped>
.operating-report{margin-bottom:30px}.report-filters{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 18px;padding:18px 22px 0}.report-filters :deep(.el-date-editor){width:100%}.report-actions{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:12px}.report-note{font-size:12px;line-height:1.8;color:var(--text-secondary,#7f8b9c);margin:10px 22px 15px}.report-snapshot{font-size:12px;color:var(--text-secondary,#7f8b9c);line-height:1.9;margin:17px 0}.report-loading{padding:30px;text-align:center;color:var(--text-secondary,#7f8b9c)}.report-metrics{margin-top:20px}.operating-report :deep(.el-alert){margin-bottom:14px}.operating-report h2,.operating-report h3{margin:0}.operating-report .table-footer{gap:16px;flex-wrap:wrap}@media(max-width:640px){.report-filters{grid-template-columns:minmax(0,1fr);padding:14px}.report-note{margin-left:14px;margin-right:14px}.report-actions{gap:6px}.report-snapshot{overflow-wrap:anywhere}}
</style>
