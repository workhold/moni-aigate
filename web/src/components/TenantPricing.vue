<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { errorText } from '../api'
import { money, time } from '../format'
import { session } from '../session'
import { parsePricingAssignment, parsePricingEnvelope, parsePricingPreview, pricingID, pricingPreviewPath, pricingRequest } from '../pricing'
import type { PricingAssignment, PricingPreviewRow } from '../pricing'
import { createPricingEpoch, createPricingPager, pricingContext } from '../pricing-state'
import PricingWriter from './PricingWriter.vue'
const emit = defineEmits<{ changed: [] }>(), route = useRoute()
const context = computed(() => pricingContext(session.me?.user.id, session.me?.tenant?.id, `${session.me?.user.platform_role}:${session.me?.tenant?.role}`, session.me?.csrf_token || '', session.mfaPending))
const tenant = ref(typeof route.query.tenant === 'string' ? route.query.tenant : ''), target = ref('default'), loading = ref(false), error = ref(''), assignment = ref<PricingAssignment | null>(null), writer = ref(false)
const gate = createPricingEpoch(), pager = createPricingPager<PricingPreviewRow>(), preview = ref(pager.snapshot())
const admin = computed(() => session.me?.user.platform_role === 'admin' && !session.mfaPending)
function selectedTarget() { return target.value === 'default' ? null : pricingID(target.value) }
function resetPreview() { pager.reset(`${context.value}:${tenant.value}:${target.value}`); preview.value = pager.snapshot(); writer.value = false }
function resetAll() { gate.reset(`${context.value}:${tenant.value}`); assignment.value = null; loading.value = false; error.value = ''; resetPreview() }
async function loadAssignment() {
  if (loading.value) return
  const ticket = gate.ticket(); loading.value = true; error.value = ''; resetPreview()
  try {
    const id = pricingID(tenant.value), raw = await pricingRequest(`/platform/tenants/${id}/pricing`, 'GET', undefined, () => gate.accepts(ticket))
    if (!gate.accepts(ticket)) return
    assignment.value = parsePricingEnvelope(raw, data => parsePricingAssignment(data, id))
  } catch (cause) { if (gate.accepts(ticket)) { assignment.value = null; error.value = errorText(cause) } }
  finally { if (gate.accepts(ticket)) loading.value = false }
}
async function loadPreview(first = true) {
  if (!assignment.value) return
  let targetID: string | null
  try { targetID = selectedTarget() } catch (cause) { error.value = errorText(cause); return }
  const id = assignment.value.tenant_id, ticket = pager.begin(first)
  if (!ticket) return
  preview.value = pager.snapshot()
  try {
    const raw = await pricingRequest(pricingPreviewPath(id, targetID, ticket.cursor), 'GET', undefined, () => pager.accepts(ticket))
    if (pager.accept(ticket, parsePricingPreview(raw, id, targetID))) preview.value = pager.snapshot()
  } catch (cause) { if (pager.fail(ticket, cause)) preview.value = pager.snapshot() }
}
const contextMatches = computed(() => !!assignment.value && !!preview.value.context && assignment.value.pricing_revision === preview.value.context.current_pricing_revision && assignment.value.price_group_id === preview.value.context.current_price_group_id)
const canAssign = computed(() => admin.value && contextMatches.value && !!preview.value.context && (preview.value.context.assignable || preview.value.context.target_price_group_id === assignment.value?.price_group_id))
function closeWriter(value: boolean) { writer.value = value; if (!value && assignment.value) void loadAssignment() }
watch([context, tenant], resetAll, { immediate: true, flush: 'sync' })
watch(target, resetPreview, { flush: 'sync' })
watch(() => route.query.tenant, value => { if (typeof value === 'string') tenant.value = value })
onBeforeUnmount(() => { gate.dispose(); pager.dispose() })
</script>
<template>
  <section class="panel tenant-pricing"><div class="panel-header"><div><div class="panel-title">租户价格域与缺价预览</div><div class="panel-subtitle">一个租户只使用一个价格域。价格组不授予模型权限，也不修改供应商成本或预算。</div></div></div>
    <div class="filters"><el-input v-model="tenant" placeholder="目标租户 UUID" aria-label="定价目标租户" /><el-button :loading="loading" @click="loadAssignment">读取当前归属</el-button></div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <template v-if="assignment"><div class="tenant-pricing-current"><span>当前域：<strong>{{ assignment.price_group_id ?? '公共默认' }}</strong></span><span>归属版本：<strong>{{ assignment.pricing_revision }}</strong></span></div>
      <div class="filters"><el-input v-model="target" placeholder="default 或目标价格组 UUID" aria-label="预览目标价格域" /><el-button :disabled="preview.loading" @click="loadPreview(true)">预览第一页</el-button><el-button v-if="admin" type="primary" :disabled="!canAssign || preview.loading" @click="writer = true">确认分配目标域</el-button></div>
      <p class="field-hint">目标填 <code>default</code> 表示公共默认。范围是该租户当前已授予且启用的模型；暂停租户仍可预览。每页独立观察，缺价模型实际调用会拒绝，不回落默认。</p>
      <el-alert v-if="preview.message" :title="preview.message" type="error" :closable="false" />
      <div v-if="preview.context" class="preview-context"><span>第 {{ preview.page }} 页 · 当前页 {{ preview.rows.length }} 个模型</span><span>租户 {{ preview.context.tenant_status === 'active' ? '启用' : '暂停' }} · 目标{{ preview.context.target_group_state === null ? '公共默认' : preview.context.target_group_state === 'active' ? '启用组' : '退休组（不能新分配）' }}</span><span :title="preview.context.as_of">DB观察 {{ time(preview.context.as_of) }} · {{ preview.context.as_of }}</span></div>
      <el-alert v-if="preview.context && !contextMatches" title="预览时的当前归属与已读取版本不同，请明确重新读取当前归属，再核查预览；页面不会自动换版本提交。" type="warning" :closable="false" />
      <el-table v-if="preview.loaded || preview.loading" v-loading="preview.loading" :data="preview.rows" empty-text="当前观察范围没有启用且获授权的模型"><el-table-column prop="public_name" label="模型" min-width="190" /><el-table-column prop="protocol" label="协议" min-width="175" /><el-table-column label="目标报价" width="110"><template #default="{ row }"><el-tag :type="row.retail_price.status === 'available' ? 'success' : 'danger'">{{ row.retail_price.status === 'available' ? '当时有价' : '缺价' }}</el-tag></template></el-table-column><el-table-column label="输入 / 百万 Tokens" min-width="185"><template #default="{ row }">{{ money(row.retail_price.input_rate) }}</template></el-table-column><el-table-column label="输出 / 百万 Tokens" min-width="185"><template #default="{ row }">{{ money(row.retail_price.output_rate) }}</template></el-table-column><el-table-column label="生效时刻" min-width="235"><template #default="{ row }"><span :title="row.retail_price.effective_at || ''">{{ row.retail_price.effective_at ?? '暂无有效版本' }}</span></template></el-table-column></el-table>
      <div v-if="preview.loaded" class="preview-footer"><el-button v-if="preview.next" :loading="preview.loading" @click="loadPreview(false)">读取下一页</el-button><span v-else>当前分页已到末页</span><span>逐页观察不能证明未来所有模型均可调用；本页缺价数不代表全租户总数。</span></div>
    </template>
  </section>
  <PricingWriter :model-value="writer" kind="tenant_pricing.assign" :assignment="assignment" :target-group="preview.context?.target_price_group_id ?? null" @update:model-value="closeWriter" @changed="emit('changed')" />
</template>
<style scoped>.tenant-pricing{margin-top:22px}.filters .el-input{max-width:440px}.tenant-pricing-current,.preview-context,.preview-footer{display:flex;gap:18px;flex-wrap:wrap;padding:16px 22px;font-size:12px;line-height:1.9;overflow-wrap:anywhere}.preview-context{background:#f5f8f9;color:var(--text-secondary)}.preview-footer{align-items:center;color:var(--text-secondary)}.tenant-pricing-current strong{font-family:monospace}.field-hint{padding:0 22px}</style>
