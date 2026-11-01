<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Refresh, Right } from '@element-plus/icons-vue'
import IdentityLayout from '../components/IdentityLayout.vue'
import { completeIdentityProof, fetchIdentityCapabilities, identityErrorText, IdentityInputError, identityUnavailableText } from '../identity-public'
import { createIdentityAction, createIdentityCapabilitiesReader } from '../identity-public-state'
import { takeIdentityProof } from '../identity-proof'
import type { IdentityPurpose } from '../identity-proof'
import { clearSession, session } from '../session'

const route = useRoute(), router = useRouter()
const purpose: IdentityPurpose = route.meta.identityPurpose === 'registration' ? 'registration' : 'password_reset'
const registration = purpose === 'registration', requestPath = registration ? '/register' : '/password/forgot'
const password = ref(''), confirmation = ref(''), tenantName = ref(''), mfaCode = ref(''), localError = ref('')
let action = createIdentityAction(purpose, takeIdentityProof(route.path), true), capabilityController: AbortController | undefined
const reader = createIdentityCapabilitiesReader(), capabilities = ref(reader.snapshot()), state = ref(action.snapshot())
const availability = computed(() => capabilities.value.capabilities?.[purpose])
const available = computed(() => availability.value?.available === true)
const canSubmit = computed(() => available.value && ['ready', 'error', 'proof_invalid'].includes(state.value.phase))
const sync = () => { state.value = action.snapshot() }
function clearInputs() { password.value = ''; confirmation.value = ''; mfaCode.value = '' }
async function refreshCapabilities() {
  capabilityController?.abort(); capabilityController = new AbortController()
  const ticket = reader.begin(); capabilities.value = reader.snapshot()
  try { const result = await fetchIdentityCapabilities(capabilityController.signal); if (reader.accept(ticket, result.data)) capabilities.value = reader.snapshot() }
  catch { if (reader.fail(ticket)) capabilities.value = reader.snapshot() }
}
async function submit() {
  localError.value = ''
  const current = action
  let submission: ReturnType<typeof current.beginCompletion>
  try {
    if (password.value !== confirmation.value) throw new IdentityInputError('两次输入的密码不一致。')
    submission = current.beginCompletion({ password: password.value, tenant_name: tenantName.value, mfa_code: mfaCode.value }, available.value); sync()
  } catch (error) { localError.value = identityErrorText(error); clearInputs(); return }
  clearInputs()
  try { await completeIdentityProof(purpose, submission.body); if (current.accept(submission.ticket)) sync() }
  catch (error) { if (current.fail(submission.ticket, error)) sync() }
}
function resetContext() { action.dispose(); action = createIdentityAction(purpose, undefined, true); clearInputs(); tenantName.value = ''; localError.value = ''; sync(); capabilityController?.abort(); reader.invalidate(); capabilities.value = reader.snapshot(); void refreshCapabilities() }
function goToLogin() { action.dispose(); clearInputs(); tenantName.value = ''; clearSession(); void router.push('/login') }
watch(() => `${session.me?.user.id || ''}/${session.me?.tenant?.id || ''}`, resetContext, { flush: 'sync' })
onMounted(() => { void refreshCapabilities() })
onBeforeUnmount(() => { action.dispose(); reader.dispose(); capabilityController?.abort(); clearInputs(); tenantName.value = ''; localError.value = '' })
</script>

<template>
  <IdentityLayout :eyebrow="registration ? 'FINISH YOUR REGISTRATION' : 'SET A NEW PASSWORD'" :title="registration ? '完成账号注册' : '设置新的账号密码'" :description="registration ? '通过最新邮件中的证明设置密码，创建你的工作空间。' : '通过邮件证明设置新密码；已启用的验证器仍需验证。'">
    <div v-if="state.phase === 'completed'" class="identity-result" aria-live="polite"><el-alert :title="registration ? '账号与工作空间已创建，请登录继续。' : '密码已更新，请重新登录。'" type="success" :closable="false" show-icon /><p>{{ registration ? '注册不会自动开通模型权限、创建 API Key 或增加余额。请登录后按平台规则完成接入。' : '所有设备的账号会话均已退出。已有 API Key 不会自动撤销；如怀疑密钥泄露，请登录后单独管理。' }}</p><el-button type="primary" @click="goToLogin">前往登录<el-icon class="el-icon--right"><Right /></el-icon></el-button></div>
    <template v-else-if="state.phase === 'unknown'"><el-alert title="本次提交结果尚未确认" type="warning" :closable="false" show-icon /><p class="identity-description">{{ state.error }}原提交已锁定，页面不会自动重试。你可以前往登录核查刚设置的密码，或明确申请新的邮件链接。</p><div class="identity-actions"><el-button type="primary" @click="goToLogin">前往登录核查</el-button><router-link :to="requestPath">重新请求邮件</router-link></div></template>
    <template v-else-if="state.phase === 'missing_proof'"><el-alert title="请从最新邮件重新打开此页面" type="info" :closable="false" show-icon /><p class="identity-description">当前页面没有可用的邮件证明。刷新、离开页面或账号切换后，需重新打开邮件链接；也可以重新申请一封邮件。</p><div class="identity-actions"><router-link :to="requestPath">重新请求邮件</router-link><el-button link type="primary" @click="goToLogin">返回登录</el-button></div></template>
    <template v-else>
      <div class="identity-capabilities"><span v-if="capabilities.loading">正在读取自助功能状态…</span><span v-else-if="available">自助身份服务当前可用</span><span v-else>当前自助功能不可用</span><el-button link type="primary" :icon="Refresh" :loading="capabilities.loading" @click="refreshCapabilities">刷新状态</el-button></div>
      <el-alert v-if="capabilities.error" :title="capabilities.error" type="warning" :closable="false" show-icon />
      <el-alert v-else-if="availability && !available" :title="identityUnavailableText(availability)" type="info" :closable="false" show-icon><p>功能暂停期间无法提交；邮件有效期不会延长，暂停不代表证明已永久撤销。</p></el-alert>
      <el-alert v-if="localError || state.error" :title="localError || state.error" type="error" :closable="false" show-icon />
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item v-if="registration" label="工作空间名称"><el-input v-model="tenantName" size="large" placeholder="例如：我的 AI 工作空间" :disabled="!canSubmit" maxlength="200" autocomplete="organization" /><div class="identity-field-hint">名称最多200字节，中文等字符会占用多个字节。</div></el-form-item>
        <el-form-item label="新密码"><el-input v-model="password" type="password" size="large" placeholder="设置12至72字节的密码" show-password autocomplete="new-password" :disabled="!canSubmit" /><div class="identity-field-hint">密码保留空格，按 UTF-8 字节计算长度；提交后会清空输入。</div></el-form-item>
        <el-form-item label="确认新密码"><el-input v-model="confirmation" type="password" size="large" placeholder="再次输入新密码" show-password autocomplete="new-password" :disabled="!canSubmit" /></el-form-item>
        <el-form-item v-if="!registration" label="验证器验证码 / 恢复码（按需填写）"><el-input v-model="mfaCode" size="large" placeholder="未启用验证器可留空" autocomplete="one-time-code" :disabled="!canSubmit" maxlength="128" /><div class="identity-field-hint">若账号已启用验证器，请填写动态验证码或一次性恢复码。</div></el-form-item>
        <el-button type="primary" native-type="submit" :loading="state.phase === 'submitting'" :disabled="!canSubmit || !password || !confirmation || (registration && !tenantName.trim())">{{ registration ? '验证并创建账号' : '验证并设置新密码' }}<el-icon class="el-icon--right"><Right /></el-icon></el-button>
      </el-form>
      <div class="identity-actions"><el-button link type="primary" @click="goToLogin">返回登录</el-button><router-link :to="requestPath">重新请求邮件</router-link></div>
      <p class="identity-notice">{{ registration ? '注册链接从签发起有效30分钟。' : '恢复链接从签发起有效15分钟。' }}请使用最新邮件；只有点击提交才会验证证明并修改账号。</p>
    </template>
  </IdentityLayout>
</template>
