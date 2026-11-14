<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, Search } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import { errorText, request, stableOperationKey } from '../api'
import { shortId, time } from '../format'
import { isAdmin, isApprover, session } from '../session'
import { cashRefundActionPath, cashRefundBusinessPath, cashRefundCompleteBody, cashRefundCreateBody, cashRefundExecuteBody, cashRefundReasonBody, cashRefundsPath, parseCashRefund, parseCashRefundPage, type CashRefundAction, type CashRefundOrder } from '../cash-refunds'
import { parseWalletOperatorSettings } from '../wallet-operator-settings'

const rows = ref<CashRefundOrder[]>([]), loading = ref(false), error = ref(''), nextCursor = ref(''), cursorHistory = ref<string[]>([])
const filters = ref({ tenantID: '', state: '', businessRef: '' })
const detail = ref<CashRefundOrder | null>(null), detailOpen = ref(false)
const createOpen = ref(false), createBusy = ref(false), createError = ref('')
const createForm = ref({ tenantID: '', lotID: '', businessRef: '', reason: '', requestID: '' })
const action = ref<CashRefundAction>('approve'), actionOpen = ref(false), actionBusy = ref(false), actionError = ref('')
const actionForm = ref({ transferOrderRef: '', transferProofRef: '', paidFiatAmount: '', reason: '' })
const businessBusy = ref(false), businessError = ref('')
const singleOperator = ref(false), disposed = ref(false)
let listEpoch = 0, detailEpoch = 0
const stateLabels: Record<string, string> = { requested: '待审批', approved: '已冻结', transfer_executed: '已执行转账', completed: '已完成', cancelled: '已取消', failed: '转账失败' }
const actionLabels: Record<CashRefundAction, string> = { approve: '批准并冻结', execute: '登记转账执行', complete: '确认完成并退出', cancel: '取消并释放', fail: '失败并释放' }
const canCreate = computed(() => session.me?.user.platform_role === 'finance_operator' || isAdmin.value && singleOperator.value)
const financeRole = computed(() => session.me?.user.platform_role || '')
const currentDetail = computed(() => detail.value)

function stateText(state: string) { return stateLabels[state] || state }
function amount(value: string | null, unit: string) { return value === null ? '—' : `${value} 微${unit}` }
function requestKey(path: string, body: unknown) { return stableOperationKey(path, body) }
async function loadSettings() {
  if (!isAdmin.value) { singleOperator.value = false; return }
  try { const response = await request<unknown>('/platform/wallet-operator-settings'); if (!disposed.value) singleOperator.value = parseWalletOperatorSettings(response.data).current.single_operator }
  catch { if (!disposed.value) singleOperator.value = false }
}
async function load(cursor = '') {
  const ticket = ++listEpoch
  loading.value = true; error.value = ''
  try {
    const path = cashRefundsPath({ ...filters.value, cursor })
    const response = await request<unknown>(path)
    if (ticket !== listEpoch || disposed.value) return
    const page = parseCashRefundPage(response.data, response.next_cursor)
    rows.value = page.rows; nextCursor.value = page.next
    if (cursor && cursorHistory.value.at(-1) !== cursor) cursorHistory.value.push(cursor)
  } catch (cause) { if (ticket === listEpoch) error.value = errorText(cause) }
  finally { if (ticket === listEpoch) loading.value = false }
}
async function refresh() { cursorHistory.value = []; nextCursor.value = ''; await load() }
async function nextPage() { if (nextCursor.value) await load(nextCursor.value) }
async function previousPage() { cursorHistory.value.pop(); await load(cursorHistory.value.at(-1) || '') }
async function openDetail(row: CashRefundOrder) {
  const ticket = ++detailEpoch
  businessError.value = ''
  try {
    const response = await request<unknown>(`/platform/cash-refunds/${encodeURIComponent(row.id)}`)
    if (ticket !== detailEpoch || disposed.value) return
    detail.value = parseCashRefund(response.data); detailOpen.value = true
  } catch (cause) { businessError.value = errorText(cause); ElMessage.error(businessError.value) }
}
async function readBusiness() {
  businessBusy.value = true; businessError.value = ''
  try {
    const response = await request<unknown>(cashRefundBusinessPath(filters.value.businessRef))
    detail.value = parseCashRefund(response.data); detailOpen.value = true
  } catch (cause) { businessError.value = errorText(cause); detailOpen.value = false }
  finally { businessBusy.value = false }
}
function openCreate() {
  createForm.value = { tenantID: filters.value.tenantID, lotID: '', businessRef: '', reason: '', requestID: '' }
  createError.value = ''; createOpen.value = true
}
async function create() {
  createBusy.value = true; createError.value = ''
  try {
    const body = cashRefundCreateBody({ tenant_id: createForm.value.tenantID, lot_id: createForm.value.lotID, business_ref: createForm.value.businessRef, reason: createForm.value.reason, request_id: createForm.value.requestID })
    const response = await request<unknown>('/platform/cash-refunds', 'POST', body, requestKey('/platform/cash-refunds', body))
    const order = parseCashRefund(response.data)
    createOpen.value = false; ElMessage.success('现金退款申请已创建，尚未产生钱包效果。')
    await refresh(); await openDetail(order)
  } catch (cause) { createError.value = errorText(cause) }
  finally { createBusy.value = false }
}
function canAction(row: CashRefundOrder, name: CashRefundAction): boolean {
  const role = financeRole.value
  if (name === 'approve') {
    if (row.state !== 'requested') return false
    if (isApprover.value) return row.maker !== session.me?.user.id && row.requested_by !== session.me?.user.id
    return isAdmin.value && singleOperator.value
  }
  if (name === 'execute') return row.state === 'approved' && (role === 'finance_operator' || isAdmin.value && (singleOperator.value || row.single_operator))
  if (name === 'complete') return row.state === 'transfer_executed' && ['finance_operator', 'finance_approver', 'admin'].includes(role)
  if (name === 'cancel') return ['requested', 'approved'].includes(row.state) && ['finance_operator', 'finance_approver', 'admin'].includes(role)
  if (name === 'fail') return ['approved', 'transfer_executed'].includes(row.state) && ['finance_operator', 'finance_approver', 'admin'].includes(role)
  return false
}
function openAction(row: CashRefundOrder, name: CashRefundAction) {
  detail.value = row; action.value = name
  actionForm.value = { transferOrderRef: row.transfer_order_ref, transferProofRef: row.transfer_proof_ref, paidFiatAmount: row.fiat_amount || '', reason: row.reason }
  actionError.value = ''; actionOpen.value = true
}
async function submitAction() {
  if (!detail.value) return
  actionBusy.value = true; actionError.value = ''
  try {
    const row = detail.value, name = action.value, path = cashRefundActionPath(row.id, name)
    let body: unknown = {}
    if (name === 'execute') body = cashRefundExecuteBody({ transfer_order_ref: actionForm.value.transferOrderRef })
    if (name === 'complete') body = cashRefundCompleteBody({ transfer_proof_ref: actionForm.value.transferProofRef, paid_fiat_amount: actionForm.value.paidFiatAmount })
    if (name === 'cancel' || name === 'fail') body = cashRefundReasonBody({ reason: actionForm.value.reason })
    if (name === 'approve' && isAdmin.value && row.maker === session.me?.user.id) {
      await ElMessageBox.confirm('你正在以 admin 同时制单并审批。该操作会记录 single_operator_exception，不属于双人审批；确认冻结该来源全部可用币吗？', '单人运营例外', { type: 'warning', confirmButtonText: '确认批准并冻结', cancelButtonText: '取消' })
    } else if (name === 'approve') {
      await ElMessageBox.confirm('批准将按当前可用币全额冻结，并按冻结时刻的币价计算退款额。确认已核对原始付款与角色吗？', '确认现金退款审批', { type: 'warning', confirmButtonText: '批准并冻结', cancelButtonText: '取消' })
    } else if (name === 'complete') {
      await ElMessageBox.confirm('确认线下转账已完成且实付金额与冻结金额一致吗？完成后币将永久退出，不能通过本单回滚。', '确认现金退款完成', { type: 'warning', confirmButtonText: '确认完成并退出', cancelButtonText: '取消' })
    }
    const response = await request<unknown>(path, 'POST', body, requestKey(path, body))
    const updated = parseCashRefund(response.data)
    detail.value = updated; actionOpen.value = false
    ElMessage.success(`${actionLabels[name]}完成。`)
    await refresh()
  } catch (cause) { actionError.value = errorText(cause) }
  finally { actionBusy.value = false }
}
watch(() => session.me?.user.id, () => { listEpoch++; detailEpoch++; rows.value = []; detailOpen.value = false; actionOpen.value = false; createOpen.value = false; void loadSettings(); void refresh() })
onMounted(() => { void loadSettings(); void refresh() })
onBeforeUnmount(() => { disposed.value = true; listEpoch++; detailEpoch++ })
</script>

<template>
  <div class="page-stack">
    <PageHeader eyebrow="CASH REFUNDS" title="现金退款" description="先冻结全部可用付费币，线下转账确认后再退出。申请与批准分离，失败或取消无损释放；本页不触发或验证外部转账。">
      <template #actions><el-button :icon="Refresh" :loading="loading" @click="refresh">刷新</el-button><el-button v-if="canCreate" type="primary" :icon="Plus" @click="openCreate">新建现金退款</el-button></template>
    </PageHeader>
    <ErrorNotice v-if="error" :message="error" />
    <ErrorNotice v-if="businessError" :message="businessError" />
    <section class="panel">
      <div class="panel-header"><div><div class="panel-title">退款单与冻结状态</div><div class="panel-subtitle">requested 展示预览；批准后才展示冻结币数、法币与 journal。可按业务号只读回查未知结果。</div></div></div>
      <div class="panel-body page-stack">
        <el-form inline @submit.prevent>
          <el-form-item label="客户 ID"><el-input v-model="filters.tenantID" placeholder="可选租户 UUID" clearable style="width: 260px" /></el-form-item>
          <el-form-item label="状态"><el-select v-model="filters.state" clearable placeholder="全部状态" style="width: 160px"><el-option v-for="(label, value) in stateLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="业务号"><el-input v-model="filters.businessRef" placeholder="精确业务号，可用于未知结果回查" clearable style="width: 280px" /></el-form-item>
          <el-form-item><el-button type="primary" :icon="Search" @click="refresh">筛选</el-button><el-button :loading="businessBusy" :disabled="!filters.businessRef" @click="readBusiness">按业务号回查</el-button></el-form-item>
        </el-form>
        <el-table v-loading="loading" :data="rows" row-key="id" empty-text="暂无现金退款单" @row-click="openDetail">
          <el-table-column prop="business_ref" label="业务号" min-width="210" />
          <el-table-column label="客户" min-width="145"><template #default="{ row }">{{ row.tenant_name || shortId(row.tenant_id) }}</template></el-table-column>
          <el-table-column label="状态" width="125"><template #default="{ row }"><el-tag :type="row.state === 'completed' ? 'success' : row.state === 'failed' ? 'danger' : row.state === 'cancelled' ? 'info' : 'warning'">{{ stateText(row.state) }}</el-tag></template></el-table-column>
          <el-table-column label="预览币数" min-width="140"><template #default="{ row }">{{ amount(row.preview_coin_amount, '币') }}</template></el-table-column>
          <el-table-column label="冻结币数" min-width="140"><template #default="{ row }">{{ amount(row.coin_amount, '币') }}</template></el-table-column>
          <el-table-column label="预览 / 冻结法币" min-width="185"><template #default="{ row }">{{ row.state === 'requested' ? amount(row.preview_fiat_amount, row.preview_fiat_currency) : amount(row.fiat_amount, row.fiat_currency) }}</template></el-table-column>
          <el-table-column label="批准时间" min-width="180"><template #default="{ row }">{{ row.approved_at ? time(row.approved_at) : '—' }}</template></el-table-column>
          <el-table-column label="操作" min-width="260" fixed="right"><template #default="{ row }"><el-button link type="primary" @click.stop="openDetail(row)">详情</el-button><el-button v-for="name in (['approve','execute','complete','cancel','fail'] as CashRefundAction[])" v-show="canAction(row, name)" :key="name" link type="primary" @click.stop="openAction(row, name)">{{ actionLabels[name] }}</el-button></template></el-table-column>
        </el-table>
        <div class="pager"><el-button :disabled="!cursorHistory.length" @click="previousPage">上一页</el-button><el-button :disabled="!nextCursor" @click="nextPage">下一页</el-button></div>
      </div>
    </section>

    <el-dialog v-model="createOpen" title="新建现金退款申请" width="560px" :close-on-click-modal="false">
      <el-alert type="info" :closable="false" title="只允许 paid + cash_original 来源；不填写币数或法币金额，批准时系统按当时全部可用币计算。requested 只有预检与预览。" />
      <el-form label-position="top" @submit.prevent="create">
        <el-form-item label="客户租户 UUID" required><el-input v-model="createForm.tenantID" placeholder="客户租户 UUID" /></el-form-item>
        <el-form-item label="付费来源 lot UUID" required><el-input v-model="createForm.lotID" placeholder="人工充值 paid 来源，不是赠送或开账来源" /></el-form-item>
        <el-form-item label="退款业务号" required><el-input v-model="createForm.businessRef" placeholder="受控唯一业务号，用于未知结果回查" /></el-form-item>
        <el-form-item label="关联客户申请 UUID（可选）"><el-input v-model="createForm.requestID" placeholder="同租户 cash_refund/open 申请" /></el-form-item>
        <el-form-item label="退款原因" required><el-input v-model="createForm.reason" type="textarea" :rows="3" maxlength="200" show-word-limit /></el-form-item>
      </el-form>
      <ErrorNotice v-if="createError" :message="createError" />
      <template #footer><el-button @click="createOpen = false">取消</el-button><el-button type="primary" :loading="createBusy" @click="create">创建申请</el-button></template>
    </el-dialog>

    <el-dialog v-model="actionOpen" :title="actionLabels[action]" width="560px" :close-on-click-modal="false">
      <template v-if="currentDetail">
        <el-descriptions :column="1" border>
          <el-descriptions-item label="业务号">{{ currentDetail.business_ref }}</el-descriptions-item>
          <el-descriptions-item label="当前状态">{{ stateText(currentDetail.state) }}</el-descriptions-item>
          <el-descriptions-item v-if="currentDetail.coin_amount" label="冻结币数">{{ amount(currentDetail.coin_amount, '币') }}</el-descriptions-item>
          <el-descriptions-item v-if="currentDetail.fiat_amount" label="冻结法币">{{ amount(currentDetail.fiat_amount, currentDetail.fiat_currency) }}</el-descriptions-item>
        </el-descriptions>
        <el-form v-if="action !== 'approve'" label-position="top" @submit.prevent="submitAction">
          <el-form-item v-if="action === 'execute'" label="线下转账指令编号" required><el-input v-model="actionForm.transferOrderRef" placeholder="不是最终付款凭证" /></el-form-item>
          <template v-if="action === 'complete'">
            <el-form-item label="已完成转账凭证" required><el-input v-model="actionForm.transferProofRef" placeholder="受控凭证编号" /></el-form-item>
            <el-form-item label="实际转出法币（微单位）" required><el-input v-model="actionForm.paidFiatAmount" placeholder="必须与冻结金额完全一致" /></el-form-item>
          </template>
          <el-form-item v-if="action === 'cancel' || action === 'fail'" :label="action === 'cancel' ? '取消原因' : '转账失败原因'" required><el-input v-model="actionForm.reason" type="textarea" :rows="3" maxlength="200" show-word-limit /></el-form-item>
        </el-form>
      </template>
      <ErrorNotice v-if="actionError" :message="actionError" />
      <template #footer><el-button @click="actionOpen = false">关闭</el-button><el-button type="primary" :loading="actionBusy" @click="submitAction">{{ actionLabels[action] }}</el-button></template>
    </el-dialog>

    <el-drawer v-model="detailOpen" title="现金退款详情" size="560px">
      <template v-if="currentDetail">
        <el-descriptions :column="1" border>
          <el-descriptions-item label="订单 ID">{{ currentDetail.id }}</el-descriptions-item>
          <el-descriptions-item label="业务号">{{ currentDetail.business_ref }}</el-descriptions-item>
          <el-descriptions-item label="来源 lot">{{ currentDetail.lot_id }}</el-descriptions-item>
          <el-descriptions-item label="状态">{{ stateText(currentDetail.state) }}</el-descriptions-item>
          <el-descriptions-item label="申请人">{{ currentDetail.requested_by }}</el-descriptions-item>
          <el-descriptions-item label="制单人">{{ currentDetail.maker }}</el-descriptions-item>
          <el-descriptions-item label="审批人">{{ currentDetail.approver || '—' }}</el-descriptions-item>
          <el-descriptions-item label="转账执行人">{{ currentDetail.transfer_executor || '—' }}</el-descriptions-item>
          <el-descriptions-item label="预览币数">{{ amount(currentDetail.preview_coin_amount, '币') }}</el-descriptions-item>
          <el-descriptions-item label="冻结币数">{{ amount(currentDetail.coin_amount, '币') }}</el-descriptions-item>
          <el-descriptions-item label="预览法币">{{ amount(currentDetail.preview_fiat_amount, currentDetail.preview_fiat_currency) }}</el-descriptions-item>
          <el-descriptions-item label="冻结法币">{{ amount(currentDetail.fiat_amount, currentDetail.fiat_currency) }}</el-descriptions-item>
          <el-descriptions-item label="买入币价版本">{{ currentDetail.buy_rate_version_id || '—' }}</el-descriptions-item>
          <el-descriptions-item label="退款币价版本">{{ currentDetail.refund_rate_version_id || '—' }}</el-descriptions-item>
          <el-descriptions-item label="批准时间">{{ currentDetail.approved_at ? time(currentDetail.approved_at) : '—' }}</el-descriptions-item>
          <el-descriptions-item label="hold journal">{{ currentDetail.hold_journal_id || '—' }}</el-descriptions-item>
          <el-descriptions-item label="retire journal">{{ currentDetail.retire_journal_id || '—' }}</el-descriptions-item>
          <el-descriptions-item label="release journal">{{ currentDetail.release_journal_id || '—' }}</el-descriptions-item>
          <el-descriptions-item label="转账指令">{{ currentDetail.transfer_order_ref || '—' }}</el-descriptions-item>
          <el-descriptions-item label="转账凭证">{{ currentDetail.transfer_proof_ref || '—' }}</el-descriptions-item>
          <el-descriptions-item label="实付法币">{{ amount(currentDetail.paid_fiat_amount, currentDetail.fiat_currency) }}</el-descriptions-item>
          <el-descriptions-item label="客户申请">{{ currentDetail.request_id || '—' }}</el-descriptions-item>
          <el-descriptions-item label="单人运营例外">{{ currentDetail.single_operator_exception ? '是（不是双人审批）' : '否' }}</el-descriptions-item>
          <el-descriptions-item label="创建时间">{{ time(currentDetail.created_at) }}</el-descriptions-item>
        </el-descriptions>
        <div class="drawer-actions"><el-button v-for="name in (['approve','execute','complete','cancel','fail'] as CashRefundAction[])" v-show="canAction(currentDetail, name)" :key="name" type="primary" @click="openAction(currentDetail, name)">{{ actionLabels[name] }}</el-button></div>
      </template>
    </el-drawer>
  </div>
</template>

<style scoped>
.page-stack { display: flex; flex-direction: column; gap: 16px; }
.pager { display: flex; justify-content: flex-end; gap: 8px; }
.drawer-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 20px; }
</style>
