<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CircleCheck, Lock, CopyDocument, Refresh } from '@element-plus/icons-vue'
import QRCode from 'qrcode'
import PageHeader from '../components/PageHeader.vue'
import { errorText, request } from '../api'
import { clearSession, loadMe, session } from '../session'
import { label, time } from '../format'

const router = useRouter(); const busy = ref(false); const error = ref(''); const code = ref('')
type Enrollment = { secret: string; otpauth_url: string; recovery_codes?: string[] }
const enrollment = ref<Enrollment | null>(null)
const acknowledged = ref(false)
const qrImage = ref(''); const qrError = ref('')
const validCode = computed(() => /^\d{6}$/.test(code.value.trim()))
let setupGeneration = 0
const passwordForm = ref({ current: '', next: '', confirmation: '', code: '' }); const passwordBusy = ref(false); const passwordError = ref('')
async function setup() {
  if (busy.value) return
  const generation = ++setupGeneration
  busy.value = true; error.value = ''; enrollment.value = null
  qrImage.value = ''; qrError.value = ''; acknowledged.value = false; code.value = ''
  try {
    const pending = (await request<Enrollment>('/auth/mfa/setup', 'POST', {})).data
    if (generation !== setupGeneration) return
    enrollment.value = pending
    try {
      const image = await QRCode.toDataURL(pending.otpauth_url, { errorCorrectionLevel: 'M', margin: 4, width: 240 })
      if (generation === setupGeneration) qrImage.value = image
    } catch {
      if (generation === setupGeneration) qrError.value = '二维码生成失败，请使用下方手动添加方式。'
    }
  }
  catch (e) { if (generation === setupGeneration) error.value = errorText(e) }
  finally { if (generation === setupGeneration) busy.value = false }
}
async function regenerate() {
  if (busy.value) return
  try {
    await ElMessageBox.confirm('重新生成后，当前待绑定的密钥、二维码和恢复码将失效。请删除验证器中旧的 moni-aigate 条目，再添加新条目并保存新的恢复码。', '重新生成绑定信息', { type: 'warning', confirmButtonText: '重新生成', cancelButtonText: '取消' })
  } catch { return }
  await setup()
}
async function enable() {
  if (busy.value || !validCode.value || !enrollment.value || (!!enrollment.value.recovery_codes?.length && !acknowledged.value)) return
  busy.value = true; error.value = ''
  try { await request('/auth/mfa/enable', 'POST', { code: code.value.trim() }); code.value = ''; enrollment.value = null; qrImage.value = ''; acknowledged.value = false; await loadMe(true); ElMessage.success('MFA 已启用。'); await router.replace('/') }
  catch (e) { error.value = errorText(e) }
  finally { busy.value = false }
}
async function copy(value: string) { try { await navigator.clipboard.writeText(value); ElMessage.success('已复制，请妥善保管。') } catch { ElMessage.warning('无法访问剪贴板，请手动选择复制。') } }
async function changePassword() {
  if (passwordBusy.value) return
  passwordBusy.value = true; passwordError.value = ''
  try {
    const size = new TextEncoder().encode(passwordForm.value.next).length
    if (size < 12 || size > 72) throw new Error('新密码须为 12–72 字节，英文字符每个占 1 字节。')
    if (passwordForm.value.next !== passwordForm.value.confirmation) throw new Error('两次输入的新密码不一致。')
    if (passwordForm.value.next === passwordForm.value.current) throw new Error('新密码须与当前密码不同。')
    if (session.me?.user.mfa_enabled && !passwordForm.value.code.trim()) throw new Error('请输入当前 MFA 验证码或一次性恢复码。')
    await request('/auth/password', 'POST', { current_password: passwordForm.value.current, new_password: passwordForm.value.next, code: passwordForm.value.code.trim() })
    passwordForm.value = { current: '', next: '', confirmation: '', code: '' }; clearSession(); ElMessage.success('密码已更改，所有登录会话已退出。请使用新密码登录。'); await router.replace('/login')
  } catch (e) { passwordError.value = errorText(e) } finally { passwordBusy.value = false }
}
onUnmounted(() => { ++setupGeneration; passwordForm.value = { current: '', next: '', confirmation: '', code: '' }; enrollment.value = null; qrImage.value = ''; qrError.value = ''; acknowledged.value = false; code.value = '' })
</script>

<template>
  <PageHeader eyebrow="ACCOUNT & SECURITY" title="账号安全" description="保护登录身份，为敏感管理与财务操作增加一道确认。" />
  <el-alert v-if="session.enrollmentRequired" type="warning" title="平台与财务账号须先启用 MFA，才能使用业务功能。" :closable="false" show-icon style="margin-bottom:22px" />
  <div v-else-if="!session.me?.user.mfa_enabled" class="mfa-optional-notice"><el-alert type="info" title="MFA 为可选功能，当前未启用。" description="无需绑定验证器即可继续使用控制台。需要额外登录保护时，可在此扫码启用。" :closable="false" show-icon /><el-button @click="router.push('/')">返回工作台</el-button></div>
  <el-alert v-if="error" type="error" :title="error" :closable="false" show-icon style="margin-bottom:22px" />
  <div class="security-grid">
    <section class="panel"><div class="panel-header"><div class="panel-title">多因素认证</div><el-tag :type="session.me?.user.mfa_enabled ? 'success' : session.enrollmentRequired ? 'warning' : 'info'">{{ session.me?.user.mfa_enabled ? '已启用' : '未启用' }}</el-tag></div><div class="panel-body">
      <div class="security-state"><div class="security-state-icon"><el-icon><CircleCheck v-if="session.me?.user.mfa_enabled" /><Lock v-else /></el-icon></div><div><strong>{{ session.me?.user.mfa_enabled ? '动态验证码已保护你的账号' : session.enrollmentRequired ? '为账号启用验证器' : '可选：为账号启用验证器' }}</strong><p>兼容支持 TOTP 的标准验证器应用</p></div></div>
      <p v-if="session.me?.user.mfa_enabled" class="muted small-text">登录及敏感再认证时使用动态验证码。恢复码只能使用一次，请与账号密码分开保存。需要重置认证方式时，请通过受控运维流程处理。</p>
      <template v-if="!session.me?.user.mfa_enabled">
        <div class="authenticator-help"><strong>动态验证码在哪儿生成？</strong><p>在手机上的 Google Authenticator、Microsoft Authenticator 或其他支持 TOTP 的验证器应用中生成。添加账号后，应用会显示每 30 秒更新的 6 位数字。</p><p>这不是短信验证码，网页也不会替你生成验证码。请先准备好验证器应用。</p></div>
      </template>
      <template v-if="!session.me?.user.mfa_enabled && !enrollment"><p class="muted small-text" style="margin:18px 0 22px">点击开始后，用验证器添加此账号，再输入应用中的动态码完成绑定。绑定期间还会展示一次性恢复码，请安全保存。</p><el-button type="primary" :loading="busy" :icon="Lock" @click="setup">开始设置 MFA</el-button></template>
      <template v-else-if="enrollment">
        <div class="enrollment-actions"><span class="field-hint">已分享绑定信息或需要重新开始？</span><el-button size="small" :icon="Refresh" :disabled="busy" @click="regenerate">重新生成绑定信息</el-button></div>
        <div class="instruction-step"><div class="step-number">1</div><div class="enrollment-step"><h3>在验证器应用中添加账号</h3><p>打开验证器，选择“添加账号”或“＋” → “扫描二维码”，扫描下面的二维码。随后应用中会出现 moni-aigate 账号和 6 位动态码。</p><div v-if="qrImage" class="enrollment-qr"><img :src="qrImage" alt="用于在验证器中添加 moni-aigate 账号的二维码" width="240" height="240" /><p>二维码在本页本地生成，请勿分享。</p></div><el-alert v-if="qrError" :title="qrError" type="warning" :closable="false" style="margin-top:14px" /><details class="manual-enrollment" :open="!!qrError"><summary>无法扫码？使用密钥手动添加</summary><p>在验证器中选择“手动输入密钥”，账号名称可填 moni-aigate，密钥填下方内容，类型选择“基于时间” / TOTP。</p><div class="secret-display mono">{{ enrollment.secret }}</div><el-button size="small" :icon="CopyDocument" @click="copy(enrollment.secret)">复制密钥</el-button><p style="margin-top:16px">支持导入链接的验证器也可使用下方 otpauth URI。密钥、URI 和二维码具有相同作用，请妥善保管。</p><el-input :model-value="enrollment.otpauth_url" aria-label="验证器导入 URI" readonly type="textarea" :rows="3" style="margin-top:8px" /></details></div></div>
        <div v-if="enrollment.recovery_codes?.length" class="instruction-step"><div class="step-number">2</div><div class="enrollment-step"><h3>安全保存一次性恢复码</h3><p>仅在无法使用验证器时用于恢复访问，每个只能使用一次。它们不是本次绑定要填写的 6 位动态码。</p><div class="secret-display mono">{{ enrollment.recovery_codes.join('\n') }}</div><el-button size="small" :icon="CopyDocument" @click="copy(enrollment.recovery_codes!.join('\n'))">复制恢复码</el-button><div style="margin-top:14px"><el-checkbox v-model="acknowledged">我已安全保存恢复码</el-checkbox></div></div></div>
        <div class="instruction-step"><div class="step-number">{{ enrollment.recovery_codes?.length ? 3 : 2 }}</div><div class="enrollment-step"><h3>输入应用中的动态码，完成绑定</h3><p>切回验证器，查看刚添加的 moni-aigate 条目，将当前 6 位数字填入下方。如果即将倒计时结束，请等待下一组数字；手机时间需设为自动同步。</p><el-form label-position="top" style="margin-top:16px" @submit.prevent="enable"><el-form-item label="验证器应用中的 6 位动态验证码"><el-input v-model="code" maxlength="6" inputmode="numeric" autocomplete="one-time-code" placeholder="输入应用中当前的 6 位数字" /></el-form-item><el-button native-type="submit" type="primary" :loading="busy" :disabled="!validCode || (!!enrollment.recovery_codes?.length && !acknowledged)">验证并启用</el-button></el-form></div></div>
      </template>
    </div></section>
    <section class="panel"><div class="panel-header"><div class="panel-title">当前账号</div></div><div class="panel-body"><dl class="detail-list"><dt>邮箱</dt><dd>{{ session.me?.user.email }}</dd><dt>平台角色</dt><dd>{{ label(session.me?.user.platform_role) }}</dd><dt>当前工作空间</dt><dd>{{ session.me?.tenant?.name || '无客户工作空间' }}</dd><dt>租户角色</dt><dd>{{ label(session.me?.tenant?.role) }}</dd><dt>敏感操作有效至</dt><dd>{{ time(session.me?.reauthenticated_until) }}</dd></dl><div class="danger-note" style="margin-top:28px">不要将登录密码、API Key 或 MFA 恢复码放入 URL、工单正文或公共聊天。API Key 与管理登录凭据分别使用。</div></div></section>
  </div>
  <section class="panel"><div class="panel-header"><div><div class="panel-title">修改登录密码</div><div class="panel-subtitle">验证当前密码与已启用的 MFA，成功后所有设备的登录会话都会退出。</div></div></div><div class="panel-body" style="max-width:640px">
    <el-alert v-if="passwordError" :title="passwordError" type="error" :closable="false" style="margin-bottom:20px" />
    <el-form label-position="top" @submit.prevent="changePassword"><el-form-item label="当前密码" required><el-input v-model="passwordForm.current" type="password" show-password autocomplete="current-password" /></el-form-item><div class="form-grid"><el-form-item label="新密码" required><el-input v-model="passwordForm.next" type="password" show-password autocomplete="new-password" /><div class="field-hint">12–72 字节；英文字符每个占 1 字节，中文占多个字节。</div></el-form-item><el-form-item label="再次输入新密码" required><el-input v-model="passwordForm.confirmation" type="password" show-password autocomplete="new-password" /></el-form-item></div><el-form-item v-if="session.me?.user.mfa_enabled" label="MFA 验证码 / 恢复码" required><el-input v-model="passwordForm.code" maxlength="32" autocomplete="one-time-code" placeholder="当前动态验证码或一次性恢复码" /></el-form-item><el-button native-type="submit" type="primary" :loading="passwordBusy" :disabled="!passwordForm.current || !passwordForm.next || !passwordForm.confirmation">更改密码并重新登录</el-button></el-form>
  </div></section>
</template>

<style scoped>
.mfa-optional-notice{display:flex;align-items:center;gap:14px;margin-bottom:22px}
.mfa-optional-notice .el-alert{flex:1}
.authenticator-help{background:#f3f8fb;border:1px solid #e0eaf1;border-radius:8px;padding:15px 17px;font-size:12px;line-height:1.8;color:#566c80}
.authenticator-help strong{font-weight:600;color:#314e65}
.authenticator-help p{margin-top:7px}
.enrollment-actions{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:10px;margin-top:20px;padding-bottom:6px}
.enrollment-actions .field-hint{margin-top:0}
.enrollment-step{min-width:0;flex:1}
.enrollment-qr{margin:16px 0;text-align:center}
.enrollment-qr img{display:block;max-width:100%;height:auto;margin:auto;border:1px solid #e2e8f0;border-radius:8px}
.manual-enrollment{margin-top:14px;font-size:12px;line-height:1.8}
.manual-enrollment summary{cursor:pointer;color:#087b7b;font-weight:500}
.manual-enrollment .secret-display{margin:12px 0}
.security-grid{grid-template-columns:minmax(0,1.25fr) minmax(0,1fr)}
@media(max-width:800px){.security-grid{grid-template-columns:minmax(0,1fr)}.mfa-optional-notice{align-items:stretch;flex-direction:column}.mfa-optional-notice .el-button{align-self:flex-start}}
</style>
