<script setup lang="ts">
import type { AlertSample } from '../alerts'
import { alertCount } from '../alerts'
import { integer, money } from '../format'
defineProps<{ sample: AlertSample | null }>()
</script>
<template>
  <div v-if="!sample" class="muted">尚无成功样本</div>
  <div v-else class="alert-sample">
    <div v-if="sample.kind === 'channel_quarantined'" class="sample-facts"><span>持久隔离 <strong>{{ sample.quarantined ? '是' : '否' }}</strong></span><span>渠道状态 <strong>{{ sample.enabled ? '启用' : '停用' }}</strong></span><p>此规则只观察持久隔离，不涵盖内存熔断或凭据健康。</p></div>
    <div v-else-if="sample.kind === 'procurement_budget_low'" class="sample-facts"><span>登记余量 <strong :class="{ 'text-danger': sample.available_amount.startsWith('-') }">{{ money(sample.available_amount) }}</strong></span><span>登记额度 <strong>{{ money(sample.limit_amount) }}</strong></span><span>已占用 <strong>{{ money(sample.held_amount) }}</strong></span><span>已登记支出 <strong>{{ money(sample.spent_amount) }}</strong></span><span>账户 <strong>{{ sample.account_state === 'paused' ? '暂停' : '启用' }}</strong></span><p>登记累计预算余量 = 额度 − 已登记支出 − 占用；可为负值，不代表供应商现金余额或周期预算。</p></div>
    <div v-else class="sample-facts"><span>符合年龄条件 <strong>{{ alertCount(sample) }}</strong></span><span>预占创建年龄 <strong>≥ {{ integer(sample.minimum_age_seconds) }} 秒</strong></span><span v-if="sample.oldest_reference_id">最早引用 <strong class="mono">{{ sample.oldest_reference_id }}</strong></span><span v-if="sample.oldest_created_at">最早创建 <strong class="mono">{{ sample.oldest_created_at }}</strong></span><p>年龄从预占创建时间起算，不是进入未知状态的持续时间；最多观察 1,001 条。</p></div>
    <div class="sample-time">样本观察时刻 <time class="mono" :datetime="sample.as_of">{{ sample.as_of }}</time></div>
  </div>
</template>
<style scoped>.alert-sample{font-size:12px;line-height:1.9;overflow-wrap:anywhere}.sample-facts{display:flex;flex-wrap:wrap;gap:10px 25px}.sample-facts span{display:flex;flex-direction:column;color:#8391a3;min-width:110px;max-width:100%}.sample-facts strong{color:#354f66;font-weight:500;word-break:break-word}.sample-facts p{flex-basis:100%;font-size:11px;color:#8794a5}.sample-time{border-top:1px solid #edf1f5;margin-top:13px;padding-top:9px;color:#8794a5;font-size:10px}.sample-time time{display:block;font-size:10px}.sample-facts .text-danger{color:#c94d58}</style>
