<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import ErrorNotice from './ErrorNotice.vue'
import { errorText, request } from '../api'
import { time } from '../format'
import { createReleaseQualificationScope, releaseNextHistory, releasePreviousHistory } from '../release-qualification-state'
import { parseReleaseEvidence, parseReleaseEvidenceList, releaseEvidenceDetailPath, releaseEvidencePath, releaseQualificationLabel } from '../release-qualification'
import type { ReleaseEvidence } from '../release-qualification'
const props=defineProps<{modelValue:boolean;channel?:{id:string;name:string}|null;model?:{id:string;public_name:string}|null}>()
const emit=defineEmits<{'update:modelValue':[boolean]}>()
const visible=computed({get:()=>props.modelValue,set:value=>emit('update:modelValue',value)})
const filter=computed(()=>props.channel?{channelID:props.channel.id}:props.model?{modelID:props.model.id}:{})
const identity=computed(()=>props.channel?`channel:${props.channel.id}`:props.model?`model:${props.model.id}`:'none')
const title=computed(()=>props.channel?.name||props.model?.public_name||'发布资格证据')
const scope=createReleaseQualificationScope(),rows=ref<ReleaseEvidence[]>([]),selected=ref<ReleaseEvidence|null>(null)
const loading=ref(false),detailLoading=ref(false),error=ref(''),nextCursor=ref(''),cursor=ref(''),cursors=ref<string[]>([])
async function load(page='',preserve=false,history?:string[]){
  if(!props.modelValue||(!props.channel&&!props.model))return
  const ticket=scope.ticket('list');loading.value=true;error.value='';if(!preserve)selected.value=null
  try{const response=await request<unknown[]>(releaseEvidencePath(filter.value,page));const parsed=parseReleaseEvidenceList(response.data,response.next_cursor??'');if(!scope.accepts(ticket))return;rows.value=parsed.rows;nextCursor.value=parsed.next;cursor.value=page;if(history!==undefined)cursors.value=history}
  catch(e){if(scope.accepts(ticket))error.value=errorText(e)}finally{if(scope.accepts(ticket))loading.value=false}
}
async function select(row:ReleaseEvidence){
  const ticket=scope.ticket('detail');detailLoading.value=true;error.value='';selected.value=null
  try{const response=await request<unknown>(releaseEvidenceDetailPath(row.id));const value=parseReleaseEvidence(response.data);if(scope.accepts(ticket))selected.value=value}
  catch(e){if(scope.accepts(ticket))error.value=errorText(e)}finally{if(scope.accepts(ticket))detailLoading.value=false}
}
async function next(){const history=releaseNextHistory(cursors.value,cursor.value);await load(nextCursor.value,false,history)}
async function previous(){const page=releasePreviousHistory(cursors.value);await load(page.cursor,false,page.history)}
watch([visible,identity],([open])=>{scope.reset();rows.value=[];selected.value=null;error.value='';nextCursor.value='';cursor.value='';cursors.value=[];loading.value=false;detailLoading.value=false;if(open)void load()},{immediate:true})
onBeforeUnmount(scope.reset)
</script>
<template>
  <el-dialog v-model="visible" :title="`${title} · 发布资格证据`" width="96%" class="release-dialog" destroy-on-close :close-on-click-modal="false" data-testid="release-evidence-dialog">
    <el-alert title="只读证据登记：Mock、外部流程或一次探测都不是真实供应商或生产资格。stale 仅表示不是当前管理版本，不改写历史路由。" type="info" :closable="false" show-icon />
    <div class="release-toolbar"><span class="muted small-text">当前页 {{ rows.length }} 条</span><el-button :icon="Refresh" :loading="loading" @click="load(cursor,true)">刷新</el-button></div>
    <ErrorNotice :message="error" @retry="load(cursor,true)" />
    <el-table v-loading="loading" :data="rows" empty-text="暂无证据记录；未记录不代表通过。" data-testid="release-evidence-list">
      <el-table-column label="供应商 / 上游模型" min-width="210"><template #default="{row}"><strong>{{row.supplier}}</strong><div class="mono release-ref">{{row.upstream_model}}</div></template></el-table-column>
      <el-table-column prop="protocol" label="协议" min-width="160" />
      <el-table-column label="资格标签" min-width="230"><template #default="{row}"><el-tag :type="row.qualification==='supplier_profile_qualified'?'success':'warning'">{{releaseQualificationLabel(row.qualification)}}</el-tag><div v-if="row.stale||row.withdrawn" class="release-flags"><el-tag v-if="row.stale" type="info">非当前版本</el-tag><el-tag v-if="row.withdrawn" type="danger">已撤回</el-tag></div></template></el-table-column>
      <el-table-column label="观察时刻" min-width="175"><template #default="{row}">{{time(row.observed_at)}}</template></el-table-column>
      <el-table-column label="操作" width="80" fixed="right"><template #default="{row}"><el-button link type="primary" @click="select(row)">详情</el-button></template></el-table-column>
    </el-table>
    <div class="table-footer"><span>无登记、无资格按钮</span><div><el-button size="small" :disabled="!cursors.length||loading" @click="previous">上一页</el-button><el-button size="small" :disabled="!nextCursor||loading" @click="next">下一页</el-button></div></div>
    <section v-loading="detailLoading" class="release-detail" data-testid="release-evidence-detail">
      <el-empty v-if="!selected&&!detailLoading" description="选择一条不可变证据查看安全元数据。" :image-size="70" />
      <template v-else-if="selected">
        <h3>{{selected.supplier}} · {{selected.upstream_model}}</h3>
        <el-alert v-if="selected.withdrawn" :title="`已撤回：${selected.withdrawal_reason} · ${time(selected.withdrawn_at)}`" type="error" :closable="false" />
        <dl class="detail-list">
          <dt>证据 ID</dt><dd class="mono release-ref">{{selected.id}}</dd><dt>作用域</dt><dd class="mono release-ref">{{selected.channel_version_id}} / {{selected.profile_version}} / {{selected.variant_key}}</dd>
          <dt>证据提交</dt><dd class="mono release-ref">{{selected.evidence_commit}}</dd><dt>证据引用 / SHA-256</dt><dd class="mono release-ref">{{selected.evidence_ref}}<br>{{selected.evidence_sha256}}</dd>
          <dt>来源</dt><dd>{{selected.source_mode}} · {{selected.source_repository}}<br><span class="mono release-ref">{{selected.source_commit}}</span></dd><dt>许可与修改</dt><dd class="mono release-ref">{{selected.license_ref}}<br>{{selected.notice_ref}}<br>{{selected.modification_ref}}</dd>
          <dt>运行 / 用量 / 重试 / 成本</dt><dd class="mono release-ref">{{selected.boundary_ref}}<br>{{selected.usage_ref}}<br>{{selected.retry_ref}}<br>{{selected.cost_ref}}（{{selected.cost_status}}）</dd>
          <dt>凭据覆盖</dt><dd><span v-if="!selected.credential_ids.length">无 direct 凭据覆盖</span><span v-for="credential in selected.credential_ids" :key="credential" class="mono release-ref release-credential">{{credential}}</span></dd>
          <dt>未关闭问题</dt><dd><span v-if="!selected.open_issue_codes.length">无</span><el-tag v-for="code in selected.open_issue_codes" :key="code" type="warning" style="margin:2px">{{code}}</el-tag></dd>
        </dl>
      </template>
    </section>
    <template #footer><el-button @click="visible=false">关闭</el-button></template>
  </el-dialog>
</template>
<style scoped>
.release-dialog{max-width:1100px}.release-toolbar{display:flex;justify-content:space-between;align-items:center;margin:14px 0}.release-flags{margin-top:6px;display:flex;gap:5px;flex-wrap:wrap}.release-detail{margin-top:18px;border-top:1px solid #e4ebef;padding-top:14px;min-height:90px}.release-ref{overflow-wrap:anywhere;word-break:break-all}.release-credential{display:block;margin-bottom:4px}@media(max-width:650px){.release-toolbar{align-items:flex-start;gap:10px}.release-detail :deep(.detail-list){grid-template-columns:1fr}.release-detail :deep(.detail-list) dt{margin-top:8px}}
</style>
