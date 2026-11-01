<script setup lang="ts">
import { computed,onBeforeUnmount,onMounted,ref,watch } from 'vue'
import { useRoute } from 'vue-router'
import PageHeader from './PageHeader.vue'
import PaymentsPanel from './PaymentsPanel.vue'
import { loadMe,session } from '../session'
import { paymentCanRead } from '../payments'
const route=useRoute(),verified=ref(false),verifying=ref(true),identityError=ref(''),generation=ref(0)
const panel=ref<InstanceType<typeof PaymentsPanel>|null>(null)
let sequence=0,disposed=false
const platform=computed(()=>route.path==='/platform/payments')
const validPath=computed(()=>['/payments','/platform/payments'].includes(route.path)&&!route.hash&&!Object.keys(route.query).length)
const context=computed(()=>JSON.stringify([session.me?.user.id,session.me?.tenant?.id,session.me?.user.platform_role,session.me?.tenant?.role,session.me?.csrf_token,session.me?.reauthenticated_until,session.me?.user.mfa_enabled,session.mfaPending,session.enrollmentRequired,route.fullPath]))
const readable=computed(()=>validPath.value&&!session.mfaPending&&!session.enrollmentRequired&&paymentCanRead(platform.value,session.me?.user.platform_role||'',session.me?.tenant?.role||'')&&(platform.value||!!session.me?.tenant))
watch(context,()=>{panel.value?.invalidate()},{flush:'sync'})
async function verify(){panel.value?.invalidate();const ticket=++sequence;verifying.value=true;verified.value=false;identityError.value='';const valid=await loadMe(true);if(disposed||ticket!==sequence)return;verifying.value=false;verified.value=valid;generation.value++;if(!valid)identityError.value='当前身份无法确认，请重新登录后核查原编号。浏览器中的待核查记录仍会保留。'}
onMounted(()=>{void verify()});onBeforeUnmount(()=>{panel.value?.invalidate();disposed=true;sequence++})
</script>
<template>
 <PageHeader eyebrow="SANDBOX PAYMENTS" :title="platform?'模拟支付运营':'模拟充值与订单'" description="本地模拟，无真实收款。保留原订单、原操作回执与独立资金状态。" />
 <el-alert v-if="!validPath" title="支付入口无效，请从钱包或平台导航进入。" type="error" :closable="false" />
 <el-skeleton v-else-if="verifying" :rows="5" animated />
 <el-alert v-else-if="identityError" :title="identityError" type="error" :closable="false" />
 <el-alert v-else-if="!readable" title="当前身份没有此支付入口的读取权限。" type="info" :closable="false" />
 <PaymentsPanel ref="panel" v-else-if="verified&&session.me" :key="`${context}|${generation}`" :platform="platform" :actor="session.me.user.id" :tenant="session.me.tenant?.id||''" :csrf="session.me.csrf_token" :context="context" :admin="session.me.user.platform_role==='admin'" :owner="session.me.tenant?.role==='owner'" @identity-check="verify" />
</template>
