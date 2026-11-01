<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { CopyDocument, Refresh } from '@element-plus/icons-vue'
import { ApiError, errorText, request } from '../api'
import { shortId, time } from '../format'
import { isAdmin, session } from '../session'
import { channelOperationApplyRequest, channelOperationCommand, channelOperationDetailPath, channelOperationHistoryPath, channelOperationItemText, channelOperationJournal, channelOperationPreflightRequest, channelOperationReasonText, createChannelOperationsScope, parseChannelOperationPreview, parseChannelOperationSummary, unknownChannelOperationResult } from '../channel-operations'
import type { ChannelOperationAction, ChannelOperationCommand, ChannelOperationItem, ChannelOperationPreview, ChannelOperationReceipt, ChannelOperationSummary, ChannelOperationTarget, SubmittedChannelOperation } from '../channel-operations'

const props = defineProps<{ modelValue: boolean; action: ChannelOperationAction; targets: ChannelOperationTarget[]; selectionEpoch: number; mode: 'preview' | 'history'; names?: Record<string, string> }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; applied: [receipt: ChannelOperationReceipt] }>()
const scope = createChannelOperationsScope()
const tab = ref('preview'), error = ref(''), preview = ref<ChannelOperationPreview | null>(null), command = ref<ChannelOperationCommand | null>(null)
const previewBusy = ref(false), applying = ref(false), detailBusy = ref(false), historyBusy = ref(false)
const receipt = ref<ChannelOperationReceipt | null>(null), activeID = ref(''), queryID = ref('')
const known = ref<SubmittedChannelOperation[]>([]), history = ref<ChannelOperationSummary[]>([]), historyLoaded = ref(false)
const historyCursor = ref(''), nextCursor = ref(''), historyPages = ref<string[]>([])
const actor = computed(() => session.me?.user.id ?? '')
const pending = computed(() => known.value.filter(operation => operation.state === 'unknown'))
const visible = computed({ get: () => props.modelValue, set: value => { if (!value) reset(); emit('update:modelValue', value) } })
const actionTitle = computed(() => (command.value?.action ?? props.action) === 'enable' ? '启用' : '停用')
const displayedItems = computed(() => receipt.value?.items ?? preview.value?.items ?? [])
function refreshKnown() { known.value = actor.value ? channelOperationJournal.list(actor.value) : [] }
function reset() {
  scope.reset(); error.value = ''; preview.value = null; command.value = null; receipt.value = null; activeID.value = ''; queryID.value = ''
  previewBusy.value = false; applying.value = false; detailBusy.value = false; historyBusy.value = false
  history.value = []; historyLoaded.value = false; historyCursor.value = ''; nextCursor.value = ''; historyPages.value = []; refreshKnown()
}
async function preflight() {
  if (!props.modelValue || !isAdmin.value || applying.value) return
  const ticket = scope.ticket('preview')
  preview.value = null; receipt.value = null; activeID.value = ''; error.value = ''; previewBusy.value = true
  try {
    const frozen = channelOperationCommand({ action: props.action, targets: props.targets })
    command.value = frozen
    const operation = channelOperationPreflightRequest(frozen)
    const result = parseChannelOperationPreview((await request<unknown>(operation.path, operation.method, operation.body)).data, frozen)
    if (scope.accepts(ticket)) preview.value = result
  } catch (e) { if (scope.accepts(ticket)) { command.value = null; error.value = errorText(e) } }
  finally { if (scope.accepts(ticket)) previewBusy.value = false }
}
async function apply() {
  if (!props.modelValue || !isAdmin.value || !command.value || !preview.value?.can_apply || applying.value || pending.value.length) return
  const submittingActor = actor.value, ticket = scope.ticket('apply'), display = scope.ticket('display')
  error.value = ''; applying.value = true
  try {
    // Allocate and remember the ID once, before any POST. An uncertain result
    // leaves this actor's journal pending even if the dialog or route closes.
    const submitted = channelOperationJournal.begin(submittingActor, command.value)
    activeID.value = submitted.operation_id; queryID.value = submitted.operation_id; refreshKnown()
    const operation = channelOperationApplyRequest(submitted.command, submitted.operation_id)
    try {
      const response = await request<unknown>(operation.path, operation.method, operation.body)
      const confirmed = channelOperationJournal.resolve(submittingActor, submitted.operation_id, response.data)
      if (!scope.accepts(ticket)) return
      refreshKnown()
      if (confirmed.status === 'applied') emit('applied', confirmed)
      if (!scope.accepts(display)) return
      activeID.value = confirmed.operation_id; receipt.value = confirmed; preview.value = null
      if (confirmed.status === 'applied') ElMessage.success(`整批已应用，${confirmed.changed_count} 个渠道状态变更。`)
      else ElMessage.warning('整批已拒绝，没有变更任何渠道。请查看逐条原因。')
    } catch (e) {
      if (!unknownChannelOperationResult(e)) channelOperationJournal.notSubmitted(submittingActor, submitted.operation_id)
      if (!scope.accepts(ticket) || !scope.accepts(display)) return
      refreshKnown(); preview.value = null
      error.value = unknownChannelOperationResult(e) ? `提交结果待确认。请只查询原操作 ID ${submitted.operation_id}，不要重新提交。${errorText(e)}` : `提交未获受理，请修正后重新预检。${errorText(e)}`
    }
  } catch (e) { if (scope.accepts(ticket) && scope.accepts(display)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) { applying.value = false; refreshKnown() } }
}
async function query(operationID = queryID.value) {
  if (!props.modelValue || !isAdmin.value) return
  const selected = operationID.trim(), queryingActor = actor.value, ticket = scope.ticket('detail'), display = scope.ticket('display')
  scope.ticket('preview'); preview.value = null; command.value = null; previewBusy.value = false
  error.value = ''; receipt.value = null; detailBusy.value = true; queryID.value = selected
  try {
    const wasPending = channelOperationJournal.list(queryingActor).some(operation => operation.operation_id === selected && operation.state === 'unknown')
    const response = await request<unknown>(channelOperationDetailPath(selected))
    const result = channelOperationJournal.resolve(queryingActor, selected, response.data)
    if (!scope.accepts(ticket) || !scope.accepts(display)) return
    activeID.value = selected; receipt.value = result; refreshKnown()
    if (wasPending && result.status === 'applied') emit('applied', result)
  } catch (e) {
    if (scope.accepts(ticket) && scope.accepts(display)) error.value = e instanceof ApiError && e.status === 404 ? '暂未查询到此回执；原事务可能仍未结束，不能据此判断未提交。请保留同一操作 ID，稍后再次查询。' : errorText(e)
  } finally { if (scope.accepts(ticket)) detailBusy.value = false }
}
async function loadHistory(cursor = '') {
  if (!props.modelValue || !isAdmin.value || historyBusy.value) return
  const ticket = scope.ticket('history'); historyBusy.value = true; error.value = ''
  try {
    const response = await request<unknown[]>(channelOperationHistoryPath(cursor))
    if (!Array.isArray(response.data) || response.data.length > 50) throw new Error('回执历史页格式无效。')
    const records = response.data.map(parseChannelOperationSummary)
    if (!scope.accepts(ticket)) return
    history.value = records; historyLoaded.value = true; historyCursor.value = cursor; nextCursor.value = response.next_cursor ?? ''
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) historyBusy.value = false }
}
function changeTab(value: string | number) { if (value === 'history' && !historyLoaded.value) void loadHistory() }
async function next() { historyPages.value.push(historyCursor.value); await loadHistory(nextCursor.value) }
async function previous() { await loadHistory(historyPages.value.pop() ?? '') }
async function copy(value: string) { try { await navigator.clipboard.writeText(value); ElMessage.success('操作 ID 已复制。') } catch { ElMessage.warning('无法复制，请手动选择操作 ID。') } }
function itemName(item: ChannelOperationItem) { return props.names?.[item.channel_id] ?? shortId(item.channel_id) }
watch(() => [props.modelValue, props.action, props.mode, actor.value, isAdmin.value], () => {
  reset(); tab.value = props.mode
  if (!props.modelValue || !isAdmin.value) return
  if (props.mode === 'preview') void preflight()
  else void loadHistory()
}, { immediate: true })
watch(() => props.selectionEpoch, () => {
  // Selection changes invalidate only the unsent preview. Receipt and apply
  // responses belong to their frozen operation, independently of the list.
  scope.ticket('preview'); preview.value = null; command.value = null; previewBusy.value = false
  if (props.modelValue && tab.value === 'preview' && !activeID.value) error.value = '列表或勾选已变化，本次预检已失效。请关闭后重新勾选渠道。'
})
onBeforeUnmount(() => scope.reset())
</script>

<template>
  <el-dialog v-model="visible" title="渠道批量启停与回执" width="1080px" destroy-on-close :close-on-click-modal="false">
    <p class="dialog-description">一次只处理当前页选中的渠道。预检后仍会在提交事务中重新核对；任一渠道被阻断则整批不变更。</p>
    <el-alert v-if="pending.length" type="warning" :closable="false" show-icon title="有结果待确认的操作。请查询原操作 ID；确认结果前不能发起新的批量提交。" style="margin-bottom:14px"><div v-for="operation in pending" :key="operation.operation_id" class="operation-reference"><span class="mono">{{ operation.operation_id }}</span><el-button link type="primary" :disabled="detailBusy" @click="query(operation.operation_id)">查询原 ID</el-button><el-button link :icon="CopyDocument" @click="copy(operation.operation_id)">复制</el-button></div></el-alert>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon style="margin-bottom:16px" />
    <el-tabs v-model="tab" @tab-change="changeTab">
      <el-tab-pane label="本次预检与提交" name="preview">
        <el-alert v-if="(command?.action ?? action) === 'enable'" type="warning" :closable="false" title="启用可能立即恢复该渠道已有的历史发布路由；仍受凭据撤销、模型绑定隔离、授权、健康与容量约束。不会解除渠道隔离。" show-icon />
        <el-alert v-else type="warning" :closable="false" title="停用阻止后续尝试准入；已经发送给上游的请求无法撤回。不会修改凭据、路由、客户授权或资金。" show-icon />
        <div v-loading="previewBusy" class="preview-summary"><template v-if="preview"><strong>预检 {{ preview.items.length }} 个渠道 · 预计变更 {{ preview.changed_count }} 个</strong><el-tag :type="preview.can_apply ? 'success' : 'danger'">{{ preview.can_apply ? '整批可提交' : '存在阻断，整批不可提交' }}</el-tag><p class="field-hint">预检不是承诺或锁。任何版本或管理 revision 变化都会在提交时重新检查。</p></template><p v-else-if="!previewBusy && !activeID" class="field-hint">请从当前渠道页重新勾选后发起预检。</p></div>
        <div v-if="activeID" class="operation-reference"><span>本次操作 ID</span><strong class="mono">{{ activeID }}</strong><el-button text :icon="CopyDocument" @click="copy(activeID)">复制</el-button><el-button :disabled="applying || detailBusy" @click="query(activeID)">只读查询结果</el-button></div>
        <el-button v-if="preview" type="primary" :loading="applying" :disabled="!preview.can_apply || previewBusy || pending.length > 0 || !isAdmin" @click="apply">确认整批{{ actionTitle }}（{{ preview.items.length }} 个）</el-button>
      </el-tab-pane>
      <el-tab-pane label="操作回执与历史" name="history">
        <div class="history-controls"><el-input v-model="queryID" placeholder="输入完整操作 UUID 查询回执" aria-label="操作回执 ID" clearable style="flex:1;min-width:230px" @keyup.enter="query()" /><el-button :loading="detailBusy" :disabled="!queryID.trim()" @click="query()">查询回执</el-button><el-button :icon="Refresh" :loading="historyBusy" @click="loadHistory(historyCursor)">刷新历史</el-button></div>
        <p v-if="known.length" class="field-hint">本标签页保留了 {{ known.length }} 个已提交 ID，关闭或切换页面后仍可查询；也可复制 ID 后手动查询。</p>
        <el-select v-if="known.length" v-model="queryID" placeholder="选择本标签页的操作 ID" aria-label="本标签页操作 ID" style="width:100%;margin-bottom:14px"><el-option v-for="operation in known" :key="operation.operation_id" :value="operation.operation_id" :label="`${operation.operation_id} · ${operation.state === 'unknown' ? '结果待确认' : operation.state === 'not_submitted' ? '未获受理' : operation.receipt?.status === 'rejected' ? '整批拒绝' : '整批已应用'}`" /></el-select>
        <el-table v-loading="historyBusy" :data="history" size="small" max-height="250" empty-text="暂无批量操作回执"><el-table-column label="操作 ID" min-width="175"><template #default="{ row }"><span class="mono" :title="row.operation_id">{{ shortId(row.operation_id) }}</span></template></el-table-column><el-table-column label="动作" width="80"><template #default="{ row }">{{ row.action === 'enable' ? '启用' : '停用' }}</template></el-table-column><el-table-column label="整体结果" min-width="120"><template #default="{ row }"><el-tag :type="row.status === 'applied' ? 'success' : 'danger'">{{ row.status === 'applied' ? '整批已应用' : '整批拒绝' }}</el-tag></template></el-table-column><el-table-column label="变更 / 目标" width="110"><template #default="{ row }">{{ row.changed_count }} / {{ row.target_count }}</template></el-table-column><el-table-column label="时间" min-width="170"><template #default="{ row }">{{ time(row.created_at) }}</template></el-table-column><el-table-column label="操作" width="85"><template #default="{ row }"><el-button link type="primary" @click="query(row.operation_id)">查看</el-button></template></el-table-column></el-table>
        <div class="history-controls"><el-button :disabled="!historyPages.length || historyBusy" @click="previous">上一页</el-button><el-button :disabled="!nextCursor || historyBusy" @click="next">下一页</el-button></div>
      </el-tab-pane>
    </el-tabs>
    <section v-loading="detailBusy" class="operation-details">
      <template v-if="receipt"><el-alert :type="receipt.status === 'applied' ? 'success' : 'error'" :closable="false" show-icon :title="receipt.status === 'applied' ? `整批已应用：${receipt.changed_count} 个渠道状态变更，${receipt.target_count - receipt.changed_count} 个无变化。` : '整批已拒绝：0 个渠道变更。其他原本可执行的项也未应用。'" /><p class="field-hint">操作 {{ receipt.operation_id }} · {{ receipt.action === 'enable' ? '启用' : '停用' }} · {{ time(receipt.created_at) }} · 操作者 {{ receipt.actor }}。以下是提交时的不可变回执，不表示渠道当前状态。</p></template>
      <el-table v-if="displayedItems.length" :data="displayedItems" size="small" max-height="420" aria-label="批量渠道预检或回执明细"><el-table-column label="渠道 / 期望版本" min-width="185"><template #default="{ row }"><strong>{{ itemName(row) }}</strong><div class="mono muted" :title="row.channel_id">{{ shortId(row.channel_id) }}</div><div class="mono small-text" :title="row.expected_version">{{ shortId(row.expected_version) }} · r{{ row.expected_revision }}</div></template></el-table-column><el-table-column label="前置 / 后置状态" min-width="180"><template #default="{ row }"><template v-if="row.before"><div>{{ row.before.enabled ? '启用' : '停用' }}{{ row.before.quarantined ? ' · 隔离' : '' }} · r{{ row.before.management_revision }}</div><div v-if="row.after">→ {{ row.after.enabled ? '启用' : '停用' }}{{ row.after.quarantined ? ' · 隔离' : '' }} · r{{ row.after.management_revision }}</div></template><span v-else>渠道不存在</span></template></el-table-column><el-table-column label="可用凭据 / 已发布绑定" min-width="155"><template #default="{ row }"><div>{{ row.live_credentials }} / {{ row.published_bindings }}</div><div class="muted small-text">旧版本绑定 {{ row.older_version_bindings }}</div></template></el-table-column><el-table-column label="逐项结果 / 原因" min-width="230"><template #default="{ row }"><el-tag :type="row.status === 'blocked' || row.status === 'not_applied' ? 'danger' : row.status === 'changed' ? 'success' : 'info'">{{ channelOperationItemText(row.status) }}</el-tag><div v-if="row.reason" class="field-hint">{{ channelOperationReasonText(row.reason) }}</div></template></el-table-column></el-table>
      <p v-if="displayedItems.length" class="field-hint">绑定数和未撤销凭据数仅供核查，不代表全部模型可用或实时容量充足。</p>
    </section>
    <template #footer><el-button @click="visible = false">关闭</el-button></template>
  </el-dialog>
</template>

<style scoped>
.preview-summary{min-height:50px;margin:18px 0}.preview-summary .el-tag{margin-left:12px}.history-controls,.operation-reference{display:flex;align-items:center;flex-wrap:wrap;gap:10px;margin:14px 0}.operation-reference .mono{overflow-wrap:anywhere;font-size:11px}.operation-details{margin-top:18px;min-height:30px}
</style>
