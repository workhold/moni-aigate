<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { cnyToMicro, money } from '../format'
import type { PeriodicBudget, PeriodicInput, PeriodicKind } from '../periodic-budget'
const props = defineProps<{ modelValue: boolean; budget: PeriodicBudget; busy: boolean; allowed: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; configure: [input: Omit<PeriodicInput, 'operation_id'>] }>()
const kind = ref<PeriodicKind>('utc_day'), enabled = ref(true), amount = ref(''), error = ref('')
const configured = computed(() => props.budget.revision !== '0')
watch(() => props.modelValue, value => {
  if (!value) { amount.value = ''; error.value = ''; return }
  kind.value = props.budget.period_kind || 'utc_day'; enabled.value = props.budget.revision === '0' || props.budget.enabled
  const raw = props.budget.limit_amount
  amount.value = raw === null ? '' : `${BigInt(raw) / 1000000n}.${(BigInt(raw) % 1000000n).toString().padStart(6, '0')}`
  error.value = ''
})
function submit() {
  if (!props.allowed || props.busy) return
  try { const limit = cnyToMicro(amount.value); error.value = ''; emit('configure', { expected_revision: props.budget.revision, period_kind: kind.value, enabled: enabled.value, limit_amount: limit }) }
  catch { error.value = '限额须为非负 CNY 金额，最多六位小数且不超过 BIGINT 金额上限。' }
}
function updateOpen(value: boolean) { if (!props.busy) emit('update:modelValue', value) }
</script>
<template>
  <el-dialog :model-value="modelValue" title="配置周期预算" width="min(600px, calc(100vw - 28px))" destroy-on-close :close-on-click-modal="false" :close-on-press-escape="!busy" :show-close="!busy" @update:model-value="updateOpen">
    <p class="dialog-description">使用 UTC 自然日或自然月。首次配置从下一完整周期生效；当前历史占用会被跟踪。预算独立于钱包余额、售价与采购账户余额。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-form label-position="top" :disabled="busy || !allowed" @submit.prevent="submit">
      <el-form-item label="固定周期"><el-select v-model="kind" :disabled="configured"><el-option value="utc_day" label="UTC 自然日" /><el-option value="utc_month" label="UTC 自然月" /></el-select><span class="field-hint">首次配置后不可更改周期；关闭预算保留占用与历史。</span></el-form-item>
      <el-form-item label="每周期限额（CNY）"><el-input v-model="amount" inputmode="decimal" placeholder="例如 100.000000" maxlength="30" /><span class="field-hint">当前限额：{{ money(budget.limit_amount) }}；降额不会改写已接受请求，后续准入按剩余额度检查。</span></el-form-item>
      <el-form-item label="预算准入"><el-switch v-model="enabled" active-text="启用限额" inactive-text="停用限额，保留跟踪" /></el-form-item>
      <p class="field-hint">基于版本 {{ budget.revision }} 提交。提交结果不明时会保留原编号，只查询原回执。</p>
    </el-form>
    <template #footer><el-button :disabled="busy" @click="emit('update:modelValue', false)">取消</el-button><el-button type="primary" :disabled="!allowed" :loading="busy" @click="submit">确认配置</el-button></template>
  </el-dialog>
</template>
<style scoped>.el-select{width:100%}.field-hint{display:block;line-height:1.8;margin-top:6px}</style>
