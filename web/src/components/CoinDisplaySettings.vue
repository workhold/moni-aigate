<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { errorText, newOperationKey, request } from '../api'
import { coin, time } from '../format'
import { isAdmin } from '../session'
import { applyCoinDisplay, coinDisplayCommand, coinDisplayConfirmation, parseCoinDisplaySetting, parseCoinDisplaySettings, publicCoinDisplay, type CoinDisplayPublic, type CoinDisplaySettings } from '../coin-display'

const settings = ref<CoinDisplaySettings | null>(null)
const loading = ref(false), saving = ref(false), error = ref(''), formError = ref('')
const dialog = ref(false), acknowledged = ref(false), operationID = ref('')
const form = reactive({ display_name: '币', enabled: false, fiat_currency: 'CNY' as 'CNY' | 'USD', rate_num: '1', rate_den: '1', reason: '' })
let generation = 0
function rateText(row: CoinDisplayPublic) { return row.fiat_currency ? `1 COIN ≈ ${row.rate_num}/${row.rate_den} ${row.fiat_currency}` : '关闭' }
async function load() {
  if (!isAdmin.value) return
  const ticket = ++generation
  loading.value = true; error.value = ''
  try {
    const response = await request<unknown>('/platform/coin-display-settings')
    if (ticket === generation && isAdmin.value) {
      settings.value = parseCoinDisplaySettings(response.data)
      applyCoinDisplay(publicCoinDisplay(settings.value.current))
    }
  } catch (cause) { if (ticket === generation) error.value = errorText(cause) }
  finally { if (ticket === generation) loading.value = false }
}
function openChange() {
  if (!settings.value || saving.value) return
  const current = settings.value.current
  form.display_name = current.display_name
  form.enabled = current.fiat_currency !== null
  form.fiat_currency = current.fiat_currency || 'CNY'
  form.rate_num = current.rate_num || '1'; form.rate_den = current.rate_den || '1'; form.reason = ''
  acknowledged.value = false; formError.value = ''; operationID.value = newOperationKey(); dialog.value = true
}
async function save() {
  if (!settings.value || saving.value) return
  formError.value = ''
  if (!acknowledged.value) { formError.value = '请先确认已核对展示配置的影响范围。'; return }
  try {
    await ElMessageBox.confirm('显示名与约合只改变控制台展示，不改变币数量、会计币价、请求扣费或退款。确认追加不可变展示版本？', '二次确认展示配置', { type: 'warning', confirmButtonText: '确认追加并写审计', cancelButtonText: '取消', closeOnClickModal: false })
  } catch { return }
  saving.value = true
  try {
    const body = coinDisplayCommand({ operation_id: operationID.value, expected_revision: settings.value.current.version, display_name: form.display_name, fiat_currency: form.enabled ? form.fiat_currency : null, rate_num: form.enabled ? form.rate_num : null, rate_den: form.enabled ? form.rate_den : null, reason: form.reason.trim(), confirmation: coinDisplayConfirmation })
    const response = await request<unknown>('/platform/coin-display-settings', 'PUT', body)
    const updated = parseCoinDisplaySetting(response.data)
    settings.value = { current: updated, versions: [updated, ...settings.value.versions.filter(item => item.operation_id !== updated.operation_id)].slice(0, 50) }
    applyCoinDisplay(publicCoinDisplay(updated))
    dialog.value = false; operationID.value = ''
    ElMessage.success('展示配置已追加新版本并写入审计。')
    await load()
  } catch (cause) { formError.value = errorText(cause) }
  finally { saving.value = false }
}
watch(isAdmin, admin => { generation++; settings.value = null; dialog.value = false; if (admin) void load() })
onMounted(() => { if (isAdmin.value) void load() })
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <section v-if="isAdmin" class="panel display-settings" aria-label="平台币展示配置">
    <div class="panel-header"><div><h2 class="panel-title">平台币展示配置</h2><p class="panel-subtitle">显示名和约合仅用于控制台，不改变 COIN、会计币价、扣费或退款。</p></div><el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button></div>
    <div class="display-body">
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
      <template v-else-if="settings">
        <el-alert type="info" :closable="false" show-icon title="约合仅供参考；请求、预占、结算、退款和报表均不读取此展示比例。" />
        <div class="display-current"><dl><dt>显示名</dt><dd>{{ settings.current.display_name }}</dd><dt>当前版本</dt><dd class="mono">{{ settings.current.version }}</dd><dt>约合配置</dt><dd>{{ rateText(settings.current) }}</dd><dt>展示示例</dt><dd>{{ coin('1000000') }}</dd><dt>最近修改人</dt><dd class="mono">{{ settings.current.actor || '系统创世版本' }}</dd></dl><el-button type="primary" plain @click="openChange">追加展示版本</el-button></div>
        <el-table :data="settings.versions" row-key="operation_id" max-height="280" aria-label="展示配置历史"><el-table-column prop="version" label="版本" width="80" /><el-table-column prop="display_name" label="显示名" width="110" /><el-table-column label="约合" min-width="175"><template #default="{ row }">{{ rateText(row) }}</template></el-table-column><el-table-column label="操作 ID" min-width="180"><template #default="{ row }"><span class="mono" :title="row.operation_id">{{ row.operation_id }}</span></template></el-table-column><el-table-column prop="reason" label="原因" min-width="200" /><el-table-column label="修改人" min-width="170"><template #default="{ row }"><span class="mono">{{ row.actor || '系统' }}</span></template></el-table-column><el-table-column label="时间" min-width="175"><template #default="{ row }">{{ time(row.created_at) }}</template></el-table-column></el-table>
      </template>
      <div v-else class="display-loading" role="status">正在读取展示配置…</div>
    </div>
  </section>

  <el-dialog v-model="dialog" title="追加平台币展示版本" width="620px" :close-on-click-modal="false" :close-on-press-escape="!saving" @closed="operationID = ''">
    <el-alert title="所有字段只影响展示。约合必须带版本并标注仅供参考；关闭时三个换算字段均为 null。" type="info" :closable="false" show-icon />
    <el-form label-position="top" style="margin-top:18px" @submit.prevent="save">
      <el-form-item label="平台币显示名（1–32 字节）" required><el-input v-model="form.display_name" maxlength="32" :disabled="saving" placeholder="例如：点数" /></el-form-item>
      <el-form-item><el-checkbox v-model="form.enabled" :disabled="saving">启用约合展示</el-checkbox></el-form-item>
      <template v-if="form.enabled"><el-form-item label="法币币种" required><el-select v-model="form.fiat_currency" :disabled="saving"><el-option label="CNY" value="CNY" /><el-option label="USD" value="USD" /></el-select></el-form-item><div class="rate-fields"><el-form-item label="比例分子（正 int64）" required><el-input v-model="form.rate_num" :disabled="saving" placeholder="例如 7" /></el-form-item><el-form-item label="比例分母（正 int64）" required><el-input v-model="form.rate_den" :disabled="saving" placeholder="例如 10" /></el-form-item></div><p class="rate-note">1 COIN = 分子 / 分母 法币，例如 7/10 表示约合 0.7。</p></template>
      <el-form-item label="变更原因（写入不可变历史）" required><el-input v-model="form.reason" type="textarea" :rows="3" maxlength="200" show-word-limit :disabled="saving" /></el-form-item>
      <el-checkbox v-model="acknowledged" :disabled="saving">我已核对显示名与比例，理解约合不参与任何资金计算。</el-checkbox>
    </el-form>
    <el-alert v-if="formError" :title="formError" type="error" :closable="false" style="margin-top:18px" />
    <template #footer><el-button :disabled="saving" @click="dialog = false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!acknowledged || !form.reason.trim()" @click="save">核对并进行二次确认</el-button></template>
  </el-dialog>
</template>

<style scoped>
.display-settings{margin-bottom:24px}.display-settings h2{margin:0}.display-body{padding:22px}.display-current{display:flex;align-items:center;justify-content:space-between;gap:22px;margin:20px 0}.display-current dl{display:grid;grid-template-columns:max-content 1fr;gap:9px 18px;margin:0;font-size:13px}.display-current dt{color:var(--text-secondary)}.display-current dd{margin:0}.display-loading{padding:20px;text-align:center;color:var(--text-secondary)}.rate-fields{display:grid;grid-template-columns:1fr 1fr;gap:16px}.rate-note{font-size:12px;color:var(--text-secondary);margin-top:-12px}@media(max-width:720px){.display-body{padding:16px}.display-current{align-items:flex-start;flex-direction:column}.display-current dl{grid-template-columns:1fr}.display-current dd{margin-bottom:8px;overflow-wrap:anywhere}.rate-fields{grid-template-columns:1fr}}
</style>
