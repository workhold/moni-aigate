<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Search } from '@element-plus/icons-vue'
import ErrorNotice from './ErrorNotice.vue'
import { isAdmin } from '../session'
import { periodicTargetChoicePath, periodicTargetPage } from '../periodic-target'
import type { PeriodicTargetChoice } from '../periodic-target'
import { createPeriodicTargetLoader, periodicTargetContent, periodicTargetEmptyActions } from '../periodic-target-state'

const props = defineProps<{ context: string }>()
const router = useRouter()
const loader = createPeriodicTargetLoader(), state = loader.state
const search = ref(''), page = ref(1)
const total = computed(() => periodicTargetPage(state.choices, search.value, 1).total)
const pageRows = computed(() => periodicTargetPage(state.choices, search.value, page.value).rows)
const content = computed(() => periodicTargetContent(state, total.value))
const empty = computed(() => periodicTargetEmptyActions(isAdmin.value))
function select(choice: PeriodicTargetChoice) { void router.push(periodicTargetChoicePath(choice)) }
watch(() => props.context, () => { search.value = ''; page.value = 1; void loader.reset() }, { immediate: true })
watch([search, () => state.choices], () => { page.value = 1 })
onBeforeUnmount(loader.dispose)
</script>

<template>
  <section class="panel target-picker" data-testid="periodic-target-picker">
    <div class="panel-header"><div><div class="panel-title">选择客户或采购账户</div><p class="panel-subtitle">周期预算必须归属于一个明确目标。选择后才会读取该目标的预算和原操作回执。</p></div><el-button :loading="state.loading" @click="loader.reload">刷新目标</el-button></div>
    <div class="panel-body">
      <ErrorNotice :message="state.error" @retry="loader.reload" />
      <template v-if="content !== 'error'">
        <el-input v-if="state.choices.length" v-model="search" :prefix-icon="Search" clearable placeholder="搜索名称、供应商标识、状态或 UUID" aria-label="搜索预算目标" class="target-search" />
        <el-skeleton v-if="content === 'loading'" :rows="4" animated />
        <div v-else-if="content === 'rows'" class="target-list">
          <article v-for="choice in pageRows" :key="`${choice.kind}:${choice.id}`" class="target-card" :data-target-id="choice.id">
            <div class="target-card-heading"><div><el-tag size="small" :type="choice.kind === 'tenant' ? 'primary' : 'warning'">{{ choice.kind === 'tenant' ? '客户' : '采购账户' }}</el-tag><span class="target-state">{{ choice.state }}</span></div><strong>{{ choice.name }}</strong></div>
            <div class="target-provider">{{ choice.provider }}</div><code class="target-id">{{ choice.id }}</code>
            <el-button class="target-select" type="primary" plain :aria-label="`查看 ${choice.name} 的周期预算`" @click="select(choice)">查看预算</el-button>
          </article>
        </div>
        <el-pagination v-if="total > 20" v-model:current-page="page" class="target-pagination" layout="prev, pager, next" :page-size="20" :total="total" />
        <div v-else-if="content === 'empty'" class="target-empty"><span>{{ empty.message }}</span><div class="target-empty-actions"><router-link v-for="action in empty.actions" :key="action.path" :to="action.path">{{ action.label }}</router-link></div></div>
        <el-empty v-else-if="content === 'search_empty'" description="没有匹配当前搜索的预算目标" />
      </template>
    </div>
  </section>
</template>

<style scoped>
.target-search{margin-bottom:18px;max-width:520px}.target-list{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.target-card{display:flex;flex-direction:column;align-items:flex-start;min-width:0;border:1px solid #e3eaf0;border-radius:8px;padding:16px}.target-card-heading{display:flex;flex-direction:column;gap:10px}.target-card-heading>div{display:flex;align-items:center;gap:8px}.target-card-heading strong{overflow-wrap:anywhere;line-height:1.5}.target-provider{margin-top:6px;color:#7d8b9d;font-size:12px}.target-id{display:block;margin-top:7px;color:#8b98a9;font-size:11px;overflow-wrap:anywhere}.target-state{color:#7d8b9d;font-size:12px}.target-select{margin-top:14px}.target-pagination{display:flex;justify-content:flex-end;margin-top:18px}.target-empty{display:flex;align-items:center;justify-content:space-between;gap:18px;flex-wrap:wrap;padding:18px 0;color:#7d8b9d;font-size:13px}.target-empty-actions{display:flex;gap:14px;flex-wrap:wrap}.target-empty-actions a{color:var(--el-color-primary);text-decoration:none}@media(max-width:700px){.target-search{max-width:none}.target-list{grid-template-columns:1fr}.target-card{padding:14px}.target-select{width:100%}.target-pagination{justify-content:flex-start;overflow-x:auto}.target-empty>div{width:100%}}
</style>
