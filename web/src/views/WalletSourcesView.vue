<script setup lang="ts">
import { computed, ref } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import WalletSources from '../components/WalletSources.vue'
import WalletSourceAudit from '../components/WalletSourceAudit.vue'
import WalletOperatorSettings from '../components/WalletOperatorSettings.vue'
import { commerceUUID } from '../commerce'
import { session } from '../session'

const draft = ref('')
const allowed = computed(() => !!session.me && !session.mfaPending && !session.enrollmentRequired && ['admin', 'finance_operator', 'finance_approver'].includes(session.me.user.platform_role))
const tenantID = computed(() => { try { return commerceUUID(draft.value.trim()) } catch { return '' } })
</script>

<template>
  <PageHeader eyebrow="WALLET FUNDING" title="资金来源与核对" description="按客户查看入账来源余额，并只读核对资金来源与消费分配。" />
  <el-alert v-if="!allowed" title="此页面仅向平台管理员、财务制单和财务审批角色开放。" type="info" :closable="false" />
  <template v-else>
    <WalletOperatorSettings />
    <section class="panel tenant-scope"><label for="wallet-source-tenant">客户租户 ID</label><el-input id="wallet-source-tenant" v-model="draft" placeholder="输入要查看的客户 UUID" clearable autocomplete="off" spellcheck="false" /><p v-if="draft && !tenantID" class="scope-error" role="status">请输入完整、规范的非零租户 UUID。</p><p v-else class="muted small-text">从“客户与成员”复制租户 ID。所有金额与核对结果只属于当前选定客户。</p></section>
    <template v-if="tenantID"><WalletSources :key="`sources:${tenantID}`" platform :tenantID="tenantID" /><WalletSourceAudit :key="`audit:${tenantID}`" :tenantID="tenantID" /></template>
    <el-empty v-else description="输入客户租户 ID 后查看资金来源与核对工具" />
  </template>
</template>

<style scoped>
.tenant-scope{padding:22px}.tenant-scope label{display:block;margin-bottom:10px;font-size:13px;font-weight:600}.tenant-scope .el-input{max-width:540px}.tenant-scope p{margin-bottom:0;line-height:1.7}.scope-error{color:var(--el-color-danger);font-size:12px}
</style>
