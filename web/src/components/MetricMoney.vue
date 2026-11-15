<script setup lang="ts">
import { computed } from 'vue'
import { coin, money } from '../format'
const props = defineProps<{ value: unknown; restricted?: boolean; unit?: 'CNY' | 'COIN' }>()
const formatted = computed(() => props.restricted ? '按角色受限' : props.unit === 'COIN' ? coin(props.value) : money(props.value))
const base = computed(() => formatted.value.split('（约合')[0] || formatted.value)
const approximation = computed(() => formatted.value.includes('（约合') ? `（约合${formatted.value.split('（约合').slice(1).join('（约合')}` : '')
const parts = computed(() => base.value.split('.'))
</script>
<template><div class="metric-value metric-money" :title="formatted"><span class="metric-whole" :class="{ 'long-amount': (parts[0]?.length || 0) > 13 }">{{ parts[0] }}</span><span v-if="parts[1]" class="metric-fraction">.{{ parts[1] }}</span><span v-if="approximation" class="metric-approx">{{ approximation }}</span></div></template>
<style scoped>.metric-approx{font-size:12px;margin-left:4px;white-space:nowrap}</style>
