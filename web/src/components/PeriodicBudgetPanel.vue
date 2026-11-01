<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import PeriodicBudgetWriter from './PeriodicBudgetWriter.vue'
import { money } from '../format'
import { parsePeriodicAllocations, parsePeriodicBudget, parsePeriodicReceipt, parsePeriodicSingle, parsePeriodicWindows, periodicIncompleteWindow, periodicOperationPath, newPeriodicCommand } from '../periodic-budget'
import type { BudgetAllocation, BudgetWindow, PeriodicBudget, PeriodicInput, PeriodicPending, PeriodicReceipt, PeriodicTarget } from '../periodic-budget'
import { createPeriodicJournal } from '../periodic-budget-journal'
import { createPeriodicEpoch, editingPeriodicReview, failPeriodicReview, periodicPageTransition, periodicPollDelay, periodicReviewBlocked, resumePeriodicReview } from '../periodic-budget-state'
import { PeriodicBudgetError, periodicIdentityFailure, periodicRequest } from '../periodic-budget-transport'

const props = defineProps<{ target: PeriodicTarget; actor: string; csrf: string; context: string; canWrite: boolean }>()
const emit = defineEmits<{ 'identity-check': [] }>()
const gate = createPeriodicEpoch(), journal = createPeriodicJournal()
gate.auth(props.context); gate.view(props.target.path)
const budget = ref<PeriodicBudget | null>(null), viewBusy = ref(false), viewError = ref(''), storageError = ref(''), commandError = ref('')
const storageReady = ref(false), writer = ref(false), saving = ref(false), inspectBusy = ref(false), identityBlocked = ref(false), needsRefresh = ref(false)
const review = ref(editingPeriodicReview()), controlID = ref<string | undefined>()
const tab = ref('windows'), windows = ref<BudgetWindow[]>([]), allocations = ref<BudgetAllocation[]>([])
const listBusy = ref(false), listError = ref(''), nextCursor = ref(''), cursorStack = ref<string[]>([]), page = ref(1)
let pollTimer: ReturnType<typeof setTimeout> | undefined, pollAttempt = 0, retryNotBefore = 0, disposed = false
const blocked = computed(() => periodicReviewBlocked(review.value))
const writeReady = computed(() => props.canWrite && !!budget.value && storageReady.value && !blocked.value && !saving.value && !identityBlocked.value && !needsRefresh.value && !viewBusy.value)
const labels: Record<string, string> = { unconfigured: '尚未配置', scheduled: '下周期生效', active: '已启用', disabled: '已停用', held: '占用中', disputed: '待对账', settled: '已结算', released: '已释放', customer_request: '客户请求', supplier_attempt: '采购尝试', legacy_customer: '历史客户占用', legacy_supplier: '历史采购占用' }
function message(cause: unknown): string { return cause instanceof Error ? cause.message : '操作未完成，请保留原编号核查。' }
function stopPoll() { clearTimeout(pollTimer); pollTimer = undefined }
function available() { return !disposed && !identityBlocked.value && document.visibilityState === 'visible' && navigator.onLine }
function identityFailure(cause: unknown) {
  if (!periodicIdentityFailure(cause)) return
  identityBlocked.value = true; stopPoll(); gate.cancel('operation'); inspectBusy.value = false
  if (cause instanceof PeriodicBudgetError && cause.code === 'reauth_required') window.dispatchEvent(new CustomEvent('moni:reauth'))
  emit('identity-check')
}
async function loadBudget() {
  const ticket = gate.ticket('view'); viewBusy.value = true; viewError.value = ''
  try {
    const value = await periodicRequest(props.target.path, { signal: ticket.signal, decode: raw => parsePeriodicSingle(raw, data => parsePeriodicBudget(data, props.target)) })
    if (!gate.accepts(ticket)) return
    budget.value = value; needsRefresh.value = false
  } catch (cause) { if (gate.accepts(ticket)) { viewError.value = message(cause); identityFailure(cause) } }
  finally { if (gate.accepts(ticket)) viewBusy.value = false }
}
async function loadList(cursor = '', direction: 'first' | 'next' | 'previous' = 'first') {
  const kind = tab.value === 'windows' ? 'windows' : 'allocations'
  if (direction === 'first') { gate.cancel('windows'); gate.cancel('allocations'); windows.value = []; allocations.value = []; nextCursor.value = ''; cursorStack.value = []; page.value = 1 }
  const ticket = gate.ticket(kind); listBusy.value = true; listError.value = ''
  try {
    const path = `${props.target.path}/${kind}${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`
    const result = await periodicRequest(path, { signal: ticket.signal, decode: raw => kind === 'windows' ? parsePeriodicWindows(raw) : parsePeriodicAllocations(raw, props.target.scope) })
    if (!gate.accepts(ticket) || kind !== tab.value) return
    const nextPath = periodicPageTransition(cursorStack.value, cursor, result.next, direction)
    if (kind === 'windows') windows.value = result.rows as BudgetWindow[]; else allocations.value = result.rows as BudgetAllocation[]
    nextCursor.value = result.next
    cursorStack.value = nextPath; page.value = nextPath.length + 1
  } catch (cause) { if (gate.accepts(ticket)) { listError.value = message(cause); identityFailure(cause) } }
  finally { if (gate.accepts(ticket)) listBusy.value = false }
}
async function restore() {
  const ticket = gate.ticket('command'); storageReady.value = false; storageError.value = ''
  try {
    const pending = await journal.load(props.actor, props.target.identityScope, props.target.path)
    if (!gate.accepts(ticket)) return
    storageReady.value = true
    if (pending) { review.value = resumePeriodicReview(pending); schedulePoll() }
  } catch (cause) { if (gate.accepts(ticket)) storageError.value = message(cause) }
}
async function cleanup(pending: PeriodicPending) {
  try {
    const cleared = await journal.finish(pending)
    if (disposed || review.value.pending?.operation_id !== pending.operation_id) return
    if (!cleared) { storageError.value = '此目标已有另一原操作标识，不能由晚回结果清除。请重新进入页面核查该标识。'; return }
    review.value = { ...review.value, cleanupBlocked: false }; storageError.value = ''
  } catch (cause) { if (!disposed) storageError.value = `已收到终态，但尚未可靠清理待核查标识，暂不能新提交。${message(cause)}` }
}
async function confirmed(receipt: PeriodicReceipt, pending: PeriodicPending) {
  stopPoll(); controlID.value = receipt.operation.resource_id
  review.value = { state: 'confirmed', pending, receipt, cleanupBlocked: true }; writer.value = false; commandError.value = ''
  await cleanup(pending)
  if (!disposed) void loadBudget()
}
async function configure(input: Omit<PeriodicInput, 'operation_id'>) {
  if (!writeReady.value) return
  gate.command(); const ticket = gate.ticket('command'); saving.value = true; commandError.value = ''; let pending: PeriodicPending | undefined
  try {
    const command = await newPeriodicCommand(props.target, props.actor, input)
    if (!gate.accepts(ticket)) return
    const admission = await journal.begin(command.pending)
    if (!gate.accepts(ticket)) return
    pending = admission.pending
    if (!admission.inserted) { review.value = resumePeriodicReview(pending); writer.value = false; commandError.value = '该目标已有待核查操作，已恢复原编号；没有发送新命令。'; schedulePoll(); return }
    review.value = { state: 'submitting', pending, receipt: null, cleanupBlocked: true }
    const receipt = await periodicRequest(props.target.path, { method: 'PUT', body: command.body, csrf: props.csrf, signal: ticket.signal, decode: raw => parsePeriodicSingle(raw, data => parsePeriodicReceipt(data, command.pending, controlID.value)) })
    if (!gate.accepts(ticket)) return
    await confirmed(receipt, pending)
  } catch (cause) {
    if (!gate.accepts(ticket)) return
    commandError.value = message(cause)
    if (!pending) { storageReady.value = false; storageError.value = message(cause); return }
    review.value = failPeriodicReview(review.value, cause); writer.value = false
    if (review.value.state === 'known_failure') { await cleanup(pending); if (cause instanceof PeriodicBudgetError && cause.status === 409) { needsRefresh.value = true; void loadBudget() } }
    else schedulePoll()
    identityFailure(cause)
  } finally { if (gate.accepts(ticket)) saving.value = false }
}
function schedulePoll(retryAfterMs = 0) {
  stopPoll()
  retryNotBefore = Math.max(retryNotBefore, Date.now() + retryAfterMs)
  if (!available() || inspectBusy.value || review.value.state !== 'unknown' || !review.value.pending) return
  const delay = periodicPollDelay(pollAttempt, Math.max(0, retryNotBefore - Date.now()))
  // Browsers turn overflowing setTimeout values into an immediate timer. Split
  // long Retry-After waits and preserve the deadline across hide/online events.
  pollTimer = setTimeout(() => { pollTimer = undefined; if (Date.now() < retryNotBefore) schedulePoll(); else void inspect() }, Math.min(delay, 2147483647))
}
async function inspect() {
  const pending = review.value.pending
  if (!pending || review.value.state !== 'unknown' || inspectBusy.value || !available()) return
  if (Date.now() < retryNotBefore) { schedulePoll(); return }
  stopPoll(); const ticket = gate.ticket('operation'); inspectBusy.value = true; let retryAfterMs = 0
  try {
    const receipt = await periodicRequest(periodicOperationPath(pending), { signal: ticket.signal, decode: raw => parsePeriodicSingle(raw, data => parsePeriodicReceipt(data, pending, controlID.value)) })
    if (!gate.accepts(ticket)) return
    await confirmed(receipt, pending)
  } catch (cause) {
    if (!gate.accepts(ticket)) return
    commandError.value = message(cause)
    if (cause instanceof PeriodicBudgetError) retryAfterMs = cause.retryAfterMs
    identityFailure(cause)
  } finally { if (gate.accepts(ticket)) { inspectBusy.value = false; pollAttempt++; schedulePoll(retryAfterMs) } }
}
function availabilityChanged() {
  if (!available()) { stopPoll(); gate.cancel('operation'); inspectBusy.value = false }
  else schedulePoll()
}
function refresh() { void loadBudget(); void loadList() }
function edit() { if (!writeReady.value) return; review.value = editingPeriodicReview(); commandError.value = ''; writer.value = true }
onMounted(() => { void restore(); void loadBudget(); void loadList(); document.addEventListener('visibilitychange', availabilityChanged); window.addEventListener('online', availabilityChanged); window.addEventListener('offline', availabilityChanged) })
onBeforeUnmount(() => { disposed = true; stopPoll(); gate.dispose(); journal.close(); document.removeEventListener('visibilitychange', availabilityChanged); window.removeEventListener('online', availabilityChanged); window.removeEventListener('offline', availabilityChanged); budget.value = null; review.value = editingPeriodicReview(); controlID.value = undefined })
</script>
<template>
  <div class="periodic-budget-panel">
    <div class="periodic-toolbar"><span class="mono">{{ target.id }}</span><div><el-button :icon="Refresh" :loading="viewBusy" @click="refresh">刷新当前状态</el-button><el-button v-if="canWrite" type="primary" :disabled="!writeReady" @click="edit">配置预算</el-button></div></div>
    <el-alert v-if="target.kind === 'wallet'" type="info" title="租户预算由平台管理，此处只读；API Key 另有独立预算，两者都会参与调用准入。" :closable="false" />
    <el-alert v-if="viewError" type="error" :title="viewError" :closable="false" />
    <el-alert v-if="storageError" type="error" :title="storageError" :closable="false" />
    <el-alert v-if="identityBlocked" type="warning" title="已停止原操作自动核查。请完成登录、再认证或恢复权限后，重新进入此页核查原编号。" :closable="false" />
    <section v-if="budget" class="panel periodic-summary">
      <div class="panel-header"><div><div class="panel-title">{{ labels[budget.state] }}</div><div class="panel-subtitle">版本 {{ budget.revision }} · {{ budget.period_kind === 'utc_day' ? 'UTC 自然日' : budget.period_kind === 'utc_month' ? 'UTC 自然月' : '尚无周期账簿' }}</div></div><span class="small-text muted">观察时刻 {{ budget.as_of }}</span></div>
      <div class="periodic-metrics"><div><span>每周期限额</span><strong>{{ money(budget.limit_amount) }}</strong></div><div><span>当前周期已计入消费</span><strong>{{ money(budget.current_window?.spent_amount) }}</strong></div><div><span>当前周期占用</span><strong>{{ money(budget.current_window?.held_amount) }}</strong></div><div><span>跨周期未决占用</span><strong>{{ money(budget.carry_held) }}</strong></div></div>
      <dl class="detail-list periodic-details"><dt>限额开始生效</dt><dd>{{ budget.effective_from || '尚未配置' }}</dd><dt>开始跟踪</dt><dd>{{ budget.tracking_started_at || '尚无周期账簿，不代表没有业务占用' }}</dd><dt>当前窗口</dt><dd>{{ budget.current_window ? `${budget.current_window.start_at} 至 ${budget.current_window.end_at}` : '当前尚无已记录窗口' }}<div v-if="budget.current_window && periodicIncompleteWindow(budget.current_window, budget)" class="small-text muted">首次跟踪的不完整周期</div></dd></dl>
      <p class="periodic-caption">消费退款不会释放周期额度。停用限额仍保留跟踪；历史未决占用延续到后续周期，供应商最终成本可以高于原预估。</p>
    </section>
    <section v-if="review.pending" class="panel periodic-operation">
      <div class="panel-header"><div><div class="panel-title">{{ review.state === 'confirmed' ? '已确认原操作' : review.state === 'known_failure' ? '原命令明确未通过' : review.state === 'submitting' ? '原命令提交中' : '原操作结果待核查' }}</div><div class="panel-subtitle mono">{{ review.pending.operation_id }}</div></div><el-button v-if="review.state === 'unknown'" :loading="inspectBusy" :disabled="identityBlocked" @click="inspect">只读查询原回执</el-button><el-button v-if="['confirmed', 'known_failure'].includes(review.state) && review.cleanupBlocked" @click="cleanup(review.pending)">重新确认标识清理</el-button></div>
      <div class="panel-body"><el-alert v-if="commandError" :title="commandError" type="warning" :closable="false" /><p v-if="review.state === 'unknown' || review.state === 'submitting'" class="field-hint">本目标不能再次提交。刷新、404 或等待很久都不能证明原命令失败；仅按原编号核查，页面可见且在线时自动退避查询。</p><dl v-if="review.receipt" class="detail-list"><dt>原结果版本</dt><dd>{{ review.receipt.operation.revision }}</dd><dt>原结果时刻</dt><dd>{{ review.receipt.result.as_of }}</dd><dt>原结果限额</dt><dd>{{ money(review.receipt.result.limit_amount) }} · {{ review.receipt.result.enabled ? '启用' : '停用' }}</dd><dt>控制器 ID</dt><dd class="mono">{{ review.receipt.operation.resource_id }}</dd></dl><p v-if="review.receipt" class="field-hint">这是原操作的不可变历史快照，上方当前状态可能已经变化。</p></div>
    </section>
    <section class="panel periodic-history"><el-tabs v-model="tab" class="periodic-tabs" @tab-change="loadList()"><el-tab-pane label="周期窗口" name="windows" /><el-tab-pane label="占用明细" name="allocations" /></el-tabs><el-alert v-if="listError" :title="listError" type="error" :closable="false" />
      <el-table v-if="tab === 'windows'" v-loading="listBusy" :data="windows" empty-text="暂无已记录周期窗口"><el-table-column label="周期开始（UTC）" min-width="240"><template #default="{ row }">{{ row.start_at }}<div v-if="budget && periodicIncompleteWindow(row, budget)" class="small-text muted">首次跟踪的不完整周期</div></template></el-table-column><el-table-column prop="end_at" label="周期结束（UTC）" min-width="240" /><el-table-column label="已计入消费" min-width="180"><template #default="{ row }">{{ money(row.spent_amount) }}</template></el-table-column><el-table-column label="占用" min-width="180"><template #default="{ row }">{{ money(row.held_amount) }}</template></el-table-column></el-table>
      <el-table v-else v-loading="listBusy" :data="allocations" empty-text="暂无占用明细"><el-table-column label="来源 / 状态" min-width="160"><template #default="{ row }">{{ labels[row.subject_kind] }}<div class="small-text muted">{{ labels[row.state] }}</div></template></el-table-column><el-table-column prop="subject_id" label="来源 ID" min-width="300" /><el-table-column label="原冻结上界" min-width="180"><template #default="{ row }">{{ money(row.upper_amount) }}</template></el-table-column><el-table-column label="占用 / 已计入消费" min-width="210"><template #default="{ row }">{{ money(row.held_amount) }}<div>{{ money(row.spent_amount) }}</div></template></el-table-column><el-table-column label="窗口 / 创建时刻" min-width="300"><template #default="{ row }"><span class="mono">{{ row.window_id || '历史占用（无窗口）' }}</span><div class="small-text muted">{{ row.created_at }}</div></template></el-table-column></el-table>
      <div class="periodic-pagination"><span>第 {{ page }} 页 · 每次读取独立快照，跨页期间消费可能变化。</span><div><el-button :disabled="listBusy || page <= 1" @click="loadList(cursorStack[cursorStack.length - 2] || '', 'previous')">上一页</el-button><el-button :disabled="listBusy || !nextCursor" @click="loadList(nextCursor, 'next')">下一页</el-button></div></div>
    </section>
    <PeriodicBudgetWriter v-if="budget" v-model="writer" :budget="budget" :busy="saving" :allowed="writeReady || saving" @configure="configure" />
  </div>
</template>
<style scoped>.periodic-toolbar{display:flex;justify-content:space-between;align-items:center;gap:16px;margin-bottom:22px}.periodic-toolbar>.mono{overflow-wrap:anywhere;color:#7f8b9c;font-size:12px}.periodic-summary,.periodic-operation,.periodic-history{margin-top:24px}.periodic-metrics{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:22px;padding:24px}.periodic-metrics span{display:block;font-size:11px;color:#8793a4;margin-bottom:10px}.periodic-metrics strong{font-size:18px;overflow-wrap:anywhere}.periodic-details{padding:0 24px}.periodic-caption{padding:4px 24px 24px;color:#8793a4;font-size:11px;line-height:1.9}.periodic-tabs{padding:0 22px}.periodic-pagination{display:flex;justify-content:space-between;align-items:center;gap:16px;padding:18px 22px;font-size:11px;color:#8b98a9;border-top:1px solid #edf0f4}.mono,.detail-list dd{overflow-wrap:anywhere}.panel-header{gap:16px;flex-wrap:wrap}.panel{overflow:hidden}.el-alert{margin-bottom:14px}@media(max-width:700px){.periodic-toolbar,.periodic-pagination{align-items:flex-start;flex-wrap:wrap}.periodic-metrics{grid-template-columns:1fr 1fr}.periodic-metrics strong{font-size:15px}}</style>
