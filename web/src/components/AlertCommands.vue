<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText } from '../api'
import { session } from '../session'
import { alertID, parseAlertEnvelope, parseAlertReceipt } from '../alerts'
import type { AlertReceipt } from '../alerts'
import { alertContext, alertRejectionLabels, alertsJournal, createAlertEpoch } from '../alerts-state'
import type { AlertReview } from '../alerts-state'
import { alertsRequest } from '../alerts-transport'
const props=defineProps<{ refresh:number;viewContext:string }>(),emit=defineEmits<{changed:[]}>()
const context=computed(()=>JSON.stringify([alertContext(session.me?.user.id,session.me?.tenant?.id,session.me?.user.platform_role||'',session.me?.csrf_token||'',session.mfaPending,session.enrollmentRequired),props.viewContext])),gate=createAlertEpoch(),pending=ref<AlertReview[]>([]),operation=ref(''),busy=ref(false),error=ref(''),receipt=ref<AlertReceipt|null>(null)
function reload(){try{pending.value=session.me?alertsJournal.list(session.me.user.id).filter(r=>['unknown','submitting'].includes(r.state)&&(session.me?.user.platform_role==='admin'||r.command.kind==='acknowledge')):[]}catch(cause){error.value=errorText(cause)}}
function reset(){gate.reset(context.value);operation.value='';busy.value=false;error.value='';receipt.value=null;pending.value=[];reload()}
async function inspect(id=operation.value){if(busy.value)return;const ticket=gate.ticket(),actor=session.me?.user.id;busy.value=true;error.value='';receipt.value=null;try{const original=pending.value.find(r=>r.command.operation_id===id),raw=await alertsRequest(`/platform/alert-operations/${alertID(id)}`,{current:()=>gate.accepts(ticket)});if(!gate.accepts(ticket))return;const result=parseAlertEnvelope(raw,data=>parseAlertReceipt(data,original?.command));if(result.operation_id!==id)throw new Error('回执操作 ID 不匹配。');if(original&&actor)alertsJournal.confirm(actor,id,result);receipt.value=result;operation.value=id;reload();emit('changed')}catch(cause){if(gate.accepts(ticket))error.value=`只读核查尚未确认，404 不会解除待核查目标，也不证明旧事务未提交。${errorText(cause)}`}finally{if(gate.accepts(ticket))busy.value=false}}
watch(context,reset,{immediate:true,flush:'sync'});watch(()=>props.refresh,reload);onBeforeUnmount(()=>gate.dispose())
</script>
<template>
  <section class="panel alert-commands"><div class="panel-header"><div><div class="panel-title">原操作核查</div><div class="panel-subtitle">结果不明时保留原 ID 和安全期望值；只读查询原回执，页面刷新后仍保留待核查标识。</div></div></div>
    <div class="filters"><el-input v-model="operation" placeholder="原操作 UUID" aria-label="告警原操作 ID" /><el-button :loading="busy" @click="inspect()">只读核查</el-button></div>
    <el-alert v-if="error" :title="error" type="warning" show-icon :closable="false" />
    <el-table v-if="pending.length" :data="pending"><el-table-column prop="command.operation_id" label="待核查原操作" min-width="285"/><el-table-column prop="command.kind" label="动作" min-width="155"/><el-table-column prop="command.target_key" label="目标" min-width="265"/><el-table-column width="90"><template #default="{row}"><el-button link :disabled="busy" @click="inspect(row.command.operation_id)">核查</el-button></template></el-table-column></el-table>
    <div v-if="receipt" class="receipt" aria-live="polite"><el-tag :type="receipt.status==='applied'?'success':'warning'">{{ receipt.status==='applied'?'原操作已应用':'原操作被拒绝' }}</el-tag><p v-if="receipt.status==='rejected'">{{ alertRejectionLabels[receipt.reason] }}</p><dl class="detail-list"><dt>原操作</dt><dd class="mono">{{ receipt.operation_id }}</dd><dt>动作 / 原结果版本</dt><dd>{{ receipt.kind }} / {{ receipt.result_revision ?? '无' }}</dd><dt>资源</dt><dd class="mono">{{ receipt.resource_id ?? '无' }}</dd><dt>回执时间 UTC</dt><dd class="mono">{{ receipt.created_at }}</dd></dl><p class="muted">这是原命令的不可变结果；资源当前状态需另外刷新读取。</p></div>
  </section>
</template>
<style scoped>.alert-commands{margin-top:24px}.receipt{padding:20px;font-size:12px;overflow-wrap:anywhere}.receipt>p{margin:12px 0}.filters .el-input{max-width:440px}</style>
