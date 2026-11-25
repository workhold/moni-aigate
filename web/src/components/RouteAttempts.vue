<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import ErrorNotice from './ErrorNotice.vue'
import { errorText, request } from '../api'
import { integer, label, shortId, time } from '../format'
import { hasPlatform } from '../session'

interface Decision { binding_id: string; channel_id: string; channel_version_id: string; physical_model_id: string; upstream_model: string; priority: number; weight: number; reason: string; filter_reason?: string; quality_tier?: string }
interface Attempt { id: string; sequence: number; channel_id: string; channel_version_id: string; credential_id?: string; upstream_model: string; physical_model_id?: string; route_revision: number; priority?: number; weight?: number; delivery_state: string; created_at: string; binding_isolated?: boolean; route_decisions?: Decision[]; draw?: string; sticky?: string; degradation?: string }
interface DecisionRow { id: string; attempt_id?: string; sequence: number; candidates: Decision[]; draw?: string; sticky?: string; degradation: string; created_at: string }
const props = defineProps<{ requestId: string }>()
const attempts = ref<Attempt[]>([]); const decisions = ref<DecisionRow[]>([]); const loading = ref(false); const error = ref('')
const ordered = computed(() => [...attempts.value].sort((a, b) => a.sequence - b.sequence))
const unadmitted = computed(() => decisions.value.filter((decision) => !decision.attempt_id))
let generation = 0
const reasons: Record<string, string> = {
  selected: '本次选中', sticky_override: '安全会话粘性命中', weighted_not_selected: '同优先级按权重未选中', lower_priority: '优先级低于当前候选池', lower_score: '排序偏好低于当前候选', disabled: '渠道或物理模型未启用', already_tried: '本请求已尝试', capacity_full: '并发容量已满', quarantined: '渠道健康隔离', circuit_open: '熔断中', half_open_busy: '半开探测名额已占用', channel_quarantined: '渠道被平台隔离', credential_revoked: '凭据已撤销', binding_isolated: '模型绑定已隔离', region_forbidden: '不符合密钥地域限制', not_authorized: '不在本次授权或 only/ignore 范围',
  capability_missing: '物理模型能力或请求参数不满足', context_too_small: '物理上下文不足', output_limit_too_small: '物理输出上限不足', tools_unsupported: '物理模型不支持所需工具', quality_below_floor: '低于质量档位门槛', margin_below_floor: '低于同一币种的最低毛利率', margin_basis_unavailable: '最低毛利缺少同一币种的付款基准',
  cost_unknown: '采购报价未知或不完整', cost_limit_exceeded: '采购单价超过请求或绑定上限', higher_cost: '同优先级采购比较成本较高', procurement_paused: '共享采购账户暂停或异常', procurement_budget_exceeded: '共享采购预算不足', procurement_bound_invalid: '采购绑定或计量硬界无效', procurement_budget_unavailable: '采购预算当前不可用',
}
function reason(value: string) { return reasons[value] || '未识别的筛选原因' }
async function load() {
  const current = ++generation
  attempts.value = []; decisions.value = []; error.value = ''
  if (!hasPlatform.value || !props.requestId) { loading.value = false; return }
  loading.value = true
  try {
    const path = `/platform/requests/${encodeURIComponent(props.requestId)}`
    const [attemptResult, decisionResult] = await Promise.all([request<Attempt[]>(`${path}/attempts`), request<DecisionRow[]>(`${path}/route-decisions`)])
    if (current === generation) { attempts.value = attemptResult.data || []; decisions.value = decisionResult.data || [] }
  } catch (e) { if (current === generation) error.value = errorText(e) }
  finally { if (current === generation) loading.value = false }
}
watch(() => [props.requestId, hasPlatform.value], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <section v-if="hasPlatform && requestId" class="route-attempts">
    <div class="route-heading"><div><h3>路由与尝试</h3><p>展示请求受理时的路由版本，以及每次选择记录的完整候选池、过滤原因和抽签。</p></div><el-button text :icon="Refresh" :loading="loading" aria-label="刷新路由与尝试" @click="load">刷新</el-button></div>
    <ErrorNotice :message="error" @retry="load" />
    <div v-loading="loading" class="route-results">
      <article v-for="attempt in ordered" :key="attempt.id" class="route-attempt">
        <div class="attempt-heading"><strong>尝试 {{ attempt.sequence }}</strong><el-tag>路由版本 {{ integer(attempt.route_revision) }}</el-tag><el-tag v-if="attempt.degradation === 'tier_changed'" type="warning">服务档位已切换</el-tag><span class="muted small-text">{{ time(attempt.created_at) }}</span></div>
        <dl class="attempt-facts"><dt>尝试 ID</dt><dd class="mono">{{ attempt.id }}</dd><dt>选中渠道</dt><dd class="mono">{{ attempt.channel_id }}</dd><dt>渠道版本</dt><dd class="mono">{{ attempt.channel_version_id }}</dd><dt>物理模型</dt><dd class="mono">{{ attempt.physical_model_id || '—' }}</dd><template v-if="attempt.credential_id"><dt>实际凭据身份</dt><dd class="mono">{{ attempt.credential_id }}</dd></template><dt>上游模型</dt><dd>{{ attempt.upstream_model }}</dd><dt>优先级 / 权重</dt><dd>{{ integer(attempt.priority) }} / {{ integer(attempt.weight) }}</dd><dt>派发状态</dt><dd>{{ label(attempt.delivery_state) }}</dd></dl>
        <el-alert v-if="attempt.binding_isolated" type="warning" title="此模型绑定当前处于隔离状态；历史选择记录保持原样。" :closable="false" style="margin:15px 0" />
        <el-table v-if="attempt.route_decisions?.length" :data="attempt.route_decisions" size="small" aria-label="本次尝试的完整路由候选与筛选原因">
          <el-table-column label="候选渠道 / 物理模型" min-width="180"><template #default="{ row }"><span class="mono" :title="row.channel_id">{{ shortId(row.channel_id) }}</span><div class="mono muted small-text" :title="row.physical_model_id">{{ shortId(row.physical_model_id) }}</div><div class="muted small-text">{{ row.upstream_model }} · {{ row.quality_tier || 'standard' }}</div></template></el-table-column>
          <el-table-column label="优先级" width="90"><template #default="{ row }">{{ integer(row.priority) }}</template></el-table-column><el-table-column label="权重" width="80"><template #default="{ row }">{{ integer(row.weight) }}</template></el-table-column>
          <el-table-column label="选择 / 排除原因" min-width="230"><template #default="{ row }"><el-tag :type="row.reason === 'selected' || row.reason === 'sticky_override' ? 'success' : ['weighted_not_selected', 'lower_priority', 'already_tried'].includes(row.reason) ? 'info' : 'warning'">{{ reason(row.reason) }}</el-tag></template></el-table-column>
        </el-table><p v-else class="field-hint">该历史尝试没有记录候选筛选明细。</p>
      </article>
      <article v-for="decision in unadmitted" :key="decision.id" class="route-attempt">
        <div class="attempt-heading"><strong>选择 {{ decision.sequence }}</strong><el-tag type="warning">未产生上游尝试</el-tag><span class="muted small-text">{{ time(decision.created_at) }}</span></div>
        <el-table :data="decision.candidates" size="small" aria-label="未准入请求的完整失败池"><el-table-column label="候选渠道 / 物理模型" min-width="180"><template #default="{ row }"><span class="mono">{{ shortId(row.channel_id) }}</span><div class="mono muted small-text">{{ shortId(row.physical_model_id) }}</div></template></el-table-column><el-table-column label="排除原因" min-width="230"><template #default="{ row }"><el-tag type="warning">{{ reason(row.reason || row.filter_reason || '') }}</el-tag></template></el-table-column></el-table>
      </article>
      <p v-if="!loading && !error && !ordered.length && !unadmitted.length" class="muted small-text">尚未产生上游尝试，或请求在派发前已结束。</p>
    </div>
  </section>
</template>

<style scoped>
.route-attempts{border-top:1px solid #e4eaf0;margin-top:27px;padding-top:23px}.route-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:18px}.route-heading h3{font-size:14px;font-weight:600}.route-heading p{font-size:11px;color:#8b98a9;line-height:1.9;margin-top:7px}.route-results{min-height:55px}.route-attempt{border:1px solid #e3eaf0;border-radius:8px;margin-top:15px;padding:18px}.attempt-heading{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin-bottom:19px}.attempt-heading strong{font-size:12px;font-weight:600}.attempt-facts{display:grid;grid-template-columns:100px minmax(0,1fr);font-size:11px;gap:13px 12px;margin:0 0 20px}.attempt-facts dt{color:#8997a8}.attempt-facts dd{margin:0;overflow-wrap:anywhere}@media(max-width:520px){.route-attempt{padding:14px}.attempt-facts{grid-template-columns:75px minmax(0,1fr);font-size:10px}.attempt-facts .mono{font-size:10px}}
</style>
