<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Refresh, Right } from '@element-plus/icons-vue'
import IdentityLayout from '../components/IdentityLayout.vue'
import { fetchIdentityCapabilities, identityAcceptedMessage, identityErrorText, identityUnavailableText, requestIdentityEmail } from '../identity-public'
import { createIdentityAction, createIdentityCapabilitiesReader } from '../identity-public-state'
import type { IdentityPurpose } from '../identity-proof'
import { clearSession, session } from '../session'

const route = useRoute(), router = useRouter()
const purpose: IdentityPurpose = route.meta.identityPurpose === 'registration' ? 'registration' : 'password_reset'
const registration = purpose === 'registration'
const email = ref(''), localError = ref('')
let action = createIdentityAction(purpose), capabilityController: AbortController | undefined
const reader = createIdentityCapabilitiesReader(), capabilities = ref(reader.snapshot()), state = ref(action.snapshot())
const availability = computed(() => capabilities.value.capabilities?.[purpose])
const available = computed(() => availability.value?.available === true)
const canSubmit = computed(() => available.value && ['ready', 'error'].includes(state.value.phase))
const sync = () => { state.value = action.snapshot() }
async function refreshCapabilities() {
  capabilityController?.abort(); capabilityController = new AbortController()
  const ticket = reader.begin(); capabilities.value = reader.snapshot()
  try { const result = await fetchIdentityCapabilities(capabilityController.signal); if (reader.accept(ticket, result.data)) capabilities.value = reader.snapshot() }
  catch { if (reader.fail(ticket)) capabilities.value = reader.snapshot() }
}
async function submit() {
  localError.value = ''
  const current = action
  let submission: ReturnType<typeof current.beginRequest>
  try { submission = current.beginRequest(email.value, available.value); sync() }
  catch (error) { localError.value = identityErrorText(error); return }
  try { await requestIdentityEmail(purpose, submission.email); if (current.accept(submission.ticket)) sync() }
  catch (error) { if (current.fail(submission.ticket, error)) sync() }
}
function newRequest() { action.newMailRequest(); localError.value = ''; sync() }
function resetContext() { action.dispose(); action = createIdentityAction(purpose); email.value = ''; localError.value = ''; sync(); capabilityController?.abort(); reader.invalidate(); capabilities.value = reader.snapshot(); void refreshCapabilities() }
function goToLogin() { action.dispose(); email.value = ''; clearSession(); void router.push('/login') }
watch(() => `${session.me?.user.id || ''}/${session.me?.tenant?.id || ''}`, resetContext, { flush: 'sync' })
onMounted(() => { void refreshCapabilities() })
onBeforeUnmount(() => { action.dispose(); reader.dispose(); capabilityController?.abort(); email.value = ''; localError.value = '' })
</script>

<template>
  <IdentityLayout :eyebrow="registration ? 'CREATE YOUR ACCOUNT' : 'RECOVER YOUR ACCOUNT'" :title="registration ? '创建你的账号' : '找回账号密码'" :description="registration ? '先验证邮箱，再设置密码和工作空间名称。' : '填写账号邮箱，通过邮件中的链接设置新密码。'">
    <div class="identity-capabilities"><span v-if="capabilities.loading">正在读取自助功能状态…</span><span v-else-if="available">{{ registration ? '自助注册已开放' : '邮件密码恢复已开放' }}</span><span v-else>当前自助功能不可用</span><el-button link type="primary" :icon="Refresh" :loading="capabilities.loading" @click="refreshCapabilities">刷新状态</el-button></div>
    <el-alert v-if="capabilities.error" :title="capabilities.error" type="warning" :closable="false" show-icon />
    <el-alert v-else-if="availability && !available" :title="identityUnavailableText(availability)" type="info" :closable="false" show-icon />
    <el-alert v-if="localError || state.error" :title="localError || state.error" :type="state.phase === 'unknown' ? 'warning' : 'error'" :closable="false" show-icon />
    <div v-if="state.phase === 'accepted' || state.phase === 'unknown'" class="identity-result" aria-live="polite">
      <el-alert v-if="state.phase === 'accepted'" :title="identityAcceptedMessage" type="success" :closable="false" show-icon />
      <p v-else>本次请求结果尚未确认。请先检查邮箱；页面不会自动重发。若你决定再申请，需重新明确提交。</p>
      <p>如果收到多封邮件，请使用最新链接。{{ registration ? '注册链接从签发起有效30分钟。' : '恢复链接从签发起有效15分钟。' }}邮件服务配置有效不代表一定能投递到收件箱。</p>
      <el-button @click="newRequest">重新填写邮件请求</el-button>
    </div>
    <el-form v-else label-position="top" @submit.prevent="submit">
      <el-form-item label="邮箱地址"><el-input v-model="email" type="email" size="large" placeholder="you@company.com" autocomplete="email" :disabled="!canSubmit" maxlength="254" /><div class="identity-field-hint">使用单个邮箱地址，保留邮箱中的点号与 + 标签。</div></el-form-item>
      <el-button type="primary" native-type="submit" :loading="state.phase === 'submitting'" :disabled="!canSubmit || !email.trim()">{{ registration ? '请求注册邮件' : '请求密码恢复邮件' }}<el-icon class="el-icon--right"><Right /></el-icon></el-button>
    </el-form>
    <div class="identity-actions"><el-button link type="primary" @click="goToLogin">返回登录</el-button><router-link v-if="registration" to="/password/forgot">忘记密码</router-link></div>
    <p class="identity-notice">{{ registration ? '创建账号后仍需登录。模型调用权限、API Key 与余额由平台另行管理。' : '如账号已启用验证器，设置新密码时仍需动态验证码或恢复码。无法完成时请联系平台运营人员。' }}</p>
  </IdentityLayout>
</template>
