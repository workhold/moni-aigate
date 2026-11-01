<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { session } from '../session'
import { ApiError, errorText, request } from '../api'
import { money, time } from '../format'
import { credentialPoolPath, parseCredentialPool } from '../credential-pool'
import type { CredentialPoolView } from '../credential-pool'
import { createProcurementScope, procurementContext, procurementJournal } from '../procurement-state'
import type { ProcurementCommand, ProcurementCommandReview } from '../procurement-state'
import { acceptProcurementResult, parseProcurementAccount, parseProcurementBinding, parseProcurementChannel, parseProcurementProposal, procurementAccountCommand, procurementAccountPath, procurementApprovalCommand, procurementBindingCommand, procurementCny, procurementObservationCommand, procurementProposalCommand, procurementProposalPath, readProcurementRows } from '../procurement'
import type { ProcurementAccount, ProcurementAccountDraft, ProcurementBinding, ProcurementChannel, ProcurementObservationDraft, ProcurementProposal, ProcurementProposalDraft, ProcurementResult, ProcurementWriteKind } from '../procurement'

const props = defineProps<{ modelValue: boolean; kind: ProcurementWriteKind; account?: ProcurementAccount | null; proposal?: ProcurementProposal | null; attempt?: { attempt_id: string; tenant_id: string; spent_amount: string } | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; applied: []; journal: [] }>()
const scope = createProcurementScope()
const context = computed(() => procurementContext(session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role))
const visible = computed({ get: () => props.modelValue, set: value => emit('update:modelValue', value) })
const busy = ref(false), loading = ref(false), error = ref(''), current = ref<ProcurementCommandReview | null>(null)
const staleEvidence = ref(false)
const source = ref<ProcurementAccount | null>(null), proposal = ref<ProcurementProposal | null>(null)
const account = ref<ProcurementAccountDraft>({ label: '', provider: '', state: 'active', limit: '' })
const observation = ref<ProcurementObservationDraft>({ status: 'unknown', amount: '', source: '', semantic: '', observed_at: '', provider_as_of: '', evidence_ref: '' })
const correction = ref<ProcurementProposalDraft>({ tenant_id: '', attempt_id: '', amount: '', basis: 'contract', evidence_ref: '', reason: '' })
const accounts = ref<ProcurementAccount[]>([]), channels = ref<ProcurementChannel[]>([]), pool = ref<CredentialPoolView | null>(null), bindings = ref<ProcurementBinding[]>([])
const binding = ref({ account_id: '', channel_id: '', credential_id: '' })
const titles: Record<ProcurementWriteKind, string> = { create: '新增共享采购账户', configure: '编辑账户与累计采购预算', bind: '永久绑定采购账户', observe: '追加人工余额观察', propose: '提交采购成本更正提案', approve: '复核并批准采购成本更正' }
const title = computed(() => titles[props.kind])
const roleAllowed = computed(() => ['propose'].includes(props.kind) ? session.me?.user.platform_role === 'finance_operator' : props.kind === 'approve' ? !staleEvidence.value && session.me?.user.platform_role === 'finance_approver' && proposal.value?.requester !== session.me?.user.id && proposal.value?.state === 'proposed' : session.me?.user.platform_role === 'admin')
const locked = computed(() => busy.value || loading.value || !!current.value)
const result = computed(() => current.value?.result as ProcurementResult | undefined)
const receiptRejected = computed(() => !!result.value && 'status' in result.value && result.value.status === 'rejected')
const canStartAgain = computed(() => !staleEvidence.value && !!current.value && (['conflict', 'rejected'].includes(current.value.state) || current.value.state === 'confirmed' && receiptRejected.value))
const availableMembers = computed(() => pool.value?.members.filter(member => !member.revoked_at && !bindings.value.some(item => item.credential_id === member.credential_id)) ?? [])

async function initialize() {
  scope.reset(context.value)
  const ticket = scope.ticket('initialize')
  busy.value = false; loading.value = true; error.value = ''; current.value = null; source.value = null; proposal.value = null
  staleEvidence.value = props.kind === 'approve' && procurementJournal.list(context.value).some(item => item.command.target === `approval:${props.proposal?.id}` && item.failureCode === 'procurement_evidence_stale')
  accounts.value = []; channels.value = []; pool.value = null; bindings.value = []
  account.value = { label: '', provider: '', state: 'active', limit: '' }
  observation.value = { status: 'unknown', amount: '', source: '', semantic: '', observed_at: new Date().toISOString(), provider_as_of: '', evidence_ref: '' }
  correction.value = { tenant_id: props.attempt?.tenant_id ?? '', attempt_id: props.attempt?.attempt_id ?? '', amount: '', basis: 'contract', evidence_ref: '', reason: '' }
  binding.value = { account_id: props.account?.id ?? '', channel_id: '', credential_id: '' }
  try {
    if (props.kind === 'configure' || props.kind === 'observe') {
      if (!props.account) throw new Error('请先选择采购账户。')
      const value = parseProcurementAccount((await request<unknown>(`${procurementAccountPath(props.account.id)}/configuration`)).data, props.account.id)
      if (!scope.accepts(ticket)) return
      source.value = value; account.value = { label: value.label, provider: value.provider, state: value.state, limit: procurementCny(value.limit_amount) }
    } else if (props.kind === 'approve') {
      if (!props.proposal) throw new Error('请先选择待审提案。')
      const value = parseProcurementProposal((await request<unknown>(procurementProposalPath(props.proposal.id))).data, props.proposal.id)
      if (!scope.accepts(ticket)) return
      proposal.value = value
    } else if (props.kind === 'bind') {
      const values = await Promise.all([readProcurementRows('/platform/procurement-accounts', parseProcurementAccount, () => scope.accepts(ticket)), readProcurementRows('/platform/channels', parseProcurementChannel, () => scope.accepts(ticket))])
      if (!scope.accepts(ticket)) return
      accounts.value = values[0]; channels.value = values[1]
    }
  } catch (cause) { if (scope.accepts(ticket)) error.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
async function selectChannel() {
  const id = binding.value.channel_id, ticket = scope.ticket('pool')
  binding.value.credential_id = ''; pool.value = null; bindings.value = []; error.value = ''
  if (!id) return
  loading.value = true
  try {
    const values = await Promise.all([request<unknown>(credentialPoolPath(id)), readProcurementRows(`/platform/procurement-bindings?channel_id=${encodeURIComponent(id)}`, parseProcurementBinding, () => scope.accepts(ticket))])
    const view = parseCredentialPool(values[0].data)
    if (view.channel_id !== id || view.channel_version_id !== view.current_version || values[1].some(item => item.channel_id !== id)) throw new Error('渠道凭据池或绑定响应对象不一致，请重新读取。')
    if (!scope.accepts(ticket)) return
    pool.value = view; bindings.value = values[1]
  } catch (cause) { if (scope.accepts(ticket)) error.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
function build(): ProcurementCommand {
  const id = crypto.randomUUID()
  if (props.kind === 'create') return procurementAccountCommand(account.value, id)
  if (props.kind === 'configure') { if (!source.value) throw new Error('请先读取完整账户配置。'); return procurementAccountCommand(account.value, id, source.value) }
  if (props.kind === 'observe') { if (!source.value) throw new Error('请先读取采购账户。'); return procurementObservationCommand(source.value.id, observation.value, id) }
  if (props.kind === 'bind') {
    const channel = channels.value.find(item => item.id === binding.value.channel_id)
    if (!channel || !availableMembers.value.some(item => item.credential_id === binding.value.credential_id)) throw new Error('请选择当前渠道中尚未绑定、未撤销的凭据。')
    return procurementBindingCommand(channel, binding.value.credential_id, binding.value.account_id, id)
  }
  if (props.kind === 'propose') {
    const value = procurementProposalCommand(correction.value, id)
    if (props.attempt && BigInt(String(value.body.amount)) < BigInt(props.attempt.spent_amount) && value.body.basis !== 'invoice') throw new Error('减少已计采购成本必须使用供应商账单依据。')
    return value
  }
  if (!proposal.value || !session.me) throw new Error('请先读取完整提案。')
  return procurementApprovalCommand(proposal.value, session.me.user.id, id)
}
function announce(value: ProcurementResult) {
  if ('status' in value) {
    if (value.status === 'rejected') error.value = `原命令已明确拒绝：${value.reason || '配置冲突或当前状态不允许'}。请重新读取后开始新编辑。`
    else emit('applied')
  } else emit('applied')
}
async function submit() {
  if (locked.value || !roleAllowed.value || !session.me) return
  const actor = session.me.user.id, owner = context.value, ticket = scope.ticket('write')
  error.value = ''
  let command: ProcurementCommand
  try { command = build(); current.value = procurementJournal.begin(owner, command); emit('journal') }
  catch (cause) { error.value = errorText(cause); return }
  busy.value = true
  try {
    const value = acceptProcurementResult(command, (await request<unknown>(command.path, command.method, command.body)).data, actor), entry = procurementJournal.confirm(owner, command.operationID, value)
    emit('journal')
    if (!scope.accepts(ticket)) return
    current.value = entry; announce(value)
  } catch (cause) {
    const entry = procurementJournal.fail(owner, command.operationID, cause, errorText(cause)); emit('journal')
    if (scope.accepts(ticket)) { current.value = entry; error.value = errorText(cause); staleEvidence.value = cause instanceof ApiError && cause.code === 'procurement_evidence_stale' }
  } finally { if (scope.accepts(ticket)) busy.value = false }
}
async function inspect() {
  if (!current.value || busy.value || !session.me) return
  const original = current.value.command, actor = session.me.user.id, owner = context.value, ticket = scope.ticket('write')
  busy.value = true; error.value = ''
  try {
    const value = acceptProcurementResult(original, (await request<unknown>(original.lookupPath)).data, actor), entry = procurementJournal.confirm(owner, original.operationID, value)
    emit('journal')
    if (!scope.accepts(ticket)) return
    current.value = entry; announce(value)
  } catch (cause) { if (scope.accepts(ticket)) error.value = `只读核查未确认原结果，保留原操作 ID。${errorText(cause)}` }
  finally { if (scope.accepts(ticket)) busy.value = false }
}
watch([() => props.modelValue, () => props.kind, () => props.account?.id, () => props.proposal?.id, context], () => {
  scope.reset(context.value); current.value = null; error.value = ''; busy.value = false; loading.value = false
  if (props.modelValue) void initialize()
}, { immediate: true })
onBeforeUnmount(() => scope.reset())
</script>

<template>
  <el-dialog v-model="visible" :title="title" width="min(780px, calc(100vw - 28px))" destroy-on-close :close-on-click-modal="false" class="procurement-editor">
    <p class="dialog-description">采购预算属于平台共享账户。客户钱包退款、免收及 24 小时释放不会恢复供应商潜在消费。</p>
    <el-alert v-if="staleEvidence" title="原提案的证据版本已变化，不能再次审批。请财务制单账号重新核对当前证据并创建新提案。" type="warning" :closable="false" show-icon class="procurement-notice" />
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="procurement-notice" />
    <el-skeleton v-if="loading" :rows="4" animated />
    <template v-else>
      <el-alert v-if="current" :type="current.state === 'confirmed' ? receiptRejected ? 'warning' : 'success' : 'warning'" :closable="false" show-icon class="procurement-notice" :title="current.state === 'confirmed' ? '已取得原操作的持久结果；请以下方回执状态为准。' : current.state === 'unknown' || current.state === 'submitting' ? '结果待确认。请保存原操作 ID，只读查询；不能换 ID 重复提交。' : '原提交未应用或发生冲突，请重新读取并明确开始新编辑。'" />
      <div v-if="current" class="procurement-operation"><span>原操作 ID</span><code>{{ current.command.operationID }}</code><el-button :loading="busy" @click="inspect">只读查询原结果</el-button><el-button v-if="canStartAgain" @click="initialize">重新读取并开始新编辑</el-button></div>
      <div v-if="result" class="procurement-result"><template v-if="'status' in result">回执：{{ result.status === 'applied' ? '已应用' : '已拒绝' }} · {{ result.reason || result.kind }}</template><template v-else>提案：{{ result.state === 'approved' ? '已由独立财务批准' : '已冻结，等待独立财务审批' }} · {{ result.id }}</template></div>
      <el-form label-position="top" :disabled="locked" @submit.prevent="submit">
        <template v-if="kind === 'create' || kind === 'configure'">
          <el-form-item label="采购账户名称" required><el-input v-model="account.label" maxlength="100" /></el-form-item>
          <el-form-item label="供应商 / 聚合平台标识" required><el-input v-model="account.provider" maxlength="100" placeholder="供应商或你接入的另一 API 平台" /></el-form-item>
          <div class="procurement-form-grid"><el-form-item label="账户状态" required><el-select v-model="account.state"><el-option label="启用" value="active" /><el-option label="暂停新准入" value="paused" /></el-select></el-form-item><el-form-item label="累计采购预算上限（CNY）" required><el-input v-model="account.limit" inputmode="decimal" placeholder="最多 6 位小数；0 不允许新增占用" /></el-form-item></div>
          <p v-if="source" class="field-hint">配置版本 {{ source.revision }} · 已占用 {{ money(source.held_amount) }} · 已计成本 {{ money(source.spent_amount) }}。允许将预算降至占用以下；这样会阻止新预占和发送，不取消既有消费或资金收尾。暂停后须显式重开。</p>
        </template>
        <template v-else-if="kind === 'bind'">
          <el-alert type="warning" title="凭据一旦绑定就永久归属此采购账户。换账户须新建凭据；多把 Key 不会产生多份余额。" :closable="false" class="procurement-notice" />
          <el-form-item label="采购账户" required><el-select v-model="binding.account_id" filterable><el-option v-for="item in accounts" :key="item.id" :label="`${item.label} · ${item.state === 'paused' ? '暂停' : '启用'}`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="渠道" required><el-select v-model="binding.channel_id" filterable @change="selectChannel"><el-option v-for="item in channels" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="当前池中尚未绑定的凭据" required><el-select v-model="binding.credential_id" filterable><el-option v-for="item in availableMembers" :key="item.credential_id" :label="`${item.label} · ${item.credential_id}`" :value="item.credential_id" /></el-select></el-form-item>
          <p class="field-hint">未绑定的凭据为 legacy_unmanaged。绑定后，该凭据在所有渠道版本的新尝试自动受采购预算约束；已发送的历史尝试保留原语义。</p>
          <p v-for="item in pool?.members || []" :key="item.credential_id" class="field-hint">{{ item.label }}：{{ bindings.find(row => row.credential_id === item.credential_id)?.account_id || 'legacy_unmanaged（尚未绑定）' }}{{ item.revoked_at ? ' · 已撤销' : '' }}</p>
        </template>
        <template v-else-if="kind === 'observe'">
          <el-alert type="info" title="这是人工证据观察，不调用供应商，不覆盖本地预算或占用，也不表示供应商余额已获认证。" :closable="false" class="procurement-notice" />
          <div class="procurement-form-grid"><el-form-item label="观察状态" required><el-select v-model="observation.status"><el-option label="已知观察值" value="known" /><el-option label="未知" value="unknown" /><el-option label="供应商不支持" value="unsupported" /><el-option label="观察失败" value="error" /></el-select></el-form-item><el-form-item v-if="observation.status === 'known'" label="观察余额（CNY）" required><el-input v-model="observation.amount" inputmode="decimal" /></el-form-item></div>
          <el-form-item label="人工观察来源" required><el-input v-model="observation.source" maxlength="100" placeholder="例如：供应商控制台人工抄录" /></el-form-item>
          <el-form-item label="余额含义" required><el-input v-model="observation.semantic" maxlength="100" placeholder="例如：供应商显示的预付可用余额" /></el-form-item>
          <el-form-item label="观察时间（含时区的 ISO 时间）" required><el-input v-model="observation.observed_at" placeholder="2026-11-01T00:00:00Z" /></el-form-item>
          <el-form-item label="供应商数据时刻（可选，含时区）"><el-input v-model="observation.provider_as_of" placeholder="未知时留空" /></el-form-item>
          <el-form-item label="受控证据编号" required><el-input v-model="observation.evidence_ref" maxlength="256" placeholder="不填写秘密或下载网址" /></el-form-item>
        </template>
        <template v-else-if="kind === 'propose'">
          <el-alert type="warning" title="填写最终供应商绝对成本，并由另一财务审批账号核对。不会修改客户账单；减少成本必须提供供应商账单依据。" :closable="false" class="procurement-notice" />
          <el-form-item label="所属租户 ID" required><el-input v-model="correction.tenant_id" /></el-form-item>
          <el-form-item label="上游尝试 ID" required><el-input v-model="correction.attempt_id" /></el-form-item>
          <div class="procurement-form-grid"><el-form-item label="最终供应商绝对成本（CNY）" required><el-input v-model="correction.amount" inputmode="decimal" /></el-form-item><el-form-item label="成本证据依据" required><el-select v-model="correction.basis"><el-option label="合同依据" value="contract" /><el-option label="供应商账单依据" value="invoice" /></el-select></el-form-item></div>
          <el-form-item label="受控证据编号" required><el-input v-model="correction.evidence_ref" maxlength="256" /></el-form-item>
          <el-form-item label="更正原因" required><el-input v-model="correction.reason" type="textarea" :rows="3" maxlength="200" /></el-form-item>
          <p class="field-hint">prepared / sending 的尝试不能对账；unknown、disputed、settled、released 才可制单。审批不会自动恢复暂停账户。</p>
        </template>
        <template v-else-if="proposal">
          <dl class="detail-list"><dt>提案 / 原制单 ID</dt><dd class="mono">{{ proposal.id }}</dd><dt>尝试 ID</dt><dd class="mono">{{ proposal.attempt_id }}</dd><dt>租户 ID</dt><dd class="mono">{{ proposal.tenant_id }}</dd><dt>证据版本</dt><dd>{{ proposal.expected_revision }}</dd><dt>最终供应商绝对成本</dt><dd>{{ money(proposal.amount) }}</dd><dt>依据</dt><dd>{{ proposal.basis === 'invoice' ? '供应商账单' : '合同' }}</dd><dt>证据编号</dt><dd>{{ proposal.evidence_ref }}</dd><dt>更正原因</dt><dd>{{ proposal.reason }}</dd><dt>制单账号</dt><dd class="mono">{{ proposal.requester }}</dd><dt>提交时间</dt><dd>{{ time(proposal.created_at) }}</dd></dl>
          <p class="field-hint">提案冻结证据数 {{ proposal.expected_evidence_count }}。请核对原尝试与供应商证据。批准将释放采购占用，并按最终成本与已计成本的差额追加采购账务；客户钱包与原报价快照保持原合同。审批不会自动重开暂停账户。</p>
        </template>
      </el-form>
    </template>
    <template #footer><el-button @click="visible = false">关闭</el-button><el-button v-if="!current" type="primary" :loading="busy || loading" :disabled="!roleAllowed || loading || (kind === 'configure' || kind === 'observe') && !source" @click="submit">{{ kind === 'approve' ? '已核对，批准更正' : kind === 'propose' ? '提交并冻结提案' : '确认提交' }}</el-button></template>
  </el-dialog>
</template>

<style scoped>
.procurement-notice{margin-bottom:18px}.procurement-form-grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}.procurement-operation{display:flex;gap:12px;flex-wrap:wrap;align-items:center;margin:18px 0}.procurement-operation code{overflow-wrap:anywhere;font-size:12px}.procurement-result{padding:12px;background:#f1f7f6;border-radius:8px;margin-bottom:18px;overflow-wrap:anywhere}.detail-list dd{overflow-wrap:anywhere}.el-select{width:100%}@media(max-width:600px){.procurement-form-grid{grid-template-columns:1fr;gap:0}}
</style>
