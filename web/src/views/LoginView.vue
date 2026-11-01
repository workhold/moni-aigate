<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Lock, Right } from '@element-plus/icons-vue'
import { errorText, request } from '../api'
import { loadMe, session } from '../session'
import { fetchIdentityCapabilities } from '../identity-public'

const route = useRoute(); const router = useRouter()
const email = ref(''); const password = ref(''); const code = ref(''); const busy = ref(false); const error = ref('')
const mfa = computed(() => session.mfaPending && !session.enrollmentRequired)
const registrationAvailable = ref(false)
const capabilityController = new AbortController()
let disposed = false
onMounted(async () => { try { const result = await fetchIdentityCapabilities(capabilityController.signal); if (!disposed) registrationAvailable.value = result.data.registration.available } catch { /* Capability discovery never blocks existing password/MFA login. */ } })
async function continueAfterLogin() {
  await loadMe(true)
  if (session.enrollmentRequired) { await router.replace('/security'); return }
  if (session.mfaPending) return
  const redirect = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') && !route.query.redirect.startsWith('//') ? route.query.redirect : '/'
  await router.replace(redirect === '/login' ? '/' : redirect)
}
async function submit() {
  if (busy.value) return
  error.value = ''; busy.value = true
  try {
    if (mfa.value) { await request('/auth/mfa/verify', 'POST', { code: code.value.trim() }); code.value = '' }
    else {
      const result = await request<{ mfa_required?: boolean; enrollment_required?: boolean }>('/auth/login', 'POST', { email: email.value.trim(), password: password.value })
      password.value = ''; session.mfaPending = !!result.data?.mfa_required
    }
    await continueAfterLogin()
  } catch (e) { error.value = errorText(e) }
  finally { busy.value = false }
}
onUnmounted(() => { disposed = true; capabilityController.abort(); password.value = ''; code.value = '' })
</script>

<template>
  <div class="login-screen">
    <section class="login-story">
      <div class="brand"><span class="brand-mark">m<span>·</span></span><span>moni<span class="brand-suffix">aigate</span></span></div>
      <div class="login-hero"><div class="eyebrow">ONE GATEWAY. CLEAR OPERATIONS.</div><h1>统一接入 AI，<br />让每一次调用<br />清晰可见。</h1><p>从渠道调度到客户账单，连接模型能力与商业运营的每一个环节。</p><div class="login-capabilities"><span>统一 API</span><span>可追溯账单</span><span>租户隔离</span></div></div>
      <div class="login-caption">MONI-AIGATE / COMMERCIAL AI GATEWAY</div>
    </section>
    <section class="login-form-column"><div class="login-card">
      <div class="eyebrow">WELCOME TO YOUR WORKSPACE</div><h2>{{ mfa ? '验证登录身份' : '登录控制台' }}</h2><p class="muted">{{ mfa ? '请输入验证器中的动态验证码，或使用一次性恢复码。' : '使用账号进入你的 AI API 工作空间。' }}</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
      <el-form label-position="top" @submit.prevent="submit">
        <template v-if="!mfa"><el-form-item label="邮箱地址"><el-input v-model="email" type="email" size="large" placeholder="you@company.com" autocomplete="username" autofocus /></el-form-item><el-form-item label="账号密码"><el-input v-model="password" type="password" size="large" placeholder="输入账号密码" show-password autocomplete="current-password" /></el-form-item></template>
        <el-form-item v-else label="MFA 验证码 / 恢复码"><el-input v-model="code" placeholder="输入验证码" size="large" maxlength="32" autocomplete="one-time-code" autofocus /></el-form-item>
        <el-button type="primary" native-type="submit" :loading="busy" :disabled="mfa ? !code.trim() : !email.trim() || !password">{{ mfa ? '验证并继续' : '登录工作空间' }}<el-icon class="el-icon--right"><Right /></el-icon></el-button>
      </el-form>
      <div v-if="!mfa" class="login-identity-links"><router-link v-if="registrationAvailable" to="/register">创建账号</router-link><router-link to="/password/forgot">忘记密码</router-link></div>
      <div class="login-pricing-link"><router-link to="/pricing">先查看公开模型与价格<el-icon><Right /></el-icon></router-link></div>
      <div class="login-footer"><el-icon><Lock /></el-icon> {{ registrationAvailable ? '可通过邮箱创建账号。' : '首次使用请联系平台运营人员。' }}已启用 MFA 的账号登录时还需输入验证码。</div>
    </div></section>
  </div>
</template>

<style scoped>
.login-pricing-link{margin-top:18px;text-align:center;font-size:11px}.login-pricing-link a{display:inline-flex;align-items:center;gap:6px;color:#158078;text-decoration:none}
.login-identity-links{display:flex;justify-content:flex-end;gap:18px;margin-top:16px;font-size:11px}.login-identity-links a{color:#158078;text-decoration:none}
</style>
