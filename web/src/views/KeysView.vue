<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, Key, CopyDocument } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import { ApiError, errorText, request } from '../api'
import { canManageKeys, session } from '../session'
import { cnyToMicro, money, time } from '../format'
import { acceptKeyAccessPolicySave, createKeyAccessScope, failKeyAccessPolicyReview, keyAccessContext, keyAccessPolicyPath, keyClientCIDRsFromText, keyCreatedSecret, newKeyAccessPolicyReview, observeKeyAccessPolicy, parseKeyAccessPolicy, retainNewerKeyPolicies, resumeKeyAccessPolicyReview, submitKeyAccessPolicyReview, unknownKeyWriteResult } from '../key-access-policy'
import type { KeyAccessPolicyReview, KeyRow } from '../key-access-policy'
import { acceptKeyLimitsSave, failKeyLimitsReview, keyLimitsDraft, keyRuntimeLimitsPath, newKeyLimitsReview, observeKeyLimits, parseKeyLimitsView, retainNewerKeyLimits, resumeKeyLimitsReview, submitKeyLimitsReview } from '../key-runtime-limits'
import type { KeyLimitsDraft, KeyLimitsReview, KeyLimitsRow } from '../key-runtime-limits'
import { acceptKeyConfigurationSave, applyKeyConfigurationToRows, failKeyConfigurationReview, keyConfigurationDraft, keyConfigurationOptions, keyConfigurationPath, keyCreateWithConfigurationRequest, newKeyConfigurationReview, observeKeyConfiguration, parseKeyConfigurationRow, parseKeyConfigurationRows, parseKeyConfigurationView, retainNewerKeySettings, resumeKeyConfigurationReview, submitKeyConfigurationReview } from '../key-configuration'
import type { KeyConfigurationReview, KeyConfigurationRow } from '../key-configuration'

const scope = createKeyAccessScope()
const context = computed(() => keyAccessContext(session.me?.user.id, session.me?.tenant?.id, session.me?.tenant?.role))
const rows = ref<KeyConfigurationRow[]>([]), models = ref<{ id: string; public_name: string }[]>([])
const loading = ref(false), error = ref(''), modelError = ref(''), search = ref(''), nextCursor = ref('')
const createOpen = ref(false), saving = ref(false), formError = ref(''), plaintext = ref(''), secretOpen = ref(false)
const createUncertain = ref(false), creationReviewed = ref(false), revoking = ref('')
const emptyForm = () => ({ name: '', scopes: [] as string[], all: false, allowed_regions: ['global'], limit: '', expires: '', clientCIDRs: '', ...keyLimitsDraft() })
const form = ref(emptyForm())
const policyOpen = ref(false), policyTarget = ref<{ id: string; name: string } | null>(null)
const policyReview = ref<KeyAccessPolicyReview | null>(null), policyText = ref(''), policyError = ref(''), policyLoading = ref(false)
const limitsOpen = ref(false), limitsTarget = ref<{ id: string; name: string } | null>(null)
const limitsReview = ref<KeyLimitsReview | null>(null), limitsForm = ref<KeyLimitsDraft>(keyLimitsDraft()), limitsError = ref(''), limitsLoading = ref(false)
const emptyConfigurationForm = () => keyConfigurationDraft({ name: '', scopes: [], allowed_regions: [], lifetime_limit: null, expires_at: null })
const configurationOpen = ref(false), configurationTarget = ref<{ id: string; name: string } | null>(null)
const configurationReview = ref<KeyConfigurationReview | null>(null), configurationForm = ref(emptyConfigurationForm()), configurationError = ref(''), configurationLoading = ref(false)
// This page retains unresolved public policy drafts across dialog closes only.
// It never persists a secret, request body or outcome in browser storage.
const unresolvedPolicies = new Map<string, KeyAccessPolicyReview>()
const unresolvedLimits = new Map<string, KeyLimitsReview>()
const unresolvedConfigurations = new Map<string, KeyConfigurationReview>()
const policySaving = computed(() => policyReview.value?.mode === 'submitting')
const policyBlocked = computed(() => policyReview.value?.mode === 'unknown' || policyReview.value?.mode === 'conflict')
const limitsSaving = computed(() => limitsReview.value?.mode === 'submitting')
const limitsBlocked = computed(() => limitsReview.value?.mode === 'unknown' || limitsReview.value?.mode === 'conflict')
const configurationSaving = computed(() => configurationReview.value?.mode === 'submitting')
const configurationBlocked = computed(() => configurationReview.value?.mode === 'unknown' || configurationReview.value?.mode === 'conflict')
const createVisible = computed({ get: () => createOpen.value, set: value => { if (value) createOpen.value = true; else if (!saving.value) closeCreate() } })
const policyVisible = computed({ get: () => policyOpen.value, set: value => { if (value) policyOpen.value = true; else if (!policySaving.value) closePolicy() } })
const limitsVisible = computed({ get: () => limitsOpen.value, set: value => { if (value) limitsOpen.value = true; else if (!limitsSaving.value) closeLimits() } })
const configurationVisible = computed({ get: () => configurationOpen.value, set: value => { if (value) configurationOpen.value = true; else if (!configurationSaving.value) closeConfiguration() } })
const regionChoices = [{ value: 'global', label: 'global（精确地域标签）' }, { value: '*', label: '*（全部地域，仍受外层授权限制）' }]
const createRegionOptions = computed(() => keyConfigurationOptions(form.value.allowed_regions, regionChoices))
const configurationRegionOptions = computed(() => keyConfigurationOptions(configurationForm.value.allowed_regions, regionChoices))
const configurationScopeOptions = computed(() => keyConfigurationOptions(configurationForm.value.scopes, [{ value: '*', label: '*（全部已授权模型，包含未来授权）' }, ...models.value.map(model => ({ value: model.id, label: model.public_name }))]))
const configurationUsage = computed(() => configurationReview.value ? (BigInt(configurationReview.value.view.spent) + BigInt(configurationReview.value.view.held)).toString() : null)
const filtered = computed(() => rows.value.filter(row => `${row.name} ${row.prefix}`.toLowerCase().includes(search.value.toLowerCase())))
function state(row: KeyRow) { return row.revoked_at ? 'revoked' : row.expires_at && new Date(row.expires_at) < new Date() ? 'expired' : 'active' }
function clearSecret() { plaintext.value = ''; secretOpen.value = false }
function closeCreate() { scope.invalidate('create'); createOpen.value = false; saving.value = false; form.value = emptyForm(); formError.value = '' }
function closePolicy() { scope.invalidate('policy'); policyOpen.value = false; policyTarget.value = null; policyReview.value = null; policyText.value = ''; policyError.value = ''; policyLoading.value = false }
function closeLimits() { scope.invalidate('runtime-limits'); limitsOpen.value = false; limitsTarget.value = null; limitsReview.value = null; limitsForm.value = keyLimitsDraft(); limitsError.value = ''; limitsLoading.value = false }
function closeConfiguration() { scope.invalidate('configuration'); configurationOpen.value = false; configurationTarget.value = null; configurationReview.value = null; configurationForm.value = emptyConfigurationForm(); configurationError.value = ''; configurationLoading.value = false }
function reset() {
  scope.reset(context.value); rows.value = []; models.value = []; nextCursor.value = ''; search.value = ''; error.value = ''; modelError.value = ''; loading.value = false
  createUncertain.value = false; creationReviewed.value = false; revoking.value = ''; unresolvedPolicies.clear(); unresolvedLimits.clear(); unresolvedConfigurations.clear(); closeCreate(); closePolicy(); closeLimits(); closeConfiguration(); clearSecret()
}
async function load(cursor = '') {
  if (!session.me?.tenant) return
  const ticket = scope.ticket('list'); loading.value = true; error.value = ''
  try {
    const response = await request<unknown>(`/keys${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`)
    if (!scope.accepts(ticket)) return false
    const loaded = retainNewerKeySettings(parseKeyConfigurationRows(response.data), rows.value)
    if (response.next_cursor !== undefined && (typeof response.next_cursor !== 'string' || response.next_cursor.length > 512)) throw new Error('密钥列表分页响应无效。')
    const combined = cursor ? [...rows.value, ...loaded] : loaded
    if (new Set(combined.map(row => row.id)).size !== combined.length) throw new Error('密钥列表发生变化，请刷新后重新分页。')
    rows.value = combined; nextCursor.value = response.next_cursor || ''
    if (!cursor && createUncertain.value) creationReviewed.value = true
    return true
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e); return false }
  finally { if (scope.accepts(ticket)) loading.value = false }
}
async function loadModels() {
  const ticket = scope.ticket('models')
  try {
    const response = await request<unknown>('/models')
    if (!scope.accepts(ticket)) return
    if (!Array.isArray(response.data) || response.data.length > 1000) throw new Error('授权模型响应无效。')
    models.value = response.data.map(value => {
      if (!value || typeof value !== 'object' || typeof value.id !== 'string' || !value.id || typeof value.public_name !== 'string') throw new Error('授权模型响应无效。')
      return { id: value.id, public_name: value.public_name }
    })
    modelError.value = ''
  } catch (e) { if (scope.accepts(ticket)) modelError.value = `授权模型加载失败：${errorText(e)}` }
}
function showCreate() {
  if (!canManageKeys.value || saving.value || createUncertain.value) return
  if (policySaving.value || limitsSaving.value || configurationSaving.value) return
  clearSecret(); closeCreate(); closePolicy(); closeLimits(); closeConfiguration(); form.value = emptyForm(); createOpen.value = true
}
function reviewCreation() {
  if (!canManageKeys.value || !createUncertain.value || !creationReviewed.value || saving.value) return
  createUncertain.value = false; creationReviewed.value = false; closeCreate(); showCreate()
}
async function create() {
  if (!canManageKeys.value || !createOpen.value || saving.value || createUncertain.value) return
  formError.value = ''
  let operation: ReturnType<typeof keyCreateWithConfigurationRequest>
  try {
    if (!form.value.all && !form.value.scopes.length) throw new Error('请选择至少一个授权模型，或明确允许全部已授权模型。')
    const expires = form.value.expires ? new Date(form.value.expires).toISOString() : null
    if (expires && Date.parse(expires) <= Date.now()) throw new Error('过期时间须晚于当前时间。')
    operation = keyCreateWithConfigurationRequest({ name: form.value.name, scopes: form.value.all ? ['*'] : form.value.scopes, allowed_regions: form.value.allowed_regions, lifetime_limit: form.value.limit ? cnyToMicro(form.value.limit) : null, expires_at: expires, allowed_client_cidrs: keyClientCIDRsFromText(form.value.clientCIDRs), rpm_limit: form.value.rpm_limit, burst_limit: form.value.burst_limit, concurrency_limit: form.value.concurrency_limit })
  } catch (e) { formError.value = errorText(e); return }
  const ticket = scope.ticket('create'); saving.value = true
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    if (!scope.accepts(ticket)) return
    parseKeyConfigurationRow(response.data)
    const secret = keyCreatedSecret(response.data)
    saving.value = false; closeCreate(); plaintext.value = secret; secretOpen.value = true; void load()
  } catch (e) {
    if (!scope.accepts(ticket)) return
    if (unknownKeyWriteResult(e)) {
      createUncertain.value = true; creationReviewed.value = false
      formError.value = '本次创建结果待核查，不能重复提交。请刷新列表检查新密钥；如果明文未收到，请撤销对应密钥后再明确新建。'
    } else formError.value = errorText(e)
  } finally { if (scope.accepts(ticket)) saving.value = false }
}
async function revoke(row: KeyRow) {
  if (!canManageKeys.value || row.revoked_at || revoking.value) return
  const keyID = row.id, name = row.name, ticket = scope.ticket('revoke'); revoking.value = keyID
  try {
    try { await ElMessageBox.confirm(`撤销「${name}」后，新调用将被拒绝。已接受请求按收费规则结束。`, '撤销 API 密钥', { confirmButtonText: '确认撤销', cancelButtonText: '保留密钥', type: 'warning', closeOnClickModal: false }) }
    catch { return }
    if (!scope.accepts(ticket) || !canManageKeys.value || !rows.value.some(item => item.id === keyID && !item.revoked_at)) return
    await request(`/keys/${encodeURIComponent(keyID)}`, 'DELETE')
    if (!scope.accepts(ticket)) return
    ElMessage.success('密钥已撤销。'); await load()
  } catch (e) { if (scope.accepts(ticket)) ElMessage.error(unknownKeyWriteResult(e) ? '撤销结果待确认，请刷新列表核查。' : errorText(e)) }
  finally { if (scope.accepts(ticket)) revoking.value = '' }
}
async function openPolicy(row: KeyRow) {
  if (!canManageKeys.value || row.revoked_at || policySaving.value || limitsSaving.value || configurationSaving.value) return
  closeConfiguration(); closeLimits(); closePolicy(); policyTarget.value = { id: row.id, name: row.name }; policyOpen.value = true
  policyReview.value = unresolvedPolicies.get(row.id) ?? null
  if (policyReview.value?.submitted) policyText.value = policyReview.value.submitted.body.allowed_client_cidrs.join('\n')
  await loadPolicy()
}
async function loadPolicy() {
  if (!policyOpen.value || !policyTarget.value || policySaving.value) return
  const keyID = policyTarget.value.id, ticket = scope.ticket('policy'); policyLoading.value = true; policyError.value = ''
  if (policyReview.value && policyBlocked.value) {
    policyReview.value = { ...policyReview.value, observed: undefined }; unresolvedPolicies.set(keyID, policyReview.value)
  }
  try {
    const response = await request<unknown>(keyAccessPolicyPath(keyID))
    if (!scope.accepts(ticket)) return
    const view = parseKeyAccessPolicy(response.data, keyID)
    if (policyReview.value && policyBlocked.value) {
      policyReview.value = observeKeyAccessPolicy(policyReview.value, view); unresolvedPolicies.set(keyID, policyReview.value)
    } else { policyReview.value = newKeyAccessPolicyReview(view); policyText.value = view.allowed_client_cidrs.join('\n') }
    rows.value = retainNewerKeyPolicies(rows.value.map(row => row.id === keyID ? { ...row, ...view } : row), rows.value)
  } catch (e) { if (scope.accepts(ticket)) policyError.value = errorText(e) }
  finally { if (scope.accepts(ticket)) policyLoading.value = false }
}
function resumePolicy() {
  if (!canManageKeys.value || !policyOpen.value || !policyReview.value || policyLoading.value || !policyBlocked.value) return
  try {
    policyReview.value = resumeKeyAccessPolicyReview(policyReview.value)
    policyText.value = policyReview.value.view.allowed_client_cidrs.join('\n'); policyError.value = ''; unresolvedPolicies.delete(policyReview.value.view.id)
  } catch (e) { policyError.value = errorText(e) }
}
async function savePolicy() {
  if (!canManageKeys.value || !policyOpen.value || !policyTarget.value || !policyReview.value || policyLoading.value || policyReview.value.mode !== 'editing') return
  policyError.value = ''
  let submitted: KeyAccessPolicyReview
  try { submitted = submitKeyAccessPolicyReview(policyReview.value, policyText.value) }
  catch (e) { policyError.value = errorText(e); return }
  const operation = submitted.submitted!, ticket = scope.ticket('policy'); policyReview.value = submitted
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    if (!scope.accepts(ticket)) return
    policyReview.value = acceptKeyAccessPolicySave(submitted, response.data)
    const view = policyReview.value.view
    policyText.value = view.allowed_client_cidrs.join('\n'); unresolvedPolicies.delete(view.id)
    rows.value = retainNewerKeyPolicies(rows.value.map(row => row.id === view.id ? { ...row, ...view } : row), rows.value)
    ElMessage.success(view.access_policy_revision === operation.body.expected_revision ? '来源策略未变化。' : '来源策略已保存。密钥保持不变。')
  } catch (e) {
    if (!scope.accepts(ticket)) return
    policyReview.value = failKeyAccessPolicyReview(submitted, e)
    if (policyBlocked.value) {
      unresolvedPolicies.set(submitted.view.id, policyReview.value)
      policyError.value = policyReview.value.mode === 'conflict' ? '来源策略已被其他操作修改。请只读刷新并复核当前配置，再开始新编辑。' : '本次保存结果待确认。请只读刷新核查当前配置；当前配置不能证明本次写入是否成功，请勿重复提交。'
    } else policyError.value = errorText(e)
  }
}
async function openLimits(row: KeyLimitsRow) {
  if (!canManageKeys.value || row.revoked_at || limitsSaving.value || policySaving.value || configurationSaving.value) return
  closeConfiguration(); closePolicy(); closeLimits(); limitsTarget.value = { id: row.id, name: row.name }; limitsOpen.value = true
  limitsReview.value = unresolvedLimits.get(row.id) ?? null
  if (limitsReview.value?.submitted) limitsForm.value = keyLimitsDraft(limitsReview.value.submitted.body)
  await loadLimits()
}
async function loadLimits() {
  if (!limitsOpen.value || !limitsTarget.value || limitsSaving.value) return
  const keyID = limitsTarget.value.id, ticket = scope.ticket('runtime-limits'); limitsLoading.value = true; limitsError.value = ''
  if (limitsReview.value && limitsBlocked.value) {
    limitsReview.value = { ...limitsReview.value, observed: undefined }; unresolvedLimits.set(keyID, limitsReview.value)
  }
  try {
    const response = await request<unknown>(keyRuntimeLimitsPath(keyID))
    if (!scope.accepts(ticket)) return
    const view = parseKeyLimitsView(response.data, keyID)
    if (limitsReview.value && limitsBlocked.value) {
      limitsReview.value = observeKeyLimits(limitsReview.value, view); unresolvedLimits.set(keyID, limitsReview.value)
    } else { limitsReview.value = newKeyLimitsReview(view); limitsForm.value = keyLimitsDraft(view) }
    rows.value = retainNewerKeyLimits(rows.value.map(row => row.id === keyID ? { ...row, ...view } : row), rows.value)
  } catch (e) { if (scope.accepts(ticket)) limitsError.value = errorText(e) }
  finally { if (scope.accepts(ticket)) limitsLoading.value = false }
}
function resumeLimits() {
  if (!canManageKeys.value || !limitsOpen.value || !limitsReview.value || limitsLoading.value || !limitsBlocked.value) return
  try {
    limitsReview.value = resumeKeyLimitsReview(limitsReview.value)
    limitsForm.value = keyLimitsDraft(limitsReview.value.view); limitsError.value = ''; unresolvedLimits.delete(limitsReview.value.view.id)
  } catch (e) { limitsError.value = errorText(e) }
}
async function saveLimits() {
  if (!canManageKeys.value || !limitsOpen.value || !limitsTarget.value || !limitsReview.value || limitsLoading.value || limitsReview.value.mode !== 'editing') return
  limitsError.value = ''
  let submitted: KeyLimitsReview
  try { submitted = submitKeyLimitsReview(limitsReview.value, limitsForm.value) }
  catch (e) { limitsError.value = errorText(e); return }
  const operation = submitted.submitted!, ticket = scope.ticket('runtime-limits'); limitsReview.value = submitted
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    if (!scope.accepts(ticket)) return
    limitsReview.value = acceptKeyLimitsSave(submitted, response.data)
    const view = limitsReview.value.view
    limitsForm.value = keyLimitsDraft(view); unresolvedLimits.delete(view.id)
    rows.value = retainNewerKeyLimits(rows.value.map(row => row.id === view.id ? { ...row, ...view } : row), rows.value)
    ElMessage.success(view.limit_revision === operation.body.expected_revision ? '运行限额未变化。' : '运行限额已保存，将在下次新调用准入观察到时切换。')
  } catch (e) {
    if (!scope.accepts(ticket)) return
    limitsReview.value = failKeyLimitsReview(submitted, e)
    if (limitsBlocked.value) {
      unresolvedLimits.set(submitted.view.id, limitsReview.value)
      limitsError.value = limitsReview.value.mode === 'conflict' ? '运行限额已被其他操作修改。请只读刷新并复核当前配置，再开始新编辑。' : '本次保存结果待确认。请只读刷新核查当前配置；当前配置不能证明本次写入是否成功，请勿重复提交。'
    } else limitsError.value = errorText(e)
  }
}
async function openConfiguration(row: KeyConfigurationRow) {
  if (!canManageKeys.value || row.revoked_at || configurationSaving.value || limitsSaving.value || policySaving.value) return
  closePolicy(); closeLimits(); closeConfiguration(); configurationTarget.value = { id: row.id, name: row.name }; configurationOpen.value = true
  configurationReview.value = unresolvedConfigurations.get(row.id) ?? null
  if (configurationReview.value?.submitted) configurationForm.value = keyConfigurationDraft(configurationReview.value.submitted.body)
  await loadConfiguration()
}
async function loadConfiguration() {
  if (!configurationOpen.value || !configurationTarget.value || configurationSaving.value) return
  const keyID = configurationTarget.value.id, ticket = scope.ticket('configuration'); configurationLoading.value = true; configurationError.value = ''
  if (configurationReview.value && configurationBlocked.value) {
    configurationReview.value = { ...configurationReview.value, observed: undefined }; unresolvedConfigurations.set(keyID, configurationReview.value)
  }
  try {
    const response = await request<unknown>(keyConfigurationPath(keyID))
    if (!scope.accepts(ticket)) return
    const view = parseKeyConfigurationView(response.data, keyID)
    if (configurationReview.value && configurationBlocked.value) {
      configurationReview.value = observeKeyConfiguration(configurationReview.value, view); unresolvedConfigurations.set(keyID, configurationReview.value)
    } else { configurationReview.value = newKeyConfigurationReview(view); configurationForm.value = keyConfigurationDraft(view) }
    rows.value = applyKeyConfigurationToRows(rows.value, view)
  } catch (e) { if (scope.accepts(ticket)) configurationError.value = errorText(e) }
  finally { if (scope.accepts(ticket)) configurationLoading.value = false }
}
function resumeConfiguration() {
  if (!canManageKeys.value || !configurationOpen.value || !configurationReview.value || configurationLoading.value || !configurationBlocked.value) return
  try {
    configurationReview.value = resumeKeyConfigurationReview(configurationReview.value)
    configurationForm.value = keyConfigurationDraft(configurationReview.value.view); configurationError.value = ''; unresolvedConfigurations.delete(configurationReview.value.view.id)
  } catch (e) { configurationError.value = errorText(e) }
}
async function saveConfiguration() {
  if (!canManageKeys.value || !configurationOpen.value || !configurationTarget.value || !configurationReview.value || configurationLoading.value || configurationReview.value.mode !== 'editing') return
  configurationError.value = ''
  let submitted: KeyConfigurationReview
  try { submitted = submitKeyConfigurationReview(configurationReview.value, configurationForm.value) }
  catch (e) { configurationError.value = errorText(e); return }
  const operation = submitted.submitted!, ticket = scope.ticket('configuration'); configurationReview.value = submitted
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    if (!scope.accepts(ticket)) return
    configurationReview.value = acceptKeyConfigurationSave(submitted, response.data)
    const view = configurationReview.value.view
    configurationForm.value = keyConfigurationDraft(view); unresolvedConfigurations.delete(view.id)
    rows.value = applyKeyConfigurationToRows(rows.value, view)
    if (configurationTarget.value) configurationTarget.value.name = view.name
    ElMessage.success(view.configuration_revision === operation.body.expected_revision ? '基本配置未变化。' : '基本配置已保存。密钥保持不变。')
  } catch (e) {
    if (!scope.accepts(ticket)) return
    configurationReview.value = failKeyConfigurationReview(submitted, e)
    if (configurationBlocked.value) {
      unresolvedConfigurations.set(submitted.view.id, configurationReview.value)
      configurationError.value = configurationReview.value.mode === 'unknown' ? '本次保存结果待确认。请只读刷新核查当前配置；当前配置不能证明本次写入是否成功，请勿重复提交。' : e instanceof ApiError && e.code === 'key_budget_below_usage' ? '累计消费上限低于当前已消费与冻结金额。请只读刷新并复核最新观察，再开始新编辑。' : '基本配置版本已变化。请只读刷新并复核当前配置，再开始新编辑。'
    } else configurationError.value = errorText(e)
  }
}
async function copy() {
  if (!plaintext.value || !secretOpen.value) return
  const secret = plaintext.value, ticket = scope.ticket('clipboard')
  try { await navigator.clipboard.writeText(secret); if (scope.accepts(ticket) && secretOpen.value && plaintext.value === secret) ElMessage.success('密钥已复制。') }
  catch { if (scope.accepts(ticket) && secretOpen.value) ElMessage.warning('无法访问剪贴板，请手动选择复制。') }
}
watch(context, () => { reset(); if (session.me?.tenant) { void load(); void loadModels() } }, { immediate: true, flush: 'sync' })
onBeforeUnmount(reset)
</script>

<template>
  <PageHeader eyebrow="ACCESS CONTROL" title="API 密钥" description="为应用创建独立密钥，控制来源地址、运行限额、模型范围与累计消费额度。">
    <el-button :icon="Refresh" :loading="loading" @click="load()">刷新</el-button><el-button v-if="canManageKeys" type="primary" :icon="Plus" :disabled="createUncertain || saving" @click="showCreate">创建密钥</el-button>
  </PageHeader>
  <ErrorNotice :message="error" @retry="load()" />
  <el-alert v-if="createUncertain" title="上次创建结果待核查" type="warning" :closable="false" show-icon class="key-alert">
    <p>请刷新列表检查是否已有新密钥；如未收到明文，先撤销对应密钥。刷新只能核查当前列表，不能证明上次创建是否成功。</p>
    <el-button :loading="loading" @click="load()">刷新列表核查</el-button><el-button v-if="canManageKeys" :disabled="!creationReviewed || loading" @click="reviewCreation">已核查，重新填写新建</el-button>
  </el-alert>
  <section class="panel">
    <div class="filters"><el-input v-model="search" placeholder="搜索已加载密钥的名称或前缀" clearable /><span class="muted small-text">已加载 {{ rows.length }} 个密钥 · 同一租户共享钱包</span></div>
    <el-table v-loading="loading" :data="filtered" empty-text="暂无 API 密钥。创建一个密钥以开始调用。">
      <el-table-column label="密钥名称" min-width="160"><template #default="{ row }"><strong>{{ row.name }}</strong><div class="mono muted" style="margin-top:6px">{{ row.prefix }}••••••••</div><div class="muted small-text">配置版本 {{ row.configuration_revision }}</div></template></el-table-column>
      <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="state(row) === 'active' ? 'success' : 'info'">{{ state(row) === 'active' ? '启用' : state(row) === 'expired' ? '已过期' : '已撤销' }}</el-tag></template></el-table-column>
      <el-table-column label="来源地址" min-width="160"><template #default="{ row }"><span v-if="!row.allowed_client_cidrs.length">不限来源</span><el-popover v-else placement="bottom" :width="360" trigger="click"><template #reference><el-button link type="primary">{{ row.allowed_client_cidrs.length }} 个来源 · 查看</el-button></template><strong>允许的客户端 IP / CIDR</strong><div v-for="cidr in row.allowed_client_cidrs" :key="cidr" class="mono cidr-line">{{ cidr }}</div></el-popover><div class="muted small-text">策略版本 {{ row.access_policy_revision }}</div></template></el-table-column>
      <el-table-column label="运行限额（配置）" min-width="190"><template #default="{ row }"><span>{{ row.rpm_limit }} RPM · 突发 {{ row.burst_limit }}</span><div class="muted small-text">并发许可 {{ row.concurrency_limit }} · 版本 {{ row.limit_revision }}</div></template></el-table-column>
      <el-table-column label="累计消费 / 上限" min-width="180"><template #default="{ row }"><span>{{ money(row.spent) }}</span><div class="muted small-text">上限 {{ row.lifetime_limit === null ? '未设置' : money(row.lifetime_limit) }} · 冻结 {{ money(row.held) }}</div></template></el-table-column>
      <el-table-column label="模型范围" min-width="150"><template #default="{ row }"><el-popover placement="bottom" :width="400" trigger="click"><template #reference><el-button link type="primary">{{ row.scopes.includes('*') ? '全部已授权模型' : `${row.scopes.length} 个指定模型` }}</el-button></template><strong>Key 模型范围（含历史值）</strong><div v-if="!row.scopes.length" class="cidr-line">历史空模型范围</div><div v-for="(modelScope, index) in row.scopes" :key="index" class="mono cidr-line">{{ JSON.stringify(modelScope) }}</div><p class="field-hint">始终与租户及所属成员授权取交集。</p></el-popover></template></el-table-column>
      <el-table-column label="地域范围" min-width="140"><template #default="{ row }"><el-popover placement="bottom" :width="360" trigger="click"><template #reference><el-button link type="primary">{{ row.allowed_regions.length ? `${row.allowed_regions.length} 个地域标签` : '空地域范围' }}</el-button></template><strong>Key 地域限制</strong><div v-if="!row.allowed_regions.length" class="cidr-line">历史空列表，当前没有可匹配地域</div><div v-for="(region, index) in row.allowed_regions" :key="index" class="mono cidr-line">{{ JSON.stringify(region) }}</div><p class="field-hint">global 是精确标签；* 仍受租户和成员授权约束。</p></el-popover></template></el-table-column>
      <el-table-column label="过期时间" min-width="165"><template #default="{ row }"><span :title="row.expires_at">{{ row.expires_at ? time(row.expires_at) : '长期有效' }}</span></template></el-table-column>
      <el-table-column label="周期预算" width="115"><template #default="{ row }"><router-link :to="{ path: '/periodic-budget', query: { key: String(row.id) } }">查看预算</router-link></template></el-table-column>
      <el-table-column v-if="canManageKeys" label="操作" width="330" fixed="right"><template #default="{ row }"><el-button type="primary" link :disabled="!!row.revoked_at || configurationSaving || limitsSaving || policySaving" @click="openConfiguration(row)">基本配置</el-button><el-button type="primary" link :disabled="!!row.revoked_at || policySaving" @click="openPolicy(row)">来源策略</el-button><el-button type="primary" link :disabled="!!row.revoked_at || limitsSaving || policySaving || configurationSaving" @click="openLimits(row)">运行限额</el-button><el-button type="danger" link :disabled="!!row.revoked_at || !!revoking" :loading="revoking === row.id" @click="revoke(row)">撤销</el-button></template></el-table-column>
    </el-table>
    <div class="table-footer"><span><el-icon><Key /></el-icon> 明文密钥仅在创建时展示一次</span><el-button v-if="nextCursor" :loading="loading" @click="load(nextCursor)">加载更多</el-button><span>撤销不删除消费记录</span></div>
  </section>
  <p class="section-note">来源限制匹配网关识别的客户端 IP，留空表示不限制来源；它与供应商出口地址、数据地域配置分别生效。密钥额度是累计收费上限，退款不会自动恢复此额度。调用仍受钱包余额、模型权限和运行限额约束。</p>

  <el-dialog v-model="createVisible" title="创建 API 密钥" width="650px" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" destroy-on-close>
    <p class="dialog-description">推荐每个应用使用独立密钥。密钥权限始终受租户与所属成员授权限制。</p>
    <el-alert v-if="formError" :title="formError" type="error" :closable="false" class="key-alert" />
    <el-alert v-if="modelError" :title="modelError" type="warning" :closable="false" class="key-alert" />
    <el-form label-position="top" :disabled="saving || createUncertain" @submit.prevent="create">
      <el-form-item label="密钥名称（最多 100 字节）"><el-input v-model="form.name" maxlength="100" placeholder="例如：生产环境服务" /></el-form-item>
      <el-form-item label="可调用模型"><el-select v-model="form.scopes" multiple filterable :disabled="form.all" placeholder="选择授权模型" style="width:100%"><el-option v-for="model in models" :key="model.id" :label="model.public_name" :value="model.id" /></el-select><el-checkbox v-model="form.all" style="margin-top:8px">允许全部已授权模型（包含未来新增授权）</el-checkbox></el-form-item>
      <el-form-item label="地域限制"><el-select v-model="form.allowed_regions" multiple filterable allow-create default-first-option placeholder="选择或输入地域标签" style="width:100%" aria-label="新密钥地域限制"><el-option v-for="option in createRegionOptions" :key="option.value" :value="option.value" :label="option.label" /></el-select><p class="field-hint">global 是精确标签，不代表所有地域；单独 * 表示不额外缩小地域。模型与地域始终受租户和成员授权交集限制。</p></el-form-item>
      <el-form-item label="允许的客户端 IP / CIDR（可选）"><el-input v-model="form.clientCIDRs" type="textarea" :rows="4" aria-label="新密钥允许的客户端 IP 或 CIDR" placeholder="每行一个，例如 203.0.113.8 或 2001:db8::/64；留空表示不限来源" /><p class="field-hint">最多 32 项、合计 4096 字节；支持 IPv4 / IPv6。不能填写域名、URL、端口、zone、IPv4-mapped IPv6 或 /0。服务端会校验、规范化并去重。</p></el-form-item>
      <div class="limits-fields"><el-form-item label="RPM（1—6,000）"><el-input-number v-model="form.rpm_limit" :min="1" :max="6000" :precision="0" aria-label="新密钥 RPM" controls-position="right" /></el-form-item><el-form-item label="突发容量（1—200）"><el-input-number v-model="form.burst_limit" :min="1" :max="200" :precision="0" aria-label="新密钥突发容量" controls-position="right" /></el-form-item><el-form-item label="并发许可（1—16）"><el-input-number v-model="form.concurrency_limit" :min="1" :max="16" :precision="0" aria-label="新密钥并发许可上限" controls-position="right" /></el-form-item></div>
      <p class="field-hint limits-hint">RPM 为令牌补充速率，突发容量是桶容量，不是额外的每分钟额度。平台和租户上限仍共同生效；当前按单活动实例内存计数，重启后令牌重新满桶。</p>
      <div class="form-grid"><el-form-item label="累计消费上限（CNY）"><el-input v-model="form.limit" placeholder="留空表示不单独限制" inputmode="decimal" /></el-form-item><el-form-item label="过期时间"><el-input v-model="form.expires" type="datetime-local" /></el-form-item></div>
    </el-form>
    <template #footer><el-button :disabled="saving" @click="closeCreate">关闭</el-button><el-button v-if="createUncertain" :loading="loading" @click="load()">刷新列表核查</el-button><el-button v-else type="primary" :loading="saving" @click="create">创建并显示密钥</el-button></template>
  </el-dialog>

  <el-dialog v-model="policyVisible" :title="`${policyTarget?.name || ''} · 来源策略`" width="650px" :close-on-click-modal="false" :close-on-press-escape="!policySaving" :show-close="!policySaving" destroy-on-close>
    <p class="dialog-description">修改此密钥允许的客户端 IP / CIDR，保存后对后续鉴权及尚未获派发准入的调用生效。已获准派发的调用及在途 SSE 不会被撤回。密钥、模型范围、累计额度及过期时间保持不变。</p>
    <el-alert v-if="policyError" :title="policyError" type="error" :closable="false" class="key-alert" />
    <div v-loading="policyLoading">
      <template v-if="policyReview">
        <p class="muted small-text">密钥 <span class="mono">{{ policyReview.view.id }}</span> · 编辑基于策略版本 {{ policyReview.view.access_policy_revision }}</p>
        <el-alert v-if="policyBlocked" :title="policyReview.mode === 'unknown' ? '保存结果待确认，当前仅可核查' : '策略版本冲突，当前仅可核查'" type="warning" :closable="false" class="key-alert"><p>只读刷新显示当前配置，不能证明上次写入是否成功。请复核后明确开始新编辑；系统不会自动覆盖或重新提交。</p></el-alert>
        <el-form label-position="top" :disabled="policyLoading || policyReview.mode !== 'editing' || !canManageKeys" @submit.prevent="savePolicy"><el-form-item :label="policyBlocked ? '上次提交的来源（保留供复核）' : '允许的客户端 IP / CIDR'"><el-input v-model="policyText" type="textarea" :rows="6" aria-label="允许的客户端 IP 或 CIDR" placeholder="每行一个 IP 或 CIDR，留空表示不限来源" /><p class="field-hint">最多 32 项、合计 4096 字节。每行不能为空；不能使用 /0、域名、URL、端口、zone 或 IPv4-mapped IPv6。最终由服务端校验、规范化和去重。</p></el-form-item></el-form>
        <div v-if="policyReview.observed" class="policy-observed"><strong>只读核查：当前策略版本 {{ policyReview.observed.access_policy_revision }}</strong><div v-if="!policyReview.observed.allowed_client_cidrs.length" class="cidr-line">不限来源</div><div v-for="cidr in policyReview.observed.allowed_client_cidrs" :key="cidr" class="mono cidr-line">{{ cidr }}</div></div>
      </template>
    </div>
    <template #footer><el-button :disabled="policySaving" @click="closePolicy">关闭</el-button><el-button :icon="Refresh" :loading="policyLoading" :disabled="policySaving" @click="loadPolicy">{{ policyBlocked ? '只读刷新核查' : '重新读取当前' }}</el-button><el-button v-if="policyBlocked" type="primary" :disabled="!policyReview?.observed || policyLoading || !canManageKeys" @click="resumePolicy">以当前策略开始新编辑</el-button><el-button v-else type="primary" :loading="policySaving" :disabled="!policyReview || policyLoading || !canManageKeys" @click="savePolicy">保存来源策略</el-button></template>
  </el-dialog>

  <el-dialog v-model="limitsVisible" :title="`${limitsTarget?.name || ''} · 运行限额`" width="700px" :close-on-click-modal="false" :close-on-press-escape="!limitsSaving" :show-close="!limitsSaving" destroy-on-close>
    <p class="dialog-description">修改此 Key 的 RPM、突发容量和并发许可上限。来源策略、模型权限、累计额度及密钥保持不变；平台和租户上限仍共同生效。</p>
    <el-alert v-if="limitsError" :title="limitsError" type="error" :closable="false" class="key-alert" />
    <div v-loading="limitsLoading">
      <template v-if="limitsReview">
        <p class="muted small-text">密钥 <span class="mono">{{ limitsReview.view.id }}</span> · 编辑基于限额版本 {{ limitsReview.view.limit_revision }}</p>
        <el-alert v-if="limitsBlocked" :title="limitsReview.mode === 'unknown' ? '保存结果待确认，当前仅可核查' : '限额版本冲突，当前仅可核查'" type="warning" :closable="false" class="key-alert"><p>只读刷新显示当前配置，不能证明上次写入是否成功。请复核后明确开始新编辑；系统不会自动覆盖或重新提交。</p></el-alert>
        <p v-if="limitsBlocked" class="field-hint">以下保留上次提交的限额，供核查比较。</p>
        <el-form label-position="top" :disabled="limitsLoading || limitsReview.mode !== 'editing' || !canManageKeys" @submit.prevent="saveLimits"><div class="limits-fields"><el-form-item label="RPM（1—6,000）"><el-input-number v-model="limitsForm.rpm_limit" :min="1" :max="6000" :precision="0" aria-label="运行限额 RPM" controls-position="right" /></el-form-item><el-form-item label="突发容量（1—200）"><el-input-number v-model="limitsForm.burst_limit" :min="1" :max="200" :precision="0" aria-label="运行限额突发容量" controls-position="right" /></el-form-item><el-form-item label="并发许可（1—16）"><el-input-number v-model="limitsForm.concurrency_limit" :min="1" :max="16" :precision="0" aria-label="运行限额并发许可上限" controls-position="right" /></el-form-item></div></el-form>
        <div v-if="limitsReview.observed" class="policy-observed"><strong>只读核查：当前限额版本 {{ limitsReview.observed.limit_revision }}</strong><p>{{ limitsReview.observed.rpm_limit }} RPM · 突发容量 {{ limitsReview.observed.burst_limit }} · 并发许可 {{ limitsReview.observed.concurrency_limit }}</p></div>
      </template>
    </div>
    <p class="field-hint">RPM 为令牌补充速率，突发容量是桶容量，不是额外的每分钟额度。修改保留旧桶，不会补满令牌；下一次新调用准入观察到新配置时才切换。已获得运行态准入许可的排队及在途调用继续，降低并发只阻止新许可。</p>
    <p class="field-hint">运行计数保存在单活动实例内存中，重启后令牌重新满桶。这里显示 Key 配置，平台、租户和其他调用限制仍共同生效。</p>
    <template #footer><el-button :disabled="limitsSaving" @click="closeLimits">关闭</el-button><el-button :icon="Refresh" :loading="limitsLoading" :disabled="limitsSaving" @click="loadLimits">{{ limitsBlocked ? '只读刷新核查' : '重新读取当前' }}</el-button><el-button v-if="limitsBlocked" type="primary" :disabled="!limitsReview?.observed || limitsLoading || !canManageKeys" @click="resumeLimits">以当前限额开始新编辑</el-button><el-button v-else type="primary" :loading="limitsSaving" :disabled="!limitsReview || limitsLoading || !canManageKeys" @click="saveLimits">保存运行限额</el-button></template>
  </el-dialog>

  <el-dialog v-model="configurationVisible" :title="`${configurationTarget?.name || ''} · 基本配置`" width="760px" :close-on-click-modal="false" :close-on-press-escape="!configurationSaving" :show-close="!configurationSaving" destroy-on-close>
    <p class="dialog-description">编辑名称、模型范围、地域、累计消费上限及有效期。来源策略和运行限额分别管理，保存不更换密钥。模型与地域只是限制，始终与租户和所属成员授权取交集。</p>
    <el-alert v-if="configurationError" :title="configurationError" type="error" :closable="false" class="key-alert" />
    <el-alert v-if="modelError" :title="`${modelError}；已有范围仍完整保留，可使用当前历史值或明确输入模型 UUID。`" type="warning" :closable="false" class="key-alert" />
    <div v-loading="configurationLoading">
      <template v-if="configurationReview">
        <p class="muted small-text">密钥 <span class="mono">{{ configurationReview.view.id }}</span> · 编辑基于配置版本 {{ configurationReview.view.configuration_revision }}</p>
        <div class="configuration-usage">当前读取快照：已消费 {{ money(configurationReview.view.spent) }} · 冻结 {{ money(configurationReview.view.held) }} · 合计 {{ money(configurationUsage) }}<p class="field-hint">消费与冻结会继续变化，不属于配置版本；提交时服务端在锁内检查最新金额，上限不能低于最新已消费与冻结之和。</p></div>
        <el-alert v-if="configurationBlocked" :title="configurationReview.mode === 'unknown' ? '保存结果待确认，当前仅可核查' : '配置或额度冲突，当前仅可核查'" type="warning" :closable="false" class="key-alert"><p>以下保留上次提交的配置。只读刷新只能观察当前状态，不能证明上次写入是否成功；复核后需明确开始新编辑，系统不会自动重新提交。</p></el-alert>
        <el-form label-position="top" :disabled="configurationLoading || configurationReview.mode !== 'editing' || !canManageKeys" @submit.prevent="saveConfiguration">
          <el-form-item label="名称（修改后最多 100 UTF-8 字节）"><el-input v-model="configurationForm.name" type="textarea" :rows="2" aria-label="基本配置名称" /><details class="configuration-original"><summary>查看原始名称</summary><pre>{{ JSON.stringify(configurationReview.view.name) }}</pre></details></el-form-item>
          <el-form-item label="模型范围"><el-select v-model="configurationForm.scopes" multiple filterable allow-create default-first-option placeholder="选择模型或输入规范 UUID" style="width:100%" aria-label="基本配置模型范围"><el-option v-for="option in configurationScopeOptions" :key="option.value" :value="option.value" :label="option.label" /></el-select><p class="field-hint">修改后使用单独的 *，或 1—1000 个模型 UUID；* 包含未来新增外层授权。目录外值仍保留，未改字段会按原始顺序和内容提交。</p><details class="configuration-original"><summary>查看原始模型范围（含目录外或历史值）</summary><pre>{{ JSON.stringify(configurationReview.view.scopes, null, 2) }}</pre></details></el-form-item>
          <el-form-item label="地域限制"><el-select v-model="configurationForm.allowed_regions" multiple filterable allow-create default-first-option placeholder="选择或输入地域标签" style="width:100%" aria-label="基本配置地域限制"><el-option v-for="option in configurationRegionOptions" :key="option.value" :value="option.value" :label="option.label" /></el-select><p class="field-hint">修改后填写 1—32 个标签，每项最多 80 UTF-8 字节。global 是精确标签；* 须单独一项，且不会授予额外地域权限。</p><details class="configuration-original"><summary>查看原始地域（未改时原样保留）</summary><pre>{{ JSON.stringify(configurationReview.view.allowed_regions, null, 2) }}</pre></details></el-form-item>
          <el-form-item label="累计消费上限（CNY）"><el-input v-model="configurationForm.limit" inputmode="decimal" maxlength="100" placeholder="留空表示无累计上限；0 表示零预算" aria-label="基本配置累计消费上限" /><p class="field-hint">支持最多 6 位小数。留空会明确移除上限；0 与未设置不同。调整不会重置累计消费或冻结，也不会变更钱包余额。</p></el-form-item>
          <el-form-item label="有效期"><el-radio-group v-model="configurationForm.expiry_mode" aria-label="基本配置有效期操作"><el-radio-button value="keep">保留原期限</el-radio-button><el-radio-button value="set">设置新期限</el-radio-button><el-radio-button value="none">移除期限</el-radio-button></el-radio-group><p class="field-hint">原始期限：<span class="mono">{{ configurationReview.view.expires_at ?? '长期有效（null）' }}</span></p><template v-if="configurationForm.expiry_mode === 'set'"><el-input v-model="configurationForm.expiry" placeholder="2030-01-02T03:04:05.123456+08:00" aria-label="基本配置新到期时间 RFC3339" /><p class="field-hint">填写带时区的 RFC3339 时间，例如 2030-01-02T03:04:05.123456+08:00；Z 表示 UTC。新期限须晚于服务端提交时刻，保存为 UTC 微秒精度。</p></template><p v-else-if="configurationForm.expiry_mode === 'none'" class="field-hint">保存将明确移除期限，已过期密钥可重新启用；已撤销密钥无法恢复。</p><p v-else class="field-hint">原始秒与微秒保持不变；保留已过期时间也可编辑其他字段。</p></el-form-item>
        </el-form>
        <div v-if="configurationReview.observed" class="policy-observed configuration-observed"><strong>只读核查：当前配置版本 {{ configurationReview.observed.configuration_revision }}</strong><dl><dt>名称</dt><dd class="mono">{{ JSON.stringify(configurationReview.observed.name) }}</dd><dt>模型范围</dt><dd class="mono">{{ JSON.stringify(configurationReview.observed.scopes) }}</dd><dt>地域</dt><dd class="mono">{{ JSON.stringify(configurationReview.observed.allowed_regions) }}</dd><dt>累计上限</dt><dd>{{ configurationReview.observed.lifetime_limit === null ? '未设置（null）' : money(configurationReview.observed.lifetime_limit) }}</dd><dt>有效期</dt><dd class="mono">{{ configurationReview.observed.expires_at ?? '长期有效（null）' }}</dd><dt>消费 / 冻结快照</dt><dd>{{ money(configurationReview.observed.spent) }} / {{ money(configurationReview.observed.held) }}</dd></dl></div>
      </template>
    </div>
    <p class="field-hint">模型、地域或有效期收紧会影响尚未获派发准入的调用；已获准派发及在途 SSE 继续按原规则结束。预算修改只影响新预占，已有冻结受保护；来源和运行限额继续独立生效。</p>
    <template #footer><el-button :disabled="configurationSaving" @click="closeConfiguration">关闭</el-button><el-button :icon="Refresh" :loading="configurationLoading" :disabled="configurationSaving" @click="loadConfiguration">{{ configurationBlocked ? '只读刷新核查' : '重新读取当前' }}</el-button><el-button v-if="configurationBlocked" type="primary" :disabled="!configurationReview?.observed || configurationLoading || !canManageKeys" @click="resumeConfiguration">以当前基本配置开始新编辑</el-button><el-button v-else type="primary" :loading="configurationSaving" :disabled="!configurationReview || configurationLoading || !canManageKeys" @click="saveConfiguration">保存基本配置</el-button></template>
  </el-dialog>

  <el-dialog v-model="secretOpen" title="保存你的 API 密钥" width="550px" :close-on-click-modal="false" destroy-on-close @close="clearSecret"><el-alert type="warning" title="明文只展示这一次。关闭后无法再次查看，请立即安全保存。" :closable="false" show-icon /><div class="secret-display mono">{{ plaintext }}</div><p class="field-hint">不要放入浏览器前端、公共代码库或 URL。请使用服务端环境变量或密钥管理服务保存。</p><template #footer><el-button :icon="CopyDocument" @click="copy">复制密钥</el-button><el-button type="primary" @click="clearSecret">已保存，关闭</el-button></template></el-dialog>
</template>

<style scoped>
.key-alert{margin-bottom:20px}.key-alert p{margin:8px 0}.cidr-line{margin-top:8px;overflow-wrap:anywhere}.policy-observed{border:1px solid #dce5ed;border-radius:8px;padding:16px;margin-top:16px}.policy-observed strong{font-size:13px}.limits-fields{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}.limits-fields .el-input-number{width:100%}.limits-hint{margin-top:0;margin-bottom:20px}.configuration-usage{padding:12px 16px;border-radius:8px;background:#f3f6f9;margin-bottom:18px;font-size:13px}.configuration-usage p{margin-bottom:0}.configuration-original{width:100%;font-size:12px;color:#65758a;margin-top:8px}.configuration-original summary{cursor:pointer}.configuration-original pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:160px;overflow:auto;background:#f5f7fa;padding:10px;border-radius:6px}.configuration-observed dl{display:grid;grid-template-columns:112px minmax(0,1fr);gap:8px;font-size:12px}.configuration-observed dt{color:#65758a}.configuration-observed dd{margin:0;overflow-wrap:anywhere}@media(max-width:600px){.limits-fields{grid-template-columns:1fr;gap:0}.configuration-observed dl{grid-template-columns:1fr}}
</style>
