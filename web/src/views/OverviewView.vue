<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Key, Refresh, Right, Connection, CopyDocument, OfficeBuilding, Lock } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import MetricMoney from '../components/MetricMoney.vue'
import { errorText, request } from '../api'
import { canViewWallet, hasPlatform, isAdmin, isFinance, session } from '../session'
import { label, coin, shortId, tagType, time } from '../format'

const router = useRouter(); const loading = ref(false); const error = ref('')
const wallet = ref<{ available: string; held: string } | null>(null); const keys = ref<{ id: string; revoked_at?: string }[]>([])
const requests = ref<{ id: string; model_name?: string; model_id: string; status: string; billing_status: string; charged_amount?: string; created_at?: string; accepted_at?: string }[]>([])
const origin = window.location.origin
const example = `curl ${origin}/v1/chat/completions \\\n  -H "Authorization: Bearer $MONI_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"YOUR_MODEL","messages":[{"role":"user","content":"你好"}],"max_tokens":128}'`
async function load() {
  if (!session.me?.tenant) return
  loading.value = true; error.value = ''
  const tasks = [request<typeof keys.value>('/keys'), request<typeof requests.value>('/requests')]
  if (canViewWallet.value) tasks.push(request('/wallet') as never)
  const results = await Promise.allSettled(tasks)
  if (results[0]?.status === 'fulfilled') keys.value = results[0].value.data as typeof keys.value; else if (results[0]?.status === 'rejected') error.value = errorText(results[0].reason)
  if (results[1]?.status === 'fulfilled') requests.value = results[1].value.data as typeof requests.value; else if (results[1]?.status === 'rejected') error.value ||= errorText(results[1].reason)
  if (results[2]?.status === 'fulfilled') wallet.value = results[2].value.data as unknown as typeof wallet.value; else if (results[2]?.status === 'rejected') error.value ||= errorText(results[2].reason)
  loading.value = false
}
async function copy() { try { await navigator.clipboard.writeText(example); ElMessage.success('调用示例已复制。请替换模型名称并设置服务端密钥。') } catch { ElMessage.warning('无法复制，请手动选择示例。') } }
onMounted(load)
</script>
<template>
  <PageHeader eyebrow="YOUR AI OPERATIONS, AT A GLANCE" :title="session.me?.tenant ? `${session.me.tenant.name} · 工作台` : '平台工作台'" description="一个入口连接模型调用、访问控制与每一笔费用。"><el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button><el-button v-if="session.me?.tenant" type="primary" :icon="Key" @click="router.push('/keys')">管理 API 密钥</el-button></PageHeader>
  <ErrorNotice :message="error" @retry="load" />
  <template v-if="session.me?.tenant"><div v-loading="loading" class="metric-grid"><div class="metric-card accent"><div class="metric-label">可用余额<el-icon><Connection /></el-icon></div><MetricMoney unit="COIN" :value="wallet?.available" :restricted="!canViewWallet" /><div class="metric-note">已扣除当前冻结费用</div></div><div class="metric-card"><div class="metric-label">冻结费用</div><MetricMoney unit="COIN" :value="wallet?.held" :restricted="!canViewWallet" /><div class="metric-note">结算后释放未使用部分</div></div><div class="metric-card"><div class="metric-label">有效 API 密钥</div><div class="metric-value">{{ keys.filter(k => !k.revoked_at).length }}<span style="font-size:12px;font-weight:400;letter-spacing:0;margin-left:7px;color:#91a0b1">个</span></div><div class="metric-note">不含已撤销密钥</div></div><div class="metric-card"><div class="metric-label">当前页待核对</div><div class="metric-value">{{ requests.filter(r => r.billing_status === 'pending_reconciliation').length }}<span style="font-size:12px;font-weight:400;letter-spacing:0;margin-left:7px;color:#91a0b1">笔</span></div><div class="metric-note">基于最近一页调用记录</div></div></div>
    <div class="split-grid"><section class="panel"><div class="panel-header"><div><div class="panel-title">最近调用</div><div class="panel-subtitle">查看请求结果与费用状态</div></div><el-button link type="primary" @click="router.push('/requests')">全部记录<el-icon><Right /></el-icon></el-button></div><el-table :data="requests.slice(0, 5)" empty-text="还没有调用记录，使用下方示例开始第一次调用。"><el-table-column label="请求 / 模型" min-width="180"><template #default="{ row }"><span class="mono">{{ shortId(row.id) }}</span><div class="muted small-text" style="margin-top:5px">{{ row.model_name || row.model_id }}</div></template></el-table-column><el-table-column label="收费状态" width="106"><template #default="{ row }"><el-tag :type="tagType(row.billing_status)">{{ label(row.billing_status) }}</el-tag></template></el-table-column><el-table-column label="实际收费" width="126"><template #default="{ row }">{{ row.billing_status === 'pending_reconciliation' ? '待核对' : coin(row.charged_amount) }}</template></el-table-column><el-table-column label="时间" min-width="160"><template #default="{ row }">{{ time(row.created_at || row.accepted_at) }}</template></el-table-column></el-table></section>
    <section class="panel"><div class="panel-header"><div class="panel-title">快速开始</div><el-tag type="info">Chat · Responses · Messages</el-tag></div><div class="panel-body" style="padding-top:6px;padding-bottom:8px"><div class="instruction-step"><span class="step-number">01</span><div><h3>选择获授权模型</h3><p>从模型目录复制统一模型名称，确认输出上限与售价。</p></div></div><div class="instruction-step"><span class="step-number">02</span><div><h3>创建受限 API 密钥</h3><p>为应用指定模型范围与累计额度，安全保存在服务端。</p></div></div><div class="instruction-step"><span class="step-number">03</span><div><h3>调用并核对账单</h3><p>通过 request_id 关联请求、用量和资金状态。</p></div></div></div></section></div>
    <section class="panel"><div class="panel-header"><div><div class="panel-title">发起第一次调用</div><div class="panel-subtitle">先设置 MONI_API_KEY 环境变量，并替换 YOUR_MODEL。示例会产生真实调用费用。</div></div><el-button :icon="CopyDocument" @click="copy">复制示例</el-button></div><div class="panel-body"><pre class="code-block">{{ example }}</pre></div></section>
  </template>
  <section v-else-if="hasPlatform" class="panel"><div class="panel-header"><div><div class="panel-title">运营准备清单</div><div class="panel-subtitle">真实数据将由业务操作产生。平台角色与客户工作空间分别管理。</div></div><el-tag>平台账号</el-tag></div><div class="panel-body"><div class="instruction-step"><span class="step-number">01</span><div><h3>确认账号安全与职责</h3><p>管理员与财务分别授权；双人控制或 admin 单人运营例外以当前钱包设置为准，自批会单独记录。</p><el-button link type="primary" @click="router.push('/security')">检查账号安全<el-icon><Right /></el-icon></el-button></div></div><div v-if="isAdmin" class="instruction-step"><span class="step-number">02</span><div><h3>接入渠道，发布模型与价格</h3><p>仅开放已验证用量与输出上界的模型。渠道密钥只输入、不回显。</p><el-button link type="primary" @click="router.push('/platform/channels')">配置上游渠道<el-icon><Right /></el-icon></el-button></div></div><div v-if="isAdmin" class="instruction-step"><span class="step-number">03</span><div><h3>创建客户并明确模型授权</h3><p>空模型授权默认拒绝。客户创建密钥后按钱包余额调用。</p><el-button link type="primary" @click="router.push('/platform/tenants')"><el-icon><OfficeBuilding /></el-icon>管理客户</el-button></div></div><div v-if="isFinance" class="instruction-step"><span class="step-number">02</span><div><h3>核实真实收款并提交入账单</h3><p>人工核款经授权账号审批后，客户钱包才增加可用余额；自批例外会单独留痕。</p><el-button link type="primary" @click="router.push('/platform/credits')">进入人工核款<el-icon><Right /></el-icon></el-button></div></div></div></section>
  <section v-else class="panel empty-state"><div class="empty-icon"><el-icon><Lock /></el-icon></div><h3>尚未分配工作空间</h3><p>请联系平台管理员确认账号所属租户与成员权限。</p></section>
</template>
