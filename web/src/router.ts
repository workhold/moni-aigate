import { createRouter, createWebHistory } from 'vue-router'
import { hasPlatform, isAdmin, loadMe, session } from './session'
import { captureIdentityProofBeforeHistory, discardUnclaimedIdentityProof } from './identity-proof'
import { isAnonymousLayout } from './public-layout'

captureIdentityProofBeforeHistory()

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/pricing', component: () => import('./views/PricingView.vue'), meta: { public: true, publicLayout: 'catalog', title: '模型与价格' } },
    { path: '/register', component: () => import('./views/IdentityRequestView.vue'), meta: { public: true, publicLayout: 'identity', identityPurpose: 'registration', title: '创建账号' } },
    { path: '/register/complete', component: () => import('./views/IdentityCompleteView.vue'), meta: { public: true, publicLayout: 'identity', identityPurpose: 'registration', title: '完成注册' } },
    { path: '/password/forgot', component: () => import('./views/IdentityRequestView.vue'), meta: { public: true, publicLayout: 'identity', identityPurpose: 'password_reset', title: '找回密码' } },
    { path: '/password/reset', component: () => import('./views/IdentityCompleteView.vue'), meta: { public: true, publicLayout: 'identity', identityPurpose: 'password_reset', title: '设置新密码' } },
    { path: '/login', component: () => import('./views/LoginView.vue'), meta: { public: true, title: '登录' } },
    { path: '/', component: () => import('./views/OverviewView.vue'), meta: { title: '工作台' } },
    { path: '/keys', component: () => import('./views/KeysView.vue'), meta: { title: 'API 密钥', tenant: true } },
    { path: '/models', component: () => import('./views/ModelsView.vue'), meta: { title: '模型与价格', tenant: true } },
    { path: '/playground', component: () => import('./views/PlaygroundView.vue'), meta: { title: 'API 联调' } },
    { path: '/requests', component: () => import('./views/RequestsView.vue'), meta: { title: '调用记录', tenant: true } },
    { path: '/wallet', component: () => import('./views/WalletView.vue'), meta: { title: '钱包与流水', tenant: true } },
    { path: '/payments', component: () => import('./components/PaymentsPage.vue'), meta: { title: '充值订单', tenant: true } },
    { path: '/periodic-budget', component: () => import('./views/PeriodicBudgetView.vue'), meta: { title: '周期预算', tenant: true } },
    { path: '/reports', component: () => import('./views/ReportsView.vue'), meta: { title: '用量与资金报表', tenant: true } },
    { path: '/members', component: () => import('./views/MembersView.vue'), meta: { title: '成员与权限', tenant: true, owner: true } },
    { path: '/security', component: () => import('./views/SecurityView.vue'), meta: { title: '账号安全' } },
    { path: '/platform/reports', component: () => import('./views/ReportsView.vue'), meta: { title: '运营与成本报表', platform: true } },
    { path: '/platform/recovery', component: () => import('./views/RecoveryView.vue'), meta: { title: '恢复核对', platform: true } },
    { path: '/platform/procurement', component: () => import('./views/ProcurementView.vue'), meta: { title: '采购账户与对账', platform: true } },
    { path: '/platform/periodic-budget', component: () => import('./views/PeriodicBudgetView.vue'), meta: { title: '周期预算', platform: true } },
    { path: '/platform/vouchers', component: () => import('./views/VouchersView.vue'), meta: { title: '运营赠额券', platform: true } },
    { path: '/platform/payments', component: () => import('./components/PaymentsPage.vue'), meta: { title: '支付与商品', platform: true } },
    { path: '/platform/wallet-sources', component: () => import('./views/WalletSourcesView.vue'), meta: { title: '资金来源与核对', platform: true } },
    { path: '/platform/alerts', component: () => import('./views/AlertsView.vue'), meta: { title: '经营告警', platform: true } },
    { path: '/platform/price-groups', component: () => import('./views/PriceGroupsView.vue'), meta: { title: '价格组与租户定价', platform: true } },
    { path: '/platform/:resource', component: () => import('./views/PlatformView.vue'), meta: { title: '平台管理', platform: true } },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async to => {
  if (isAnonymousLayout(to.meta.publicLayout)) { document.title = `${String(to.meta.title)} · moni-aigate`; return true }
  const authenticated = await loadMe()
  if (!authenticated && !to.meta.public) return { path: '/login', query: { redirect: to.fullPath } }
  if (authenticated && session.enrollmentRequired && to.path !== '/security') return '/security'
  if (authenticated && to.path === '/login' && !session.mfaPending) return '/'
  if (authenticated && session.mfaPending && !session.enrollmentRequired && to.path !== '/login') return '/login'
  if (to.meta.tenant && !session.me?.tenant) return '/'
  if (to.meta.owner && session.me?.tenant?.role !== 'owner') return '/'
  if (to.meta.platform && !hasPlatform.value) return '/'
  if (['/platform/alerts', '/platform/periodic-budget', '/platform/payments', '/platform/wallet-sources'].includes(to.path) && !['admin','finance_operator','finance_approver'].includes(session.me?.user.platform_role || '')) return '/'
  if (to.path === '/payments' && !['owner','billing_viewer'].includes(session.me?.tenant?.role || '')) return '/'
  if (to.meta.platform && ['tenants', 'users', 'channels', 'catalog', 'prices', 'cost-versions'].includes(String(to.params.resource)) && !isAdmin.value) return '/'
  document.title = `${String(to.meta.title || '控制台')} · moni-aigate`
})
router.afterEach(to => { discardUnclaimedIdentityProof(to.path) })
