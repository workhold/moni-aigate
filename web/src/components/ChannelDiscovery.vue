<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { CopyDocument, Refresh, Search } from '@element-plus/icons-vue'
import { errorText, request } from '../api'
import { shortId, time } from '../format'
import { createDiscoveryScope, discoveryDetailPath, discoveryErrorID, discoveryFormats, discoveryHistoryPath, discoveryRequest, discoveryStatusText, parseDiscoveryMetadata, parseDiscoveryView } from '../discovery'
import type { DiscoveryChannel, DiscoveryFormat, DiscoveryMetadata, DiscoveryView } from '../discovery'

const props = defineProps<{ modelValue: boolean; channel: DiscoveryChannel | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const scope = createDiscoveryScope()
const format = ref<DiscoveryFormat>('openai')
const history = ref<DiscoveryMetadata[]>([]), selectedID = ref(''), result = ref<DiscoveryView | null>(null)
const error = ref(''), running = ref(false), historyBusy = ref(false), detailBusy = ref(false), search = ref('')
const nextCursor = ref(''), cursor = ref(''), cursors = ref<string[]>([])
const visible = computed({ get: () => props.modelValue, set: value => emit('update:modelValue', value) })
const canDiscover = computed(() => !!props.channel?.enabled && !props.channel?.quarantined)
const matchingModels = computed(() => result.value?.models.filter(model => model.toLowerCase().includes(search.value.toLowerCase())) ?? [])
const unbound = computed(() => new Set(result.value?.unbound ?? []))

async function loadHistory(page = '', select = '', autoLatest = false) {
  if (!props.channel || !props.modelValue) return
  const channelID = props.channel.id, ticket = scope.ticket('history'), selection = scope.snapshot('detail')
  historyBusy.value = true
  try {
    const response = await request<unknown[]>(discoveryHistoryPath(channelID, page))
    const records = (response.data ?? []).map(parseDiscoveryMetadata)
    if (records.some(record => record.channel_id !== channelID)) throw new Error('发现历史的渠道不匹配。')
    if (!scope.accepts(ticket)) return
    history.value = records; cursor.value = page; nextCursor.value = response.next_cursor ?? ''
    // A history refresh may restore its captured selection only while no newer
    // detail selection has happened during the fetch.
    if (scope.accepts(selection)) {
      if (select) await selectDiscovery(select)
      else if (autoLatest && records[0]) await selectDiscovery(records[0].id)
    }
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) historyBusy.value = false }
}
async function selectDiscovery(id: string) {
  if (!props.channel || !props.modelValue) return
  const channelID = props.channel.id, ticket = scope.ticket('detail')
  selectedID.value = id; result.value = null; detailBusy.value = true
  try {
    const response = await request<unknown>(discoveryDetailPath(channelID, id))
    const value = parseDiscoveryView(response.data)
    if (value.channel_id !== channelID || value.id !== id) throw new Error('发现记录的渠道或标识不匹配。')
    if (scope.accepts(ticket)) result.value = value
  } catch (e) { if (scope.accepts(ticket)) error.value = errorText(e) }
  finally { if (scope.accepts(ticket)) detailBusy.value = false }
}
async function discover() {
  if (!props.channel || !canDiscover.value || running.value) return
  // Invalidate pending history/detail reads before starting a new observation.
  scope.reset()
  const channelID = props.channel.id, ticket = scope.ticket('run')
  const operation = discoveryRequest(channelID, props.channel.current_version, format.value)
  running.value = true; error.value = ''; result.value = null; selectedID.value = ''; search.value = ''; cursors.value = []
  historyBusy.value = false; detailBusy.value = false
  try {
    const response = await request<unknown>(operation.path, operation.method, operation.body)
    const value = parseDiscoveryView(response.data)
    if (value.channel_id !== channelID) throw new Error('发现记录的渠道不匹配。')
    if (!scope.accepts(ticket)) return
    result.value = value; selectedID.value = value.id
    await loadHistory('', '', false)
  } catch (e) {
    if (!scope.accepts(ticket)) return
    error.value = errorText(e)
    await loadHistory('', discoveryErrorID(e), false)
  } finally { if (scope.accepts(ticket)) running.value = false }
}
async function refresh() {
  error.value = ''
  await loadHistory(cursor.value, selectedID.value)
}
async function next() { cursors.value.push(cursor.value); await loadHistory(nextCursor.value) }
async function previous() { await loadHistory(cursors.value.pop() ?? '') }
async function copy(value: string) { try { await navigator.clipboard.writeText(value); ElMessage.success('模型名称已复制。') } catch { ElMessage.warning('无法复制，请手动选择模型名称。') } }
watch(() => [props.modelValue, props.channel?.id, props.channel?.current_version], () => {
  scope.reset(); history.value = []; selectedID.value = ''; result.value = null; error.value = ''; search.value = ''; format.value = 'openai'
  running.value = false; historyBusy.value = false; detailBusy.value = false; nextCursor.value = ''; cursor.value = ''; cursors.value = []
  if (props.modelValue && props.channel) void loadHistory('', '', true)
}, { immediate: true })
</script>

<template>
  <el-dialog v-model="visible" :title="`${channel?.name || ''} · 发现模型`" width="960px" destroy-on-close :close-on-click-modal="false">
    <p class="dialog-description">读取该渠道的供应商模型目录，核对现有绑定与目录差异。仅发送目录 GET，不调用生成接口，不扣客户钱包。</p>
    <el-alert title="目录只证明供应商列出了模型，不代表协议、可用性或计费上界已经认证。发现结果不会自动创建、授权、定价、发布或删除模型。" type="info" :closable="false" show-icon />
    <el-alert v-if="!canDiscover" title="渠道已停用或隔离，可查看已有记录；恢复可用状态后才能重新发现。" type="warning" :closable="false" style="margin-top:14px" />
    <div class="discovery-controls">
      <el-select v-model="format" aria-label="供应商目录格式" :disabled="running" style="width:230px"><el-option v-for="option in discoveryFormats" :key="option.id" :value="option.id" :label="option.title" /></el-select>
      <el-button type="primary" :loading="running" :disabled="!canDiscover" @click="discover">读取模型目录</el-button>
      <span class="muted small-text">当前渠道版本 {{ shortId(channel?.current_version) }}</span>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon style="margin-bottom:16px" />
    <div class="discovery-controls">
      <el-select :model-value="selectedID || undefined" placeholder="选择发现记录" aria-label="发现历史" :loading="historyBusy" :disabled="running || historyBusy" style="flex:1;min-width:220px" @change="selectDiscovery"><el-option v-for="item in history" :key="item.id" :value="item.id" :label="`${time(item.created_at)} · ${item.format} · ${discoveryStatusText(item.status)}${item.stale ? ' · 已过时' : ''}`" /></el-select>
      <el-button :icon="Refresh" :loading="historyBusy" :disabled="running" @click="refresh">刷新记录</el-button>
      <el-button :disabled="!cursors.length || historyBusy || running" @click="previous">上一页</el-button>
      <el-button :disabled="!nextCursor || historyBusy || running" @click="next">下一页</el-button>
    </div>
    <div v-loading="detailBusy">
      <template v-if="result">
        <p class="small-text muted">记录 {{ result.id }} · {{ time(result.created_at) }} · {{ discoveryStatusText(result.status) }} · 目录格式 {{ result.format }} · 渠道版本 {{ shortId(result.channel_version_id) }}</p>
        <p class="field-hint">实际凭据身份：<span v-if="result.credential_id" class="mono">{{ result.credential_id }}</span><span v-else>此历史记录未返回凭据身份</span>。目录仅反映此次使用的成员权限，不代表整个凭据池。</p>
        <el-alert v-if="result.stale" title="此记录已过时：渠道版本、凭据状态或已发布绑定已变化。请重新发现，再按现有配置流程核实并发布。" type="warning" :closable="false" show-icon style="margin:14px 0" />
        <template v-if="result.status === 'completed'">
          <p>目录 {{ result.models.length }} 个模型 · {{ result.pages }} 页 · 未绑定 {{ result.unbound.length }} 个 · 已绑定 {{ result.present.length }} 条 · 目录缺失 {{ result.missing.length }} 条</p>
          <p class="field-hint">绑定差异基于本次发现开始时的已发布快照。一个上游模型可以对应多个公开模型；目录缺失仅供核查，不会触发下架或删除。</p>
          <el-tabs>
            <el-tab-pane label="模型目录 / 未绑定">
              <el-input v-model="search" :prefix-icon="Search" placeholder="搜索模型名称" clearable aria-label="搜索目录模型" style="margin:10px 0" />
              <el-table :data="matchingModels.map(model => ({ model }))" max-height="360" empty-text="目录中没有匹配模型"><el-table-column label="供应商模型名称" min-width="280"><template #default="{ row }"><span class="mono">{{ row.model }}</span></template></el-table-column><el-table-column label="开始时绑定状态" width="160"><template #default="{ row }"><el-tag :type="unbound.has(row.model) ? 'info' : 'success'">{{ unbound.has(row.model) ? '未绑定' : '已有绑定' }}</el-tag></template></el-table-column><el-table-column label="操作" width="85"><template #default="{ row }"><el-button link type="primary" :icon="CopyDocument" aria-label="复制供应商模型名称" @click="copy(row.model)">复制</el-button></template></el-table-column></el-table>
            </el-tab-pane>
            <el-tab-pane label="已绑定且目录存在"><el-table :data="result.present" max-height="360" empty-text="没有目录中存在的已发布绑定"><el-table-column prop="public_name" label="公开模型" min-width="180" /><el-table-column prop="upstream_model" label="上游模型" min-width="240" /><el-table-column prop="revision" label="路由版本" width="100" /><el-table-column label="模型状态" width="100"><template #default="{ row }"><el-tag :type="row.enabled === false ? 'info' : 'success'">{{ row.enabled === false ? '已停用' : '启用' }}</el-tag></template></el-table-column><el-table-column label="绑定渠道版本" min-width="160"><template #default="{ row }">{{ shortId(row.channel_version_id) }}</template></el-table-column></el-table></el-tab-pane>
            <el-tab-pane label="绑定存在但目录缺失"><el-alert title="供应商目录可能受账号权限或供应商策略影响。请人工核实；这里没有删除或停用操作。" type="warning" :closable="false" style="margin:10px 0" /><el-table :data="result.missing" max-height="360" empty-text="没有目录缺失的绑定"><el-table-column prop="public_name" label="公开模型" min-width="180" /><el-table-column prop="upstream_model" label="上游模型" min-width="240" /><el-table-column prop="revision" label="路由版本" width="100" /><el-table-column label="模型状态" width="100"><template #default="{ row }"><el-tag :type="row.enabled === false ? 'info' : 'success'">{{ row.enabled === false ? '已停用' : '启用' }}</el-tag></template></el-table-column><el-table-column label="绑定渠道版本" min-width="160"><template #default="{ row }">{{ shortId(row.channel_version_id) }}</template></el-table-column></el-table></el-tab-pane>
          </el-tabs>
        </template>
        <el-alert v-else-if="result.status === 'failed'" :title="`本次目录发现失败${result.error_code ? `（${result.error_code}）` : ''}，未保存部分模型作为成功结果。可以检查配置后重新发现。`" type="error" :closable="false" style="margin:16px 0" />
        <el-alert v-else :title="result.status === 'interrupted' ? '本次执行未留下完成结果，可能已中断。可以重新发起；不会恢复或重放生成请求。' : '本次目录读取尚未留下完成结果。可手动刷新记录；页面不会自动轮询或重发。'" type="info" :closable="false" style="margin:16px 0" />
      </template>
      <el-empty v-else-if="!detailBusy" description="选择历史记录，或读取当前渠道的模型目录。" />
    </div>
    <template #footer><el-button @click="visible = false">关闭</el-button></template>
  </el-dialog>
</template>

<style scoped>
.discovery-controls { display:flex; align-items:center; flex-wrap:wrap; gap:10px; margin:18px 0 }
</style>
