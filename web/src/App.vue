<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Grid, Key, Cpu, Connection, Wallet, Setting, OfficeBuilding, Switch, ArrowRight, Menu, Refresh, Lock, Money, Tickets, Document, User, PriceTag, CircleCheck, DataAnalysis, Close } from '@element-plus/icons-vue'
import { clearSession, hasPlatform, isAdmin, isFinance, loadMe, session } from './session'
import { errorText, request } from './api'
import { label } from './format'
import { isAnonymousLayout } from './public-layout'
import { groupPlatformLinks, parseNavigationGroupState, platformGroupForPath, serializeNavigationGroupState } from './navigation'

const route = useRoute(); const router = useRouter()
const mobileOpen = ref(false); const switching = ref(false)
const securityOpen = ref(false); const securityBusy = ref(false); const securityError = ref('')
const reauth = ref({ password: '', code: '' })
const links = computed(() => [
  { path: '/', title: '工作台', icon: Grid, show: true },
  { path: '/models', title: '模型与价格', icon: Cpu, show: !!session.me?.tenant },
  { path: '/keys', title: 'API 密钥', icon: Key, show: !!session.me?.tenant },
  { path: '/playground', title: 'API 联调', icon: Connection, show: !!session.me },
  { path: '/requests', title: '调用记录', icon: Connection, show: !!session.me?.tenant },
  { path: '/wallet', title: '钱包与流水', icon: Wallet, show: ['owner', 'billing_viewer'].includes(session.me?.tenant?.role || '') },
  { path: '/payments', title: '充值订单', icon: Money, show: ['owner', 'billing_viewer'].includes(session.me?.tenant?.role || '') },
  { path: '/periodic-budget', title: '周期预算', icon: CircleCheck, show: ['owner', 'billing_viewer'].includes(session.me?.tenant?.role || '') },
  { path: '/reports', title: '用量与资金报表', icon: DataAnalysis, show: ['owner', 'billing_viewer'].includes(session.me?.tenant?.role || '') },
  { path: '/members', title: '成员与权限', icon: User, show: session.me?.tenant?.role === 'owner' },
].filter(item => item.show))
const platformLinks = computed(() => [
  { path: '/platform/tenants', title: '客户与成员', icon: OfficeBuilding, show: isAdmin.value },
  { path: '/platform/users', title: '平台账号', icon: User, show: isAdmin.value },
  { path: '/platform/channels', title: '上游渠道', icon: Switch, show: isAdmin.value },
  { path: '/platform/catalog', title: '模型与路由', icon: Cpu, show: isAdmin.value },
  { path: '/platform/prices', title: '售价版本', icon: PriceTag, show: isAdmin.value },
  { path: '/platform/price-groups', title: '价格组与租户定价', icon: PriceTag, show: ['admin', 'finance_operator', 'finance_approver'].includes(session.me?.user.platform_role || '') },
  { path: '/platform/cost-versions', title: '渠道成本版本', icon: PriceTag, show: isAdmin.value },
  { path: '/platform/procurement', title: '采购账户与对账', icon: Wallet, show: hasPlatform.value },
  { path: '/platform/periodic-budget', title: '周期预算', icon: CircleCheck, show: ['admin', 'finance_operator', 'finance_approver'].includes(session.me?.user.platform_role || '') },
  { path: '/platform/vouchers', title: '运营赠额券', icon: Tickets, show: hasPlatform.value },
  { path: '/platform/payments', title: '支付与商品', icon: Money, show: ['admin', 'finance_operator', 'finance_approver'].includes(session.me?.user.platform_role || '') },
  { path: '/platform/wallet-sources', title: '资金来源与核对', icon: Wallet, show: ['admin', 'finance_operator', 'finance_approver'].includes(session.me?.user.platform_role || '') },
  { path: '/platform/wallet-requests', title: '客户钱包申请', icon: Document, show: ['admin', 'finance_operator', 'finance_approver'].includes(session.me?.user.platform_role || '') },
  { path: '/platform/cash-refunds', title: '现金退款', icon: Money, show: ['admin', 'finance_operator', 'finance_approver'].includes(session.me?.user.platform_role || '') },
  { path: '/platform/alerts', title: '经营告警', icon: CircleCheck, show: ['admin', 'finance_operator', 'finance_approver'].includes(session.me?.user.platform_role || '') },
  { path: '/platform/reports', title: '运营与成本报表', icon: DataAnalysis, show: hasPlatform.value },
  { path: '/platform/credits', title: '人工核款', icon: Money, show: hasPlatform.value },
  { path: '/platform/refunds', title: '消费退款', icon: Tickets, show: hasPlatform.value },
  { path: '/platform/reconciliations', title: '收费核对', icon: CircleCheck, show: isFinance.value },
  { path: '/platform/recovery', title: '恢复核对', icon: Lock, show: hasPlatform.value },
  { path: '/platform/audit', title: '操作审计', icon: Document, show: hasPlatform.value },
].filter(item => item.show))
const currentTitle = computed(() => [...links.value, ...platformLinks.value].find(item => item.path === route.path)?.title || String(route.meta.title || '控制台'))
const platformGroups = computed(() => groupPlatformLinks(platformLinks.value))
const currentPlatformGroup = computed(() => {
  const id = platformGroupForPath(route.path)
  return platformGroups.value.some(group => group.id === id) ? id : ''
})
let storedGroups: Record<string, boolean> = {}
try { storedGroups = parseNavigationGroupState(sessionStorage.getItem('moni:platform-navigation:v1')) } catch { storedGroups = {} }
const expandedGroups = ref<Record<string, boolean>>(storedGroups)
const mobileViewport = ref(false)
let viewportMedia: MediaQueryList | null = null
let returnFocus: HTMLElement | null = null
let previousBodyOverflow = ''
function expandCurrentGroup() {
  if (!currentPlatformGroup.value) return
  expandedGroups.value = { ...expandedGroups.value, [currentPlatformGroup.value]: true }
}
function scrollCurrentNav() {
  void nextTick(() => {
    const current = document.querySelector('.sidebar .nav-link.selected')
    if (current) current.scrollIntoView({ block: 'nearest', behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })
  })
}
function togglePlatformGroup(id: string) {
  expandedGroups.value = { ...expandedGroups.value, [id]: !expandedGroups.value[id] }
  try { sessionStorage.setItem('moni:platform-navigation:v1', serializeNavigationGroupState(expandedGroups.value)) } catch { /* storage is an optional UI preference */ }
}
function setMobileOpen(open: boolean) {
  if (open) { expandCurrentGroup(); scrollCurrentNav() }
  mobileOpen.value = open
}
function sidebarKeys(event: KeyboardEvent) {
  if (!mobileOpen.value || !mobileViewport.value) return
  if (event.key === 'Escape') { event.preventDefault(); setMobileOpen(false); return }
  if (event.key !== 'Tab') return
  const sidebar = document.getElementById('app-sidebar')
  if (!sidebar) return
  const focusable = [...sidebar.querySelectorAll<HTMLElement>('a[href],button:not([disabled]),input,select,textarea,[tabindex]:not([tabindex="-1"])')].filter(element => element.getClientRects().length)
  if (!focusable.length) return
  const first = focusable[0], last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}
watch([() => route.path, currentPlatformGroup], () => { expandCurrentGroup(); scrollCurrentNav() }, { immediate: true })
watch(mobileOpen, open => {
  if (open) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    previousBodyOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    void nextTick(() => document.querySelector<HTMLElement>('.sidebar-close')?.focus())
  } else {
    document.body.style.overflow = previousBodyOverflow
    returnFocus?.focus()
    returnFocus = null
  }
})
onMounted(() => {
  viewportMedia = window.matchMedia('(max-width: 800px)')
  const update = () => { mobileViewport.value = viewportMedia!.matches; if (!mobileViewport.value) mobileOpen.value = false }
  update()
  viewportMedia.addEventListener('change', update)
  document.addEventListener('keydown', sidebarKeys)
  onBeforeUnmount(() => { viewportMedia?.removeEventListener('change', update) })
})

async function switchTenant(id: string) {
  switching.value = true
  try { await request('/tenant/switch', 'POST', { tenant_id: id }); await loadMe(true); await router.replace('/'); window.location.reload() }
  catch (error) { ElMessage.error(errorText(error)) }
  finally { switching.value = false }
}
async function logout() {
  try { await request('/auth/logout', 'POST', {}); clearSession(); await router.replace('/login') }
  catch (error) { ElMessage.error(errorText(error)) }
}
function openReauth() { if (isAnonymousLayout(route.meta.publicLayout)) return; securityError.value = ''; reauth.value = { password: '', code: '' }; securityOpen.value = true }
async function submitReauth() {
  securityBusy.value = true; securityError.value = ''
  try { await request('/auth/reauth', 'POST', reauth.value); await loadMe(true); securityOpen.value = false; reauth.value = { password: '', code: '' }; ElMessage.success('再认证成功，请重试刚才的操作。') }
  catch (error) { securityError.value = errorText(error) }
  finally { securityBusy.value = false }
}
function unauthenticated() { if (isAnonymousLayout(route.meta.publicLayout)) return; clearSession(); if (route.path !== '/login') void router.replace('/login') }
function publicLogin() { if (route.meta.publicLayout === 'identity') clearSession() }
watch(() => route.meta.publicLayout, value => { if (isAnonymousLayout(value)) { securityOpen.value = false; mobileOpen.value = false; reauth.value = { password: '', code: '' } } })
window.addEventListener('moni:reauth', openReauth)
window.addEventListener('moni:unauthenticated', unauthenticated)
onBeforeUnmount(() => { window.removeEventListener('moni:reauth', openReauth); window.removeEventListener('moni:unauthenticated', unauthenticated); document.removeEventListener('keydown', sidebarKeys); document.body.style.overflow = previousBodyOverflow })
</script>

<template>
  <div v-if="isAnonymousLayout(route.meta.publicLayout)" class="public-shell">
    <header class="public-header"><router-link to="/pricing" class="brand"><span class="brand-mark">m<span>·</span></span><span>moni<span class="brand-suffix">aigate</span></span></router-link><nav aria-label="公开导航"><router-link to="/pricing">模型与价格</router-link><router-link to="/login" class="public-login" @click="publicLogin">登录控制台<el-icon><ArrowRight /></el-icon></router-link></nav></header>
    <main><router-view :key="route.path" /></main><footer class="public-footer">moni-aigate · 统一 AI 接入与商业运营</footer>
  </div>
  <div v-else-if="route.path === '/login'" class="login-root"><router-view /></div>
  <div v-else class="app-shell">
    <div v-if="mobileOpen" class="sidebar-backdrop" @click="setMobileOpen(false)" />
    <aside id="app-sidebar" class="sidebar" :class="{ 'is-mobile-open': mobileOpen }" :inert="mobileViewport && !mobileOpen">
      <div class="sidebar-head"><router-link to="/" class="brand" @click="setMobileOpen(false)"><span class="brand-mark">m<span>·</span></span><span>moni<span class="brand-suffix">aigate</span></span><span class="version">0.1</span></router-link><button type="button" class="sidebar-close" aria-label="关闭导航" @click="setMobileOpen(false)"><el-icon><Close /></el-icon></button></div>
      <div class="workspace-caption">AI API OPERATIONS</div>
      <nav class="sidebar-nav" aria-label="主导航">
        <div class="nav-heading">工作空间</div>
        <router-link v-for="item in links" :key="item.path" :to="item.path" class="nav-link" :class="{ selected: route.path === item.path }" @click="setMobileOpen(false)"><el-icon><component :is="item.icon" /></el-icon><span>{{ item.title }}</span><span v-if="route.path === item.path" class="nav-active-dot" /></router-link>
        <template v-if="platformGroups.length"><div class="nav-heading platform-heading">平台运营</div><div v-for="group in platformGroups" :key="group.id" class="nav-group"><button type="button" class="nav-group-title" :aria-expanded="!!expandedGroups[group.id]" :aria-controls="`platform-group-${group.id}`" @click="togglePlatformGroup(group.id)"><span>{{ group.title }}</span><el-icon class="nav-group-chevron" :class="{ expanded: !!expandedGroups[group.id] }"><ArrowRight /></el-icon></button><el-collapse-transition><div v-show="expandedGroups[group.id]" :id="`platform-group-${group.id}`" class="nav-group-links"><router-link v-for="item in group.items" :key="item.path" :to="item.path" class="nav-link" :class="{ selected: route.path === item.path }" @click="setMobileOpen(false)"><el-icon><component :is="item.icon" /></el-icon><span>{{ item.title }}</span><span v-if="route.path === item.path" class="nav-active-dot" /></router-link></div></el-collapse-transition></div></template>
        <div class="nav-heading platform-heading">设置</div>
        <router-link to="/security" class="nav-link" :class="{ selected: route.path === '/security' }" @click="setMobileOpen(false)"><el-icon><Setting /></el-icon><span>账号安全</span><span v-if="route.path === '/security'" class="nav-active-dot" /></router-link>
      </nav>
      <div class="sidebar-bottom"><span class="status-dot" /><div><strong>可控试运营</strong><span>文本多协议 · 单实例</span></div><el-icon><Lock /></el-icon></div>
    </aside>

    <section class="main-column">
      <header class="topbar">
        <div class="breadcrumbs"><el-button class="mobile-menu" text :icon="Menu" aria-label="打开导航" :aria-expanded="mobileOpen" aria-controls="app-sidebar" @click="setMobileOpen(true)" /><span class="breadcrumb-root">控制台</span><el-icon><ArrowRight /></el-icon><strong>{{ currentTitle }}</strong></div>
        <div class="topbar-right">
          <el-select v-if="session.me?.tenants?.length" :model-value="session.me?.tenant?.id" :disabled="switching" placeholder="选择工作空间" class="tenant-switch" @change="switchTenant"><el-option v-for="tenant in session.me?.tenants || []" :key="tenant.id" :label="tenant.name" :value="tenant.id" /></el-select>
          <el-dropdown trigger="click"><button class="user-button"><span class="avatar">{{ session.me?.user.email.slice(0, 1).toUpperCase() }}</span><span class="user-email">{{ session.me?.user.email }}</span></button><template #dropdown><el-dropdown-menu><el-dropdown-item disabled>{{ label(session.me?.user.platform_role || session.me?.tenant?.role) }}</el-dropdown-item><el-dropdown-item :icon="Lock" @click="openReauth">敏感操作再认证</el-dropdown-item><el-dropdown-item :icon="Setting" @click="router.push('/security')">账号安全</el-dropdown-item><el-dropdown-item divided :icon="Switch" @click="logout">退出登录</el-dropdown-item></el-dropdown-menu></template></el-dropdown>
        </div>
      </header>
      <main class="content"><router-view :key="route.path" /></main>
      <footer class="app-footer"><span>moni-aigate · 统一 AI 接入与商业运营</span><span>费用以已结算账单为准</span></footer>
    </section>

    <el-dialog v-model="securityOpen" title="敏感操作再认证" width="440px" destroy-on-close :close-on-click-modal="false" @close="reauth = { password: '', code: '' }">
      <p class="dialog-description">输入账号密码与已启用的 MFA 验证码，确认当前操作身份。</p>
      <el-alert v-if="securityError" type="error" :closable="false" :title="securityError" />
      <el-form label-position="top" @submit.prevent="submitReauth"><el-form-item label="账号密码"><el-input v-model="reauth.password" type="password" show-password autocomplete="current-password" /></el-form-item><el-form-item v-if="session.me?.user.mfa_enabled" label="MFA 验证码"><el-input v-model="reauth.code" maxlength="16" autocomplete="one-time-code" @keyup.enter="submitReauth" /></el-form-item></el-form>
      <template #footer><el-button @click="securityOpen = false">取消</el-button><el-button type="primary" :loading="securityBusy" :disabled="!reauth.password" :icon="Refresh" @click="submitReauth">验证身份</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.public-shell{min-height:100vh;background:#f6f9fb}.public-header{height:78px;max-width:1180px;margin:auto;padding:0 30px;display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid #e4ebef;gap:20px}.public-header .brand{color:#284e60;margin:0;gap:10px}.public-header .brand-mark{background:#167e79;color:#fff}.public-header .brand-suffix{color:#79929e}.public-header nav{display:flex;gap:27px;align-items:center;font-size:11px;color:#8699a6}.public-header nav>a:not(.public-login){color:inherit;text-decoration:none}.public-login{display:flex;align-items:center;gap:8px;color:#187e79;text-decoration:none;font-weight:500}.public-footer{max-width:1120px;margin:auto;padding:23px 30px 28px;color:#99a7b1;font-size:10px;border-top:1px solid #e4ebef}@media(max-width:650px){.public-header{height:69px;padding:0 20px;gap:12px}.public-header .brand{font-size:17px;gap:8px}.public-header nav{gap:10px}.public-header nav>a:not(.public-login){display:none}.public-login{font-size:11px}.public-footer{padding:20px;font-size:9px}}
</style>
