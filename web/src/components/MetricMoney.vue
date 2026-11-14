<script setup lang="ts">
import { computed } from 'vue'
import { coin, money } from '../format'
const props = defineProps<{ value: unknown; restricted?: boolean; unit?: 'CNY' | 'COIN' }>()
const formatted = computed(() => props.restricted ? '按角色受限' : props.unit === 'COIN' ? coin(props.value) : money(props.value))
const parts = computed(() => formatted.value.split('.'))
</script>
<template><div class="metric-value metric-money" :title="formatted"><span class="metric-whole" :class="{ 'long-amount': (parts[0]?.length || 0) > 13 }">{{ parts[0] }}</span><span v-if="parts[1]" class="metric-fraction">.{{ parts[1] }}</span></div></template>
