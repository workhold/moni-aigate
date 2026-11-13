<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, Search, InfoFilled } from '@element-plus/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import ChannelDiscovery from '../components/ChannelDiscovery.vue'
import CredentialPool from '../components/CredentialPool.vue'
import ChannelOperations from '../components/ChannelOperations.vue'
import RouteEditor from '../components/RouteEditor.vue'
import CostEditor from '../components/CostEditor.vue'
import PricingWriter from '../components/PricingWriter.vue'
import ModelPublicationEditor from '../components/ModelPublicationEditor.vue'
import ModelAliases from '../components/ModelAliases.vue'
import { parsePublication, publicationPath, publicationLabel } from '../public-catalog'
import { keyAccessContext } from '../key-access-policy'
import { errorText, request, stableOperationKey } from '../api'
import { cnyToMicro, integer, label, money, shortId, tagType, time } from '../format'
import { isAdmin, isApprover, session } from '../session'
import { protocolInfo, protocols } from '../inference'
import { modelGenerationSettings } from '../model-contract'
import { parsePriceVersion } from '../pricing'
import { parseCostVersion } from '../cost-aware-routing'
import { parseTextRetailQuantitiesWire } from '../metering-wire'
import type { DiscoveryChannel } from '../discovery'
import { createChannelOperationsScope, selectedChannelOperationTargets } from '../channel-operations'
import type { ChannelOperationAction, ChannelOperationTarget } from '../channel-operations'
import channelDefaults from '../channel-defaults.json'

type Row = Record<string, unknown>
type InputValues = Record<string, string | number | boolean | string[] | null | undefined>
type FieldKind = 'text' | 'password' | 'money' | 'integer' | 'select' | 'multi' | 'date' | 'textarea' | 'check'
interface Field { key: string; title: string; kind?: FieldKind; required?: boolean; hint?: string; placeholder?: string; source?: 'tenants' | 'models' | 'channels'; options?: { label: string; value: string }[]; initial?: string | number | boolean | string[]; min?: number }
interface Column { key: string; title: string; type?: 'money' | 'date' | 'status' | 'id' | 'bool' | 'integer'; width?: number; minWidth?: number }
interface Config { title: string; eyebrow: string; description: string; endpoint: string; columns: Column[]; fields?: Field[]; createTitle?: string; note?: string; finance?: boolean }
const route = useRoute(); const resource = computed(() => String(route.params.resource))
const canCreateFinance = computed(() => session.me?.user.platform_role === 'finance_operator')
const modelFields: Field[] = [{ key: 'public_name', title: '对外模型名称', required: true, placeholder: '例如：gpt-team' }, { key: 'protocol', title: '调用协议', kind: 'select', required: true, initial: 'openai_chat', options: protocols.map(p => ({ label: p.title, value: p.id })), hint: '每个模型使用固定协议；路由池中的所有渠道须支持该协议，不进行跨协议转换。' }, { key: 'context_limit', title: '总上下文窗口（Tokens）', kind: 'integer', required: true, initial: 8192, min: 1 }, { key: 'input_token_upper_bound', title: '计费输入硬上界（可选）', kind: 'integer', min: 1, placeholder: '留空使用完整上下文窗口', hint: '没有单独的供应商输入硬界时，使用完整上下文窗口，不要减去输出上限。两个独立上界可能保守过预占；更小输入界须有供应商强制保证，不能填写近似分词估算。' }, { key: 'output_limit', title: '最大计费输出（Tokens）', kind: 'integer', required: true, initial: 2048, min: 1 }, { key: 'default_output_limit', title: '默认输出上限（Tokens）', kind: 'integer', required: true, initial: 512, min: 1 }, { key: 'supports_tools', title: '支持客户端函数工具转发', kind: 'check', initial: false }]
const configs: Record<string, Config> = {
  tenants: { title: '客户与成员', eyebrow: 'TENANT MANAGEMENT', description: '邀请客户、分配模型授权与成员角色，数据访问始终限定在租户内。', endpoint: '/platform/tenants', createTitle: '创建客户', columns: [{ key: 'name', title: '客户名称', minWidth: 180 }, { key: 'owner_email', title: '所有者邮箱', minWidth: 210 }, { key: 'status', title: '状态', type: 'status', width: 100 }, { key: 'created_at', title: '创建时间', type: 'date', minWidth: 180 }], fields: [{ key: 'name', title: '客户 / 工作空间名称', required: true }, { key: 'owner_email', title: '所有者邮箱', required: true }, { key: 'owner_password', title: '初始密码', kind: 'password', required: true, hint: '至少 12 位。请通过安全渠道单独交付，不要放在公开聊天或工单。' }, { key: 'model_ids', title: '授权模型', kind: 'multi', source: 'models', initial: [], hint: '空集合表示无模型权限，不会默认为全部。' }] },
  users: { title: '平台账号', eyebrow: 'PLATFORM IDENTITIES', description: '分别授予管理员、财务制单与财务审批职责。', endpoint: '/platform/users', createTitle: '创建平台账号', columns: [{ key: 'email', title: '邮箱', minWidth: 240 }, { key: 'platform_role', title: '平台角色', type: 'status', minWidth: 150 }, { key: 'mfa_enabled', title: 'MFA', type: 'bool', width: 100 }, { key: 'status', title: '状态', type: 'status', width: 100 }, { key: 'created_at', title: '创建时间', type: 'date', minWidth: 180 }], fields: [{ key: 'email', title: '邮箱', required: true }, { key: 'password', title: '初始密码', kind: 'password', required: true }, { key: 'platform_role', title: '平台角色', kind: 'select', required: true, initial: 'finance_operator', options: [{ label: '平台管理员', value: 'admin' }, { label: '财务制单', value: 'finance_operator' }, { label: '财务审批', value: 'finance_approver' }] }], note: '平台管理员不默认拥有财务权限。同一笔核款或退款不能由同一个账号制单并批准。' },
  channels: { title: '上游渠道', eyebrow: 'PROVIDER CONNECTIONS', description: '安全管理平台共享渠道，凭据加密保存且不会在查询中回显。', endpoint: '/platform/channels', createTitle: '接入渠道', columns: [{ key: 'name', title: '渠道名称', minWidth: 170 }, { key: 'base_url', title: '接口地址', minWidth: 240 }, { key: 'region', title: '数据地域', width: 120 }, { key: 'enabled', title: '启用', type: 'bool', width: 85 }, { key: 'quarantined', title: '隔离', type: 'bool', width: 85 }], fields: [{ key: 'name', title: '渠道名称', required: true }, { key: 'base_url', title: '上游基础 URL', required: true, placeholder: 'https://api.example.com/v1', hint: '填写含版本的基础路径，如 OpenAI/Anthropic 的 /v1 或 Gemini 的 /v1beta；按模型协议追加端点。仅访问受控出口，重定向不会跟随。' }, { key: 'secret', title: '供应商 API 密钥', kind: 'password', required: true }, { key: 'region', title: '数据地域', required: true, initial: channelDefaults.region, hint: 'global 是与新客户和 API Key 默认授权一致的精确地域标签，不是通配符，也不保证数据驻留。使用 cn 等其它标签前，须先配置客户与密钥的对应授权，并核实供应商地域。' }, { key: 'allowed_cidrs', title: '显式允许的私网 CIDR', kind: 'textarea', hint: '可留空。每行一个 CIDR；仅在确实需要私网渠道时填写。', initial: '' }], note: '渠道更新将创建新版本。旧请求保留原配置；撤销或停用凭据会阻止后续尝试准入。探测可能产生真实供应商费用。' },
  catalog: { title: '模型与路由', eyebrow: 'MODEL ROUTING', description: '发布明确的能力合同，为同一逻辑模型配置优先级、权重与容量。', endpoint: '/platform/models', createTitle: '发布模型', columns: [{ key: 'public_name', title: '统一模型名称', minWidth: 190 }, { key: 'protocol', title: '调用协议', minWidth: 170 }, { key: 'billing_mode', title: '计费方式', minWidth: 140 }, { key: 'enabled', title: '启用', type: 'bool', width: 90 }, { key: 'active_revision', title: '路由版本', type: 'integer', width: 110 }, { key: 'context_limit', title: '上下文上界', type: 'integer', minWidth: 120 }, { key: 'output_limit', title: '最大输出', type: 'integer', minWidth: 120 }], fields: modelFields, note: 'Chat、Responses、Messages 支持文本、SSE 与基础函数工具转发；Gemini 当前仅无状态纯文本与 SSE。另支持仅输入计费的 Embeddings 与 Jina Rerank。发布前须验证渠道协议、可信 usage 与整个请求的输入硬上界；托管工具、媒体和有状态会话须另行支持。' },
  prices: { title: '售价版本', eyebrow: 'RETAIL PRICING', description: '客户售价按版本生效，跨渠道回退不改变已接受请求的售价。', endpoint: '/prices', createTitle: '新建售价版本', columns: [{ key: 'model_name', title: '模型', minWidth: 190 }, { key: 'schema_id', title: 'Schema / default', minWidth: 280 }, { key: 'rates', title: '完整费率向量', minWidth: 300 }, { key: 'effective_at', title: '生效时间', type: 'date', minWidth: 190 }], note: '费率列表直接展示不可变完整 RatesWire；页面不从向量重建或隐藏旧标量价格。' },
  'cost-versions': { title: '渠道成本版本', eyebrow: 'PROVIDER COST VERSIONS', description: '按渠道与上游模型维护独立成本依据，新版本追加保存。', endpoint: '/platform/cost-versions', createTitle: '新增成本版本', columns: [{ key: 'channel_name', title: '渠道', minWidth: 180 }, { key: 'upstream_model', title: '上游模型', minWidth: 180 }, { key: 'schema_id', title: 'Schema / default', minWidth: 280 }, { key: 'basis', title: '成本依据', type: 'status', minWidth: 110 }, { key: 'rates', title: '完整费率向量', minWidth: 300 }, { key: 'effective_at', title: '生效时间', type: 'date', minWidth: 180 }], note: '历史成本版本不可原地修改。known 版本显示 supplier RatesWire，unknown 版本明确没有费率；页面不把 unknown 当作免费。' },
  credits: { title: '人工核款', eyebrow: 'FUNDING OPERATIONS', description: '先核实真实收款，再由不同财务账号审批入账。', endpoint: '/platform/credits', createTitle: '录入核款单', finance: true, columns: [{ key: 'business_ref', title: '收款业务号', minWidth: 185 }, { key: 'tenant_id', title: '客户', type: 'id', minWidth: 150 }, { key: 'amount', title: '入账金额', type: 'money', minWidth: 150 }, { key: 'state', title: '状态', type: 'status', width: 105 }, { key: 'requester', title: '制单人', type: 'id', minWidth: 140 }, { key: 'created_at', title: '创建时间', type: 'date', minWidth: 180 }], fields: [{ key: 'tenant_id', title: '客户', kind: 'select', source: 'tenants', required: true }, { key: 'business_ref', title: '真实收款业务号', required: true, hint: '必须与收款凭证一致；重复业务号不会再次入账。' }, { key: 'amount', title: '已核实收款金额（CNY）', kind: 'money', required: true }, { key: 'proof_ref', title: '受控财务凭证引用', required: true, hint: '填写受控存储引用或凭证编号，不要填写公开的秘密下载链接。' }], note: '这是人工核款流程，不是在线支付。只有审批入账成功后，客户可用余额才会增加。' },
  refunds: { title: '消费退款', eyebrow: 'WALLET REFUNDS', description: '绑定原消费，追加补偿流水，退款累计不能超过实际收费。', endpoint: '/platform/refunds', createTitle: '申请钱包退款', finance: true, columns: [{ key: 'id', title: '退款单', type: 'id', minWidth: 155 }, { key: 'tenant_id', title: '客户', type: 'id', minWidth: 150 }, { key: 'settlement_id', title: '原消费结算单', type: 'id', minWidth: 160 }, { key: 'amount', title: '退款金额', type: 'money', minWidth: 145 }, { key: 'state', title: '状态', type: 'status', width: 100 }, { key: 'reason', title: '原因', minWidth: 190 }], fields: [{ key: 'tenant_id', title: '客户', kind: 'select', source: 'tenants', required: true }, { key: 'settlement_id', title: '原消费结算单 ID', required: true, hint: '可由客户调用明细中的结算单 ID 确认，不使用请求 ID 代替。' }, { key: 'amount', title: '退回钱包金额（CNY）', kind: 'money', required: true }, { key: 'reason', title: '退款原因', kind: 'textarea', required: true }], note: '此操作只退回平台钱包，不代表现金原路退款。现金退款走线下财务流程，需另行记录并核对。' },
  reconciliations: { title: '收费核对', eyebrow: 'BILLING RECONCILIATION', description: '按可信证据处理异常费用，不按估算或最大预占扣款。', endpoint: '/platform/reconciliations', finance: true, columns: [{ key: 'request_id', title: '请求 ID', type: 'id', minWidth: 170 }, { key: 'tenant_id', title: '客户', type: 'id', minWidth: 150 }, { key: 'reason', title: '核对原因', minWidth: 190 }, { key: 'status', title: '状态', type: 'status', width: 120 }, { key: 'deadline', title: '资金截止', type: 'date', minWidth: 180 }], note: '24 小时截止尚未结算，一律释放客户冻结。可信用量提前保存也不延长截止；迟到证据仅修正平台成本。新增客户收费须由不同财务账号批准。' },
  audit: { title: '操作审计', eyebrow: 'AUDIT TRAIL', description: '查看权限、配置与资金变更的操作者和关联业务。', endpoint: '/platform/audit', columns: [{ key: 'created_at', title: '时间', type: 'date', minWidth: 180 }, { key: 'actor', title: '操作者', type: 'id', minWidth: 160 }, { key: 'action', title: '操作', minWidth: 200 }, { key: 'target', title: '目标', type: 'id', minWidth: 180 }, { key: 'tenant_scope', title: '租户范围', type: 'id', minWidth: 155 }], note: '审计不展示原始模型正文、明文凭据或供应商错误响应。资金与成功敏感变更审计随业务事务持久化。' },
}
const config = computed(() => configs[resource.value])
const rows = ref<Row[]>([]); const loading = ref(false); const error = ref(''); const search = ref(''); const nextCursor = ref(''); const cursors = ref<string[]>([])
const lookups = ref<{ tenants: Row[]; models: Row[]; channels: Row[] }>({ tenants: [], models: [], channels: [] })
const filtered = computed(() => rows.value.filter(row => !search.value || config.value?.columns.some(col => String(cell(row, col.key) ?? '').toLowerCase().includes(search.value.toLowerCase()))))
const listScope = createChannelOperationsScope()
const channelTable = ref<{ clearSelection(): void } | null>(null), selectedChannelIDs = ref<string[]>([]), selectionEpoch = ref(0)
const operationsOpen = ref(false), operationsMode = ref<'preview' | 'history'>('preview'), operationsAction = ref<ChannelOperationAction>('disable')
const operationsTargets = ref<ChannelOperationTarget[]>([]), operationsNames = ref<Record<string, string>>({})
function clearChannelSelection() { selectedChannelIDs.value = []; selectionEpoch.value++; channelTable.value?.clearSelection() }
function channelSelectionChanged(selection: Row[]) {
  if (resource.value !== 'channels' || !isAdmin.value) return
  const visible = new Set(filtered.value.map(row => String(row.id)))
  const selected = [...new Set(selection.map(row => String(row.id)).filter(id => visible.has(id)))].sort()
  if (selected.join(',') !== selectedChannelIDs.value.join(',')) { selectedChannelIDs.value = selected; selectionEpoch.value++ }
}
function openChannelOperation(action: ChannelOperationAction) {
  if (!isAdmin.value || resource.value !== 'channels' || loading.value) return
  try {
    operationsTargets.value = selectedChannelOperationTargets(filtered.value, selectedChannelIDs.value)
    operationsNames.value = Object.fromEntries(filtered.value.filter(row => selectedChannelIDs.value.includes(String(row.id))).map(row => [String(row.id), String(row.name)]))
    operationsAction.value = action; operationsMode.value = 'preview'; operationsOpen.value = true
  } catch (e) { ElMessage.error(errorText(e)) }
}
function openChannelOperationHistory() { operationsTargets.value = []; operationsNames.value = {}; operationsMode.value = 'history'; operationsOpen.value = true }
function channelOperationApplied() { if (resource.value === 'channels') void load() }
watch(search, clearChannelSelection, { flush: 'sync' })
watch(isAdmin, () => { clearChannelSelection(); operationsOpen.value = false; listScope.reset() })
const dialog = ref(false); const dialogTitle = ref(''); const fields = ref<Field[]>([]); const values = ref<InputValues>({}); const savePath = ref(''); const saveMethod = ref('POST'); const saving = ref(false); const formError = ref('')
const reconciliationSchemaID = ref('')
const modelForm = computed(() => savePath.value === '/platform/models')
const inputOnlyModel = computed(() => modelForm.value && !!protocolInfo(values.value.protocol)?.inputOnly)
const geminiModel = computed(() => modelForm.value && values.value.protocol === 'gemini_generate_content')
const visibleFields = computed(() => fields.value.filter(field => (!inputOnlyModel.value || !['output_limit', 'default_output_limit', 'supports_tools'].includes(field.key)) && (!geminiModel.value || field.key !== 'supports_tools')).map(field => inputOnlyModel.value && field.key === 'context_limit' ? { ...field, title: '整个请求的输入容量（Tokens）', hint: '须覆盖整个文本批次或 query 与全部 documents 的聚合计费 Tokens，不是单条文本或文档的窗口。' } : inputOnlyModel.value && field.key === 'input_token_upper_bound' ? { ...field, hint: '留空使用整个请求的输入容量。更小上界必须由供应商保证；不能将单条文本的限制当作整个批次的上界，也不能使用近似分词估算。' } : geminiModel.value && field.key === 'public_name' ? { ...field, hint: 'Gemini 模型名称限 1 至 100 位 ASCII 字母、数字、点、短横线或下划线，以字母或数字开头，不能包含连续点。' } : geminiModel.value && field.key === 'output_limit' ? { ...field, hint: '供应商须保证 maxOutputTokens 约束全部计费输出，包括候选与思考 Tokens。' } : field))
const detailRow = ref<Row | null>(null); const detailOpen = ref(false)
const detailFields = computed(() => {
  const allowed = new Set([...(config.value?.columns.map(c => c.key) || []), 'id', 'business_ref', 'tenant_id', 'state', 'status', 'amount', 'request_id', 'settlement_id', 'reason', 'requester', 'requester_id', 'approver', 'approver_id', 'journal_id', 'created_at', 'approved_at', 'effective_at', 'deadline', 'action', 'actor', 'actor_id', 'target', 'target_type', 'tenant_scope', 'request_id', 'sanitized_diff'])
  return Object.entries(detailRow.value || {}).filter(([key]) => allowed.has(key))
})
const memberTenant = ref<Row | null>(null); const members = ref<Row[]>([]); const membersOpen = ref(false); const membersBusy = ref(false); const membersError = ref('')
const routeModel = ref<Row | null>(null); const routeOpen = ref(false); const costOpen = ref(false); const priceOpen = ref(false)
const publicationModel = ref<{ id: string; public_name: string } | null>(null), publicationOpen = ref(false)
const aliasModel = ref<{ id: string; public_name: string } | null>(null), aliasOpen = ref(false)
const platformContext = computed(() => keyAccessContext(session.me?.user.id, session.me?.tenant?.id, session.me?.user.platform_role))
const discoveryChannel = ref<DiscoveryChannel | null>(null); const discoveryOpen = ref(false)
const credentialChannel = ref<DiscoveryChannel | null>(null); const credentialOpen = ref(false)
function manageCredentials(row: Row) {
  credentialChannel.value = { id: String(row.id), name: String(row.name), current_version: String(row.current_version) }
  credentialOpen.value = true
}
function discoverModels(row: Row) {
  discoveryChannel.value = { id: String(row.id), name: String(row.name), current_version: String(row.current_version), enabled: row.enabled === true, quarantined: row.quarantined === true }
  discoveryOpen.value = true
}

function cell(row: Row, key: string): unknown {
  if (key === 'model_name') return row.model_name || lookups.value.models.find(m => m.id === row.model_id)?.public_name || row.model_id
  if (key === 'channel_name') return row.channel_name || lookups.value.channels.find(c => c.id === row.channel_id)?.name || row.channel_id
  if (key === 'protocol') return protocolInfo(row.protocol ?? (row.profile as Row | undefined)?.protocol)?.title || '未知协议'
  if (key === 'billing_mode') return protocolInfo(row.protocol ?? (row.profile as Row | undefined)?.protocol)?.inputOnly ? '仅输入计费' : '输入与输出计费'
  if (key === 'context_limit' || key === 'output_limit') return row[key] ?? (row.profile as Row | undefined)?.[key]
  if (key === 'active_revision') return row.active_revision ?? row.revision ?? 0
  if (key === 'region') return row.region ?? row.data_region
  if (key === 'state') return row.state ?? row.status
  if (key === 'status') return row.status ?? row.state
  if (key === 'actor') return row.actor ?? row.actor_id
  if (key === 'requester') return row.requester ?? row.requester_id
  if (key === 'tenant_id') return row.tenant_name || lookups.value.tenants.find(t => t.id === row.tenant_id)?.name || row.tenant_id
  if (key === 'rates') return row.rates && typeof row.rates === 'object' ? JSON.stringify(row.rates) : '未知成本'
  return row[key]
}
function parseReadRows(resourceName: string, value: unknown): Row[] {
  if (!Array.isArray(value)) throw new Error('管理列表响应无效。')
  if (resourceName === 'prices') return value.map(parsePriceVersion) as unknown as Row[]
  if (resourceName === 'cost-versions') return value.map(parseCostVersion) as unknown as Row[]
  return value as Row[]
}
async function load(page = cursors.value.at(-1) || '') {
  if (!config.value) return
  const ticket = listScope.ticket('list'), endpoint = config.value.endpoint
  clearChannelSelection()
  loading.value = true; error.value = ''
  try { const res = await request<unknown>(`${endpoint}${page ? `?cursor=${encodeURIComponent(page)}` : ''}`); if (listScope.accepts(ticket)) { rows.value = parseReadRows(resource.value, res.data); nextCursor.value = res.next_cursor || '' } }
  catch (e) { if (listScope.accepts(ticket)) error.value = errorText(e) } finally { if (listScope.accepts(ticket)) loading.value = false }
}
async function lookup() {
  const ticket = listScope.ticket('lookup')
  const tasks = [] as Promise<void>[]
  if (['tenants', 'catalog', 'prices'].includes(resource.value)) tasks.push(request<Row[]>('/platform/models').then(r => { if (listScope.accepts(ticket)) lookups.value.models = r.data || [] }))
  if (['credits', 'refunds'].includes(resource.value)) tasks.push(request<Row[]>('/platform/tenants').then(r => { if (listScope.accepts(ticket)) lookups.value.tenants = r.data || [] }))
  if (['catalog', 'cost-versions'].includes(resource.value)) tasks.push(request<Row[]>('/platform/channels').then(r => { if (listScope.accepts(ticket)) lookups.value.channels = r.data || [] }))
  const result = await Promise.allSettled(tasks)
  const failed = result.find(r => r.status === 'rejected')
  if (listScope.accepts(ticket) && failed?.status === 'rejected') error.value ||= `选项加载失败：${errorText(failed.reason)}`
}
function options(field: Field) { return field.options || (field.source ? lookups.value[field.source].map(row => ({ label: String(row.public_name || row.name || row.id), value: String(row.id) })) : []) }
function openForm(title: string, inputFields: Field[], path: string, method = 'POST', initial?: InputValues) {
  dialogTitle.value = title; fields.value = inputFields; savePath.value = path; saveMethod.value = method; formError.value = ''
  values.value = Object.fromEntries(inputFields.map(f => [f.key, initial?.[f.key] ?? f.initial ?? (f.kind === 'multi' ? [] : f.kind === 'check' ? false : f.kind === 'integer' && !f.required ? undefined : '')]))
  dialog.value = true
}
function create() {
  if (resource.value === 'cost-versions' && isAdmin.value) { costOpen.value = true; return }
  if (resource.value === 'prices' && isAdmin.value) { priceOpen.value = true; return }
  if (!config.value?.fields) return
  openForm(config.value.createTitle || '新建', config.value.fields, config.value.endpoint)
}
async function save() {
  saving.value = true; formError.value = ''
  try {
    const body: Record<string, unknown> = {}
    for (const field of visibleFields.value) {
      const raw = values.value[field.key]
      if (field.required && (raw === '' || raw === undefined || raw === null || (Array.isArray(raw) && !raw.length))) throw new Error(`请填写${field.title}。`)
      if (field.kind === 'money') body[field.key] = cnyToMicro(String(raw))
      else if (field.kind === 'integer') { if (!field.required && (raw === '' || raw === undefined || raw === null)) continue; const v = Number(raw); if (!Number.isSafeInteger(v) || (field.min !== undefined && v < field.min)) throw new Error(`${field.title}须为有效整数。`); body[field.key] = v }
      else if (field.kind === 'date') { const date = new Date(String(raw)); if (Number.isNaN(date.getTime())) throw new Error('请选择有效的生效时间。'); body[field.key] = date.toISOString() }
      else if (field.key === 'retail_quantities') { let parsed: unknown; try { parsed = JSON.parse(String(raw)) } catch { throw new Error('retail_quantities 必须是有效 JSON。') } body[field.key] = parseTextRetailQuantitiesWire(parsed, reconciliationSchemaID.value) }
      else if (field.key === 'allowed_cidrs') body[field.key] = String(raw || '').split(/[\n,]+/).map(s => s.trim()).filter(Boolean)
      else if (field.kind === 'password' && !field.required && !raw) continue
      else if (field.kind === 'password') body[field.key] = raw
      else body[field.key] = typeof raw === 'string' ? raw.trim() : raw
    }
    if (modelForm.value) Object.assign(body, modelGenerationSettings(values.value.protocol, values.value.output_limit, values.value.default_output_limit, values.value.supports_tools))
    const financial = ['/platform/credits', '/platform/refunds', '/platform/reconciliations'].some(path => savePath.value === path || savePath.value.startsWith(`${path}/`))
    await request(savePath.value, saveMethod.value, body, financial ? stableOperationKey(savePath.value, body) : undefined)
    dialog.value = false; values.value = {}; ElMessage.success('操作已完成。'); await load()
    if (membersOpen.value && memberTenant.value) await loadMembers(memberTenant.value)
  } catch (e) { formError.value = errorText(e) } finally { saving.value = false }
}
async function toggle(row: Row) {
  const originalResource = resource.value, originalContext = platformContext.value
  const kind = resource.value === 'catalog' ? 'models' : resource.value
  const body = resource.value === 'tenants' ? { status: row.status === 'active' ? 'suspended' : 'active' } : { enabled: !row.enabled }
  let publicNote = ''
  if (originalResource === 'catalog' && body.enabled === true) {
    try { const result = await request<unknown>(publicationPath(String(row.id))); const publication = parsePublication(result.data, String(row.id)); if (publication.public_visible) publicNote = '此模型保留了公开标记，重新启用后将恢复在匿名价格目录展示。' }
    catch (e) { ElMessage.error(errorText(e)); return }
  }
  if (originalResource !== resource.value || originalContext !== platformContext.value) return
  try { await ElMessageBox.confirm(`确认${body.enabled === false || body.status === 'suspended' ? '停用' : '启用'}「${row.name || row.public_name}」？${publicNote}已接受请求仍按既定规则处理。`, '更改可用状态', { type: 'warning', confirmButtonText: '确认变更', cancelButtonText: '取消' }) } catch { return }
  if (originalResource !== resource.value || originalContext !== platformContext.value) return
  try { await request(`/platform/${kind}/${encodeURIComponent(String(row.id))}`, 'PATCH', body); ElMessage.success('状态已更新。'); await load() } catch (e) { ElMessage.error(errorText(e)) }
}
function editChannel(row: Row) {
  const channelFields = configs.channels!.fields!.filter(f => f.key !== 'secret')
  openForm('更新渠道信息（密钥请在凭据池管理）', channelFields, `/platform/channels/${encodeURIComponent(String(row.id))}`, 'PATCH', { name: String(row.name), base_url: String(row.base_url), region: String(row.region || row.data_region || ''), allowed_cidrs: ((row.allowed_cidrs || []) as string[]).join('\n') })
}
async function probe(row: Row) {
  try { await ElMessageBox.confirm('探测会按已发布路由选择一个可用凭据，可能产生供应商费用；结果仅代表该成员本次校验。确认继续？', '渠道探测', { type: 'warning', confirmButtonText: '执行探测', cancelButtonText: '取消' }) } catch { return }
  try { const res = await request<{ ok: boolean; credential_id: string; message?: string; latency_ms?: number }>(`/platform/channels/${encodeURIComponent(String(row.id))}/probe`, 'POST', {}); const identity = `凭据 ${res.data.credential_id}：`; if (res.data.ok) ElMessage.success(`${identity}本次协议与用量校验通过${res.data.latency_ms !== undefined ? `，耗时 ${res.data.latency_ms}ms` : ''}。仅代表该成员。`); else ElMessage.warning(identity + (res.data.message || '本次探测未通过，请检查凭据配置。')); await load() } catch (e) { ElMessage.error(errorText(e)) }
}
async function approve(row: Row) {
  const state = String(row.state || row.status || '')
  if (!['pending', 'proposed', 'pending_approval'].includes(state)) return
  try { await ElMessageBox.confirm('审批将产生正式资金操作。请先核实原始凭证、客户与金额，且确认自己不是制单人。', '确认财务审批', { type: 'warning', confirmButtonText: '核实并批准', cancelButtonText: '取消' }) } catch { return }
  const path = `/platform/${resource.value}/${encodeURIComponent(String(row.id))}/approve`
  try { await request(path, 'POST', {}, stableOperationKey(path, {})); ElMessage.success('审批已完成。'); await load() } catch (e) { ElMessage.error(errorText(e)) }
}
function canApprove(row: Row) { return isApprover.value && ['pending', 'proposed', 'pending_approval'].includes(String(row.state || row.status)) && String(row.requester || row.requester_id || '') !== session.me?.user.id }
function editGrant(row: Row) { openForm('客户模型授权', [{ key: 'model_ids', title: '授权模型', kind: 'multi', source: 'models', initial: [], hint: '空集合明确拒绝所有模型。授权收回后会阻止后续派发。' }], `/platform/tenants/${encodeURIComponent(String(row.id))}`, 'PATCH', { model_ids: (row.model_ids || []) as string[] }) }
async function loadMembers(row: Row) {
  memberTenant.value = row; membersOpen.value = true; membersBusy.value = true; membersError.value = ''
  try { members.value = (await request<Row[]>(`/platform/tenants/${encodeURIComponent(String(row.id))}/members`)).data || [] }
  catch (e) { membersError.value = errorText(e) } finally { membersBusy.value = false }
}
const memberFields: Field[] = [{ key: 'email', title: '成员邮箱', required: true }, { key: 'password', title: '新成员初始密码', kind: 'password', required: true }, { key: 'role', title: '角色', kind: 'select', required: true, initial: 'member', options: [{ label: '所有者', value: 'owner' }, { label: '成员', value: 'member' }, { label: '账单查看者', value: 'billing_viewer' }] }, { key: 'allow_all_models', title: '允许全部租户已授权模型', kind: 'check', initial: false }, { key: 'model_ids', title: '指定模型', kind: 'multi', source: 'models', initial: [], hint: '未勾选全部且指定模型为空时，无模型调用权限。' }]
function createMember() { if (memberTenant.value) openForm('添加客户成员', memberFields, `/platform/tenants/${encodeURIComponent(String(memberTenant.value.id))}/members`) }
function editMember(row: Row) {
  if (!memberTenant.value) return
  const fs = memberFields.filter(f => !['email', 'password'].includes(f.key)).concat({ key: 'status', title: '成员状态', kind: 'select', initial: 'active', options: [{ label: '启用', value: 'active' }, { label: '停用', value: 'suspended' }] })
  openForm('修改成员权限', fs, `/platform/tenants/${encodeURIComponent(String(memberTenant.value.id))}/members/${encodeURIComponent(String(row.user_id || row.id))}`, 'PATCH', { role: String(row.role), status: String(row.status), allow_all_models: !!row.allow_all_models, model_ids: (row.model_ids || []) as string[] })
}
function editRoute(row: Row) { if (isAdmin.value) { routeModel.value = row; routeOpen.value = true } }
function editPublication(row: Row) { if (isAdmin.value) { publicationModel.value = { id: String(row.id), public_name: String(row.public_name) }; publicationOpen.value = true } }
function editAliases(row: Row) { if (isAdmin.value) { aliasModel.value = { id: String(row.id), public_name: String(row.public_name) }; aliasOpen.value = true } }
function rowPublicationLabel(row: Row) { return typeof row.public_visible === 'boolean' ? publicationLabel({ enabled: row.enabled === true, public_visible: row.public_visible }) : '查看公开设置' }
function resolve(row: Row, action: 'release' | 'settle') {
  const fs: Field[] = [{ key: 'reason', title: '处理理由', kind: 'textarea', required: true }]
  if (action === 'settle') {
    const schemaID = String(row.schema_id || '')
    reconciliationSchemaID.value = schemaID
    const initial = JSON.stringify({ schema_id: schemaID, version: '1', side: 'retail', quantities: [{ dimension: 'text_input_token', unit: 'token', quantity: '0' }, { dimension: 'text_output_token', unit: 'token', quantity: '0' }] }, null, 2)
    fs.push({ key: 'retail_quantities', title: '可信最终 retail 用量向量', kind: 'textarea', required: true, initial, hint: '必须完整填写当前请求 schema 的 QuantitiesWire；数量使用十进制字符串。不得填写 input_tokens/output_tokens 或估算值。' }, { key: 'evidence_ref', title: '可信用量证据引用', required: true, hint: '不得以估算、临时累计量或最大预占代替最终量。' })
  }
  if (action === 'release') reconciliationSchemaID.value = ''
  fs.push({ key: 'action', title: '处理方式', kind: 'select', required: true, initial: action, options: [{ label: action === 'release' ? '释放客户冻结' : '提交收费复核', value: action }] })
  openForm(action === 'release' ? '释放冻结' : '提交有证据收费', fs, `/platform/reconciliations/${encodeURIComponent(String(row.id))}/resolve`)
}
function showDetail(row: Row) { detailRow.value = row; detailOpen.value = true }
function formatDetail(value: unknown): string { return typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value ?? '—') }
async function next() { const page = nextCursor.value; cursors.value.push(page); await load(page) }
async function previous() { cursors.value.pop(); await load() }
watch([resource, platformContext], () => { listScope.reset(); clearChannelSelection(); operationsOpen.value = false; discoveryOpen.value = false; discoveryChannel.value = null; credentialOpen.value = false; credentialChannel.value = null; routeOpen.value = false; routeModel.value = null; publicationOpen.value = false; publicationModel.value = null; aliasOpen.value = false; aliasModel.value = null; costOpen.value = false; dialog.value = false; values.value = {}; reconciliationSchemaID.value = ''; rows.value = []; search.value = ''; cursors.value = []; lookups.value = { tenants: [], models: [], channels: [] }; void load(); void lookup() }, { flush: 'sync' })
onMounted(() => { void load(); void lookup() })
onBeforeUnmount(() => listScope.reset())
</script>

<template>
  <template v-if="config">
    <PageHeader :eyebrow="config.eyebrow" :title="config.title" :description="config.description"><el-button :icon="Refresh" :loading="loading" @click="load()">刷新</el-button><el-button v-if="(config.fields || resource === 'cost-versions' || resource === 'prices') && (isAdmin || (config.finance && canCreateFinance))" type="primary" :icon="Plus" @click="create">{{ config.createTitle }}</el-button></PageHeader>
    <ErrorNotice :message="error" @retry="load()" />
    <section class="panel"><div class="filters"><el-input v-model="search" placeholder="搜索当前页记录" :prefix-icon="Search" clearable /><span class="muted small-text">当前页 {{ rows.length }} 条</span><template v-if="resource === 'channels' && isAdmin"><span class="muted small-text">已选 {{ selectedChannelIDs.length }} 个（仅当前页）</span><el-button :disabled="loading || !selectedChannelIDs.length" @click="openChannelOperation('enable')">批量启用</el-button><el-button :disabled="loading || !selectedChannelIDs.length" @click="openChannelOperation('disable')">批量停用</el-button><el-button @click="openChannelOperationHistory">批量回执</el-button></template></div>
      <el-table ref="channelTable" v-loading="loading" :data="filtered" row-key="id" empty-text="暂无记录。完成相关业务操作后，数据将显示在这里。" @selection-change="channelSelectionChanged">
        <el-table-column v-if="resource === 'channels' && isAdmin" type="selection" width="44" :reserve-selection="false" :selectable="() => !loading" />
        <el-table-column v-for="column in config.columns" :key="column.key" :label="column.title" :width="column.width" :min-width="column.minWidth || 120"><template #default="{ row }"><span v-if="column.type === 'money'">{{ money(cell(row, column.key)) }}</span><span v-else-if="column.type === 'date'">{{ time(cell(row, column.key)) }}</span><el-tag v-else-if="column.type === 'status'" :type="tagType(cell(row, column.key))">{{ label(cell(row, column.key)) }}</el-tag><el-tag v-else-if="column.type === 'bool'" :type="cell(row, column.key) ? (column.key === 'quarantined' ? 'danger' : 'success') : 'info'">{{ cell(row, column.key) ? '是' : '否' }}</el-tag><span v-else-if="column.type === 'id'" class="mono" :title="String(cell(row, column.key) || '')">{{ shortId(cell(row, column.key)) }}</span><span v-else-if="column.type === 'integer'">{{ integer(cell(row, column.key)) }}</span><span v-else>{{ cell(row, column.key) ?? '—' }}</span></template></el-table-column>
        <el-table-column v-if="resource === 'catalog'" label="公开目录" min-width="180"><template #default="{ row }"><el-tag :type="row.public_visible === true && row.enabled === true ? 'success' : 'info'">{{ rowPublicationLabel(row) }}</el-tag></template></el-table-column>
        <el-table-column label="操作" :width="resource === 'channels' ? 350 : resource === 'tenants' ? 350 : resource === 'catalog' ? 260 : resource === 'reconciliations' ? 225 : 100" fixed="right"><template #default="{ row }"><div class="list-action">
          <template v-if="resource === 'tenants'"><el-button link type="primary" @click="loadMembers(row)">成员</el-button><el-button link type="primary" @click="editGrant(row)">授权</el-button><router-link :to="{ path: '/platform/price-groups', query: { tenant: String(row.id) } }" class="tenant-pricing-link">定价</router-link><router-link :to="{ path: '/platform/periodic-budget', query: { tenant: String(row.id) } }" class="tenant-pricing-link">周期预算</router-link><el-button link :type="row.status === 'active' ? 'danger' : 'primary'" @click="toggle(row)">{{ row.status === 'active' ? '暂停' : '启用' }}</el-button></template>
          <template v-else-if="resource === 'channels'"><el-button link type="primary" @click="editChannel(row)">更新</el-button><el-button v-if="isAdmin" link type="primary" @click="manageCredentials(row)">凭据池</el-button><el-button link type="primary" @click="probe(row)">探测</el-button><el-button v-if="isAdmin" link type="primary" @click="discoverModels(row)">发现模型</el-button><el-button link :type="row.enabled ? 'danger' : 'primary'" @click="toggle(row)">{{ row.enabled ? '停用' : '启用' }}</el-button></template>
          <template v-else-if="resource === 'catalog'"><el-button link type="primary" @click="editRoute(row)">路由</el-button><el-button v-if="isAdmin" link type="primary" @click="editAliases(row)">别名</el-button><el-button v-if="isAdmin" link type="primary" @click="editPublication(row)">公开设置</el-button><el-button link :type="row.enabled ? 'danger' : 'primary'" @click="toggle(row)">{{ row.enabled ? '停用' : '启用' }}</el-button></template>
          <template v-else-if="['credits','refunds'].includes(resource)"><el-button v-if="canApprove(row)" link type="primary" @click="approve(row)">{{ resource === 'refunds' ? '批准退款' : '审批入账' }}</el-button><span v-else-if="['pending','pending_approval'].includes(String(row.state || row.status))" class="muted small-text">待其他账号审批</span><el-button v-else link type="primary" @click="showDetail(row)">明细</el-button></template>
          <template v-else-if="resource === 'reconciliations'"><el-button v-if="canCreateFinance" link type="primary" @click="resolve(row, 'settle')">提交证据</el-button><el-button v-if="isApprover" link type="warning" @click="resolve(row, 'release')">释放</el-button><el-button v-if="canApprove(row)" link type="primary" @click="approve(row)">批准</el-button></template>
          <el-button v-else link type="primary" @click="showDetail(row)">明细</el-button>
        </div></template></el-table-column>
      </el-table><div class="table-footer"><span>仅展示当前角色获授权的记录</span><div><el-button size="small" :disabled="!cursors.length || loading" @click="previous">上一页</el-button><el-button size="small" :disabled="!nextCursor || loading" @click="next">下一页</el-button></div></div>
    </section><p v-if="config.note" class="section-note"><el-icon><InfoFilled /></el-icon>{{ config.note }}</p>
  </template>
  <el-empty v-else description="没有这个管理页面" />
  <RouteEditor v-model="routeOpen" :model="routeModel" @saved="load()" />
  <ModelAliases v-model="aliasOpen" :model="aliasModel" />
  <ModelPublicationEditor v-model="publicationOpen" :model="publicationModel" @saved="load()" />
  <CostEditor v-model="costOpen" @saved="load()" />
  <PricingWriter v-model="priceOpen" kind="default_price.publish" @changed="load()" />
  <ChannelDiscovery v-model="discoveryOpen" :channel="discoveryChannel" />
  <CredentialPool v-model="credentialOpen" :channel="credentialChannel" @saved="load()" />
  <ChannelOperations v-model="operationsOpen" :mode="operationsMode" :action="operationsAction" :targets="operationsTargets" :selection-epoch="selectionEpoch" :names="operationsNames" @applied="channelOperationApplied" />

  <el-dialog v-model="dialog" :title="dialogTitle" width="570px" destroy-on-close :close-on-click-modal="false" @close="values = {}"><p class="dialog-description">请核对内容后提交。涉及敏感操作时，服务端会要求近期身份再认证。</p><el-alert v-if="formError" :title="formError" type="error" :closable="false" style="margin-bottom:20px" />
    <el-alert v-if="inputOnlyModel" title="仅输入计费：输出上限和默认输出固定为 0，工具与 SSE 关闭。发布前须核实供应商对整个请求的聚合输入硬上界及可信 usage 合同。" type="info" :closable="false" style="margin-bottom:20px" />
    <el-alert v-if="geminiModel" title="Gemini 当前仅支持无状态纯文本 JSON / SSE，函数工具固定关闭。渠道基础 URL 使用供应商的版本路径，如 /v1beta。" type="info" :closable="false" style="margin-bottom:20px" />
    <el-form label-position="top" @submit.prevent="save"><el-form-item v-for="field in visibleFields" :key="field.key" :label="field.title" :required="field.required">
      <el-checkbox v-if="field.kind === 'check'" v-model="values[field.key]">{{ field.title }}</el-checkbox>
      <el-select v-else-if="field.kind === 'select' || field.kind === 'multi'" v-model="values[field.key]" :multiple="field.kind === 'multi'" filterable :placeholder="field.placeholder || '请选择'" style="width:100%"><el-option v-for="option in options(field)" :key="option.value" :label="option.label" :value="option.value" /></el-select>
      <el-input-number v-else-if="field.kind === 'integer'" v-model="values[field.key]" :min="field.min" :step="1" :precision="0" :placeholder="field.placeholder" controls-position="right" style="width:100%" />
      <el-input v-else-if="field.kind === 'textarea'" v-model="values[field.key]" type="textarea" :rows="3" :placeholder="field.placeholder" maxlength="2000" />
      <el-input v-else v-model="values[field.key]" :type="field.kind === 'password' ? 'password' : field.kind === 'date' ? 'datetime-local' : 'text'" :show-password="field.kind === 'password'" :autocomplete="field.kind === 'password' ? 'new-password' : 'off'" :placeholder="field.placeholder || (field.kind === 'money' ? '0.000000' : '')" :maxlength="field.kind === 'password' ? 256 : 1000" />
      <div v-if="field.hint" class="field-hint">{{ field.hint }}</div>
    </el-form-item></el-form><template #footer><el-button @click="dialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="save">确认提交</el-button></template>
  </el-dialog>
  <el-dialog v-model="membersOpen" :title="`${memberTenant?.name || ''} · 成员`" width="850px" destroy-on-close><div class="page-actions" style="justify-content:flex-end;margin-bottom:15px"><el-button type="primary" :icon="Plus" @click="createMember">添加成员</el-button></div><ErrorNotice :message="membersError" @retry="memberTenant && loadMembers(memberTenant)" /><el-table v-loading="membersBusy" :data="members" empty-text="暂无成员"><el-table-column prop="email" label="邮箱" min-width="220" /><el-table-column label="角色" min-width="130"><template #default="{ row }">{{ label(row.role) }}</template></el-table-column><el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="tagType(row.status)">{{ label(row.status) }}</el-tag></template></el-table-column><el-table-column label="操作" width="100"><template #default="{ row }"><el-button link type="primary" @click="editMember(row)">权限</el-button></template></el-table-column></el-table></el-dialog>
  <el-dialog v-model="detailOpen" title="业务明细" width="650px" destroy-on-close><dl v-if="detailRow" class="detail-list"><template v-for="[key, value] in detailFields" :key="key"><dt>{{ key }}</dt><dd :class="{ mono: typeof value !== 'object' }"><pre v-if="typeof value === 'object'" class="code-block" style="font-size:10px">{{ formatDetail(value) }}</pre><span v-else>{{ formatDetail(value) }}</span></dd></template></dl></el-dialog>
</template>
