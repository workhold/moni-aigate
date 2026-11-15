<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, newOperationKey, request } from '../api'
import { commerceReasonLabels, commerceWrite, parseCommerceWriteResult } from '../commerce'
import type { CommerceKind, CommerceReceipt, VoucherBatch, VoucherDraft, VoucherRevocation } from '../commerce'
import { commerceContext, commerceDeadline, commerceJournal, createCommerceDeliveryGate, createCommerceScope, unknownCommerceWrite } from '../commerce-state'
import type { SafeCommerceCommand } from '../commerce-state'
import { coin } from '../format'
import { session } from '../session'

const props = defineProps<{ open: boolean; kind: CommerceKind; scope: string; batch?: VoucherBatch | null; proposal?: VoucherRevocation | null; enabled: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; settled: [receipt: CommerceReceipt]; changed: [] }>()
const scope = createCommerceScope(), delivery = createCommerceDeliveryGate(), context = computed(() => commerceContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}:${session.me?.csrf_token}:${session.mfaPending}`))
const busy = ref(false), unknown = ref(false), error = ref(''), receipt = ref<CommerceReceipt | null>(null), command = ref<SafeCommerceCommand | null>(null), codes = ref<{ voucher_id: string; code: string }[]>([]), copied = ref(false)
const draft = ref<VoucherDraft>({ face: '', count: '1', expires_at: '', purpose_ref: '', reason: '', code: '' })
const title = computed(() => ({ 'voucher.batch.create': '发行运营赠额券', 'voucher.batch.approve': '独立审批批次', 'voucher.batch.void': '作废未激活批次', 'voucher.revocation.create': '提议撤销未兑换券', 'voucher.revocation.approve': '独立审批撤销提案', 'voucher.redeem': '兑换运营赠额' })[props.kind])
const permitted = computed(() => props.kind === 'voucher.redeem' ? session.me?.tenant?.id === props.scope && session.me?.tenant?.role === 'owner' : ['voucher.batch.approve', 'voucher.revocation.approve'].includes(props.kind) ? session.me?.user.platform_role === 'finance_approver' : session.me?.user.platform_role === 'finance_operator')
const needsReason = computed(() => ['voucher.batch.create', 'voucher.batch.void', 'voucher.revocation.create'].includes(props.kind))
function clear() { scope.reset(context.value); delivery.clear(); draft.value = { face: '', count: '1', expires_at: '', purpose_ref: '', reason: '', code: '' }; codes.value = []; copied.value = false; busy.value = false; unknown.value = false; error.value = ''; receipt.value = null; command.value = null }
function close() { clear(); emit('update:open', false) }
function updateOpen(value: boolean) { if (!value) close() }
async function submit() {
  if (busy.value || unknown.value || receipt.value || !permitted.value || !session.me) return
  if (['voucher.batch.create', 'voucher.redeem'].includes(props.kind) && !props.enabled) { error.value = '当前兑换能力未开启，请刷新能力状态后核实。'; return }
  error.value = ''; copied.value = false
  const actor = session.me.user.id, ticket = scope.ticket('write'), kind = props.kind
  let original: SafeCommerceCommand | null = null
  let deliveryTicket = 0
  try {
    const operation_id = newOperationKey(), outgoing = commerceWrite(kind, operation_id, draft.value, props.batch ?? null, props.proposal ?? null)
    original = commerceJournal.begin(actor, { operation_id, kind, scope: props.scope, resource_id: kind === 'voucher.revocation.approve' ? props.proposal!.id : ['voucher.batch.create', 'voucher.redeem'].includes(kind) ? null : props.batch!.id, created_at: new Date().toISOString().replace(/\.(\d{3})Z$/, (_all, ms: string) => `.${ms}000Z`) })
    deliveryTicket = delivery.begin(); command.value = original; draft.value.code = ''; busy.value = true; emit('changed')
    const response = await commerceDeadline(request<unknown>(outgoing.path, 'POST', outgoing.body, undefined, () => scope.accepts(ticket) && delivery.accepts(deliveryTicket)))
    if (!scope.accepts(ticket) || !delivery.accepts(deliveryTicket)) return
    const result = parseCommerceWriteResult(response, { operation_id, actor, scope: original.scope, kind })
    if (result.batch) {
      const body = outgoing.body as Record<string, unknown>
      if (result.batch.maker !== actor || ['face_amount','count','expires_at','purpose_ref','reason'].some(field => result.batch![field as keyof VoucherBatch] !== body[field])) throw new Error('创建结果与原批次请求不一致，请只读核查。')
    }
    commerceJournal.settle(actor, original, result.receipt)
    delivery.settled(deliveryTicket)
    receipt.value = result.receipt; codes.value = result.codes; emit('settled', result.receipt); emit('changed')
  } catch (cause) {
    if (!scope.accepts(ticket)) return
    if (original) {
      unknown.value = unknownCommerceWrite(cause)
      if (!unknown.value) { try { commerceJournal.definiteFailure(actor, original, cause) } catch { unknown.value = true } }
      if (unknown.value) delivery.unknown(deliveryTicket); else delivery.settled(deliveryTicket)
      emit('changed')
    }
    error.value = unknown.value ? '原操作结果待核查，仅查询原回执。未查到回执、刷新或关闭窗口都不会允许重复提交。一次性兑换券不会恢复展示。' : errorText(cause)
    codes.value = []; draft.value.code = ''
  } finally { if (scope.accepts(ticket)) busy.value = false }
}
async function inspect() {
  if (!command.value || !session.me || busy.value) return
  const original = command.value, actor = session.me.user.id, ticket = scope.ticket('lookup')
  busy.value = true; error.value = ''
  try {
    const base = original.scope === 'platform' ? '/platform' : '/wallet'
    const response = await request<unknown>(`${base}/commerce-operations/${original.operation_id}`, 'GET', undefined, undefined, () => scope.accepts(ticket))
    if (!scope.accepts(ticket)) return
    receipt.value = commerceJournal.settle(actor, original, response.data); unknown.value = false; emit('settled', receipt.value); emit('changed')
  } catch (cause) { if (scope.accepts(ticket)) error.value = `只读查询尚未确认。404 不证明原提交未生效，不能换 ID 重复操作。${errorText(cause)}` }
  finally { if (scope.accepts(ticket)) busy.value = false }
}
async function copyCodes() {
  const ticket = scope.ticket('copy')
  try { await navigator.clipboard.writeText(codes.value.map(item => item.code).join('\n')); if (scope.accepts(ticket)) copied.value = true }
  catch { if (scope.accepts(ticket)) error.value = '复制失败，请手动选择并安全保存当前兑换券。' }
}
watch([() => props.open, context, () => props.scope, () => props.kind, () => props.batch?.id, () => props.proposal?.id], clear, { flush: 'sync' })
onBeforeUnmount(clear)
</script>

<template>
  <el-dialog :model-value="open" :title="title" width="660px" destroy-on-close :close-on-click-modal="false" @update:model-value="updateOpen">
    <p class="dialog-description">运营赠额不代表收款或现金收入，不产生自动现金退款资格。发行与审批不会给钱包入账；客户兑换成功后才到账。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <p v-if="command" class="mono commerce-operation">原操作 ID：{{ command.operation_id }}</p>
    <el-alert v-if="receipt" :type="receipt.status === 'accepted' ? 'success' : 'warning'" :title="commerceReasonLabels[receipt.reason] || receipt.reason" :closable="false" show-icon />
    <p v-if="receipt?.amount">本次已入账 {{ coin(receipt.amount) }}。资金来源与钱包仍以当前读取结果为准。</p>
    <template v-if="codes.length"><el-alert title="兑换券仅在本窗口展示一次。关闭、退出或切换身份后会清除；丢失后只能作废/双审撤销原批次，再明确新建。" type="warning" :closable="false" /><el-input :model-value="codes.map(item => item.code).join('\n')" type="textarea" :rows="Math.min(codes.length + 1, 10)" readonly class="commerce-codes" aria-label="一次性兑换券" /><el-button @click="copyCodes">{{ copied ? '已复制，请安全保存' : '复制当前兑换券' }}</el-button></template>
    <p v-else-if="receipt?.status === 'accepted' && kind === 'voucher.batch.create'">批次已创建，本次没有可恢复的兑换券明文。请按原批次作废或双审撤销后再明确新建。</p>
    <el-form v-if="!receipt && !unknown" label-position="top" :disabled="busy || !permitted" @submit.prevent="submit">
      <template v-if="kind === 'voucher.batch.create'"><div class="commerce-form-row"><el-form-item label="每张面额（币，最多六位小数）"><el-input v-model="draft.face" inputmode="decimal" autocomplete="off" /></el-form-item><el-form-item label="数量（1—100）"><el-input v-model="draft.count" inputmode="numeric" maxlength="3" /></el-form-item></div><el-form-item label="过期时间（UTC Z，精度最多六位微秒）"><el-input v-model="draft.expires_at" placeholder="2026-12-01T00:00:00.000000Z" /><span class="field-hint">最终由数据库核验为创建后 1 小时至 365 天，浏览器时间不作资格判断。</span></el-form-item><el-form-item label="用途受控编号"><el-input v-model="draft.purpose_ref" placeholder="campaign:2026-11" maxlength="256" /></el-form-item></template>
      <el-form-item v-if="kind === 'voucher.redeem'" label="完整兑换券"><el-input v-model="draft.code" type="password" show-password autocomplete="off" :maxlength="85" placeholder="mav1.…" /><span class="field-hint">只在本次提交内存中使用，不保存券码或兑换请求正文。</span></el-form-item>
      <el-form-item v-if="needsReason" label="操作理由（最多 200 UTF-8 字节）"><el-input v-model="draft.reason" type="textarea" :rows="3" /></el-form-item>
      <p v-if="batch" class="commerce-caption">批次 {{ batch.id }} · 版本 {{ batch.revision }} · 已兑 {{ batch.redeemed_count }} / {{ batch.count }} · 面额 {{ coin(batch.face_amount) }}</p>
      <p v-if="proposal" class="commerce-caption">审批原提案 {{ proposal.id }}：按其冻结版本 {{ proposal.expected_revision }} 和已兑数 {{ proposal.expected_redeemed_count }} 核验；不重传新证据。理由：{{ proposal.reason }}</p>
    </el-form>
    <template #footer><el-button @click="close">{{ codes.length ? '已保存，清除并关闭' : '关闭' }}</el-button><el-button v-if="unknown" :loading="busy" @click="inspect">只读查询原回执</el-button><el-button v-else-if="!receipt" type="primary" :loading="busy" :disabled="!permitted || (['voucher.batch.create','voucher.redeem'].includes(kind) && !enabled)" @click="submit">确认提交</el-button></template>
  </el-dialog>
</template>
<style scoped>.commerce-form-row{display:grid;grid-template-columns:2fr 1fr;gap:18px}.commerce-operation,.commerce-caption{overflow-wrap:anywhere;font-size:12px;line-height:1.8}.commerce-codes{margin:16px 0;font-family:monospace}.commerce-caption{color:var(--text-secondary)}@media(max-width:600px){.commerce-form-row{grid-template-columns:1fr}}</style>
