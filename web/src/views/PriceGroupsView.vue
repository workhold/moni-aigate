<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { errorText } from '../api'
import { session } from '../session'
import PageHeader from '../components/PageHeader.vue'
import PricingCommands from '../components/PricingCommands.vue'
import PricingWriter from '../components/PricingWriter.vue'
import TenantPricing from '../components/TenantPricing.vue'
import GroupMultiplierCompiler from '../components/GroupMultiplierCompiler.vue'
import { parseGroupPrice, parsePriceGroup, parsePricingEnvelope, parsePricingPage, pricingID, pricingQuery, pricingRequest } from '../pricing'
import type { GroupPrice, PriceGroup, PricingKind } from '../pricing'
import { createPricingEpoch, createPricingPager, pricingContext } from '../pricing-state'
const context = computed(() => pricingContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}`, session.me?.csrf_token || '', session.mfaPending))
const reader = computed(() => ['admin','finance_operator','finance_approver'].includes(session.me?.user.platform_role || '') && !session.mfaPending), admin = computed(() => session.me?.user.platform_role === 'admin' && !session.mfaPending)
const groups = createPricingPager<PriceGroup>(), history = createPricingPager<GroupPrice>(), groupState = ref(groups.snapshot()), priceState = ref(history.snapshot()), detailGate = createPricingEpoch()
const selected = ref<PriceGroup | null>(null), lookup = ref(''), detailBusy = ref(false), error = ref(''), open = ref(false), kind = ref<PricingKind>('price_group.create'), journalRevision = ref(0)
async function loadGroups(first = true) {
  if (!reader.value) return
  const ticket = groups.begin(first); if (!ticket) return
  groupState.value = groups.snapshot()
  try {
    const raw = await pricingRequest(pricingQuery('/platform/price-groups', ticket.cursor), 'GET', undefined, () => groups.accepts(ticket))
    if (groups.accept(ticket, parsePricingPage(raw, value => parsePriceGroup(value), row => row.id))) groupState.value = groups.snapshot()
  } catch (cause) { if (groups.fail(ticket, cause)) groupState.value = groups.snapshot() }
}
async function select(id: string) {
  if (!reader.value) return
  const ticket = detailGate.ticket(); detailBusy.value = true; error.value = ''; selected.value = null; history.reset(`${context.value}:${id}`); priceState.value = history.snapshot()
  try {
    const valid = pricingID(id), raw = await pricingRequest(`/platform/price-groups/${valid}`, 'GET', undefined, () => detailGate.accepts(ticket))
    if (!detailGate.accepts(ticket)) return
    selected.value = parsePricingEnvelope(raw, value => parsePriceGroup(value, valid)); lookup.value = valid; void loadPrices()
  } catch (cause) { if (detailGate.accepts(ticket)) error.value = errorText(cause) }
  finally { if (detailGate.accepts(ticket)) detailBusy.value = false }
}
async function loadPrices(first = true) {
  if (!selected.value || !reader.value) return
  const group = selected.value.id, ticket = history.begin(first); if (!ticket) return
  priceState.value = history.snapshot()
  try {
    const raw = await pricingRequest(pricingQuery(`/platform/price-groups/${group}/price-versions`, ticket.cursor), 'GET', undefined, () => history.accepts(ticket))
    if (history.accept(ticket, parsePricingPage(raw, value => parseGroupPrice(value, group), row => row.id))) priceState.value = history.snapshot()
  } catch (cause) { if (history.fail(ticket, cause)) priceState.value = history.snapshot() }
}
function write(action: PricingKind) { kind.value = action; open.value = true }
function closeWriter(value: boolean) { open.value = value; if (!value) { void loadGroups(); if (selected.value) void select(selected.value.id) } }
function reset() { groups.reset(context.value); history.reset(context.value); detailGate.reset(context.value); groupState.value = groups.snapshot(); priceState.value = history.snapshot(); selected.value = null; lookup.value = ''; detailBusy.value = false; error.value = ''; open.value = false; journalRevision.value++; void loadGroups() }
watch(context, reset, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { groups.dispose(); history.dispose(); detailGate.dispose() })
</script>
<template>
  <PageHeader eyebrow="RETAIL PRICE DOMAINS" title="价格组与租户定价" description="独立维护公共默认与单租户价格域。组内缺价不会回落默认，历史价格与已接受请求保持不可变。"><el-button :icon="Refresh" :loading="groupState.loading" @click="loadGroups()">刷新第一页</el-button><el-button v-if="admin" type="primary" :icon="Plus" @click="write('price_group.create')">创建价格组</el-button></PageHeader>
  <el-alert v-if="!reader" title="当前账号没有价格组读取权限。" type="error" :closable="false" />
  <template v-else>
    <p class="section-note">公共默认价继续由管理员在<router-link v-if="admin" to="/platform/prices">售价版本</router-link><span v-else>售价版本</span>维护；该旧发布入口仅写默认域。财务角色可读本页，写操作需要当前管理员与近期再认证。</p>
    <section class="panel"><div class="panel-header"><div><div class="panel-title">价格组</div><div class="panel-subtitle">第 {{ groupState.page || 1 }} 页 · 每页最多 50 条 · 退休组仍保留已有租户的生效计划。</div></div></div><el-alert v-if="groupState.message" :title="groupState.message" type="error" :closable="false" /><el-table v-loading="groupState.loading" :data="groupState.rows" empty-text="暂无价格组"><el-table-column prop="label" label="名称" min-width="160" /><el-table-column prop="id" label="组 ID" min-width="290" /><el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.state === 'active' ? 'success' : 'info'">{{ row.state === 'active' ? '启用' : '退休' }}</el-tag></template></el-table-column><el-table-column prop="revision" label="配置版本" min-width="165" /><el-table-column prop="created_at" label="创建时刻（UTC 微秒）" min-width="240" /><el-table-column label="操作" width="100"><template #default="{ row }"><el-button link @click="select(row.id)">查看组价</el-button></template></el-table-column></el-table><div class="price-pagination"><el-button v-if="groupState.next" :loading="groupState.loading" @click="loadGroups(false)">下一页</el-button><span v-else-if="groupState.loaded">当前分页已到末页</span></div></section>
    <section class="panel price-history"><div class="panel-header"><div><div class="panel-title">组详情与不可变价格版本</div><div class="panel-subtitle">可按组 ID 读取，不受当前列表页限制；历史含未来计划价。</div></div></div><div class="filters"><el-input v-model="lookup" placeholder="价格组 UUID" aria-label="查询价格组 ID" /><el-button :loading="detailBusy" @click="select(lookup)">读取组</el-button><el-button v-if="selected && admin" @click="write('price_group.update')">修改名称 / 状态</el-button><el-button v-if="selected && admin" type="primary" :disabled="selected.state !== 'active'" @click="write('group_price.publish')">追加组价</el-button></div><el-alert v-if="error" :title="error" type="error" :closable="false" />
      <template v-if="selected"><p class="price-selection">{{ selected.label }} · {{ selected.state === 'active' ? '启用' : '退休：禁止新发布或新分配' }} · 配置版本 {{ selected.revision }} · {{ selected.id }}</p><el-alert v-if="priceState.message" :title="priceState.message" type="error" :closable="false" /><el-table v-loading="priceState.loading" :data="priceState.rows" empty-text="此组尚无售价版本；已分配租户的缺价模型会拒绝调用"><el-table-column prop="id" label="价格版本 ID" min-width="290" /><el-table-column prop="model_id" label="模型 ID" min-width="290" /><el-table-column prop="schema_id" label="原计量域" min-width="290" /><el-table-column label="完整费率（微元 / token）" min-width="260"><template #default="{ row }"><div v-for="line in row.rates.rates" :key="line.dimension">{{ line.dimension }}：{{ line.numerator }} / {{ line.denominator }} · {{ line.unit }}</div></template></el-table-column><el-table-column prop="effective_at" label="生效时刻（UTC 微秒）" min-width="240" /><el-table-column prop="created_at" label="发布时刻（UTC 微秒）" min-width="240" /></el-table><div class="price-pagination"><span>第 {{ priceState.page || 1 }} 页</span><el-button :disabled="priceState.loading" @click="loadPrices()">刷新第一页</el-button><el-button v-if="priceState.next" :loading="priceState.loading" @click="loadPrices(false)">下一页</el-button><span v-else-if="priceState.loaded">当前分页已到末页</span></div></template>
    </section>
    <GroupMultiplierCompiler v-if="selected && admin && selected.state === 'active'" :group="selected" @changed="journalRevision++" />
    <TenantPricing @changed="journalRevision++" />
    <PricingCommands :refresh="journalRevision" @changed="journalRevision++" />
    <PricingWriter :model-value="open" :kind="kind" :group="selected" @update:model-value="closeWriter" @changed="journalRevision++" />
  </template>
</template>
<style scoped>.price-history{margin-top:22px}.price-pagination{padding:16px 22px;display:flex;gap:16px;align-items:center;font-size:12px;color:var(--text-secondary)}.price-selection{padding:4px 22px 16px;font-size:12px;line-height:1.9;overflow-wrap:anywhere}.filters .el-input{max-width:390px}</style>
