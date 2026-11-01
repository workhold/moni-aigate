<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { request, errorText } from '../api'
import { session, isAdmin } from '../session'
import { createKeyAccessScope, keyAccessContext } from '../key-access-policy'
import { parsePublication, publicationPath, publicationLabel } from '../public-catalog'
import { acceptPublication, failPublication, newPublicationReview, observePublication, publicationJournal, submitPublication } from '../public-catalog-state'
import type { PublicationReview } from '../public-catalog-state'

const props = defineProps<{ modelValue: boolean; model: { id: string; public_name: string } | null }>()
const emit = defineEmits<{ 'update:modelValue': [boolean]; saved: [] }>()
const open = computed({ get: () => props.modelValue, set: value => emit('update:modelValue', value) })
const context = computed(() => keyAccessContext(session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role))
const scope = createKeyAccessScope(), review = shallowRef<PublicationReview>(), visible = ref(false), busy = ref(false), error = ref(''), checked = ref(false)
const readOnly = computed(() => review.value?.mode !== 'editing' || busy.value)
const current = computed(() => review.value?.observed || review.value?.view)
async function load() {
  if (!open.value || !props.model || !isAdmin.value) return
  const id = props.model.id, ticket = scope.ticket('editor'); busy.value = true; error.value = ''
  try {
    const result = await request<unknown>(publicationPath(id))
    if (!scope.accepts(ticket)) return
    const view = parsePublication(result.data, id), pending = publicationJournal.read(context.value, id)
    review.value = pending ? observePublication(pending, view) : newPublicationReview(view)
    visible.value = current.value!.public_visible; checked.value = !!pending
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) busy.value = false }
}
async function verify() {
  const previous = review.value
  if (!previous || !['unknown', 'conflict'].includes(previous.mode) || busy.value) return
  const id = previous.view.id, ticket = scope.ticket('editor'); busy.value = true; error.value = ''
  try {
    const result = await request<unknown>(publicationPath(id))
    if (!scope.accepts(ticket)) return
    review.value = observePublication(previous, result.data); visible.value = review.value.observed!.public_visible; checked.value = true
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) busy.value = false }
}
function restart() {
  if (!review.value || busy.value || !checked.value) return
  try { review.value = publicationJournal.restart(context.value, review.value); visible.value = review.value.view.public_visible; checked.value = false; error.value = '' }
  catch (e) { error.value = errorText(e) }
}
async function save() {
  if (!review.value || readOnly.value || !isAdmin.value) return
  let pending: PublicationReview, journalTicket: number
  const actorContext = context.value
  try { pending = submitPublication(review.value, visible.value); journalTicket = publicationJournal.begin(actorContext, pending) }
  catch (e) { error.value = errorText(e); return }
  review.value = pending; error.value = ''; checked.value = false; busy.value = true
  const operation = pending.submitted!, ticket = scope.ticket('editor')
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    const accepted = acceptPublication(pending, response.data)
    publicationJournal.finish(actorContext, pending.view.id, journalTicket)
    if (!scope.accepts(ticket)) return
    review.value = accepted; visible.value = accepted.view.public_visible
    ElMessage.success(accepted.view.public_visible ? accepted.view.enabled ? '公开标记已保存，模型可在公开目录展示。' : '公开标记已保存；当前停用隐藏，重新启用后恢复公开。' : '已设为私有。')
    emit('saved')
  } catch (e) {
    const failed = failPublication(pending, e); publicationJournal.finish(actorContext, pending.view.id, journalTicket, failed)
    if (!scope.accepts(ticket)) return
    review.value = failed; error.value = errorText(e)
  } finally { if (scope.accepts(ticket)) busy.value = false }
}
watch([() => props.modelValue, () => props.model?.id, context], () => {
  scope.reset(context.value); review.value = undefined; visible.value = false; busy.value = false; error.value = ''; checked.value = false
  if (open.value && props.model && isAdmin.value) void load()
}, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => scope.reset())
</script>

<template>
  <el-dialog v-model="open" title="模型公开设置" width="min(560px, calc(100vw - 32px))" destroy-on-close :close-on-click-modal="false">
    <p class="dialog-description">公开目录仅展示逻辑模型、能力限制和当前客户售价。公开标记与模型授权、路由、启停和客户资金相互独立。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" class="publication-alert" />
    <div v-if="current" class="publication-summary"><strong>{{ current.public_name }}</strong><span>{{ publicationLabel(current) }}</span><span>公开配置版本 {{ current.public_revision }}</span></div>
    <el-alert v-if="review?.mode === 'unknown'" title="原保存结果尚未确认。这里只读核查当前状态，当前值相同也不能证明原请求已成功；明确开始新编辑后才可再提交。" type="warning" :closable="false" class="publication-alert" />
    <el-alert v-else-if="review?.mode === 'conflict'" title="公开配置版本已变化。请只读核查，再明确开始新编辑；不会自动重试原保存。" type="warning" :closable="false" class="publication-alert" />
    <el-form v-if="review" label-position="top" @submit.prevent="save"><el-form-item label="允许在匿名价格目录展示"><el-switch v-model="visible" :disabled="readOnly" active-text="公开" inactive-text="私有" /></el-form-item></el-form>
    <el-alert v-if="current && !current.enabled && visible" title="模型当前已停用，目录仍隐藏。保留公开标记后，重新启用将恢复公开展示。" type="warning" :closable="false" class="publication-alert" />
    <p class="publication-note">隐藏不撤回访客已经看到的资料；后续目录读取会重新检查公开和启用状态。公开模型仍需单独授予租户、成员与 API Key 调用权限。</p>
    <template #footer><div class="publication-actions"><el-button @click="open = false">关闭</el-button><template v-if="review && ['unknown', 'conflict'].includes(review.mode)"><el-button :loading="busy" @click="verify">只读核查当前状态</el-button><el-button type="primary" :disabled="busy || !checked" @click="restart">明确开始新编辑</el-button></template><el-button v-else-if="review" type="primary" :loading="busy" :disabled="readOnly" @click="save">保存公开设置</el-button><el-button v-else :loading="busy" @click="load">重新读取</el-button></div></template>
  </el-dialog>
</template>

<style scoped>
.publication-summary{display:flex;flex-wrap:wrap;gap:9px 15px;align-items:center;margin-bottom:20px;font-size:11px;color:#8296a5}.publication-summary strong{font-size:15px;color:#36586c;overflow-wrap:anywhere}.publication-alert{margin-bottom:17px}.publication-note{font-size:11px;line-height:1.9;color:#8a9ba8}.publication-actions{display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px}.publication-actions .el-button{margin:0}@media(max-width:520px){.publication-actions .el-button{font-size:11px}.publication-summary{flex-direction:column;align-items:flex-start}}
</style>
