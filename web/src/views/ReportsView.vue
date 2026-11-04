<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Refresh, InfoFilled } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import RouteAttempts from '../components/RouteAttempts.vue'
import MetricMoney from '../components/MetricMoney.vue'
import OperatingReport from '../components/OperatingReport.vue'
import { errorText, request, stableOperationKey } from '../api'
import { cnyToMicro, integer, label, shortId, time } from '../format'
import { providerCostText } from '../reporting'
import { canViewWallet, session } from '../session'

interface Summary { tenant_id?: string; credited: string; charged: string; refunded: string; released: string; requests: number; known_provider_cost?: string; unknown_provider_costs?: number; unknown_probe_costs?: number; projection_pending?: number; as_of?: string }
interface Tenant { id: string; name: string; status: string }
interface Cost { id: string; tenant_id: string; tenant_name?: string; request_id: string; channel_id: string; channel_name?: string; upstream_model: string; cost_amount: string | null; cost_status: string; created_at: string }
const route = useRoute(); const platform = computed(() => !!route.meta.platform)
const allowed = computed(() => platform.value || canViewWallet.value)
const canCorrect = computed(() => session.me?.user.platform_role === 'finance_operator')
const summary = ref<Summary | null>(null); const costs = ref<Cost[]>([]); const tenants = ref<Tenant[]>([])
const tenantId = ref(''); const loading = ref(false); const error = ref(''); const nextCursor = ref(''); const cursors = ref<string[]>([])
let loadGeneration = 0
const correction = ref<Cost | null>(null); const correctionOpen = ref(false); const saving = ref(false); const formError = ref('')
const form = ref({ amount: '', basis: 'invoice', evidence_ref: '' })
const attemptsOpen = ref(false); const attemptsRequest = ref('')
function showAttempts(id: string) { attemptsRequest.value = id; attemptsOpen.value = true }
function query(cursor = '') {
  const params = new URLSearchParams()
  if (platform.value && tenantId.value) params.set('tenant_id', tenantId.value)
  if (cursor) params.set('cursor', cursor)
  return params.size ? `?${params}` : ''
}
async function load(cursor = cursors.value.at(-1) || '') {
  if (!allowed.value) return
  const generation = ++loadGeneration
  loading.value = true; error.value = ''
  const summaryTask = request<Summary>(`${platform.value ? '/platform' : ''}/reports/summary${query()}`)
  const costTask = platform.value ? request<Cost[]>(`/platform/costs${query(cursor)}`) : Promise.resolve(null)
  const [summaryResult, costResult] = await Promise.allSettled([summaryTask, costTask])
  if (generation !== loadGeneration) return
  if (summaryResult.status === 'fulfilled') summary.value = summaryResult.value.data
  else { summary.value = null; error.value = errorText(summaryResult.reason) }
  if (costResult.status === 'fulfilled' && costResult.value) { costs.value = costResult.value.data || []; nextCursor.value = costResult.value.next_cursor || '' }
  else if (costResult.status === 'rejected') { costs.value = []; nextCursor.value = ''; error.value ||= errorText(costResult.reason) }
  loading.value = false
}
async function filterTenant() { cursors.value = []; nextCursor.value = ''; await load('') }
async function next() { const cursor = nextCursor.value; cursors.value.push(cursor); await load(cursor) }
async function previous() { cursors.value.pop(); await load() }
function correct(row: Cost) { correction.value = row; form.value = { amount: '', basis: 'invoice', evidence_ref: '' }; formError.value = ''; correctionOpen.value = true }
async function saveCorrection() {
  if (!correction.value || saving.value) return
  saving.value = true; formError.value = ''
  try {
    if (!form.value.evidence_ref.trim()) throw new Error('请填写受控成本凭证引用。')
    const body = { tenant_id: correction.value.tenant_id, attempt_id: correction.value.id, amount: cnyToMicro(form.value.amount), basis: form.value.basis, evidence_ref: form.value.evidence_ref.trim() }
    const path = '/platform/cost-corrections'
    await request(path, 'POST', body, stableOperationKey(path, body))
    correctionOpen.value = false; ElMessage.success('成本事实已更正，客户收费不受影响。'); await load()
  } catch (e) { formError.value = errorText(e) } finally { saving.value = false }
}
onMounted(async () => {
  void load()
  if (platform.value) {
    try { tenants.value = (await request<Tenant[]>('/platform/tenants')).data || [] }
    catch (e) { error.value ||= `客户选项加载失败：${errorText(e)}` }
  }
})
</script>

<template>
  <PageHeader :eyebrow="platform ? 'OPERATING REPORTS & PROVIDER COSTS' : 'WORKSPACE REPORTS'" :title="platform ? '运营与成本报表' : '用量与资金报表'" :description="platform ? '上方按请求创建窗口核对经营事实，下方查看累计钱包指标与供应商成本更正。' : '按累计钱包入账、消费、退款与释放，查看本工作空间的资金与请求情况。'"><el-button :icon="Refresh" :loading="loading" @click="load()">刷新累计指标</el-button></PageHeader>
  <el-alert v-if="!allowed" title="当前角色没有资金报表查看权限。请联系租户所有者。" type="info" :closable="false" />
  <template v-else>
    <OperatingReport v-if="platform" />
    <section v-if="platform" class="panel"><div class="filters"><span class="muted">累计钱包指标与成本核对</span><el-select v-model="tenantId" clearable filterable placeholder="全部客户" style="width:250px" aria-label="报表客户范围" @change="filterTenant"><el-option v-for="tenant in tenants" :key="tenant.id" :label="tenant.name" :value="tenant.id" /></el-select><span class="muted small-text">全生命周期累计 · CNY</span></div></section>
    <ErrorNotice :message="error" @retry="load()" />
    <el-alert v-if="summary?.projection_pending" type="warning" :closable="false" show-icon :title="`还有 ${integer(summary.projection_pending)} 条业务事件等待报表更新，累计指标可能暂未包含最新操作。钱包余额以同步账本为准。`" style="margin-bottom:22px" />
    <div v-loading="loading" class="metric-grid">
      <div class="metric-card"><div class="metric-label">累计钱包入账</div><MetricMoney :value="summary?.credited" /><div class="metric-note">含人工核款、赠额、模拟入账及非现金开账，不等于现金收款</div></div>
      <div class="metric-card"><div class="metric-label">累计客户消费</div><MetricMoney :value="summary?.charged" /><div class="metric-note">已结算收费，退款另记</div></div>
      <div class="metric-card"><div class="metric-label">累计钱包退款</div><MetricMoney :value="summary?.refunded" /><div class="metric-note">消费退款补偿，非现金退款</div></div>
      <div class="metric-card"><div class="metric-label">累计释放冻结</div><MetricMoney :value="summary?.released" /><div class="metric-note">预占未消费部分的释放</div></div>
      <div class="metric-card"><div class="metric-label">已受理请求</div><div class="metric-value">{{ integer(summary?.requests) }}</div><div class="metric-note">由已接受请求业务事件累计</div></div>
      <template v-if="platform">
        <div class="metric-card"><div class="metric-label">已知供应商成本</div><MetricMoney :value="summary?.known_provider_cost" /><div class="metric-note">仅汇总具备有效成本事实的部分</div></div>
        <div class="metric-card"><div class="metric-label">待核对成本尝试</div><div class="metric-value" :class="{ 'text-warning': summary?.unknown_provider_costs }">{{ integer(summary?.unknown_provider_costs) }}</div><div class="metric-note">包括重试与失败尝试的未知成本</div></div>
        <div class="metric-card"><div class="metric-label">平台探测成本待核对</div><div class="metric-value" :class="{ 'text-warning': summary?.unknown_probe_costs }">{{ integer(summary?.unknown_probe_costs) }}</div><div class="metric-note">探测次数；金额未知，未按零计入</div></div>
      </template>
    </div>
    <el-alert v-if="platform && (summary?.unknown_provider_costs || summary?.unknown_probe_costs)" type="warning" :closable="false" show-icon title="供应商成本尚未完整。已知成本不等于总成本，不能据此确认毛利；未知尝试与平台探测成本不会按零计入。" style="margin-bottom:22px" />
    <section v-if="platform" class="panel"><div class="panel-header"><div><div class="panel-title">供应商尝试成本</div><div class="panel-subtitle">失败与重试也可能产生供应商费用；按合同或账单证据更正，不改变客户结算。</div></div></div>
      <el-table v-loading="loading" :data="costs" empty-text="此范围暂无供应商尝试成本记录">
        <el-table-column label="尝试 / 请求" min-width="180"><template #default="{ row }"><div class="mono" :title="row.id">{{ shortId(row.id) }}</div><el-button link type="primary" class="small-text mono" :title="row.request_id" @click="showAttempts(row.request_id)">{{ shortId(row.request_id) }}</el-button></template></el-table-column>
        <el-table-column label="客户" min-width="145"><template #default="{ row }">{{ row.tenant_name || shortId(row.tenant_id) }}</template></el-table-column>
        <el-table-column label="渠道 / 上游模型" min-width="200"><template #default="{ row }"><div>{{ row.channel_name || shortId(row.channel_id) }}</div><div class="muted small-text">{{ row.upstream_model }}</div></template></el-table-column>
        <el-table-column label="成本依据" min-width="115"><template #default="{ row }"><el-tag :type="row.cost_status === 'unknown' || row.cost_amount == null ? 'warning' : 'info'">{{ label(row.cost_status) }}</el-tag></template></el-table-column>
        <el-table-column label="成本金额" min-width="150"><template #default="{ row }"><span :class="{ 'text-warning': row.cost_status === 'unknown' || row.cost_amount == null }">{{ providerCostText(row.cost_amount, row.cost_status) }}</span></template></el-table-column>
        <el-table-column label="时间" min-width="180"><template #default="{ row }">{{ time(row.created_at) }}</template></el-table-column>
        <el-table-column v-if="canCorrect" label="操作" width="110" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="correct(row)">更正成本</el-button></template></el-table-column>
      </el-table><div class="table-footer"><span>当前页 {{ costs.length }} 条 · 各次尝试单独核对</span><div><el-button size="small" :disabled="!cursors.length || loading" @click="previous">上一页</el-button><el-button size="small" :disabled="!nextCursor || loading" @click="next">下一页</el-button></div></div>
    </section>
    <section v-else class="panel"><div class="panel-body"><div class="panel-title">资金余额与明细</div><p class="muted small-text" style="margin:12px 0 18px">累计指标由业务事件更新。当前可用余额、冻结和逐笔流水请在钱包查看；逐次模型调用请在调用记录中核对。</p><router-link to="/wallet"><el-button>查看钱包与流水</el-button></router-link><router-link to="/requests" style="margin-left:10px"><el-button>查看调用记录</el-button></router-link></div></section>
    <p class="section-note"><el-icon><InfoFilled /></el-icon><span>累计钱包指标统计时点：{{ time(summary?.as_of) }}。累计指标异步更新，不用于付费请求准入或替代同步资金账本。{{ platform ? '上方经营窗口报表独立读取同一只读快照。平台探测不归属于具体客户，探测待核对次数始终单列全平台数量。' : '' }}</span></p>
  </template>
  <el-dialog v-model="attemptsOpen" title="路由与尝试" width="950px" destroy-on-close><div class="muted small-text">请求 ID <code class="mono">{{ attemptsRequest }}</code></div><RouteAttempts v-if="platform && attemptsOpen" :request-id="attemptsRequest" /></el-dialog>
  <el-dialog v-model="correctionOpen" title="更正供应商成本" width="540px" destroy-on-close :close-on-click-modal="false" @closed="form = { amount: '', basis: 'invoice', evidence_ref: '' }; correction = null">
    <p class="dialog-description">仅追加有证据的成本事实。此操作不增加客户收费，也不改变钱包。请核对尝试 ID 与受控凭证。</p><dl class="detail-list" style="margin-bottom:22px"><dt>尝试 ID</dt><dd class="mono">{{ correction?.id }}</dd><dt>原成本</dt><dd>{{ providerCostText(correction?.cost_amount, correction?.cost_status) }}</dd><dt>原依据</dt><dd>{{ label(correction?.cost_status) }}</dd></dl>
    <el-alert v-if="formError" :title="formError" type="error" :closable="false" style="margin-bottom:20px" />
    <el-form label-position="top" @submit.prevent="saveCorrection"><el-form-item label="完整成本金额（CNY）" required><el-input v-model="form.amount" placeholder="输入经凭证核实的金额" inputmode="decimal" /><div class="field-hint">填写此尝试的完整成本，不是增减差额；零成本也必须有明确凭据。</div></el-form-item><el-form-item label="成本依据" required><el-select v-model="form.basis" style="width:100%"><el-option label="供应商账单" value="invoice" /><el-option label="供应商合同" value="contract" /></el-select></el-form-item><el-form-item label="受控成本凭证引用" required><el-input v-model="form.evidence_ref" maxlength="256" placeholder="受控账单或合同引用，不填写秘密下载链接" /></el-form-item></el-form>
    <template #footer><el-button @click="correctionOpen = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveCorrection">保存成本更正</el-button></template>
  </el-dialog>
</template>
