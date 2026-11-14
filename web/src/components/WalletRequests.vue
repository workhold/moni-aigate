<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { errorText, request, stableOperationKey } from '../api'
import { shortId, time } from '../format'
import { session } from '../session'
import { parseWalletRequest, parseWalletRequestPage, walletRequestLabel, walletRequestStateLabel, walletRequestsPath, type WalletRequest, type WalletRequestKind } from '../wallet-requests'

const tenantID = computed(() => session.me?.tenant?.id || '')
const owner = computed(() => session.me?.tenant?.role === 'owner')
const allowed = computed(() => ['owner', 'billing_viewer'].includes(session.me?.tenant?.role || ''))
const rows = ref<WalletRequest[]>([]), loading = ref(false), busy = ref(false), error = ref(''), formError = ref('')
const kind = ref<WalletRequestKind>('topup'), note = ref(''), next = ref(''), history = ref<string[]>([])
let generation = 0

async function load(page = '', reset = false) {
  if (!allowed.value || !tenantID.value) return
  const ticket = ++generation, tenant = tenantID.value
  if (reset) history.value = []
  loading.value = true; error.value = ''
  try {
    const response = await request<unknown>(walletRequestsPath(page))
    if (ticket !== generation || tenant !== tenantID.value) return
    const parsed = parseWalletRequestPage(response.data, response.next_cursor, tenant)
    rows.value = parsed.rows; next.value = parsed.next
  } catch (cause) { if (ticket === generation) error.value = errorText(cause) }
  finally { if (ticket === generation) loading.value = false }
}
async function create() {
  if (!owner.value || busy.value) return
  formError.value = ''
  const body = { kind: kind.value, note: note.value.trim() }
  if (!body.note) { formError.value = '请填写付款流水号或申请说明。'; return }
  busy.value = true
  try {
    const key = stableOperationKey(`/wallet/requests:${tenantID.value}`, body)
    const response = await request<unknown>('/wallet/requests', 'POST', body, key)
    const created = parseWalletRequest(response.data, tenantID.value)
    note.value = ''; ElMessage.success(`申请已创建（${walletRequestStateLabel(created.state)}），尚未产生钱包效果。`)
    await load('', true)
  } catch (cause) { formError.value = errorText(cause) }
  finally { busy.value = false }
}
async function close(row: WalletRequest) {
  if (!owner.value || row.state !== 'open' || busy.value) return
  let reason = ''
  try {
    const result = await ElMessageBox.prompt('确认取消这条尚未关联的申请？取消不会产生钱包效果。请填写原因。', '取消钱包申请', { confirmButtonText: '确认取消申请', cancelButtonText: '返回', inputPlaceholder: '例如：付款信息填写有误', inputValidator: value => !!value?.trim() || '请填写取消原因。' })
    reason = result.value.trim()
  } catch { return }
  busy.value = true
  const path = `/wallet/requests/${encodeURIComponent(row.id)}/close`, body = { reason }
  try {
    await request(path, 'POST', body, stableOperationKey(`${tenantID.value}:${path}`, body))
    ElMessage.success('申请已关闭，未产生钱包效果。'); await load('', true)
  } catch (cause) { ElMessage.error(errorText(cause)) }
  finally { busy.value = false }
}
async function nextPage() { const page = next.value; if (!page) return; await load(page); if (!error.value) history.value.push(page) }
async function previousPage() { const stack = [...history.value]; stack.pop(); const page = stack.at(-1) || ''; await load(page); if (!error.value) history.value = stack }
function refresh() { void load('', true) }
defineExpose({ refresh })
watch(tenantID, () => { generation++; rows.value = []; next.value = ''; history.value = []; error.value = ''; if (tenantID.value) void load() }, { immediate: true })
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <section v-if="allowed" class="panel wallet-requests" aria-label="客户钱包申请">
    <div class="panel-header"><div><h2 class="panel-title">钱包申请</h2><p class="panel-subtitle">提交线下付款或现金退款申请。open 和 linked 都不是已付款、已退款或已入账，只有对应单据审批后才执行钱包操作。</p></div><el-button :icon="Refresh" :loading="loading" @click="load('', true)">刷新</el-button></div>
    <div class="request-body">
      <el-alert title="每租户最多保留 20 条未关闭申请。财务仍需核对真实付款与凭证；申请本身不会改变余额。" type="info" :closable="false" show-icon />
      <el-form v-if="owner" class="request-form" label-position="top" @submit.prevent="create"><div class="request-fields"><el-form-item label="申请类型"><el-select v-model="kind" :disabled="busy"><el-option label="充值申请" value="topup" /><el-option label="现金退款申请" value="cash_refund" /></el-select></el-form-item><el-form-item label="付款流水号或申请说明" required><el-input v-model="note" type="textarea" :rows="2" maxlength="1000" show-word-limit :disabled="busy" placeholder="只填写供财务核对的受控编号和必要说明，不要填写密钥或公开凭证链接" /></el-form-item></div><el-button type="primary" :icon="Plus" :loading="busy" @click="create">提交申请</el-button></el-form>
      <el-alert v-if="formError" :title="formError" type="error" :closable="false" />
      <el-alert v-else-if="error" :title="error" type="error" :closable="false" />
      <el-table v-loading="loading" :data="rows" row-key="id" empty-text="暂无钱包申请" aria-label="钱包申请列表"><el-table-column label="申请" min-width="150"><template #default="{ row }"><span class="mono" :title="row.id">{{ shortId(row.id) }}</span><div class="request-kind">{{ walletRequestLabel(row.kind) }}</div></template></el-table-column><el-table-column prop="note" label="说明" min-width="230" /><el-table-column label="状态" min-width="135"><template #default="{ row }"><el-tag :type="row.state === 'closed' ? 'info' : row.state === 'linked' ? 'warning' : 'primary'">{{ walletRequestStateLabel(row.state) }}</el-tag></template></el-table-column><el-table-column label="关联单据" min-width="135"><template #default="{ row }"><span class="mono">{{ row.linked_order_id ? shortId(row.linked_order_id) : '—' }}</span></template></el-table-column><el-table-column label="创建时间" min-width="170"><template #default="{ row }">{{ time(row.created_at) }}</template></el-table-column><el-table-column v-if="owner" label="操作" width="120" fixed="right"><template #default="{ row }"><el-button v-if="row.state === 'open'" link type="warning" :disabled="busy" @click="close(row)">取消申请</el-button><span v-else class="request-kind">不可取消</span></template></el-table-column></el-table>
      <div class="table-footer"><span>当前页 {{ rows.length }} 条</span><div><el-button size="small" :disabled="!history.length || loading" @click="previousPage">上一页</el-button><el-button size="small" :disabled="!next || loading" @click="nextPage">下一页</el-button></div></div>
    </div>
  </section>
</template>

<style scoped>
.wallet-requests{margin-top:24px}.wallet-requests h2{margin:0}.request-body{padding:22px}.request-form{display:flex;align-items:flex-end;gap:18px;margin:20px 0}.request-fields{display:grid;grid-template-columns:220px minmax(280px,1fr);gap:18px;flex:1}.request-form .el-form-item{margin-bottom:0}.request-kind{font-size:12px;color:var(--text-secondary);margin-top:5px}.request-body>.el-alert{margin-top:14px}@media(max-width:720px){.request-body{padding:16px}.request-form{align-items:stretch;flex-direction:column}.request-fields{grid-template-columns:1fr}}
</style>
