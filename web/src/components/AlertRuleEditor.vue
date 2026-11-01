<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText } from '../api'
import { cnyToMicro } from '../format'
import { session } from '../session'
import { alertCommand, alertKindLabels, alertKinds, alertScope } from '../alerts'
import type { AlertCapabilities, AlertKind, AlertParameters, AlertReceipt, AlertRule } from '../alerts'
import { alertContext, alertRejectionLabels, alertsJournal, createAlertEpoch, submitAlertCommand } from '../alerts-state'
const props=defineProps<{rule:AlertRule|null;capabilities:AlertCapabilities|null}>(),emit=defineEmits<{changed:[]}>()
const kind=ref<AlertKind>('channel_quarantined'),scope=ref(''),name=ref(''),state=ref<'enabled'|'disabled'>('disabled'),threshold=ref('0'),recovery=ref('0'),age=ref('3600'),trigger=ref('1'),recoveryCount=ref('0'),impact=ref(false),busy=ref(false),error=ref(''),receipt=ref<AlertReceipt|null>(null),version=ref(0)
const context=computed(()=>alertContext(session.me?.user.id,session.me?.tenant?.id,session.me?.user.platform_role||'',session.me?.csrf_token||'',session.mfaPending,session.enrollmentRequired)),gate=createAlertEpoch()
function monetary(v:string){const n=BigInt(v);return `${n/1000000n}.${(n%1000000n).toString().padStart(6,'0')}`}
function reset(){gate.reset(context.value);const r=props.rule;kind.value=r?.kind||'channel_quarantined';scope.value=r?.scope_id||'';name.value=r?.label||'';state.value=r?.state==='enabled'?'enabled':'disabled';threshold.value=r&&'threshold_amount'in r.parameters?monetary(r.parameters.threshold_amount):'0';recovery.value=r&&'recovery_amount'in r.parameters?monetary(r.parameters.recovery_amount):'0';age.value=r&&'minimum_age_seconds'in r.parameters?r.parameters.minimum_age_seconds:'3600';trigger.value=r&&'trigger_count'in r.parameters?r.parameters.trigger_count:'1';recoveryCount.value=r&&'recovery_count'in r.parameters?r.parameters.recovery_count:'0';impact.value=false;busy.value=false;error.value='';receipt.value=null;version.value++}
const target=computed(()=>props.rule?`rule:${props.rule.id}`:`create:${kind.value}:${scope.value}`),blocked=computed(()=>{void version.value;try{return !session.me||alertsJournal.blocked(session.me.user.id,target.value)}catch{return true}})
const writable=computed(()=>session.me?.user.platform_role==='admin'&&!session.mfaPending&&!session.enrollmentRequired&&props.capabilities!==null&&!props.capabilities.recovery_mode&&props.rule?.state!=='retired')
function parameters():AlertParameters{if(kind.value==='channel_quarantined')return {};if(kind.value==='procurement_budget_low')return {threshold_amount:cnyToMicro(threshold.value),recovery_amount:cnyToMicro(recovery.value)};return {minimum_age_seconds:age.value,trigger_count:trigger.value,recovery_count:recoveryCount.value}}
async function save(retire=false){if(busy.value||!writable.value||blocked.value||receipt.value)return;if(props.rule&&!impact.value){error.value='请先确认配置变更与退役的影响。';return}const ticket=gate.ticket(),actor=session.me?.user.id,csrf=session.me?.csrf_token||'',rule=props.rule;if(!actor)return;busy.value=true;error.value='';try{const input=retire?{expected_revision:rule!.configuration_revision}:rule?{expected_revision:rule.configuration_revision,label:name.value,state:state.value,parameters:parameters()}:{kind:kind.value,scope_type:alertScope(kind.value),scope_id:scope.value,label:name.value,state:state.value,parameters:parameters()},command=await alertCommand(retire?'rule_retire':rule?'rule_configure':'rule_create',crypto.randomUUID(),actor,input,rule?.id||null,rule||undefined);if(!gate.accepts(ticket))return;const result=await submitAlertCommand(command,csrf,()=>gate.accepts(ticket));if(!gate.accepts(ticket)||!result)return;receipt.value=result;emit('changed')}catch(cause){if(gate.accepts(ticket)){error.value=errorText(cause);emit('changed')}}finally{if(gate.accepts(ticket)){busy.value=false;version.value++}}}
watch([context,()=>props.rule],reset,{immediate:true,flush:'sync'});onBeforeUnmount(()=>gate.dispose())
</script>
<template>
  <div class="alert-writer">
    <p class="dialog-description">{{ rule ? '修改名称、阈值或状态都会按当前配置版本保存。' : '为一个固定资源创建站内观察规则。新规则默认停用，可明确选择启用。' }}评估部署默认关闭；开启规则后仍需部署评估能力可用才会采样。</p>
    <el-alert v-if="!writable" title="当前为只读：规则写入需要平台管理员、有效认证和非恢复模式。" type="info" :closable="false"/>
    <el-alert v-if="blocked" title="此目标有结果待核查的原操作，不能换 ID 重发；请在原操作核查区查询。" type="warning" :closable="false"/>
    <el-alert v-if="error" :title="error" type="warning" :closable="false" show-icon/>
    <el-alert v-if="receipt" :title="receipt.status==='applied'?'原操作已应用；请关闭后查看刷新结果。':alertRejectionLabels[receipt.reason]" :type="receipt.status==='applied'?'success':'warning'" :closable="false"/>
    <p v-if="receipt" class="receipt-id mono">原操作 {{ receipt.operation_id }} · 结果版本 {{ receipt.result_revision ?? '无' }}</p>
    <el-form label-position="top" :disabled="busy||!writable||blocked||receipt!==null" @submit.prevent="save()">
      <div class="form-grid"><el-form-item label="规则种类"><el-select v-model="kind" :disabled="!!rule" aria-label="告警规则种类"><el-option v-for="k in alertKinds" :key="k" :value="k" :label="alertKindLabels[k]"/></el-select></el-form-item><el-form-item label="目标资源 UUID"><el-input v-model="scope" :disabled="!!rule" aria-label="告警目标资源 UUID"/><div class="field-hint">{{ alertScope(kind)==='channel'?'渠道':alertScope(kind)==='tenant'?'客户工作空间':'采购账户' }}；种类与目标在创建后不可更改。</div></el-form-item></div>
      <el-form-item label="规则名称"><el-input v-model="name" maxlength="160" aria-label="告警规则名称"/><div class="field-hint">1–80 个 Unicode 字符、最多 160 UTF-8 字节；不接受首尾空白或控制字符。</div></el-form-item>
      <el-form-item label="规则状态"><el-radio-group v-model="state"><el-radio-button value="disabled">停用</el-radio-button><el-radio-button value="enabled">启用</el-radio-button></el-radio-group></el-form-item>
      <template v-if="kind==='procurement_budget_low'"><div class="form-grid"><el-form-item label="触发阈值（CNY）"><el-input v-model="threshold" inputmode="decimal" aria-label="预算触发阈值"/></el-form-item><el-form-item label="恢复阈值（CNY）"><el-input v-model="recovery" inputmode="decimal" aria-label="预算恢复阈值"/></el-form-item></div><p class="field-hint">登记余量 ≤ 触发阈值时打开；登记余量严格大于恢复阈值时结束。恢复阈值须 ≥ 触发阈值，金额最多 6 位小数。</p></template>
      <template v-if="kind==='customer_reconciliation_aged'||kind==='procurement_unresolved_aged'"><el-form-item label="预占创建年龄（秒，600–604800）"><el-input v-model="age" inputmode="numeric" aria-label="告警创建年龄"/></el-form-item><div class="form-grid"><el-form-item label="触发计数（1–1000）"><el-input v-model="trigger" inputmode="numeric" aria-label="告警触发计数"/></el-form-item><el-form-item label="恢复计数（0–999）"><el-input v-model="recoveryCount" inputmode="numeric" aria-label="告警恢复计数"/></el-form-item></div><p class="field-hint">恢复计数须小于触发计数；只观察符合年龄的客户待核对或采购 unknown/disputed 预占。</p></template>
      <p v-if="kind==='channel_quarantined'" class="field-hint">无需阈值参数；仅当渠道持久隔离为 true 时触发，渠道停用本身不代表恢复。</p>
      <div v-if="rule" class="danger-note"><p>有效配置变化（包含名称）会结束当前发生记录并清空当前版本样本；完全不变的配置不结束记录。停用结束当前记录；退役为不可恢复终态，历史仍保留。</p><el-checkbox v-model="impact">我已核对上述影响及当前版本 {{ rule.configuration_revision }}</el-checkbox></div>
      <div class="writer-actions"><el-button v-if="rule" type="danger" plain :disabled="!impact" @click="save(true)">退役规则</el-button><el-button type="primary" :loading="busy" :disabled="!!rule&&!impact" native-type="submit">{{ rule?'保存完整配置':'创建规则' }}</el-button></div>
    </el-form>
  </div>
</template>
<style scoped>.alert-writer .el-alert{margin-bottom:15px}.writer-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:24px}.field-hint{margin-bottom:17px}.danger-note{margin-top:20px}.danger-note .el-checkbox{height:auto;white-space:normal;line-height:1.8;margin-top:10px}.receipt-id{margin:12px 0;overflow-wrap:anywhere}@media(max-width:520px){.writer-actions{flex-wrap:wrap}.writer-actions .el-button{margin:0}.danger-note :deep(.el-checkbox__label){white-space:normal}}</style>
