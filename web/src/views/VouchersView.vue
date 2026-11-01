<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import CommerceWriter from '../components/CommerceWriter.vue'
import CommerceCommands from '../components/CommerceCommands.vue'
import VoucherPolicy from '../components/VoucherPolicy.vue'
import { errorText, request } from '../api'
import { commerceUUID, parseCommerceCapabilities, parseCommercePage, parseVoucherBatch, parseVoucherRevocation } from '../commerce'
import type { CommerceCapabilities, CommerceKind, VoucherBatch, VoucherRevocation } from '../commerce'
import { commerceContext, createCommerceScope } from '../commerce-state'
import { money, shortId } from '../format'
import { session } from '../session'

const scope = createCommerceScope(), context = computed(() => commerceContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}:${session.me?.csrf_token}`))
const role = computed(() => session.me?.user.platform_role), operator = computed(() => role.value === 'finance_operator'), approver = computed(() => role.value === 'finance_approver')
const rows = ref<VoucherBatch[]>([]), caps = ref<CommerceCapabilities | null>(null), loading = ref(false), error = ref(''), capsError = ref(''), cursor = ref(''), next = ref(''), trail = ref<string[]>([])
const detail = ref<VoucherBatch | null>(null), revocations = ref<VoucherRevocation[]>([]), detailBusy = ref(false), detailError = ref(''), detailCursor = ref(''), detailNext = ref(''), detailTrail = ref<string[]>([])
const lookupBatch = ref(''), lookupProposal = ref(''), proposal = ref<VoucherRevocation | null>(null), proposalError = ref(''), proposalBusy = ref(false)
const writerOpen = ref(false), writerKind = ref<CommerceKind>('voucher.batch.create'), writerBatch = ref<VoucherBatch | null>(null), writerProposal = ref<VoucherRevocation | null>(null)
const states = { proposed: '待独立审批', active: '已激活', voided: '未激活作废', revoked: '已撤销未兑换券' }
async function capabilities() {
  const ticket = scope.ticket('caps'); caps.value = null; capsError.value = ''
  try { const response = await request<unknown>('/platform/commerce-capabilities', 'GET', undefined, undefined, () => scope.accepts(ticket)); if (scope.accepts(ticket)) caps.value = parseCommerceCapabilities(response.data) }
  catch (cause) { if (scope.accepts(ticket)) capsError.value = errorText(cause) }
}
async function load(page = cursor.value, navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
  const ticket = scope.ticket('list'), previous = cursor.value; loading.value = true; error.value = ''
  try {
    const response = await request<unknown>(`/platform/voucher-batches${page ? `?cursor=${encodeURIComponent(page)}` : ''}`, 'GET', undefined, undefined, () => scope.accepts(ticket))
    if (!scope.accepts(ticket)) return
    const parsed = parseCommercePage(response.data, response.next_cursor, parseVoucherBatch)
    if (page && parsed.next === page) throw new Error('批次分页没有前进，请刷新第一页。')
    rows.value = parsed.rows; next.value = parsed.next; cursor.value = page
    if (navigation === 'next') trail.value.push(previous); else if (navigation === 'previous') trail.value.pop()
  } catch (cause) { if (scope.accepts(ticket)) { error.value = errorText(cause); rows.value = []; next.value = '' } }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
function closeDetail() { scope.invalidate('detail'); detail.value = null; detailBusy.value = false; detailError.value = ''; revocations.value = []; detailCursor.value = ''; detailNext.value = ''; detailTrail.value = [] }
async function inspectBatch(id: string, page = '', navigation: 'refresh' | 'next' | 'previous' = 'refresh') {
  const ticket = scope.ticket('detail'), previous = detailCursor.value; detailBusy.value = true; detailError.value = ''
  try {
    const batchID = commerceUUID(id)
    const [batchResponse, response] = await Promise.all([request<unknown>(`/platform/voucher-batches/${batchID}`, 'GET', undefined, undefined, () => scope.accepts(ticket)), request<unknown>(`/platform/voucher-batches/${batchID}/revocations${page ? `?cursor=${encodeURIComponent(page)}` : ''}`, 'GET', undefined, undefined, () => scope.accepts(ticket))])
    if (!scope.accepts(ticket)) return
    const batch = parseVoucherBatch(batchResponse.data, batchID), parsed = parseCommercePage(response.data, response.next_cursor, raw => parseVoucherRevocation(raw, batchID))
    detail.value = batch; revocations.value = parsed.rows; detailNext.value = parsed.next; detailCursor.value = page
    if (navigation === 'next') detailTrail.value.push(previous); else if (navigation === 'previous') detailTrail.value.pop()
  } catch (cause) { if (scope.accepts(ticket)) { detailError.value = errorText(cause); revocations.value = []; detailNext.value = '' } }
  finally { if (scope.accepts(ticket)) detailBusy.value = false }
}
async function inspectProposal() {
  const ticket = scope.ticket('proposal'); proposalBusy.value = true; proposal.value = null; proposalError.value = ''
  try {
    const id = commerceUUID(lookupProposal.value), response = await request<unknown>(`/platform/voucher-revocations/${id}`, 'GET', undefined, undefined, () => scope.accepts(ticket))
    if (!scope.accepts(ticket)) return
    const parsed = parseVoucherRevocation(response.data); if (parsed.id !== id) throw new Error('撤销提案返回了不同身份。'); proposal.value = parsed
  } catch (cause) { if (scope.accepts(ticket)) proposalError.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) proposalBusy.value = false }
}
function openWriter(kind: CommerceKind, batch: VoucherBatch | null = null, original: VoucherRevocation | null = null) { writerKind.value = kind; writerBatch.value = batch; writerProposal.value = original; writerOpen.value = true }
function settled() { void load(); void capabilities(); if (detail.value) void inspectBatch(detail.value.id, detailCursor.value); if (proposal.value) void inspectProposal() }
function reset() { scope.reset(context.value); closeDetail(); rows.value = []; caps.value = null; error.value = ''; loading.value = false; capsError.value = ''; cursor.value = ''; next.value = ''; trail.value = []; lookupBatch.value = ''; lookupProposal.value = ''; proposal.value = null; proposalError.value = ''; proposalBusy.value = false; writerOpen.value = false; writerBatch.value = null; writerProposal.value = null; if (session.me && ['admin','finance_operator','finance_approver'].includes(role.value || '')) { void load(''); void capabilities() } }
watch(context, reset, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => scope.reset())
</script>
<template>
  <PageHeader eyebrow="VOUCHER OPERATIONS" title="运营赠额券" description="财务制单、独立审批、客户兑换后入账；发行与审批不增加钱包。"><el-button :loading="loading" @click="load(); capabilities()">刷新</el-button><el-button v-if="operator" type="primary" :disabled="caps?.vouchers_enabled !== true" @click="openWriter('voucher.batch.create')">发行批次</el-button></PageHeader>
  <ErrorNotice :message="capsError" @retry="capabilities" /><el-alert v-if="caps" :type="caps.vouchers_enabled ? 'info' : 'warning'" :title="caps.vouchers_enabled ? '兑换能力已开启。旧批次仍须符合审批、期限与未兑换条件。在线支付尚未提供。' : '发行与兑换当前关闭。已有批次的审批、作废、撤销及原回执查询仍可进行。'" :closable="false" show-icon />
  <ErrorNotice :message="error" @retry="load()" />
  <section class="panel voucher-list"><div class="panel-title">批次</div><el-table v-loading="loading" :data="rows" empty-text="暂无运营赠额批次"><el-table-column label="批次 / 用途" min-width="195"><template #default="{ row }"><span class="mono" :title="row.id">{{ shortId(row.id) }}</span><div>{{ row.purpose_ref }}</div></template></el-table-column><el-table-column label="面额 / 数量" min-width="155"><template #default="{ row }">{{ money(row.face_amount) }} × {{ row.count }}</template></el-table-column><el-table-column label="状态 / 已兑" min-width="170"><template #default="{ row }"><el-tag>{{ states[row.state as keyof typeof states] }}</el-tag><div>{{ row.expired ? '已到期' : '截至观察时刻未到期' }} · 已兑 {{ row.redeemed_count }} / 剩余 {{ row.remaining_count }}</div></template></el-table-column><el-table-column label="过期（UTC）" min-width="230"><template #default="{ row }">{{ row.expires_at }}<div class="small-text muted">观察 {{ row.observed_at }}</div></template></el-table-column><el-table-column label="操作" min-width="260" fixed="right"><template #default="{ row }"><el-button link @click="closeDetail(); inspectBatch(row.id)">详情 / 撤销提案</el-button><el-button v-if="approver && row.state === 'proposed' && !row.expired && row.maker !== session.me?.user.id" link type="primary" @click="openWriter('voucher.batch.approve', row)">独立审批</el-button><el-button v-if="operator && row.state === 'proposed' && row.maker === session.me?.user.id" link type="danger" @click="openWriter('voucher.batch.void', row)">作废</el-button><el-button v-if="operator && row.state === 'active'" link type="danger" @click="openWriter('voucher.revocation.create', row)">提议撤销</el-button></template></el-table-column></el-table><div class="table-footer"><span>固定每页最多 50 条；到期不改写审批历史。</span><div><el-button :disabled="loading || !trail.length" @click="load(trail.at(-1) || '', 'previous')">上一页</el-button><el-button :disabled="loading || !next" @click="load(next, 'next')">下一页</el-button></div></div><div class="voucher-lookup"><el-input v-model="lookupBatch" placeholder="已知批次 UUID" /><el-button @click="closeDetail(); inspectBatch(lookupBatch)">读取批次</el-button></div></section>
  <section v-if="detail || detailError || detailBusy" class="panel voucher-detail"><div class="panel-header"><div class="panel-title">批次详情与撤销决定</div><el-button @click="closeDetail">关闭详情</el-button></div><ErrorNotice :message="detailError" /><template v-if="detail"><p class="mono">{{ detail.id }} · 版本 {{ detail.revision }}</p><p>创建理由：{{ detail.reason }} · 制单人 {{ detail.maker }}</p><p>审批人：{{ detail.approver || '尚未审批' }} · 激活 {{ detail.activated_at || '—' }}</p><p>终结人：{{ detail.terminal_actor || '无' }} · 终结 {{ detail.terminal_at || '—' }}</p><el-table v-loading="detailBusy" :data="revocations" empty-text="尚无撤销提案"><el-table-column label="提案 / 制单人" min-width="200"><template #default="{ row }"><span class="mono">{{ row.id }}</span><div>{{ row.maker }}</div></template></el-table-column><el-table-column label="冻结证据 / 理由" min-width="220"><template #default="{ row }">版本 {{ row.expected_revision }} · 已兑 {{ row.expected_redeemed_count }}<div>{{ row.reason }}</div></template></el-table-column><el-table-column label="不可变决定" min-width="240"><template #default="{ row }"><template v-if="row.decision"><el-tag :type="row.decision.status === 'accepted' ? 'success' : 'warning'">{{ row.decision.status === 'accepted' ? '已批准撤销' : '证据过期，拒绝撤销' }}</el-tag><div>{{ row.decision.approver }} · {{ row.decision.decided_at }}</div><span class="mono">{{ row.decision.operation_id }}</span></template><span v-else>待独立审批</span></template></el-table-column><el-table-column label="操作" width="150"><template #default="{ row }"><el-button v-if="approver && !row.decision && row.maker !== session.me?.user.id" link type="primary" @click="openWriter('voucher.revocation.approve', null, row)">审批原提案</el-button></template></el-table-column></el-table><div class="table-footer"><span>撤销仅停止未兑换券，不扣回已入账赠额。</span><div><el-button :disabled="detailBusy || !detailTrail.length" @click="inspectBatch(detail.id, detailTrail.at(-1) || '', 'previous')">上一页</el-button><el-button :disabled="detailBusy || !detailNext" @click="inspectBatch(detail.id, detailNext, 'next')">下一页</el-button></div></div></template></section>
  <section class="panel voucher-detail"><div class="panel-title">按提案 ID 复核</div><div class="voucher-lookup"><el-input v-model="lookupProposal" placeholder="撤销提案 UUID" @input="scope.invalidate('proposal'); proposal = null; proposalBusy = false" /><el-button :loading="proposalBusy" @click="inspectProposal">只读读取</el-button></div><ErrorNotice :message="proposalError" /><template v-if="proposal"><p class="mono">{{ proposal.id }} · 原批次 {{ proposal.batch_id }}</p><p>{{ proposal.reason }} · 原版本 {{ proposal.expected_revision }} · 原已兑 {{ proposal.expected_redeemed_count }}</p><p v-if="proposal.decision">{{ proposal.decision.status === 'accepted' ? '已批准撤销' : '证据过期，已拒绝' }} · {{ proposal.decision.approver }} · {{ proposal.decision.decided_at }}</p><el-button v-if="approver && !proposal.decision && proposal.maker !== session.me?.user.id" @click="openWriter('voucher.revocation.approve', null, proposal)">独立审批原提案</el-button></template></section>
  <VoucherPolicy @changed="capabilities" /><CommerceCommands scope="platform" @settled="settled" /><CommerceWriter v-model:open="writerOpen" :kind="writerKind" scope="platform" :batch="writerBatch" :proposal="writerProposal" :enabled="caps?.vouchers_enabled === true" @settled="settled" />
</template>
<style scoped>.voucher-list,.voucher-detail{margin-top:24px}.voucher-detail{font-size:12px;line-height:1.8;overflow-wrap:anywhere}.voucher-lookup{display:flex;gap:12px;max-width:650px;margin-top:16px}</style>
