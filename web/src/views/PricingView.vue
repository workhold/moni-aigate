<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Search, Refresh, CopyDocument, ArrowRight, Cpu } from '@element-plus/icons-vue'
import { fetchPublicCatalog, publicFilters, publicProtocols } from '../public-catalog'
import { ratePerMillionText } from '../metering-wire'
import { createPublicCatalogPager } from '../public-catalog-state'
import { integer, time } from '../format'

const query = ref(''), selectedProtocol = ref(''), pager = createPublicCatalogPager(), state = ref(pager.snapshot())
let controller: AbortController | undefined
let filterTimer: ReturnType<typeof setTimeout> | undefined
function sync() { state.value = pager.snapshot() }
async function load(reset = true) {
  if (reset) { clearTimeout(filterTimer); controller?.abort() }
  let ticket
  try { ticket = pager.begin(publicFilters(query.value, selectedProtocol.value), reset) }
  catch (error) { pager.invalidate(error); sync(); return }
  if (!ticket) return
  const current = new AbortController(); controller = current; sync()
  try { const page = await fetchPublicCatalog(ticket.filters, ticket.cursor, current.signal); if (pager.accept(ticket, page)) sync() }
  catch (error) { if (pager.fail(ticket, error)) sync() }
}
function applyFilters() { void load(true) }
watch([query, selectedProtocol], () => { clearTimeout(filterTimer); controller?.abort(); pager.invalidate(); sync(); filterTimer = setTimeout(() => { void load(true) }, 350) }, { flush: 'sync' })
async function copy(value: string) {
  try { await navigator.clipboard.writeText(value); ElMessage.success('模型名称已复制。') }
  catch { ElMessage.warning('复制未完成，请选择模型名称手动复制。') }
}
onMounted(() => { void load() })
onBeforeUnmount(() => { clearTimeout(filterTimer); pager.dispose(); controller?.abort() })
</script>

<template>
  <div class="public-pricing">
    <section class="pricing-intro">
      <div class="eyebrow">MODELS &amp; RETAIL PRICING</div>
      <h1>看清模型能力，<br class="pricing-mobile-break" />再开始接入。</h1>
      <p>查看平台公开模型的协议、产品限制与公共默认售价，选择适合你的 AI API。账号实际价以当前租户价格域为准。</p>
      <div class="pricing-facts"><span><span class="status-dot" /> 公共默认价 · CNY / 百万计费 Tokens</span><span>价格由实际调用时的服务端冻结</span></div>
    </section>

    <section class="pricing-catalog" aria-label="公开模型目录">
      <form class="pricing-filter" @submit.prevent="applyFilters">
        <el-input v-model="query" :prefix-icon="Search" placeholder="按模型名称前缀搜索（区分大小写）" clearable aria-label="模型名称前缀" maxlength="100" />
        <el-select v-model="selectedProtocol" placeholder="全部协议" clearable aria-label="协议筛选"><el-option label="全部协议" value="" /><el-option v-for="item in publicProtocols" :key="item.id" :label="item.title" :value="item.id" /></el-select>
        <el-button :icon="Refresh" :loading="state.loading" @click="load(true)">刷新目录</el-button>
      </form>
      <div class="pricing-context"><span>已加载 {{ state.rows.length }} 个模型</span><span>当前售价观察；各模型保留本次读取时间</span></div>
      <el-alert v-if="state.error" class="pricing-error" :title="state.error" type="error" :closable="false" show-icon><el-button link type="primary" @click="load(!state.rows.length)">{{ state.rows.length ? '重试读取下一页' : '重新读取目录' }}</el-button></el-alert>
      <div v-if="state.loading && !state.rows.length" class="pricing-grid" aria-label="正在读取目录"><div v-for="index in 3" :key="index" class="pricing-card"><el-skeleton :rows="7" animated /></div></div>
      <el-empty v-else-if="state.loaded && !state.rows.length" :description="state.filters.q || state.filters.protocol ? '没有符合当前筛选的公开模型' : '暂时没有公开模型'" />
      <div v-if="state.rows.length" class="pricing-grid">
        <article v-for="row in state.rows" :key="row.model.id" class="pricing-card">
          <div class="pricing-card-top"><span class="pricing-model-icon"><el-icon><Cpu /></el-icon></span><el-tag size="small" effect="plain">{{ publicProtocols.find(item => item.id === row.model.protocol)?.title }}</el-tag></div>
          <div class="pricing-name"><h2>{{ row.model.public_name }}</h2><el-button text :icon="CopyDocument" :aria-label="`复制模型名称 ${row.model.public_name}`" @click="copy(row.model.public_name)" /></div>
          <div class="pricing-capabilities"><span>纯文本输入</span><span v-if="row.model.capabilities.streaming">SSE 流式</span><span v-if="row.model.capabilities.tools">客户端函数工具</span><span v-if="row.model.capabilities.input_only">仅输入计费</span></div>
          <dl class="pricing-limits"><dt>{{ row.model.capabilities.input_only ? '整请求输入容量' : '上下文窗口' }}</dt><dd>{{ integer(row.model.limits.context_tokens) }} Tokens</dd><dt>计费输入硬上界</dt><dd>{{ integer(row.model.limits.input_token_upper_bound) }} Tokens</dd><template v-if="!row.model.capabilities.input_only"><dt>最大 / 默认输出</dt><dd>{{ integer(row.model.limits.max_output_tokens) }} / {{ integer(row.model.limits.default_output_tokens) }}</dd></template></dl>
          <div v-if="row.model.pricing.status === 'available'" class="pricing-rates"><div><span>输入 / 百万 Tokens</span><strong>{{ ratePerMillionText(row.model.pricing.rates, 'text_input_token') }}</strong></div><div><span>输出 / 百万 Tokens</span><strong>{{ ratePerMillionText(row.model.pricing.rates, 'text_output_token') }}</strong><small v-if="row.model.capabilities.input_only">固定为0，仅输入计费</small></div></div>
          <div v-else class="pricing-unavailable"><strong>暂无有效售价</strong><span>不能按免费模型处理，请稍后查看。</span></div>
          <div class="pricing-observed"><span :title="row.as_of">观察于 {{ time(row.as_of) }}</span><span v-if="row.model.pricing.effective_at" :title="row.model.pricing.effective_at">价格生效 {{ time(row.model.pricing.effective_at) }}</span></div>
        </article>
      </div>
      <div class="pricing-pagination"><el-button v-if="state.next_cursor" :loading="state.loading" :disabled="state.loading" @click="load(false)">加载更多<el-icon class="el-icon--right"><ArrowRight /></el-icon></el-button><span v-else-if="state.loaded && state.rows.length">已加载当前筛选下的所有可见结果</span><span v-if="state.loaded && !state.rows.length && state.last_as_of" :title="state.last_as_of">本次观察 {{ time(state.last_as_of) }}</span></div>
    </section>
    <aside class="pricing-disclaimer"><strong>公开展示与调用权限分开管理</strong><p>公开模型需要账号、模型授权、API Key 与足够余额才可调用。目录不锁定价格，也不保证供应商实时可用；新增公开模型可能需刷新后出现。具体费用以实际请求冻结的售价与已结算账单为准。</p></aside>
  </div>
</template>

<style scoped>
.public-pricing{max-width:1180px;margin:auto;padding:52px 30px 40px}.pricing-intro{padding:16px 0 36px}.pricing-intro h1{font-size:38px;letter-spacing:-1.3px;line-height:1.45;margin:16px 0;color:#18384c}.pricing-intro>p{font-size:13px;line-height:1.9;color:#7b8e9f;max-width:650px}.pricing-facts{display:flex;gap:22px;flex-wrap:wrap;margin-top:22px;font-size:11px;color:#5b7d88}.pricing-facts>span{display:flex;align-items:center;gap:7px}.pricing-mobile-break{display:none}.pricing-filter{display:flex;gap:12px;align-items:center;padding:20px;background:#fff;border:1px solid #e1e9ee;border-radius:10px}.pricing-filter .el-input{flex:1;min-width:0}.pricing-filter .el-select{width:220px}.pricing-context{display:flex;justify-content:space-between;gap:12px;padding:18px 2px;color:#8496a5;font-size:11px}.pricing-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:18px}.pricing-card{background:#fff;border:1px solid #e0e8ee;border-radius:11px;padding:22px;min-width:0;display:flex;flex-direction:column}.pricing-card-top{display:flex;justify-content:space-between;align-items:center;gap:8px}.pricing-card-top .el-tag{font-size:10px;max-width:100%;white-space:normal;height:auto;min-height:24px;line-height:1.5}.pricing-model-icon{display:flex;width:35px;height:35px;border-radius:10px;background:#eaf5f3;align-items:center;justify-content:center;color:#13887e;flex-shrink:0}.pricing-name{display:flex;align-items:flex-start;gap:7px;margin-top:18px}.pricing-name h2{font-size:17px;line-height:1.6;letter-spacing:-.2px;overflow-wrap:anywhere;flex:1;min-width:0}.pricing-name .el-button{flex-shrink:0;padding:6px;height:30px}.pricing-capabilities{display:flex;gap:7px;flex-wrap:wrap;font-size:10px;color:#6f8c96;margin-top:12px;min-height:24px}.pricing-capabilities span{background:#f1f7f7;border-radius:4px;padding:4px 6px}.pricing-limits{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:13px 8px;margin:22px 0;color:#8a99a7;font-size:11px}.pricing-limits dd{color:#4a6576;margin:0;text-align:right;overflow-wrap:anywhere}.pricing-rates{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:13px;background:#f5f9fa;border-radius:7px;padding:15px;margin-top:auto}.pricing-rates span{display:block;font-size:10px;color:#8a9ba7;margin-bottom:8px}.pricing-rates strong{font-size:14px;color:#167f78;font-weight:600;overflow-wrap:anywhere;line-height:1.7}.pricing-rates small{display:block;color:#7c959c;font-size:9px;margin-top:4px}.pricing-unavailable{display:flex;flex-direction:column;gap:9px;background:#faf7ef;border-radius:7px;padding:16px;margin-top:auto;min-height:76px}.pricing-unavailable strong{font-size:13px;color:#9a7c40}.pricing-unavailable span{font-size:10px;color:#a39477}.pricing-observed{display:flex;flex-direction:column;gap:5px;font-size:9px;color:#96a4af;line-height:1.7;margin-top:14px}.pricing-pagination{display:flex;justify-content:center;align-items:center;gap:10px;flex-wrap:wrap;min-height:65px;font-size:11px;color:#8c9da9}.pricing-error{margin-bottom:18px}.pricing-disclaimer{border-top:1px solid #e1e8ed;margin-top:14px;padding:24px 0;color:#8497a5;font-size:11px;line-height:1.9}.pricing-disclaimer strong{font-weight:500;color:#5f7c8b}.pricing-disclaimer p{margin-top:7px;max-width:850px}
@media(max-width:1050px){.pricing-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:650px){.public-pricing{padding:30px 20px}.pricing-intro{padding:8px 0 26px}.pricing-intro h1{font-size:29px;letter-spacing:-.8px}.pricing-mobile-break{display:block}.pricing-facts{gap:10px}.pricing-filter{flex-wrap:wrap;padding:15px;gap:10px}.pricing-filter .el-input{flex:1 0 100%}.pricing-filter .el-select{flex:1;width:auto;min-width:0}.pricing-filter>.el-button{margin:0}.pricing-grid{grid-template-columns:minmax(0,1fr)}.pricing-context{flex-direction:column;gap:7px}.pricing-card{padding:20px}.pricing-disclaimer{margin-top:4px}.pricing-rates strong{font-size:14px}}
</style>
