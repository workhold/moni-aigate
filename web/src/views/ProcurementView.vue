<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Plus, Refresh } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import ProcurementWriter from '../components/ProcurementWriter.vue'
import { errorText, request } from '../api'
import { money, shortId, time } from '../format'
import { session } from '../session'
import { createProcurementScope, procurementContext, procurementJournal, procurementOperationID } from '../procurement-state'
import type { ProcurementCommand, ProcurementCommandReview } from '../procurement-state'
import { acceptProcurementResult, parseProcurementAccount, parseProcurementBinding, parseProcurementEvent, parseProcurementObservation, parseProcurementPage, parseProcurementProposal, parseProcurementReceipt, parseProcurementReservation, procurementAccountPath, procurementProposalPath, procurementReceiptPath } from '../procurement'
import type { ProcurementAccount, ProcurementBinding, ProcurementEvent, ProcurementObservation, ProcurementProposal, ProcurementReservation, ProcurementResult, ProcurementWriteKind } from '../procurement'

type MainTab = 'accounts' | 'bindings' | 'proposals'
type DetailTab = 'reservations' | 'events' | 'balance-observations' | 'bindings'
const scope = createProcurementScope()
const context = computed(() => procurementContext(session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role))
const admin = computed(() => session.me?.user.platform_role === 'admin'), operator = computed(() => session.me?.user.platform_role === 'finance_operator'), approver = computed(() => session.me?.user.platform_role === 'finance_approver')
const tab = ref<MainTab>('accounts'), rows = ref<ProcurementAccount[]>([]), bindings = ref<ProcurementBinding[]>([]), proposals = ref<ProcurementProposal[]>([])
const loading = ref(false), error = ref(''), cursor = ref(''), next = ref(''), trail = ref<string[]>([])
const detail = ref<ProcurementAccount | null>(null), detailTab = ref<DetailTab>('reservations'), detailBusy = ref(false), detailError = ref(''), detailCursor = ref(''), detailNext = ref(''), detailTrail = ref<string[]>([])
const reservations = ref<ProcurementReservation[]>([]), events = ref<ProcurementEvent[]>([]), observations = ref<ProcurementObservation[]>([]), accountBindings = ref<ProcurementBinding[]>([]), reservationFilter = ref('')
const visibleReservations = computed(() => reservations.value.filter(row => !reservationFilter.value || row.state === reservationFilter.value))
const writerOpen = ref(false), writerKind = ref<ProcurementWriteKind>('create'), writerAccount = ref<ProcurementAccount | null>(null), writerProposal = ref<ProcurementProposal | null>(null), writerAttempt = ref<ProcurementReservation | null>(null)
const journalTick = ref(0), history = computed(() => { journalTick.value; return procurementJournal.list(context.value) })
const queryKind = ref<'operation' | 'proposal'>('operation'), queryID = ref(''), queryCommand = ref<ProcurementCommand | null>(null), queryBusy = ref(false), queryError = ref(''), queryResult = ref<ProcurementResult | null>(null)
const proposalOpen = ref(false), viewedProposal = ref<ProcurementProposal | null>(null), proposalBusy = ref(false), proposalError = ref('')
const statusLabels: Record<string, string> = { active: '启用', paused: '暂停', prepared: '预占待派发', sending: '已获发送许可', unknown: '消费未知，保留占用', disputed: '证据冲突，保留占用', settled: '已计成本', released: '已释放', proposed: '待独立审批', approved: '已批准', known: '已知观察值', unsupported: '不支持查询', error: '观察失败', submitting: '提交中', conflict: '冲突未应用', rejected: '明确拒绝', confirmed: '已取得持久结果' }
function state(value: string) { return statusLabels[value] || value }
function remaining(account: ProcurementAccount) { return (BigInt(account.limit_amount) - BigInt(account.held_amount) - BigInt(account.spent_amount)).toString() }
function endpoint(resource: MainTab) { return resource === 'accounts' ? '/platform/procurement-accounts' : resource === 'bindings' ? '/platform/procurement-bindings' : '/platform/procurement-cost-proposals' }
async function load(page = cursor.value, navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
  const resource = tab.value, ticket = scope.ticket('main'), previous = cursor.value
  loading.value = true; error.value = ''
  try {
    const response = await request<unknown>(`${endpoint(resource)}${page ? `?cursor=${encodeURIComponent(page)}` : ''}`)
    if (!scope.accepts(ticket)) return
    if (resource === 'accounts') { const parsed = parseProcurementPage(response.data, response.next_cursor, parseProcurementAccount); rows.value = parsed.rows; next.value = parsed.next }
    else if (resource === 'bindings') { const parsed = parseProcurementPage(response.data, response.next_cursor, parseProcurementBinding); bindings.value = parsed.rows; next.value = parsed.next }
    else { const parsed = parseProcurementPage(response.data, response.next_cursor, parseProcurementProposal); proposals.value = parsed.rows; next.value = parsed.next }
    if (navigation === 'next') trail.value.push(previous)
    else if (navigation === 'previous') trail.value.pop()
    cursor.value = page
  } catch (cause) { if (scope.accepts(ticket)) error.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
function closeDetail() {
  scope.invalidate('detail'); detail.value = null; detailBusy.value = false; detailError.value = ''; detailCursor.value = ''; detailNext.value = ''; detailTrail.value = []
  reservations.value = []; events.value = []; observations.value = []; accountBindings.value = []
}
function selectAccount(account: ProcurementAccount) {
  closeDetail(); detail.value = account; detailTab.value = 'reservations'; reservationFilter.value = ''; void loadDetail('')
}
async function loadDetail(page = detailCursor.value, navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
  if (!detail.value) return
  const id = detail.value.id, section = detailTab.value, ticket = scope.ticket('detail'), previous = detailCursor.value
  const path = section === 'bindings' ? `/platform/procurement-bindings?account_id=${encodeURIComponent(id)}` : `${procurementAccountPath(id)}/${section}`
  detailBusy.value = true; detailError.value = ''
  if (section === 'reservations') { reservations.value = []; detailNext.value = '' }
  try {
    const [accountResponse, response] = await Promise.all([request<unknown>(`${procurementAccountPath(id)}/configuration`), request<unknown>(`${path}${page ? `${path.includes('?') ? '&' : '?'}cursor=${encodeURIComponent(page)}` : ''}`)])
    const account = parseProcurementAccount(accountResponse.data, id)
    const parser = section === 'reservations' ? parseProcurementReservation : section === 'events' ? parseProcurementEvent : section === 'bindings' ? parseProcurementBinding : parseProcurementObservation
    const parsed = parseProcurementPage<ProcurementReservation | ProcurementEvent | ProcurementBinding | ProcurementObservation>(response.data, response.next_cursor, parser)
    if (parsed.rows.some(row => row.account_id !== id)) throw new Error('采购明细返回了其他账户的数据。')
    if (!scope.accepts(ticket)) return
    detail.value = account
    if (section === 'reservations') reservations.value = parsed.rows as ProcurementReservation[]
    else if (section === 'events') events.value = parsed.rows as ProcurementEvent[]
    else if (section === 'bindings') accountBindings.value = parsed.rows as ProcurementBinding[]
    else observations.value = parsed.rows as ProcurementObservation[]
    detailNext.value = parsed.next
    if (navigation === 'next') detailTrail.value.push(previous)
    else if (navigation === 'previous') detailTrail.value.pop()
    detailCursor.value = page
  } catch (cause) { if (scope.accepts(ticket)) detailError.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) detailBusy.value = false }
}
function openWriter(kind: ProcurementWriteKind, account: ProcurementAccount | null = null, proposal: ProcurementProposal | null = null, attempt: ProcurementReservation | null = null) {
  writerKind.value = kind; writerAccount.value = account; writerProposal.value = proposal; writerAttempt.value = attempt; writerOpen.value = true
}
function applied() { void load(); if (detail.value) void loadDetail() }
async function showProposal(proposal: ProcurementProposal) {
  const ticket = scope.ticket('proposal')
  proposalOpen.value = true; viewedProposal.value = null; proposalBusy.value = true; proposalError.value = ''
  try { const value = parseProcurementProposal((await request<unknown>(procurementProposalPath(proposal.id))).data, proposal.id); if (scope.accepts(ticket)) viewedProposal.value = value }
  catch (cause) { if (scope.accepts(ticket)) proposalError.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) proposalBusy.value = false }
}
function selectHistory(entry: ProcurementCommandReview) {
  scope.invalidate('lookup'); queryBusy.value = false; queryCommand.value = entry.command; queryID.value = entry.command.operationID; queryKind.value = entry.command.path.includes('procurement-cost-proposals') ? 'proposal' : 'operation'; queryError.value = ''; queryResult.value = null
  void inspect()
}
function changeQuery() { scope.invalidate('lookup'); queryCommand.value = null; queryResult.value = null; queryError.value = ''; queryBusy.value = false }
async function inspect() {
  if (!session.me || queryBusy.value) return
  const owner = context.value, actor = session.me.user.id, original = queryCommand.value, ticket = scope.ticket('lookup')
  queryBusy.value = true; queryError.value = ''; queryResult.value = null
  try {
    const id = procurementOperationID(queryID.value.trim()), path = original ? original.lookupPath : queryKind.value === 'proposal' ? procurementProposalPath(id) : procurementReceiptPath(id)
    const response = await request<unknown>(path)
    const value = original ? acceptProcurementResult(original, response.data, actor) : queryKind.value === 'proposal' ? parseProcurementProposal(response.data, id) : parseProcurementReceipt(response.data, id)
    if (original) { procurementJournal.confirm(owner, original.operationID, value); journalTick.value++ }
    if (!scope.accepts(ticket)) return
    queryResult.value = value
  } catch (cause) { if (scope.accepts(ticket)) queryError.value = `只读查询未确认原结果。没有查到回执也不能换 ID 重复提交。${errorText(cause)}` }
  finally { if (scope.accepts(ticket)) queryBusy.value = false }
}
function resetPage() {
  scope.reset(context.value); writerOpen.value = false; proposalOpen.value = false; viewedProposal.value = null; proposalBusy.value = false; proposalError.value = ''
  closeDetail(); rows.value = []; bindings.value = []; proposals.value = []; cursor.value = ''; next.value = ''; trail.value = []; error.value = ''; loading.value = false
  queryID.value = ''; queryCommand.value = null; queryResult.value = null; queryError.value = ''; queryBusy.value = false; journalTick.value++
  if (session.me && (admin.value || operator.value || approver.value)) void load('')
}
watch([context, tab], resetPage, { immediate: true })
watch(detailTab, () => { detailCursor.value = ''; detailNext.value = ''; detailTrail.value = []; reservations.value = []; events.value = []; observations.value = []; accountBindings.value = []; if (detail.value) void loadDetail('') })
watch(proposalOpen, value => { if (!value) { scope.invalidate('proposal'); proposalBusy.value = false; viewedProposal.value = null } })
onBeforeUnmount(() => scope.reset())
</script>

<template>
  <PageHeader eyebrow="PROCUREMENT OPERATIONS" title="采购账户与对账" description="为多个渠道与凭据共享累计采购预算，分别追踪客户收费与供应商消费。"><el-button :icon="Refresh" :loading="loading" @click="load()">刷新</el-button><el-button v-if="admin" type="primary" :icon="Plus" @click="openWriter('create')">新增采购账户</el-button></PageHeader>
  <el-alert type="info" title="预算是本平台登记的采购限额。人工余额观察不覆盖预算；客户退款或未知账单释放也不会抹去潜在供应商消费。" :closable="false" show-icon class="procurement-notice" />
  <ErrorNotice :message="error" @retry="load()" />
  <section class="panel">
    <el-tabs v-model="tab" class="procurement-tabs"><el-tab-pane label="共享采购账户" name="accounts" /><el-tab-pane label="永久凭据绑定" name="bindings" /><el-tab-pane label="采购成本更正" name="proposals" /></el-tabs>
    <div v-if="tab === 'bindings' && admin || tab === 'proposals' && operator" class="procurement-toolbar"><el-button v-if="tab === 'bindings'" type="primary" @click="openWriter('bind')">绑定已有凭据</el-button><el-button v-else type="primary" @click="openWriter('propose')">提交成本更正提案</el-button></div>
    <el-table v-if="tab === 'accounts'" v-loading="loading" :data="rows" empty-text="暂无采购账户">
      <el-table-column label="采购账户" min-width="200"><template #default="{ row }"><strong>{{ row.label }}</strong><div class="mono small-text muted" :title="row.id">{{ shortId(row.id) }}</div></template></el-table-column><el-table-column prop="provider" label="供应商标识" min-width="145" />
      <el-table-column label="状态" width="90"><template #default="{ row }"><el-tag :type="row.state === 'active' ? 'success' : 'warning'">{{ state(row.state) }}</el-tag></template></el-table-column>
      <el-table-column label="累计预算" min-width="165"><template #default="{ row }">{{ money(row.limit_amount) }}</template></el-table-column><el-table-column label="采购占用" min-width="165"><template #default="{ row }">{{ money(row.held_amount) }}</template></el-table-column><el-table-column label="已计采购成本" min-width="165"><template #default="{ row }">{{ money(row.spent_amount) }}</template></el-table-column>
      <el-table-column label="操作" min-width="260" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="selectAccount(row)">账务详情</el-button><router-link :to="{ path: '/platform/periodic-budget', query: { procurement: row.id } }">周期预算</router-link><el-button v-if="admin" link @click="openWriter('configure', row)">配置</el-button></template></el-table-column>
    </el-table>
    <el-table v-else-if="tab === 'bindings'" v-loading="loading" :data="bindings" empty-text="暂无已绑定凭据；未绑定凭据保持 legacy_unmanaged">
      <el-table-column label="凭据 ID" min-width="190"><template #default="{ row }"><span class="mono" :title="row.credential_id">{{ row.credential_id }}</span></template></el-table-column><el-table-column label="采购账户 ID" min-width="190"><template #default="{ row }"><span class="mono" :title="row.account_id">{{ shortId(row.account_id) }}</span></template></el-table-column><el-table-column label="渠道 ID" min-width="190"><template #default="{ row }"><span class="mono" :title="row.channel_id">{{ shortId(row.channel_id) }}</span></template></el-table-column><el-table-column label="绑定时间" min-width="185"><template #default="{ row }">{{ time(row.bound_at) }}</template></el-table-column><el-table-column label="当前渠道管理版本" min-width="165"><template #default="{ row }">{{ row.channel_revision }}</template></el-table-column>
    </el-table>
    <el-table v-else v-loading="loading" :data="proposals" empty-text="暂无采购成本更正提案">
      <el-table-column label="提案 / 原操作 ID" min-width="180"><template #default="{ row }"><span class="mono" :title="row.id">{{ shortId(row.id) }}</span></template></el-table-column><el-table-column label="尝试 ID" min-width="165"><template #default="{ row }"><span class="mono" :title="row.attempt_id">{{ shortId(row.attempt_id) }}</span></template></el-table-column><el-table-column label="最终供应商成本" min-width="175"><template #default="{ row }">{{ money(row.amount) }}</template></el-table-column><el-table-column label="依据 / 状态" min-width="170"><template #default="{ row }">{{ row.basis === 'invoice' ? '供应商账单' : '合同' }}<div><el-tag :type="row.state === 'approved' ? 'success' : 'warning'">{{ state(row.state) }}</el-tag></div></template></el-table-column><el-table-column label="操作" min-width="170" fixed="right"><template #default="{ row }"><el-button link @click="showProposal(row)">详情</el-button><el-button v-if="approver && row.state === 'proposed' && row.requester !== session.me?.user.id" link type="primary" @click="openWriter('approve', null, row)">独立复核</el-button></template></el-table-column>
    </el-table>
    <div class="pagination-row"><span>每页最多 50 条</span><div><el-button :disabled="loading || !trail.length" @click="load(trail[trail.length - 1] || '', 'previous')">上一页</el-button><el-button :disabled="loading || !next" @click="load(next, 'next')">下一页</el-button></div></div>
  </section>

  <section v-if="detail" class="panel procurement-detail">
    <div class="panel-header"><div><div class="panel-title">{{ detail.label }} · 采购账务</div><div class="mono small-text muted">{{ detail.id }} · 配置版本 {{ detail.revision }}</div></div><div><el-button :loading="detailBusy" @click="loadDetail()">刷新明细</el-button><el-button @click="closeDetail">关闭</el-button></div></div>
    <div class="procurement-metrics"><div><span>累计预算</span><strong>{{ money(detail.limit_amount) }}</strong></div><div><span>采购占用</span><strong>{{ money(detail.held_amount) }}</strong></div><div><span>已计采购成本</span><strong>{{ money(detail.spent_amount) }}</strong></div><div><span>预算余量</span><strong>{{ money(remaining(detail)) }}</strong></div></div>
    <p class="procurement-caption">预算余量可能为负数，不是供应商实时余额。{{ detail.state === 'paused' ? '账户已暂停；已发请求仍须完成成本记录和对账。' : '预算与权限仍在预占和派发时复核。' }}</p>
    <ErrorNotice :message="detailError" @retry="loadDetail()" />
    <el-tabs v-model="detailTab" class="procurement-tabs"><el-tab-pane label="采购预占与未知消费" name="reservations" /><el-tab-pane label="预算事件" name="events" /><el-tab-pane label="人工余额观察" name="balance-observations" /><el-tab-pane label="账户绑定" name="bindings" /></el-tabs>
    <template v-if="detailTab === 'reservations'">
      <div class="procurement-toolbar"><el-select v-model="reservationFilter" placeholder="当前页全部状态" clearable style="width:250px"><el-option label="消费未知，保留占用" value="unknown" /><el-option label="证据冲突，保留占用" value="disputed" /></el-select><span class="field-hint">筛选仅作用于当前页。</span></div>
      <el-table v-loading="detailBusy" :data="visibleReservations" empty-text="本页暂无对应采购预占">
        <el-table-column label="上游尝试 / 租户" min-width="210"><template #default="{ row }"><div class="mono" :title="row.attempt_id">{{ row.attempt_id }}</div><div class="mono small-text muted" :title="row.tenant_id">{{ shortId(row.tenant_id) }}</div></template></el-table-column>
        <el-table-column label="状态" min-width="205"><template #default="{ row }"><el-tag :type="['unknown','disputed'].includes(row.state) ? 'warning' : 'info'">{{ state(row.state) }}</el-tag><div v-if="row.anomaly" class="field-hint">异常已阻止新发送，请核查证据</div></template></el-table-column>
        <el-table-column label="原供应商用量界" min-width="220"><template #default="{ row }"><div v-for="bound in row.supplier_bounds.bounds" :key="bound.dimension">{{ bound.dimension === 'text_input_token' ? '输入' : '输出' }}：{{ bound.quantity }} {{ bound.unit }}</div><div class="mono small-text muted" :title="row.supplier_bounds.schema_id">schema {{ shortId(row.supplier_bounds.schema_id) }} · {{ row.variant_key }}</div></template></el-table-column>
        <el-table-column label="原采购金额上界" min-width="170"><template #default="{ row }">{{ money(row.upper_amount) }}</template></el-table-column><el-table-column label="保留占用" min-width="165"><template #default="{ row }">{{ money(row.held_amount) }}</template></el-table-column><el-table-column label="已计成本" min-width="165"><template #default="{ row }">{{ money(row.spent_amount) }}</template></el-table-column><el-table-column v-if="operator" label="证据对账" min-width="140" fixed="right"><template #default="{ row }"><el-button link type="primary" :disabled="['prepared','sending'].includes(row.state)" @click="openWriter('propose', null, null, row)">提交更正</el-button></template></el-table-column>
      </el-table>
    </template>
    <el-table v-else-if="detailTab === 'events'" v-loading="detailBusy" :data="events" empty-text="暂无采购预算事件"><el-table-column prop="type" label="事件类型" min-width="150" /><el-table-column label="尝试 ID" min-width="180"><template #default="{ row }"><span class="mono" :title="row.attempt_id">{{ shortId(row.attempt_id) }}</span></template></el-table-column><el-table-column label="占用变化" min-width="170"><template #default="{ row }">{{ money(row.held_delta) }}</template></el-table-column><el-table-column label="成本变化" min-width="170"><template #default="{ row }">{{ money(row.spent_delta) }}</template></el-table-column><el-table-column prop="evidence_ref" label="证据编号" min-width="180" /><el-table-column label="时间" min-width="185"><template #default="{ row }">{{ time(row.created_at) }}</template></el-table-column></el-table>
    <template v-else-if="detailTab === 'balance-observations'"><div class="procurement-toolbar"><el-button v-if="admin" type="primary" @click="openWriter('observe', detail)">追加人工观察</el-button><span class="field-hint">仅人工证据；不自动查供应商、不覆盖预算或占用。</span></div><el-table v-loading="detailBusy" :data="observations" empty-text="暂无人工余额观察"><el-table-column label="观察状态" min-width="145"><template #default="{ row }">{{ row.status === 'unknown' ? '余额未知' : state(row.status) }}</template></el-table-column><el-table-column label="人工观察金额" min-width="170"><template #default="{ row }">{{ money(row.amount) }}</template></el-table-column><el-table-column prop="source" label="来源" min-width="180" /><el-table-column prop="semantic" label="余额含义" min-width="190" /><el-table-column label="观察 / 供应商时刻" min-width="200"><template #default="{ row }">{{ time(row.observed_at) }}<div class="small-text muted">{{ time(row.provider_as_of) }}</div></template></el-table-column><el-table-column prop="evidence_ref" label="证据编号" min-width="180" /></el-table></template>
    <template v-else><div v-if="admin" class="procurement-toolbar"><el-button type="primary" @click="openWriter('bind', detail)">绑定已有凭据</el-button></div><el-table v-loading="detailBusy" :data="accountBindings" empty-text="暂无归属此账户的凭据"><el-table-column prop="credential_id" label="永久绑定凭据 ID" min-width="300" /><el-table-column prop="channel_id" label="渠道 ID" min-width="300" /><el-table-column label="绑定时间" min-width="190"><template #default="{ row }">{{ time(row.bound_at) }}</template></el-table-column></el-table></template>
    <div class="pagination-row"><span>每页事件与明细不代表全生命周期累计。</span><div><el-button :disabled="detailBusy || !detailTrail.length" @click="loadDetail(detailTrail[detailTrail.length - 1] || '', 'previous')">上一页</el-button><el-button :disabled="detailBusy || !detailNext" @click="loadDetail(detailNext, 'next')">下一页</el-button></div></div>
  </section>

  <section class="panel procurement-history"><div class="panel-header"><div><div class="panel-title">原操作回执与提案核查</div><div class="panel-subtitle">本标签页保留原操作 ID。关闭页面前请保存 ID；未知结果只读核查，不自动重放。</div></div></div>
    <div class="panel-body"><div class="procurement-query"><el-select v-model="queryKind" style="width:190px" @change="changeQuery"><el-option label="管理命令回执" value="operation" /><el-option label="采购更正提案" value="proposal" /></el-select><el-input v-model="queryID" placeholder="原操作 UUID；审批手动查询请填提案 ID" @input="changeQuery" /><el-button :loading="queryBusy" @click="inspect">只读查询</el-button></div><p v-if="queryCommand" class="field-hint">按本标签页保存的原命令查询；审批通过提案的 approval_operation_id 核对归属。</p><ErrorNotice :message="queryError" @retry="inspect" /><dl v-if="queryResult" class="detail-list procurement-query-result"><template v-if="'status' in queryResult"><dt>操作 ID</dt><dd class="mono">{{ queryResult.operation_id }}</dd><dt>持久结果</dt><dd>{{ queryResult.status === 'applied' ? '已应用' : '已拒绝' }} · {{ queryResult.reason || queryResult.kind }}</dd><dt>操作账号</dt><dd class="mono">{{ queryResult.actor }}</dd><dt>目标</dt><dd class="mono">{{ queryResult.target_id }}</dd></template><template v-else><dt>提案 ID</dt><dd class="mono">{{ queryResult.id }}</dd><dt>状态 / 最终成本</dt><dd>{{ state(queryResult.state) }} · {{ money(queryResult.amount) }}</dd><dt>证据 / 原因</dt><dd>{{ queryResult.evidence_ref }} · {{ queryResult.reason }}</dd><dt>审批操作 ID</dt><dd class="mono">{{ queryResult.approval_operation_id || '尚未审批' }}</dd><dt>审批账号</dt><dd class="mono">{{ queryResult.approver || '尚未审批' }}</dd></template></dl></div>
    <el-table :data="history" empty-text="本标签页当前身份暂无采购写命令"><el-table-column label="原操作 ID" min-width="300"><template #default="{ row }"><code>{{ row.command.operationID }}</code></template></el-table-column><el-table-column label="结果阶段" min-width="200"><template #default="{ row }">{{ state(row.state) }}<div v-if="row.result && 'status' in row.result">{{ row.result.status === 'rejected' ? '回执：已拒绝' : '回执：已应用' }}</div></template></el-table-column><el-table-column label="操作对象" min-width="190"><template #default="{ row }"><span class="mono">{{ row.command.target }}</span></template></el-table-column><el-table-column label="操作" width="130" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="selectHistory(row)">查原结果</el-button></template></el-table-column></el-table>
  </section>
  <ProcurementWriter v-model="writerOpen" :kind="writerKind" :account="writerAccount" :proposal="writerProposal" :attempt="writerAttempt" @applied="applied" @journal="journalTick++" />
  <el-dialog v-model="proposalOpen" title="采购成本更正提案" width="min(720px, calc(100vw - 28px))" destroy-on-close><ErrorNotice :message="proposalError" /><el-skeleton v-if="proposalBusy" :rows="5" animated /><dl v-if="viewedProposal" class="detail-list"><dt>提案 ID</dt><dd class="mono">{{ viewedProposal.id }}</dd><dt>租户 / 尝试</dt><dd class="mono">{{ viewedProposal.tenant_id }}<br />{{ viewedProposal.attempt_id }}</dd><dt>证据版本</dt><dd>{{ viewedProposal.expected_revision }}</dd><dt>最终成本 / 依据</dt><dd>{{ money(viewedProposal.amount) }} · {{ viewedProposal.basis === 'invoice' ? '供应商账单' : '合同' }}</dd><dt>证据引用</dt><dd>{{ viewedProposal.evidence_ref }}</dd><dt>更正原因</dt><dd>{{ viewedProposal.reason }}</dd><dt>制单账号</dt><dd class="mono">{{ viewedProposal.requester }}</dd><dt>审批账号</dt><dd class="mono">{{ viewedProposal.approver || '尚未审批' }}</dd><dt>审批操作 ID</dt><dd class="mono">{{ viewedProposal.approval_operation_id || '尚未审批' }}</dd><dt>状态</dt><dd>{{ state(viewedProposal.state) }}</dd></dl></el-dialog>
</template>

<style scoped>
.procurement-notice{margin-bottom:22px}.procurement-tabs{padding:0 22px}.procurement-toolbar{display:flex;gap:14px;align-items:center;flex-wrap:wrap;padding:0 22px 18px}.procurement-detail,.procurement-history{margin-top:26px}.procurement-metrics{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:18px;padding:24px}.procurement-metrics span{display:block;color:#8793a4;font-size:11px;margin-bottom:9px}.procurement-metrics strong{font-size:17px;font-weight:600;overflow-wrap:anywhere}.procurement-caption{padding:0 24px 22px;font-size:11px;color:#8793a4;line-height:1.9}.procurement-query{display:flex;gap:12px;align-items:center}.procurement-query .el-input{flex:1}.procurement-query-result{margin-top:20px}.detail-list dd,code,.mono{overflow-wrap:anywhere}.panel{overflow:hidden}.pagination-row{display:flex;justify-content:space-between;align-items:center;gap:16px;padding:18px 22px;font-size:11px;color:#8b98a9;border-top:1px solid #edf0f4}@media(max-width:700px){.procurement-metrics{grid-template-columns:1fr 1fr}.procurement-query{flex-wrap:wrap}.procurement-query .el-input{flex-basis:100%}.pagination-row{align-items:flex-start;flex-wrap:wrap}.procurement-metrics strong{font-size:14px}.panel-header{gap:14px;flex-wrap:wrap}}
</style>
