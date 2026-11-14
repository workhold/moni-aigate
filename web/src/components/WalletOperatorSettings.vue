<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { errorText, newOperationKey, request } from '../api'
import { time } from '../format'
import { isAdmin } from '../session'
import { parseWalletOperatorSetting, parseWalletOperatorSettings, walletOperatorCommand, walletOperatorConfirmation, type WalletOperatorSettings } from '../wallet-operator-settings'

const settings = ref<WalletOperatorSettings | null>(null)
const loading = ref(false), saving = ref(false), error = ref(''), formError = ref('')
const dialog = ref(false), acknowledged = ref(false), reason = ref(''), target = ref(false), operationID = ref('')
let generation = 0

async function load() {
  if (!isAdmin.value) return
  const ticket = ++generation
  loading.value = true; error.value = ''
  try {
    const response = await request<unknown>('/platform/wallet-operator-settings')
    if (ticket === generation && isAdmin.value) settings.value = parseWalletOperatorSettings(response.data)
  } catch (cause) { if (ticket === generation) error.value = errorText(cause) }
  finally { if (ticket === generation) loading.value = false }
}
function openChange() {
  if (!settings.value || saving.value) return
  target.value = !settings.value.current.single_operator
  reason.value = ''; formError.value = ''; acknowledged.value = false
  operationID.value = newOperationKey(); dialog.value = true
}
async function save() {
  if (!settings.value || saving.value) return
  formError.value = ''
  if (!acknowledged.value) { formError.value = '请先确认已理解单人运营的影响。'; return }
  const enabling = target.value
  try {
    await ElMessageBox.confirm(
      enabling
        ? '开启后，同一 admin 账号可以制单并审批钱包单据；每次自批都会记录 single_operator_exception。这不是双人审批。确认开启？'
        : '关闭后，admin 不再获得钱包制单/审批例外，财务角色仍须不同账号制单与审批；已批准历史单据保持有效。确认关闭？',
      '二次确认单人运营设置',
      { type: 'warning', confirmButtonText: enabling ? '确认开启并写审计' : '确认关闭并写审计', cancelButtonText: '取消', closeOnClickModal: false },
    )
  } catch { return }
  saving.value = true
  try {
    const body = walletOperatorCommand({ operation_id: operationID.value, expected_revision: settings.value.current.version, single_operator: target.value, reason: reason.value.trim(), confirmation: walletOperatorConfirmation })
    const response = await request<unknown>('/platform/wallet-operator-settings', 'PUT', body)
    const updated = parseWalletOperatorSetting(response.data)
    settings.value = { current: updated, versions: [updated, ...settings.value.versions.filter(item => item.operation_id !== updated.operation_id)].slice(0, 50) }
    dialog.value = false; operationID.value = ''
    ElMessage.success('单人运营设置已追加新版本并写入审计。')
    await load()
  } catch (cause) { formError.value = errorText(cause) }
  finally { saving.value = false }
}
watch(isAdmin, admin => { generation++; settings.value = null; dialog.value = false; if (admin) void load() })
onMounted(() => { if (isAdmin.value) void load() })
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <section v-if="isAdmin" class="panel operator-settings" aria-label="单人运营设置">
    <div class="panel-header"><div><h2 class="panel-title">单人运营设置</h2><p class="panel-subtitle">仅影响钱包制单、审批与后续转账例外；对账、恢复、采购和兑换券仍执行各自的双人控制。</p></div><el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button></div>
    <div class="operator-body">
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
      <template v-else-if="settings">
        <el-alert :type="settings.current.single_operator ? 'warning' : 'info'" :title="settings.current.single_operator ? '当前允许 admin 单人制单并审批；自批不属于双人审批，且会单独记录例外审计。' : '当前关闭单人运营；钱包制单与审批必须由不同账号完成。'" :closable="false" show-icon />
        <div class="operator-current"><dl><dt>当前状态</dt><dd><el-tag :type="settings.current.single_operator ? 'warning' : 'info'">{{ settings.current.single_operator ? '开启（例外）' : '关闭（双人控制）' }}</el-tag></dd><dt>当前版本</dt><dd class="mono">{{ settings.current.version }}</dd><dt>最近修改人</dt><dd class="mono">{{ settings.current.actor || '系统创世版本' }}</dd><dt>最近修改时间</dt><dd>{{ time(settings.current.created_at) }}</dd></dl><el-button type="primary" plain @click="openChange">{{ settings.current.single_operator ? '关闭单人运营' : '开启单人运营' }}</el-button></div>
        <p class="operator-note">建议生产环境关闭此例外。开启、关闭及理由都会追加为不可变版本；历史批准单据保留批准时的开关快照。</p>
        <el-table :data="settings.versions" row-key="operation_id" max-height="260" aria-label="单人运营设置历史"><el-table-column prop="version" label="版本" width="80" /><el-table-column label="状态" width="110"><template #default="{ row }">{{ row.single_operator ? '开启' : '关闭' }}</template></el-table-column><el-table-column prop="reason" label="原因" min-width="210" /><el-table-column label="修改人" min-width="190"><template #default="{ row }"><span class="mono">{{ row.actor || '系统' }}</span></template></el-table-column><el-table-column label="时间" min-width="175"><template #default="{ row }">{{ time(row.created_at) }}</template></el-table-column></el-table>
      </template>
      <div v-else class="operator-loading" role="status">正在读取单人运营设置…</div>
    </div>
  </section>

  <el-dialog v-model="dialog" :title="target ? '开启单人运营例外' : '关闭单人运营例外'" width="590px" :close-on-click-modal="false" :close-on-press-escape="!saving" @closed="operationID = ''">
    <el-alert :title="target ? '开启后，admin 可兼具钱包制单与审批权限；同一账号自批会固定写入 single_operator_exception。' : '关闭后不再允许 admin 使用钱包财务例外，不会改变已批准历史。'" type="warning" :closable="false" show-icon />
    <el-form label-position="top" style="margin-top:20px" @submit.prevent="save"><el-form-item label="变更原因（写入不可变历史）" required><el-input v-model="reason" type="textarea" :rows="3" maxlength="200" show-word-limit :disabled="saving" placeholder="说明本次变更的运营背景与核对依据" /></el-form-item><el-checkbox v-model="acknowledged" :disabled="saving">我已核对影响范围，理解单人运营例外不等于双人审批。</el-checkbox></el-form>
    <el-alert v-if="formError" :title="formError" type="error" :closable="false" style="margin-top:18px" />
    <template #footer><el-button :disabled="saving" @click="dialog = false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!acknowledged || !reason.trim()" @click="save">核对并进行二次确认</el-button></template>
  </el-dialog>
</template>

<style scoped>
.operator-settings{margin-bottom:24px}.operator-settings h2{margin:0}.operator-body{padding:22px}.operator-current{display:flex;align-items:center;justify-content:space-between;gap:22px;margin-top:20px}.operator-current dl{display:grid;grid-template-columns:max-content 1fr;gap:9px 18px;margin:0;font-size:13px}.operator-current dt{color:var(--text-secondary)}.operator-current dd{margin:0}.operator-note{color:var(--text-secondary);font-size:12px;line-height:1.8;margin:20px 0}.operator-loading{padding:20px;text-align:center;color:var(--text-secondary)}@media(max-width:720px){.operator-body{padding:16px}.operator-current{align-items:flex-start;flex-direction:column}.operator-current dl{grid-template-columns:1fr}.operator-current dd{margin-bottom:8px;overflow-wrap:anywhere}}
</style>
