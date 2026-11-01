<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import PageHeader from '../components/PageHeader.vue'
import PeriodicBudgetPanel from '../components/PeriodicBudgetPanel.vue'
import { loadMe, session } from '../session'
import { parsePeriodicTarget, periodicCanRead, periodicCanWrite } from '../periodic-budget'
const route = useRoute(), verified = ref(false), verifying = ref(false), identityError = ref('')
let sequence = 0, disposed = false
const context = computed(() => JSON.stringify([session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role, session.me?.tenant?.role, session.me?.csrf_token, session.me?.reauthenticated_until, session.me?.user.mfa_enabled, session.mfaPending, session.enrollmentRequired]))
const target = computed(() => {
  try { const at = route.fullPath.indexOf('?'); return parsePeriodicTarget(route.path, at < 0 ? '' : route.fullPath.slice(at).split('#')[0]!, session.me?.tenant?.id) } catch { return null }
})
const readable = computed(() => !!target.value && !session.mfaPending && !session.enrollmentRequired && periodicCanRead(target.value, session.me?.user.platform_role || '', session.me?.tenant?.role || ''))
const writable = computed(() => !!target.value && periodicCanWrite(target.value, session.me?.user.platform_role || '', session.me?.tenant?.role || ''))
async function verifyIdentity(initial = false) {
  const ticket = ++sequence; if (initial) { verified.value = false; verifying.value = true }; identityError.value = ''
  const valid = await loadMe(true)
  if (disposed || ticket !== sequence) return
  verifying.value = false; verified.value = valid
  if (!valid) identityError.value = '当前登录身份无法确认，请重新登录后按原编号核查。待核查标识仍保存在浏览器中。'
}
onMounted(() => { void verifyIdentity(true) })
onBeforeUnmount(() => { disposed = true; sequence++ })
</script>
<template>
  <PageHeader eyebrow="PERIODIC BUDGETS" :title="target?.title || '周期预算'" description="分别控制客户、API Key 与采购账户的周期用量，保留跨周期未决占用与原操作回执。" />
  <el-alert v-if="!target" title="预算目标无效；请从钱包、API 密钥、客户或采购账户入口进入。" type="error" :closable="false" />
  <el-skeleton v-else-if="verifying" :rows="5" animated />
  <el-alert v-else-if="identityError" :title="identityError" type="error" :closable="false" />
  <el-alert v-else-if="!readable" title="当前身份没有此预算入口的查看权限。" type="info" :closable="false" />
  <PeriodicBudgetPanel v-else-if="verified && session.me && target" :key="`${context}|${target.path}`" :target="target" :actor="session.me.user.id" :csrf="session.me.csrf_token" :context="context" :can-write="writable" @identity-check="verifyIdentity" />
</template>
