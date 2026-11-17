<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { errorText, request } from '../api'
import { isAdmin, session } from '../session'
import { createKeyAccessScope, keyAccessContext } from '../key-access-policy'
import { acceptRouteActivation, readRouteRevisions, routeActivation, routeActivationJournal } from '../route-revisions'
import type { RouteActivationReview, RouteRevision } from '../route-revisions'
import { time } from '../format'

const props = defineProps<{ modelValue: boolean; model: Record<string, unknown> | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [] }>()
const context = computed(() => keyAccessContext(session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role))
const scope = createKeyAccessScope()
const rows = ref<RouteRevision[]>([]), error = ref(''), loading = ref(false), saving = ref(false)
const review = ref<RouteActivationReview | null>(null)
const visible = computed({ get: () => props.modelValue, set: value => { if (!value) reset(); emit('update:modelValue', value) } })
const current = computed(() => rows.value.find(row => row.active))
const blocked = computed(() => !!review.value && review.value.mode !== 'saved')
function reset() { scope.reset(context.value); rows.value = []; error.value = ''; loading.value = false; saving.value = false; review.value = null }
async function read() {
  if (!props.modelValue || !props.model || !isAdmin.value || loading.value || saving.value) return
  const id = String(props.model.id), ticket = scope.ticket('read')
  loading.value = true; error.value = ''
  try {
    const latest = await readRouteRevisions(id, () => scope.accepts(ticket))
    if (!scope.accepts(ticket)) return
    rows.value = latest
    const pending = routeActivationJournal.get(context.value, id)
    if (pending?.mode === 'unknown' || pending?.mode === 'conflict') review.value = routeActivationJournal.observe(context.value, id, latest)
    else if (pending?.mode === 'saved') review.value = null
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
function resume() {
  if (!props.model) return
  try { routeActivationJournal.resume(context.value, String(props.model.id)); review.value = null; error.value = '' }
  catch (e) { error.value = errorText(e) }
}
async function activate(target: RouteRevision) {
  if (!props.model || !isAdmin.value || blocked.value || loading.value || saving.value || target.active || !target.current_profile || !current.value) return
  const id = String(props.model.id), actor = context.value
  let operation
  try { operation = routeActivation(id, current.value.revision, target.revision) } catch (e) { error.value = errorText(e); return }
  try {
    await ElMessageBox.confirm(`确认将「${props.model.public_name}」从版本 ${operation.expected} 切换到版本 ${operation.target}？只影响新请求；在途请求仍保留原路由、价格与结算快照。`, '确认路由切换 / 回退', { type: 'warning', confirmButtonText: '确认激活此版本', cancelButtonText: '取消' })
  } catch { return }
  if (!props.modelValue || String(props.model.id) !== id || context.value !== actor || blocked.value || saving.value) return
  const ticket = scope.ticket('write')
  try { review.value = routeActivationJournal.begin(actor, id, operation) } catch (e) { error.value = errorText(e); return }
  saving.value = true; error.value = ''
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    acceptRouteActivation(operation, response.data); routeActivationJournal.saved(actor, id)
    if (!scope.accepts(ticket)) return
    review.value = null; ElMessage.success(`当前路由已指向版本 ${operation.target}。`); emit('saved')
    saving.value = false
    await read()
  } catch (e) {
    routeActivationJournal.fail(actor, id, e)
    if (scope.accepts(ticket)) { review.value = routeActivationJournal.get(actor, id); error.value = errorText(e) }
  } finally { if (scope.accepts(ticket)) saving.value = false }
}
watch(() => [props.modelValue, props.model?.id], () => {
  reset()
  if (!props.modelValue || !props.model || !isAdmin.value) return
  const pending = routeActivationJournal.get(context.value, String(props.model.id))
  if (pending && pending.mode !== 'saved') review.value = pending
  void read()
}, { immediate: true, flush: 'sync' })
watch(context, () => { reset(); emit('update:modelValue', false) }, { flush: 'sync' })
onBeforeUnmount(reset)
</script>

<template>
  <el-dialog v-model="visible" :title="`${model?.public_name || ''} · 路由历史与回退`" width="900px" destroy-on-close :close-on-click-modal="false" class="route-history-dialog" data-testid="route-history-dialog">
    <p class="dialog-description">历史版本不可变。切换或回退只移动新请求的活动指针；旧请求继续使用原路由、价格、成本和凭据快照。</p>
    <el-alert title="版本号可以下降：回退不是发布新副本。活动标记不代表供应商资格、渠道健康或生产可用性。" type="info" :closable="false" show-icon />
    <el-alert v-if="review?.mode === 'submitting'" title="原激活仍在等待响应，不能重复提交。关闭后可回来只读核查。" type="warning" :closable="false" class="route-history-notice" />
    <el-alert v-else-if="review?.mode === 'unknown'" :title="`版本 ${review.operation.target} 的激活结果未知，已禁止重发。请只读刷新确认当前活动版本。`" type="warning" :closable="false" class="route-history-notice" />
    <el-alert v-else-if="review?.mode === 'conflict'" title="激活时发生版本冲突。请只读核查当前活动版本，再显式开始新操作。" type="warning" :closable="false" class="route-history-notice" />
    <div v-if="review?.observedActive !== undefined" class="route-review"><span>已读取当前活动版本 {{ review.observedActive }}<template v-if="review.observedActive === review.operation.target">；当前已指向原目标，但这不反推原请求的网络结果。</template></span><el-button :disabled="loading || saving" @click="resume">已核查，允许新操作</el-button></div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" class="route-history-notice" />
    <div v-loading="loading" class="route-history-list">
      <article v-for="row in rows" :key="row.id" class="route-revision" :class="{ active: row.active }" :data-route-revision="row.revision">
        <div class="route-revision-heading"><div><strong>版本 {{ row.revision }}</strong><el-tag v-if="row.active" type="success" size="small">当前活动</el-tag><el-tag size="small" :type="row.release_intent === 'commercial' ? 'warning' : 'info'">{{ row.release_intent === 'commercial' ? 'commercial' : 'test-only' }}</el-tag></div><time>{{ time(row.created_at) }}</time></div>
        <p class="field-hint">Profile {{ row.profile_version }} · {{ row.current_profile ? '匹配当前能力版本' : '与当前能力版本不匹配，不能激活' }}</p>
        <div v-for="binding in row.bindings" :key="`${binding.channel_id}:${binding.channel_version_id}`" class="route-revision-binding"><div><strong>{{ binding.channel_name }}</strong><el-tag size="small">{{ binding.integration_mode }}</el-tag><el-tag size="small" effect="plain">{{ binding.channel_kind }}</el-tag></div><code>{{ binding.upstream_model }}</code><span>优先级 {{ binding.priority }} · 权重 {{ binding.weight }} · 并发 {{ binding.max_inflight }}</span></div>
        <el-button v-if="isAdmin" type="primary" :plain="!row.active" :disabled="row.active || !row.current_profile || blocked || loading || saving" :loading="saving && review?.operation.target === row.revision" :aria-label="`激活路由版本 ${row.revision}`" @click="activate(row)">{{ row.active ? '当前版本' : row.revision < (current?.revision || 0) ? '回退到此版本' : '切换到此版本' }}</el-button>
      </article>
      <el-empty v-if="!loading && !error && !rows.length" description="暂无可读取的路由历史" />
    </div>
    <template #footer><el-button @click="visible = false">关闭</el-button><el-button :icon="Refresh" :loading="loading" :disabled="saving" @click="read">只读刷新</el-button></template>
  </el-dialog>
</template>

<style scoped>
.route-history-notice{margin-top:14px}.route-review{display:flex;align-items:center;justify-content:space-between;gap:14px;flex-wrap:wrap;margin:14px 0;padding:12px 14px;border-radius:8px;background:#fff8e8;color:#8a641f;font-size:12px}.route-history-list{margin-top:18px;max-height:55vh;overflow:auto}.route-revision{border:1px solid #e3eaf0;border-radius:10px;padding:16px;margin-bottom:12px;min-width:0}.route-revision.active{border-color:#9dcdb6;background:#f6fbf8}.route-revision-heading,.route-revision-heading>div,.route-revision-binding>div{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.route-revision-heading{justify-content:space-between}.route-revision-heading time{font-size:11px;color:#8493a5}.route-revision .field-hint{overflow-wrap:anywhere}.route-revision-binding{display:grid;grid-template-columns:minmax(0,1.2fr) minmax(0,1fr) auto;gap:12px;align-items:center;border-top:1px solid #edf1f5;padding:11px 0}.route-revision-binding code,.route-revision-binding strong{overflow-wrap:anywhere}.route-revision-binding span{font-size:11px;color:#8493a5}.route-revision>.el-button{margin-top:8px}@media(max-width:700px){.route-revision-binding{grid-template-columns:1fr;gap:7px}.route-history-list{max-height:52vh}.route-revision{padding:13px}}
</style>
