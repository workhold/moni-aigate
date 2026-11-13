<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { errorText, request } from '../api'
import { createPricingEpoch } from '../pricing-state'
import { fetchModelAliases, parseModelAliases, stableAliasCreate, stableAliasRetire } from '../model-aliases'
import type { ModelAlias } from '../model-aliases'

const props = defineProps<{ model: { id: string; public_name: string } | null }>()
const visible = defineModel<boolean>({ default: false })
const aliases = ref<ModelAlias[]>([]), loading = ref(false), saving = ref(false), error = ref(''), draft = ref('')
const scope = createPricingEpoch()
const modelID = () => props.model?.id || ''
async function load() {
  if (!props.model) return
  const ticket = scope.ticket(); loading.value = true; error.value = ''
  try {
    const rows = await fetchModelAliases(modelID())
    if (scope.accepts(ticket)) aliases.value = rows
  } catch (cause) { if (scope.accepts(ticket)) { aliases.value = []; error.value = errorText(cause) } }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
async function create() {
  if (!props.model || !draft.value.trim() || saving.value) return
  const operation = stableAliasCreate(modelID(), draft.value.trim()), ticket = scope.ticket()
  saving.value = true; error.value = ''
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    const created = parseModelAliases([response.data])[0]
    if (!scope.accepts(ticket)) return
    aliases.value = [...aliases.value.filter(item => item.alias !== created.alias), created].sort((a, b) => a.alias.localeCompare(b.alias))
    draft.value = ''; ElMessage.success('模型别名已登记。')
  } catch (cause) { if (scope.accepts(ticket)) error.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) saving.value = false }
}
async function retire(item: ModelAlias) {
  if (item.state !== 'active' || saving.value) return
  try { await ElMessageBox.confirm(`退役后「${item.alias}」不能重新指向其他模型。确认退役？`, '退役模型别名', { type: 'warning', confirmButtonText: '确认退役', cancelButtonText: '取消' }) } catch { return }
  const operation = stableAliasRetire(item.alias, item.revision), ticket = scope.ticket()
  saving.value = true; error.value = ''
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    const retired = parseModelAliases([response.data])[0]
    if (!scope.accepts(ticket)) return
    aliases.value = aliases.value.map(row => row.alias === retired.alias ? retired : row)
    ElMessage.success('模型别名已退役。')
  } catch (cause) { if (scope.accepts(ticket)) error.value = errorText(cause) }
  finally { if (scope.accepts(ticket)) saving.value = false }
}
watch([visible, () => props.model?.id], () => { if (visible.value && props.model) { draft.value = ''; void load() } }, { flush: 'sync' })
watch(() => props.model?.id, () => { aliases.value = []; error.value = ''; draft.value = '' }, { flush: 'sync' })
onBeforeUnmount(() => scope.dispose())
</script>
<template>
  <el-dialog v-model="visible" :title="`${model?.public_name || ''} · 模型别名`" width="760px" destroy-on-close :close-on-click-modal="false">
    <p class="dialog-description">别名只映射到这个已发布逻辑模型，沿用原模型的授权、售价、路由和计量。退役后的名字不会重新指向其他模型。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" style="margin-bottom:16px" />
    <div class="alias-create"><el-input v-model="draft" maxlength="100" placeholder="例如：gpt-4o" aria-label="模型别名" @keyup.enter="create" /><el-button type="primary" :icon="Plus" :loading="saving" :disabled="!draft.trim()" @click="create">登记别名</el-button></div>
    <el-table v-loading="loading" :data="aliases" empty-text="暂无模型别名"><el-table-column prop="alias" label="客户可见名" min-width="220" /><el-table-column prop="state" label="状态" width="110"><template #default="{ row }"><el-tag :type="row.state === 'active' ? 'success' : 'info'">{{ row.state === 'active' ? '启用' : '已退役' }}</el-tag></template></el-table-column><el-table-column prop="revision" label="修订" width="85" /><el-table-column label="操作" width="105"><template #default="{ row }"><el-button v-if="row.state === 'active'" link type="danger" :disabled="saving" @click="retire(row)">退役</el-button><span v-else class="muted small-text">终态</span></template></el-table-column></el-table>
    <template #footer><el-button :icon="Refresh" :disabled="loading || saving" @click="load">刷新</el-button><el-button @click="visible = false">关闭</el-button></template>
  </el-dialog>
</template>
<style scoped>
.alias-create{display:flex;gap:12px;margin-bottom:18px}.alias-create .el-input{max-width:420px}@media(max-width:700px){.alias-create{display:grid}.alias-create .el-input{max-width:none}}
</style>
