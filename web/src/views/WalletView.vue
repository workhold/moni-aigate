<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Refresh, Wallet, InfoFilled } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import MetricMoney from '../components/MetricMoney.vue'
import FundingPanel from '../components/FundingPanel.vue'
import WalletSources from '../components/WalletSources.vue'
import { errorText, request } from '../api'
import { label, money, shortId, time } from '../format'
import { canViewWallet, session } from '../session'
import { commerceContext, createCommerceScope } from '../commerce-state'

interface Wallet { available: string; held: string; total_credited?: string; total_charged?: string; total_refunded?: string }
interface Journal { id: string; kind: string; amount?: string; reference?: string; created_at: string; lines?: { account_kind: string; delta: string }[] }
const wallet = ref<Wallet | null>(null); const rows = ref<Journal[]>([]); const loading = ref(false); const error = ref(''); const cursor = ref(''); const cursorStack = ref<string[]>([])
const scope = createCommerceScope(), context = computed(() => commerceContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}:${session.me?.csrf_token}`))
const sources = ref<InstanceType<typeof WalletSources>>()
function journalAmount(row: Journal) {
  if (row.amount !== undefined) return row.amount
  const entries = row.lines || []
  const focus = ['settle', 'settlement', 'consume'].includes(row.kind) ? entries.find(line => line.account_kind === 'REVENUE') : entries.find(line => line.account_kind === 'AVAILABLE') || entries.find(line => line.account_kind === 'HELD')
  if (!focus) return undefined
  try { const amount = BigInt(focus.delta); return (amount < 0n ? -amount : amount).toString() } catch { return undefined }
}
async function load(page = '') {
  if (!canViewWallet.value) return
  const ticket = scope.ticket('wallet')
  loading.value = true; error.value = ''
  const results = await Promise.allSettled([request<Wallet>('/wallet', 'GET', undefined, undefined, () => scope.accepts(ticket)), request<Journal[]>(`/journals${page ? `?cursor=${encodeURIComponent(page)}` : ''}`, 'GET', undefined, undefined, () => scope.accepts(ticket))])
  if (!scope.accepts(ticket)) return
  if (results[0].status === 'fulfilled') wallet.value = results[0].value.data; else error.value = errorText(results[0].reason)
  if (results[1].status === 'fulfilled') { rows.value = results[1].value.data || []; cursor.value = results[1].value.next_cursor || '' } else error.value ||= errorText(results[1].reason)
  loading.value = false
}
function refreshWallet() { void load(); void sources.value?.refresh() }
async function next() { const page = cursor.value; cursorStack.value.push(page); await load(page) }
async function previous() { cursorStack.value.pop(); await load(cursorStack.value.at(-1) || '') }
watch(context, () => { scope.reset(context.value); wallet.value = null; rows.value = []; cursor.value = ''; cursorStack.value = []; error.value = ''; loading.value = false; if (canViewWallet.value) void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => scope.reset())
</script>
<template>
  <PageHeader eyebrow="WALLET & LEDGER" title="钱包与流水" description="可用余额、冻结费用与不可变业务流水，使用 CNY 统一计量。"><router-link v-if="canViewWallet" to="/reports"><el-button>查看累计报表</el-button></router-link><router-link v-if="canViewWallet" to="/periodic-budget"><el-button>周期预算</el-button></router-link><el-button :icon="Refresh" :loading="loading" @click="refreshWallet">刷新</el-button></PageHeader>
  <el-alert v-if="!canViewWallet" title="当前角色没有钱包查看权限。请联系租户所有者。" type="info" :closable="false" />
  <template v-else><ErrorNotice :message="error" @retry="load()" /><div v-loading="loading" class="metric-grid"><div class="metric-card accent"><div class="metric-label">可用余额<el-icon><Wallet /></el-icon></div><MetricMoney :value="wallet?.available" /><div class="metric-note">可用于新的付费调用</div></div><div class="metric-card"><div class="metric-label">当前冻结</div><MetricMoney :value="wallet?.held" /><div class="metric-note">进行中 / 等待费用核对</div></div><div class="metric-card"><div class="metric-label">累计钱包入账</div><MetricMoney :value="wallet?.total_credited" /><div class="metric-note">含人工核款、赠额、本地模拟入账与非现金开账，不等于现金收入</div></div><div class="metric-card"><div class="metric-label">累计消费</div><MetricMoney :value="wallet?.total_charged" /><div class="metric-note">退款另记补偿流水</div></div></div><section class="panel"><div class="panel-header"><div><div class="panel-title">业务流水</div><div class="panel-subtitle">入账、冻结、消费、释放与退款均关联独立业务记录。</div></div></div><el-table v-loading="loading" :data="rows" empty-text="暂无钱包流水"><el-table-column label="流水 ID" min-width="170"><template #default="{ row }"><span class="mono" :title="row.id">{{ shortId(row.id) }}</span></template></el-table-column><el-table-column label="类型" min-width="140"><template #default="{ row }">{{ label(row.kind) }}</template></el-table-column><el-table-column label="业务金额" min-width="160"><template #default="{ row }">{{ money(journalAmount(row)) }}</template></el-table-column><el-table-column label="关联业务" min-width="180"><template #default="{ row }"><span class="mono">{{ shortId(row.reference) }}</span></template></el-table-column><el-table-column label="时间" min-width="180"><template #default="{ row }">{{ time(row.created_at) }}</template></el-table-column></el-table><div class="table-footer"><span>当前页 {{ rows.length }} 条</span><div><el-button size="small" :disabled="!cursorStack.length || loading" @click="previous">上一页</el-button><el-button size="small" :disabled="!cursor || loading" @click="next">下一页</el-button></div></div></section><p class="section-note"><el-icon><InfoFilled /></el-icon>余额由同步资金账本提供。可通过人工核款或已开启的运营赠额券入账；初始余额导入为非现金开账；消费退款退回平台钱包。当前未提供在线支付或自动原路现金退款。</p><WalletSources ref="sources" /><FundingPanel @changed="refreshWallet" /></template>
</template>
