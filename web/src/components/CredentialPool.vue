<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, ArrowUp, Plus, Refresh } from '@element-plus/icons-vue'
import { errorText, request } from '../api'
import { shortId, time } from '../format'
import { clearCredentialPoolSecrets, createCredentialPoolScope, credentialPoolDrafts, credentialPoolPath, credentialPoolSaveRequest, credentialRevokeRequest, isCurrentCredentialPool, moveCredentialPoolMember, newCredentialPoolDraft, parseCredentialPool } from '../credential-pool'
import type { CredentialPoolChannel, CredentialPoolDraft, CredentialPoolView } from '../credential-pool'

const props = defineProps<{ modelValue: boolean; channel: CredentialPoolChannel | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [view: CredentialPoolView] }>()
const scope = createCredentialPoolScope()
const pool = ref<CredentialPoolView | null>(null), drafts = ref<CredentialPoolDraft[]>([])
const version = ref(''), error = ref(''), loading = ref(false), saving = ref(false), revoking = ref('')
const visible = computed({ get: () => props.modelValue, set: value => { if (!value) reset(); emit('update:modelValue', value) } })
const busy = computed(() => loading.value || saving.value || !!revoking.value)
const editable = computed(() => !!pool.value && isCurrentCredentialPool(pool.value))
function reset() {
  scope.reset(); clearCredentialPoolSecrets(drafts.value); drafts.value = []; pool.value = null
  version.value = ''; error.value = ''; loading.value = false; saving.value = false; revoking.value = ''
}
function apply(view: CredentialPoolView) {
  clearCredentialPoolSecrets(drafts.value); pool.value = view; drafts.value = credentialPoolDrafts(view)
}
async function load(historical = '') {
  if (!props.modelValue || !props.channel || saving.value || revoking.value) return
  const channelID = props.channel.id, ticket = scope.ticket()
  clearCredentialPoolSecrets(drafts.value); drafts.value = []; pool.value = null; error.value = ''; loading.value = true
  try {
    const response = await request<unknown>(credentialPoolPath(channelID, historical.trim()))
    const view = parseCredentialPool(response.data)
    if (view.channel_id !== channelID || (historical.trim() && view.channel_version_id !== historical.trim())) throw new Error('凭据池的渠道或版本不匹配。')
    if (!scope.accepts(ticket)) return
    apply(view); version.value = historical.trim()
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
function add() { if (editable.value && !busy.value && drafts.value.length < 32) drafts.value.push(newCredentialPoolDraft()) }
function remove(index: number) {
  if (!editable.value || busy.value || drafts.value.length <= 1) return
  const removed = drafts.value.splice(index, 1); clearCredentialPoolSecrets(removed)
}
function move(index: number, direction: -1 | 1) { if (editable.value && !busy.value) moveCredentialPoolMember(drafts.value, index, direction) }
async function save() {
  if (!pool.value || !editable.value || busy.value || !props.modelValue) return
  error.value = ''
  try {
    const operation = credentialPoolSaveRequest(pool.value, drafts.value), channelID = pool.value.channel_id, ticket = scope.ticket()
    saving.value = true
    try {
      const response = await request<unknown>(operation.path, operation.method, operation.body)
      const view = parseCredentialPool(response.data)
      if (view.channel_id !== channelID || !isCurrentCredentialPool(view)) throw new Error('保存结果的渠道或当前版本不匹配，请刷新核实。')
      if (!scope.accepts(ticket)) return
      apply(view); version.value = ''; emit('saved', view)
      ElMessage.success('凭据池新版本已保存。请重新发布原模型路由后启用。')
    } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
    finally { if (scope.accepts(ticket)) saving.value = false }
  } catch (e) { error.value = errorText(e) }
}
async function revoke(member: CredentialPoolDraft) {
  if (!pool.value || !member.credential_id || member.revoked_at || busy.value) return
  const channelID = pool.value.channel_id, credentialID = member.credential_id, selectedVersion = pool.value.channel_version_id, confirmation = scope.snapshot()
  try {
    await ElMessageBox.confirm(`永久撤销凭据 ${credentialID}？该平台凭据身份在全部历史渠道版本中都会被禁用，无法恢复。仅从新池移出不会达到此效果；本操作不保证供应商处的同一密钥已全局失效。撤销后会重新读取此版本，未保存的池编辑将清除。`, '永久紧急撤销', { type: 'warning', confirmButtonText: '永久撤销此身份', cancelButtonText: '取消', closeOnClickModal: false })
  } catch { return }
  if (!scope.accepts(confirmation) || !props.modelValue || busy.value) return
  const ticket = scope.ticket(); revoking.value = credentialID; error.value = ''
  try {
    const operation = credentialRevokeRequest(channelID, credentialID)
    await request<{ ok: boolean }>(operation.path, operation.method)
    if (!scope.accepts(ticket)) return
    // Reload only this version's metadata; never replay a saved pool or route.
    const response = await request<unknown>(credentialPoolPath(channelID, selectedVersion))
    const view = parseCredentialPool(response.data)
    if (view.channel_id !== channelID || view.channel_version_id !== selectedVersion) throw new Error('撤销结果的渠道或版本不匹配，请刷新核实。')
    if (!scope.accepts(ticket)) return
    apply(view); emit('saved', view); ElMessage.success('凭据身份已永久撤销。已撤销成员需从新池移出或替换。')
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) revoking.value = '' }
}
watch(() => [props.modelValue, props.channel?.id], () => { reset(); if (props.modelValue && props.channel) void load() }, { immediate: true })
onBeforeUnmount(reset)
</script>

<template>
  <el-dialog v-model="visible" :title="`${channel?.name || ''} · 凭据池`" width="1000px" destroy-on-close :close-on-click-modal="false">
    <p class="dialog-description">在一个渠道内管理多个供应商凭据。先按渠道选路，再按池内权重选择可用成员；渠道总并发与凭据并发同时生效。</p>
    <el-alert title="保存会创建新渠道版本。请在模型与路由中重新发布原路由后启用；保存凭据池不会发布路由、授权模型或修改资金。" type="info" :closable="false" show-icon />
    <div class="pool-controls"><el-input v-model="version" placeholder="输入历史渠道版本 ID" aria-label="历史渠道版本 ID" :disabled="busy" clearable style="flex:1;min-width:220px" @keyup.enter="load(version)" /><el-button :disabled="busy || !version.trim()" @click="load(version)">查看历史</el-button><el-button :icon="Refresh" :disabled="saving || !!revoking" :loading="loading" @click="load()">恢复查看当前</el-button></div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon style="margin-bottom:16px" />
    <div v-loading="loading">
      <template v-if="pool">
        <p class="pool-version small-text muted">渠道版本 <span class="mono">{{ pool.channel_version_id }}</span> · 当前版本 <span class="mono">{{ pool.current_version }}</span></p>
        <el-alert v-if="!editable" title="历史池配置只读，不能直接恢复或覆盖当前池。紧急撤销是覆盖全部历史版本的独立身份操作。" type="warning" :closable="false" style="margin:14px 0" />
        <el-alert v-else-if="pool.stale" title="当前渠道或凭据状态已变化。可以修订当前池；已撤销成员须移出或替换后才能保存。" type="warning" :closable="false" style="margin:14px 0" />
        <p class="field-hint">顺序第一项是主凭据。移出只影响待发布的新版本，旧路由仍可使用该身份；需要立即阻止旧版本派发时，请使用“永久撤销”。供应商密钥仅在新增时输入，查询不会回显。</p>
        <article v-for="(member, index) in drafts" :key="member.credential_id || `new-${index}`" class="pool-member">
          <div class="member-heading"><strong>成员 {{ index + 1 }}</strong><el-tag v-if="index === 0">主凭据</el-tag><el-tag v-if="member.revoked_at" type="danger">已永久撤销</el-tag><el-tag v-else-if="!member.credential_id" type="info">新增</el-tag><span class="mono muted member-id" :title="member.credential_id">{{ member.credential_id ? shortId(member.credential_id) : '保存后分配身份' }}</span></div>
          <el-form label-position="top" :disabled="!editable || busy"><div class="pool-fields"><el-form-item label="名称（必填，最多 64 字节）"><el-input v-model="member.label" maxlength="64" aria-label="凭据名称" placeholder="用于辨认供应商账号" /></el-form-item><el-form-item label="权重（1—1,000,000）"><el-input-number v-model="member.weight" :min="1" :max="1000000" :precision="0" aria-label="凭据权重" controls-position="right" /></el-form-item><el-form-item label="凭据并发（1—1,000）"><el-input-number v-model="member.max_inflight" :min="1" :max="1000" :precision="0" aria-label="凭据并发" controls-position="right" /></el-form-item></div><el-form-item v-if="!member.credential_id" label="新供应商密钥"><el-input v-model="member.secret" type="password" autocomplete="new-password" aria-label="新供应商密钥" placeholder="只在本次编辑内保存；关闭或切换版本会清除" /></el-form-item></el-form>
          <p v-if="member.revoked_at" class="field-hint">撤销于 {{ time(member.revoked_at) }}，不能恢复或保留在新池中。</p>
          <div class="member-actions"><el-button :icon="ArrowUp" :disabled="!editable || busy || index === 0" aria-label="凭据上移" @click="move(index, -1)">上移</el-button><el-button :icon="ArrowDown" :disabled="!editable || busy || index === drafts.length - 1" aria-label="凭据下移" @click="move(index, 1)">下移</el-button><el-button :disabled="!editable || busy || drafts.length <= 1" @click="remove(index)">从新池移出</el-button><el-button v-if="member.credential_id" type="danger" plain :disabled="busy || !!member.revoked_at" :loading="revoking === member.credential_id" @click="revoke(member)">永久撤销</el-button></div>
        </article>
        <el-button :icon="Plus" :disabled="!editable || busy || drafts.length >= 32" @click="add">新增凭据（{{ drafts.length }} / 32）</el-button>
      </template>
      <el-empty v-else-if="!loading && !error" description="暂无凭据池配置。" />
    </div>
    <template #footer><el-button @click="visible = false">关闭</el-button><el-button type="primary" :loading="saving" :disabled="!editable || loading || !!revoking" @click="save">保存新版本</el-button></template>
  </el-dialog>
</template>

<style scoped>
.pool-controls{display:flex;align-items:center;flex-wrap:wrap;gap:10px;margin:18px 0}.pool-version{overflow-wrap:anywhere;line-height:1.8}.pool-member{border:1px solid #e3eaf0;border-radius:8px;padding:18px;margin:16px 0}.member-heading{display:flex;align-items:center;flex-wrap:wrap;gap:10px;margin-bottom:16px}.member-id{margin-left:auto;font-size:11px}.pool-fields{display:grid;grid-template-columns:minmax(160px,1fr) 190px 180px;gap:16px}.pool-fields .el-input-number{width:100%}.member-actions{display:flex;gap:8px;flex-wrap:wrap}.member-actions .el-button{margin:0}@media(max-width:700px){.pool-fields{grid-template-columns:1fr}.pool-member{padding:14px}.member-id{margin-left:0}}
</style>
