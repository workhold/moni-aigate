<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, InfoFilled } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import { errorText, request } from '../api'
import { time } from '../format'
import { session } from '../session'

interface Gate { required: boolean; epoch: number; proposal_id?: string; requester?: string; approver?: string; evidence_ref?: string; reason?: string; proposed_at?: string; approved_at?: string; latched_at?: string }
interface RecoveryStatus { gate: Gate; recovery_mode: boolean; restart_required: boolean }
const status = ref<RecoveryStatus | null>(null); const loading = ref(false); const saving = ref(false); const error = ref(''); const formError = ref('')
const form = ref({ evidence_ref: '', reason: '' })
const canPropose = computed(() => session.me?.user.platform_role === 'finance_operator' && status.value?.gate.required && !status.value.gate.proposal_id)
const canApprove = computed(() => session.me?.user.platform_role === 'finance_approver' && status.value?.gate.required && !!status.value.gate.proposal_id && status.value.gate.requester !== session.me?.user.id)
async function load() {
  loading.value = true; error.value = ''
  try { status.value = (await request<RecoveryStatus>('/recovery/status')).data }
  catch (e) { status.value = null; error.value = errorText(e) } finally { loading.value = false }
}
async function propose() {
  if (!canPropose.value || saving.value || !status.value) return
  saving.value = true; formError.value = ''
  try {
    if (!form.value.evidence_ref.trim() || !form.value.reason.trim()) throw new Error('请填写受控核对证据引用与恢复理由。')
    await request('/recovery/propose', 'POST', { epoch: status.value.gate.epoch, evidence_ref: form.value.evidence_ref.trim(), reason: form.value.reason.trim() })
    form.value = { evidence_ref: '', reason: '' }; ElMessage.success('恢复提案已冻结，等待独立财务审批账号复核。'); await load()
  } catch (e) { formError.value = errorText(e) } finally { saving.value = false }
}
async function approve() {
  if (!canApprove.value || saving.value || !status.value) return
  try { await ElMessageBox.confirm('请先核对提案证据与资金账本。批准会解除持久恢复门禁；当前服务仍保持恢复模式，须由运维移除恢复配置并重启。', '批准恢复核对', { type: 'warning', confirmButtonText: '已核对，批准', cancelButtonText: '取消' }) } catch { return }
  saving.value = true; error.value = ''
  try {
    await request('/recovery/approve', 'POST', { epoch: status.value.gate.epoch, proposal_id: status.value.gate.proposal_id })
    ElMessage.success('恢复核对已批准。请交由运维按流程重启后恢复业务。'); await load()
  } catch (e) { error.value = errorText(e) } finally { saving.value = false }
}
onMounted(() => { void load() })
</script>

<template>
  <PageHeader eyebrow="RECOVERY CONTROL" title="恢复核对" description="在灾后恢复或资金核查期间，由不同财务账号提交证据与批准恢复。"><el-button :icon="Refresh" :loading="loading" @click="load">刷新状态</el-button></PageHeader>
  <ErrorNotice :message="error" @retry="load" />
  <el-skeleton v-if="loading && !status" :rows="5" animated />
  <template v-if="status">
    <el-alert v-if="status.gate.required" type="warning" title="持久恢复门禁尚未解除。请完成资金核对、提交提案，再由独立财务审批账号复核。" :closable="false" show-icon style="margin-bottom:22px" />
    <el-alert v-else-if="status.restart_required || status.recovery_mode" type="warning" :title="status.gate.approved_at ? '双人恢复核对已通过，当前进程仍处于恢复模式。请由运维移除 RECOVERY_MODE 配置并重启，确认就绪后再开放付费调用。' : '当前进程处于恢复模式，尚无本批次审批记录。请按恢复手册核对状态，不要将服务就绪或门禁状态视为已完成财务核对。'" :closable="false" show-icon style="margin-bottom:22px" />
    <el-alert v-else type="success" title="当前无需恢复审批。业务是否可用仍以服务就绪状态为准。" :closable="false" show-icon style="margin-bottom:22px" />
    <section class="panel"><div class="panel-header"><div><div class="panel-title">当前恢复阶段</div><div class="panel-subtitle">提案绑定当前恢复批次；批次、提案或账本校验冲突时，需刷新并重新核对。</div></div><el-tag :type="status.gate.required || status.recovery_mode ? 'warning' : 'success'">{{ status.gate.required ? '待核对' : status.recovery_mode ? (status.gate.approved_at ? '待运维重启' : '恢复模式') : '无需恢复审批' }}</el-tag></div><div class="panel-body"><dl class="detail-list"><dt>恢复批次</dt><dd class="mono">{{ status.gate.epoch }}</dd><dt>本进程恢复模式</dt><dd>{{ status.recovery_mode ? '开启' : '关闭' }}</dd><dt>进入恢复时间</dt><dd>{{ time(status.gate.latched_at) }}</dd><dt>提案 ID</dt><dd class="mono">{{ status.gate.proposal_id || '尚无提案' }}</dd><dt>提交账号</dt><dd class="mono">{{ status.gate.requester || '—' }}</dd><dt>核对证据</dt><dd>{{ status.gate.evidence_ref || '—' }}</dd><dt>恢复理由</dt><dd>{{ status.gate.reason || '—' }}</dd><dt>提案时间</dt><dd>{{ time(status.gate.proposed_at) }}</dd><dt>审批账号</dt><dd class="mono">{{ status.gate.approver || '—' }}</dd><dt>审批时间</dt><dd>{{ time(status.gate.approved_at) }}</dd></dl><div v-if="canApprove" style="margin-top:25px"><el-button type="primary" :loading="saving" @click="approve">核对并批准恢复</el-button></div><p v-else-if="status.gate.required && status.gate.proposal_id" class="field-hint" style="margin-top:22px">提案已冻结，须由不同的财务审批账号处理；普通管理员与制单人不能批准。</p></div></section>
    <section v-if="canPropose" class="panel"><div class="panel-header"><div class="panel-title">提交恢复提案</div></div><div class="panel-body" style="max-width:680px"><el-alert v-if="formError" :title="formError" type="error" :closable="false" style="margin-bottom:20px" /><el-form label-position="top" @submit.prevent="propose"><el-form-item label="受控核对证据引用" required><el-input v-model="form.evidence_ref" maxlength="256" placeholder="已完成核对的受控记录引用" /><div class="field-hint">填写受控引用或编号，不填写秘密下载链接。</div></el-form-item><el-form-item label="恢复理由" required><el-input v-model="form.reason" type="textarea" :rows="3" maxlength="200" show-word-limit /></el-form-item><el-button native-type="submit" type="primary" :loading="saving">提交并冻结提案</el-button></el-form></div></section>
    <p class="section-note"><el-icon><InfoFilled /></el-icon>此页面用于核对与审批，不执行服务重启。提交和批准均要求近期再认证，审批不修改任何资金流水。</p>
  </template>
</template>
