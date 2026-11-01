<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Plus, Refresh, User } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import { errorText, request } from '../api'
import { label, tagType } from '../format'
import { session } from '../session'

interface Member { id?: string; user_id: string; email: string; role: string; status: string; allow_all_models?: boolean; model_ids?: string[] }
const rows = ref<Member[]>([]); const models = ref<{ id: string; public_name: string }[]>([]); const loading = ref(false); const error = ref('')
const open = ref(false); const saving = ref(false); const formError = ref(''); const editing = ref<Member | null>(null)
const form = ref({ email: '', password: '', role: 'member', status: 'active', allow_all_models: false, model_ids: [] as string[] })
async function load() {
  loading.value = true; error.value = ''
  const results = await Promise.allSettled([request<Member[]>('/members'), request<typeof models.value>('/models')])
  if (results[0].status === 'fulfilled') rows.value = results[0].value.data || []; else error.value = errorText(results[0].reason)
  if (results[1].status === 'fulfilled') models.value = results[1].value.data || []; else error.value ||= errorText(results[1].reason)
  loading.value = false
}
function show(row?: Member) {
  editing.value = row || null; formError.value = ''
  form.value = row ? { email: row.email, password: '', role: row.role, status: row.status, allow_all_models: !!row.allow_all_models, model_ids: [...(row.model_ids || [])] } : { email: '', password: '', role: 'member', status: 'active', allow_all_models: false, model_ids: [] }
  open.value = true
}
async function save() {
  saving.value = true; formError.value = ''
  try {
    if (!editing.value && (!form.value.email.trim() || form.value.password.length < 12)) throw new Error('请填写邮箱，并设置至少 12 位的初始密码。')
    const body = { role: form.value.role, status: form.value.status, allow_all_models: form.value.allow_all_models, model_ids: form.value.model_ids }
    if (editing.value) await request(`/members/${encodeURIComponent(editing.value.user_id || editing.value.id || '')}`, 'PATCH', body)
    else await request('/members', 'POST', { email: form.value.email.trim(), password: form.value.password, role: body.role, allow_all_models: body.allow_all_models, model_ids: body.model_ids })
    open.value = false; form.value.password = ''; ElMessage.success('成员信息已更新。'); await load()
  } catch (e) { formError.value = errorText(e) } finally { saving.value = false }
}
onMounted(load)
</script>
<template>
  <PageHeader eyebrow="TEAM ACCESS" title="成员与权限" description="为工作空间添加成员，并在自身权限范围内分配模型访问。"><el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button><el-button type="primary" :icon="Plus" @click="show()">添加成员</el-button></PageHeader>
  <ErrorNotice :message="error" @retry="load" />
  <section class="panel"><div class="panel-header"><div class="panel-title"><el-icon><User /></el-icon>{{ session.me?.tenant?.name }} 的成员</div><span class="muted small-text">{{ rows.length }} 位成员</span></div><el-table v-loading="loading" :data="rows" empty-text="暂无成员"><el-table-column label="邮箱" min-width="240"><template #default="{ row }"><strong>{{ row.email }}</strong><el-tag v-if="row.user_id === session.me?.user.id" type="info" style="margin-left:9px">当前账号</el-tag></template></el-table-column><el-table-column label="租户角色" min-width="130"><template #default="{ row }">{{ label(row.role) }}</template></el-table-column><el-table-column label="模型权限" min-width="160"><template #default="{ row }">{{ row.allow_all_models ? '全部租户已授权模型' : `${row.model_ids?.length || 0} 个指定模型` }}</template></el-table-column><el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="tagType(row.status)">{{ label(row.status) }}</el-tag></template></el-table-column><el-table-column label="操作" width="100"><template #default="{ row }"><el-button v-if="row.role !== 'owner' && row.user_id !== session.me?.user.id" type="primary" link @click="show(row)">管理权限</el-button><span v-else class="muted small-text">平台管理</span></template></el-table-column></el-table></section><p class="section-note">成员授权不能扩大租户或当前所有者的模型权限。新增所有者由平台管理员处理；当前账号不能自行降权或停用。</p>
  <el-dialog v-model="open" :title="editing ? '管理成员权限' : '添加成员'" width="530px" :close-on-click-modal="false" destroy-on-close @close="form.password = ''"><el-alert v-if="formError" :title="formError" type="error" :closable="false" style="margin-bottom:20px" /><el-form label-position="top" @submit.prevent="save"><el-form-item label="邮箱"><el-input v-model="form.email" :disabled="!!editing" type="email" autocomplete="off" /></el-form-item><el-form-item v-if="!editing" label="初始密码"><el-input v-model="form.password" type="password" show-password autocomplete="new-password" /><div class="field-hint">至少 12 位，通过安全渠道单独交付给成员。</div></el-form-item><div class="form-grid"><el-form-item label="成员角色"><el-select v-model="form.role" style="width:100%"><el-option label="成员" value="member" /><el-option label="账单查看者" value="billing_viewer" /></el-select></el-form-item><el-form-item v-if="editing" label="状态"><el-select v-model="form.status" style="width:100%"><el-option label="启用" value="active" /><el-option label="停用" value="suspended" /></el-select></el-form-item></div><el-form-item label="模型授权"><el-select v-model="form.model_ids" multiple filterable :disabled="form.allow_all_models" style="width:100%" placeholder="指定模型，空集合表示无调用权限"><el-option v-for="model in models" :key="model.id" :label="model.public_name" :value="model.id" /></el-select><el-checkbox v-model="form.allow_all_models" style="margin-top:9px">允许全部租户已授权模型</el-checkbox><div class="field-hint">只可委派自身有效授权，最终权限仍取各层限制的交集。</div></el-form-item></el-form><template #footer><el-button @click="open = false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存成员</el-button></template></el-dialog>
</template>
