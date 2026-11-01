import { parseAlertJSON } from './alerts-json.ts'
export const alertKinds = ['channel_quarantined', 'procurement_budget_low', 'customer_reconciliation_aged', 'procurement_unresolved_aged'] as const
export type AlertKind = typeof alertKinds[number]
export type AlertScope = 'channel' | 'procurement_account' | 'tenant'
export type AlertState = 'enabled' | 'disabled' | 'retired'
export type AlertFreshness = 'disabled' | 'retired' | 'paused' | 'never_evaluated' | 'fresh' | 'stale'
export type AlertCommandKind = 'rule_create' | 'rule_configure' | 'rule_retire' | 'acknowledge'
export type AlertParameters = Record<string, never> | { threshold_amount: string; recovery_amount: string } | { minimum_age_seconds: string; trigger_count: string; recovery_count: string }
export interface AlertSnapshot { kind: AlertKind; scope_type: AlertScope; scope_id: string; label: string; parameters: AlertParameters }
export type AlertSample = { kind: 'channel_quarantined'; as_of: string; enabled: boolean; quarantined: boolean } | { kind: 'procurement_budget_low'; as_of: string; account_state: 'active' | 'paused'; limit_amount: string; held_amount: string; spent_amount: string; available_amount: string; currency: 'CNY' } | { kind: 'customer_reconciliation_aged' | 'procurement_unresolved_aged'; as_of: string; minimum_age_seconds: string; observed_count: string; count_is_lower_bound: boolean; oldest_created_at: string | null; oldest_reference_id: string | null }
export interface AlertCapabilities { deployment_enabled: boolean; recovery_mode: boolean; evaluation_available: boolean; reason: 'available' | 'deployment_disabled' | 'recovery_mode'; max_enabled_rules: string; evaluation_interval_seconds: string; stale_after_seconds: string; observed_at: string }
export interface AlertRuntime { next_evaluation_at: string | null; last_attempt_at: string | null; last_success_at: string | null; evaluated_revision: string | null; last_sample: AlertSample | null; last_error_code: string | null; current_episode_id: string | null; freshness: AlertFreshness; observed_at: string }
export interface AlertRule extends AlertSnapshot { id: string; state: AlertState; configuration_revision: string; created_by: string; updated_by: string; created_at: string; updated_at: string; retired_at: string | null; runtime: AlertRuntime }
export interface AlertEpisode { id: string; rule_id: string; generation: string; rule_revision: string; rule_snapshot: AlertSnapshot; status: 'open' | 'acknowledged' | 'resolved'; action_revision: string; opened_at: string; initial_sample: AlertSample; last_seen_at: string; latest_sample: AlertSample; acknowledged_by: string | null; acknowledged_at: string | null; resolved_at: string | null; resolve_reason: string | null; evaluation_freshness: AlertFreshness; observed_at: string }
export interface AlertEvent { id: string; episode_id: string; action_revision: string; type: 'opened' | 'acknowledged' | 'recovered' | 'rule_changed' | 'rule_disabled' | 'rule_retired'; actor: string; observed_at: string; safe_sample: AlertSample | null; created_at: string }
export interface AlertReceipt { operation_id: string; kind: AlertCommandKind; status: 'applied' | 'rejected'; reason: string; resource_id: string | null; result_revision: string | null; created_at: string }
export interface AlertPage<T> { rows: T[]; next: string | null }
export interface AlertCommandIdentity { operation_id: string; actor: string; kind: AlertCommandKind; target_id: string | null; target_key: string; canonical_digest: string; expected_revision: string | null; expected_result_revision: string }
export interface AlertCommand extends AlertCommandIdentity { path: string; method: 'POST' | 'PUT'; body: Readonly<Record<string, unknown>> }
const encoder = new TextEncoder(), max = 9223372036854775807n
const freshness = ['disabled','retired','paused','never_evaluated','fresh','stale'] as const
const runtimeErrors = ['source_missing','source_query_timeout','source_unavailable','clock_regression','counter_exhausted','evaluation_failed'] as const
const reasons = ['alert_revision_stale','alert_rule_limit','alert_rule_exists','alert_rule_retired','alert_source_missing','alert_not_open','alert_already_acknowledged','alert_not_found'] as const
const eventTypes = ['opened','acknowledged','recovered','rule_changed','rule_disabled','rule_retired'] as const
export function alertInvalid(): never { throw new Error('告警合同数据无效；写操作请仅查询原操作回执。') }
export function alertObject(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return alertInvalid()
  const row = value as Record<string, unknown>
  if (Object.keys(row).length !== keys.length || keys.some(key => !Object.hasOwn(row,key))) return alertInvalid()
  return row
}
function unicode(value: string) { return ![...value].some(c => { const n=c.codePointAt(0)!; return n>=0xd800 && n<=0xdfff }) }
export function alertChoice<T extends string>(value: unknown, allowed: readonly T[]): T { if (typeof value !== 'string' || !allowed.includes(value as T)) return alertInvalid(); return value as T }
function bool(value: unknown): boolean { if (typeof value !== 'boolean') return alertInvalid(); return value }
export function alertID(value: unknown): string { if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) || value==='00000000-0000-0000-0000-000000000000') return alertInvalid(); return value }
export function alertUint(value: unknown, limit=max, minimum=0n): string { if (typeof value!=='string' || value.length>101 || !/^(0|[1-9][0-9]*)$/.test(value) || BigInt(value)>limit || BigInt(value)<minimum) return alertInvalid(); return value }
export function alertSigned(value: unknown): string { if (typeof value!=='string' || !/^(0|[1-9][0-9]{0,100}|-[1-9][0-9]{0,100})$/.test(value)) return alertInvalid(); return value }
export function alertLabel(value: unknown): string { if (typeof value!=='string' || !unicode(value) || /\p{Cc}/u.test(value) || /^[\p{White_Space}]|[\p{White_Space}]$/u.test(value) || [...value].length<1 || [...value].length>80 || encoder.encode(value).length>160) return alertInvalid(); return value }
export function alertTime(value: unknown): string {
  if (typeof value!=='string') return alertInvalid()
  const m=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})\.(\d{6})Z$/.exec(value)
  if (!m) return alertInvalid()
  const [y,mo,d,h,mi,s]=m.slice(1,7).map(Number) as [number,number,number,number,number,number], leap=y%4===0&&(y%100!==0||y%400===0), days=[31,leap?29:28,31,30,31,30,31,31,30,31,30,31]
  if (y<1 || mo<1 || mo>12 || d<1 || d>days[mo-1]! || h>23 || mi>59 || s>59) return alertInvalid()
  return value
}
export function alertMicros(value: string): bigint { const valid=alertTime(value), date=new Date(valid.slice(0,23)+'Z'); return BigInt(date.getTime())*1000n+BigInt(valid.slice(23,26)) }
function nullable<T>(value: unknown, parse: (v: unknown)=>T): T|null { return value===null?null:parse(value) }
function safeText(value: unknown, limit=200): string { if (typeof value!=='string' || !value || !unicode(value) || /\p{Cc}/u.test(value) || encoder.encode(value).length>limit) return alertInvalid(); return value }
export function alertScope(kind: AlertKind): AlertScope { return kind==='channel_quarantined'?'channel':kind==='customer_reconciliation_aged'?'tenant':'procurement_account' }
export function parseAlertParameters(kind: AlertKind, value: unknown): AlertParameters {
  if (kind==='channel_quarantined') { alertObject(value,[]); return {} }
  if (kind==='procurement_budget_low') { const r=alertObject(value,['threshold_amount','recovery_amount']), threshold_amount=alertUint(r.threshold_amount), recovery_amount=alertUint(r.recovery_amount); if(BigInt(recovery_amount)<BigInt(threshold_amount)) return alertInvalid(); return {threshold_amount,recovery_amount} }
  const r=alertObject(value,['minimum_age_seconds','trigger_count','recovery_count']), minimum_age_seconds=alertUint(r.minimum_age_seconds,604800n,600n), trigger_count=alertUint(r.trigger_count,1000n,1n), recovery_count=alertUint(r.recovery_count,999n)
  if (BigInt(recovery_count)>=BigInt(trigger_count)) return alertInvalid(); return {minimum_age_seconds,trigger_count,recovery_count}
}
export function parseAlertSample(value: unknown, expected?: AlertKind): AlertSample {
  if (!value || typeof value!=='object') return alertInvalid()
  const kind=alertChoice((value as Record<string,unknown>).kind,alertKinds)
  if (expected && kind!==expected) return alertInvalid()
  if (kind==='channel_quarantined') { const r=alertObject(value,['kind','as_of','enabled','quarantined']); return {kind,as_of:alertTime(r.as_of),enabled:bool(r.enabled),quarantined:bool(r.quarantined)} }
  if (kind==='procurement_budget_low') { const r=alertObject(value,['kind','as_of','account_state','limit_amount','held_amount','spent_amount','available_amount','currency']); const out={kind,as_of:alertTime(r.as_of),account_state:alertChoice(r.account_state,['active','paused'] as const),limit_amount:alertUint(r.limit_amount),held_amount:alertUint(r.held_amount,10n**100n-1n),spent_amount:alertUint(r.spent_amount,10n**100n-1n),available_amount:alertSigned(r.available_amount),currency:alertChoice(r.currency,['CNY'] as const)}; if (BigInt(out.available_amount)!==BigInt(out.limit_amount)-BigInt(out.held_amount)-BigInt(out.spent_amount)) return alertInvalid(); return out }
  const r=alertObject(value,['kind','as_of','minimum_age_seconds','observed_count','count_is_lower_bound','oldest_created_at','oldest_reference_id']), out={kind,as_of:alertTime(r.as_of),minimum_age_seconds:alertUint(r.minimum_age_seconds,604800n,600n),observed_count:alertUint(r.observed_count,1001n),count_is_lower_bound:bool(r.count_is_lower_bound),oldest_created_at:nullable(r.oldest_created_at,alertTime),oldest_reference_id:nullable(r.oldest_reference_id,alertID)}
  if (out.count_is_lower_bound!==(out.observed_count==='1001') || (out.observed_count==='0' ? out.oldest_created_at!==null||out.oldest_reference_id!==null : out.oldest_created_at===null||out.oldest_reference_id===null||alertMicros(out.oldest_created_at)>alertMicros(out.as_of)-BigInt(out.minimum_age_seconds)*1000000n)) return alertInvalid()
  return out
}
export function parseAlertSnapshot(value: unknown): AlertSnapshot { const r=alertObject(value,['kind','scope_type','scope_id','label','parameters']), kind=alertChoice(r.kind,alertKinds); if(r.scope_type!==alertScope(kind)) return alertInvalid(); return {kind,scope_type:alertScope(kind),scope_id:alertID(r.scope_id),label:alertLabel(r.label),parameters:parseAlertParameters(kind,r.parameters)} }
export function parseAlertCapabilities(value: unknown): AlertCapabilities {
  const r=alertObject(value,['deployment_enabled','recovery_mode','evaluation_available','reason','max_enabled_rules','evaluation_interval_seconds','stale_after_seconds','observed_at']), out={deployment_enabled:bool(r.deployment_enabled),recovery_mode:bool(r.recovery_mode),evaluation_available:bool(r.evaluation_available),reason:alertChoice(r.reason,['available','deployment_disabled','recovery_mode'] as const),max_enabled_rules:alertUint(r.max_enabled_rules),evaluation_interval_seconds:alertUint(r.evaluation_interval_seconds),stale_after_seconds:alertUint(r.stale_after_seconds),observed_at:alertTime(r.observed_at)}
  if(out.max_enabled_rules!=='64'||out.evaluation_interval_seconds!=='60'||out.stale_after_seconds!=='180'||out.evaluation_available!==(out.deployment_enabled&&!out.recovery_mode)||out.reason!==(out.recovery_mode?'recovery_mode':out.deployment_enabled?'available':'deployment_disabled')) return alertInvalid(); return out
}
export function parseAlertRule(value: unknown, expected?: string): AlertRule {
  const r=alertObject(value,['id','kind','scope_type','scope_id','label','state','parameters','configuration_revision','created_by','updated_by','created_at','updated_at','retired_at','runtime']), snapshot=parseAlertSnapshot(Object.fromEntries(['kind','scope_type','scope_id','label','parameters'].map(k=>[k,r[k]]))), v=alertObject(r.runtime,['next_evaluation_at','last_attempt_at','last_success_at','evaluated_revision','last_sample','last_error_code','current_episode_id','freshness','observed_at'])
  const runtime:AlertRuntime={next_evaluation_at:nullable(v.next_evaluation_at,alertTime),last_attempt_at:nullable(v.last_attempt_at,alertTime),last_success_at:nullable(v.last_success_at,alertTime),evaluated_revision:nullable(v.evaluated_revision,v=>alertUint(v,max,1n)),last_sample:nullable(v.last_sample,v=>parseAlertSample(v,snapshot.kind)),last_error_code:nullable(v.last_error_code,v=>alertChoice(v,runtimeErrors)),current_episode_id:nullable(v.current_episode_id,alertID),freshness:alertChoice(v.freshness,freshness),observed_at:alertTime(v.observed_at)}
  const out:AlertRule={...snapshot,id:alertID(r.id),state:alertChoice(r.state,['enabled','disabled','retired'] as const),configuration_revision:alertUint(r.configuration_revision,max,1n),created_by:alertID(r.created_by),updated_by:alertID(r.updated_by),created_at:alertTime(r.created_at),updated_at:alertTime(r.updated_at),retired_at:nullable(r.retired_at,alertTime),runtime}
  if ((expected!==undefined&&out.id!==alertID(expected)) || (out.state==='retired')!==(out.retired_at!==null) || (runtime.last_sample===null)!==(runtime.last_success_at===null) || (runtime.last_sample===null)!==(runtime.evaluated_revision===null) || (runtime.last_sample!==null&&(runtime.last_sample.as_of!==runtime.last_success_at||runtime.evaluated_revision!==out.configuration_revision)) || (out.state!=='enabled'&&runtime.freshness!==out.state) || (out.state==='enabled'&&['disabled','retired'].includes(runtime.freshness))) return alertInvalid(); return out
}
export function parseAlertEpisode(value: unknown, expected?: string): AlertEpisode {
  const r=alertObject(value,['id','rule_id','generation','rule_revision','rule_snapshot','status','action_revision','opened_at','initial_sample','last_seen_at','latest_sample','acknowledged_by','acknowledged_at','resolved_at','resolve_reason','evaluation_freshness','observed_at']), snapshot=parseAlertSnapshot(r.rule_snapshot)
  const out:AlertEpisode={id:alertID(r.id),rule_id:alertID(r.rule_id),generation:alertUint(r.generation,max,1n),rule_revision:alertUint(r.rule_revision,max,1n),rule_snapshot:snapshot,status:alertChoice(r.status,['open','acknowledged','resolved'] as const),action_revision:alertUint(r.action_revision,max,1n),opened_at:alertTime(r.opened_at),initial_sample:parseAlertSample(r.initial_sample,snapshot.kind),last_seen_at:alertTime(r.last_seen_at),latest_sample:parseAlertSample(r.latest_sample,snapshot.kind),acknowledged_by:nullable(r.acknowledged_by,alertID),acknowledged_at:nullable(r.acknowledged_at,alertTime),resolved_at:nullable(r.resolved_at,alertTime),resolve_reason:nullable(r.resolve_reason,v=>alertChoice(v,['recovered','rule_changed','rule_disabled','rule_retired'] as const)),evaluation_freshness:alertChoice(r.evaluation_freshness,freshness),observed_at:alertTime(r.observed_at)}
  if ((expected!==undefined&&out.id!==alertID(expected)) || (out.acknowledged_at===null)!==(out.acknowledged_by===null) || (out.status==='resolved')!==(out.resolved_at!==null) || (out.resolved_at===null)!==(out.resolve_reason===null) || (out.status==='open'&&out.acknowledged_at!==null) || (out.status==='acknowledged'&&out.acknowledged_at===null) || out.initial_sample.as_of!==out.opened_at || out.latest_sample.as_of!==out.last_seen_at) return alertInvalid()
  for(const sample of [out.initial_sample,out.latest_sample]) if('minimum_age_seconds'in sample&&sample.minimum_age_seconds!==(snapshot.parameters as {minimum_age_seconds:string}).minimum_age_seconds) return alertInvalid(); return out
}
export function parseAlertEvent(value: unknown, episode?: string, kind?:AlertKind): AlertEvent {
  const r=alertObject(value,['id','episode_id','action_revision','type','actor','observed_at','safe_sample','created_at']), type=alertChoice(r.type,eventTypes), actor=r.actor==='system'?'system':alertID(r.actor), sample=nullable(r.safe_sample,parseAlertSample)
  if ((['opened','recovered'].includes(type))!==(sample!==null) || (['opened','recovered'].includes(type))!==(actor==='system')) return alertInvalid()
  const out:AlertEvent={id:alertID(r.id),episode_id:alertID(r.episode_id),action_revision:alertUint(r.action_revision,max,1n),type,actor,observed_at:alertTime(r.observed_at),safe_sample:sample,created_at:alertTime(r.created_at)}
  if((episode!==undefined&&out.episode_id!==alertID(episode))||(kind!==undefined&&sample!==null&&sample.kind!==kind)) return alertInvalid(); return out
}
export function parseAlertReceipt(value: unknown, command?: AlertCommandIdentity): AlertReceipt {
  const r=alertObject(value,['operation_id','kind','status','reason','resource_id','result_revision','created_at']), out:AlertReceipt={operation_id:alertID(r.operation_id),kind:alertChoice(r.kind,['rule_create','rule_configure','rule_retire','acknowledge'] as const),status:alertChoice(r.status,['applied','rejected'] as const),reason:typeof r.reason==='string'?r.reason:alertInvalid(),resource_id:nullable(r.resource_id,alertID),result_revision:nullable(r.result_revision,v=>alertUint(v,max,1n)),created_at:alertTime(r.created_at)}
  if(out.status==='applied'?(out.reason!==''||out.resource_id===null||out.result_revision===null):(!reasons.includes(out.reason as typeof reasons[number])||out.result_revision!==null||(out.kind==='rule_create')!==(out.resource_id===null))) return alertInvalid()
  if(command&&(out.operation_id!==command.operation_id||out.kind!==command.kind||(command.target_id!==null&&out.resource_id!==command.target_id)||(out.status==='applied'&&out.result_revision!==command.expected_result_revision))) return alertInvalid(); return out
}
export function parseAlertEnvelope<T>(value: unknown, parse: (data:unknown)=>T): T { const r=alertObject(value,['data','request_id']);safeText(r.request_id);return parse(r.data) }
export function alertCursor(value: unknown): string|null {
  if(value===null) return null
  if(typeof value!=='string'||value.length<1||value.length>512||!/^[A-Za-z0-9_-]+$/.test(value)) return alertInvalid()
  try { const bytes=atob(value.replace(/-/g,'+').replace(/_/g,'/'));if(btoa(bytes).replace(/=+$/,'').replace(/\+/g,'-').replace(/\//g,'_')!==value) return alertInvalid();const decoded=new TextDecoder('utf-8',{fatal:true}).decode(Uint8Array.from(bytes,c=>c.charCodeAt(0))),row=alertObject(parseAlertJSON(decoded),['v','t','id','filter_digest']);if(row.v!==1||typeof row.filter_digest!=='string'||!/^[0-9a-f]{64}$/.test(row.filter_digest))return alertInvalid();alertTime(row.t);alertID(row.id) } catch {return alertInvalid()}
  return value
}
export function parseAlertPage<T>(value:unknown,parse:(data:unknown)=>T,key:(row:T)=>string):AlertPage<T> {const r=alertObject(value,['data','next_cursor','request_id']);safeText(r.request_id);if(!Array.isArray(r.data)||r.data.length>50)return alertInvalid();const rows=r.data.map(value=>parse(value));if(new Set(rows.map(key)).size!==rows.length)return alertInvalid();return {rows,next:alertCursor(r.next_cursor)}}
export function alertQuery(route:'rules'|'episodes'|'events',filters:Record<string,string>={},cursor:string|null=null,episode?:string):string {
  const allowed=route==='rules'?['state','kind','scope_id']:route==='episodes'?['status','rule_id','kind']:[]
  if(Object.keys(filters).some(key=>!allowed.includes(key))) return alertInvalid()
  const query=new URLSearchParams()
  for(const [key,value]of Object.entries(filters)){if(value==='')continue;if(key==='state')alertChoice(value,['enabled','disabled','retired']);else if(key==='status')alertChoice(value,['open','acknowledged','resolved']);else if(key==='kind')alertChoice(value,alertKinds);else alertID(value);query.set(key,value)}
  if(query.has('scope_id')&&!query.has('kind')) return alertInvalid()
  const next=alertCursor(cursor);if(next)query.set('cursor',next)
  const base=route==='rules'?'/platform/alert-rules':route==='episodes'?'/platform/alerts':`/platform/alerts/${alertID(episode)}/events`
  return base+(query.size?'?'+query.toString():'')
}
function freeze<T>(value:T):T {if(value&&typeof value==='object'){Object.values(value).forEach(freeze);Object.freeze(value)}return value}
function nextRevision(value:string){const n=BigInt(value)+1n;if(n>max)return alertInvalid();return n.toString()}
export async function alertCommand(kind:AlertCommandKind,operation:string,actor:string,input:unknown,target:string|null=null,previous?:AlertRule):Promise<AlertCommand>{
  alertID(operation);alertID(actor);if(kind==='rule_create'?target!==null:target===null)return alertInvalid();if(target!==null)alertID(target)
  let body:Record<string,unknown>, expected:string|null=null, result='1', targetKey=target===null?'':`${kind==='acknowledge'?'episode':'rule'}:${target}`, path=target===null?'/platform/alert-rules':kind==='acknowledge'?`/platform/alerts/${target}/acknowledge`:`/platform/alert-rules/${target}${kind==='rule_retire'?'/retire':''}`
  if(kind==='rule_create'){const r=alertObject(input,['kind','scope_type','scope_id','label','state','parameters']), k=alertChoice(r.kind,alertKinds);if(r.scope_type!==alertScope(k))return alertInvalid();body={kind:k,scope_type:alertScope(k),scope_id:alertID(r.scope_id),label:alertLabel(r.label),state:alertChoice(r.state,['enabled','disabled']),parameters:parseAlertParameters(k,r.parameters)};targetKey=`create:${k}:${body.scope_id}`}
  else if(kind==='rule_configure'){const r=alertObject(input,['expected_revision','label','state','parameters']);if(!previous||previous.id!==target)return alertInvalid();expected=alertUint(r.expected_revision,max,1n);if(expected!==previous.configuration_revision)return alertInvalid();body={expected_revision:expected,label:alertLabel(r.label),state:alertChoice(r.state,['enabled','disabled']),parameters:parseAlertParameters(previous.kind,r.parameters)};result=body.label===previous.label&&body.state===previous.state&&JSON.stringify(body.parameters)===JSON.stringify(previous.parameters)?expected:nextRevision(expected)}
  else {const name=kind==='acknowledge'?'expected_action_revision':'expected_revision',r=alertObject(input,[name]);expected=alertUint(r[name],max,1n);body={[name]:expected};result=nextRevision(expected)}
  const canonical=JSON.stringify({v:1,actor,kind,target_id:target,body}).replace(/\u2028/g,'\\u2028').replace(/\u2029/g,'\\u2029'), canonical_digest=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',encoder.encode(canonical))),x=>x.toString(16).padStart(2,'0')).join('')
  return freeze({operation_id:operation,actor,kind,target_id:target,target_key:targetKey,canonical_digest,expected_revision:expected,expected_result_revision:result,path,method:kind==='rule_configure'?'PUT':'POST',body:{operation_id:operation,...body}})
}
export function parseAlertIdentity(value:unknown):AlertCommandIdentity {const r=alertObject(value,['operation_id','actor','kind','target_id','target_key','canonical_digest','expected_revision','expected_result_revision']),kind=alertChoice(r.kind,['rule_create','rule_configure','rule_retire','acknowledge'] as const),target=nullable(r.target_id,alertID),expected=nullable(r.expected_revision,v=>alertUint(v,max,1n));if(typeof r.canonical_digest!=='string'||!/^[0-9a-f]{64}$/.test(r.canonical_digest)||typeof r.target_key!=='string'||(kind==='rule_create')!==(target===null)||(kind==='rule_create')!==(expected===null))return alertInvalid();if(kind==='rule_create'){const m=/^create:([^:]+):(.+)$/.exec(r.target_key);if(!m)return alertInvalid();alertChoice(m[1],alertKinds);alertID(m[2])}else if(r.target_key!==`${kind==='acknowledge'?'episode':'rule'}:${target}`)return alertInvalid();const result=alertUint(r.expected_result_revision,max,1n);if(kind==='rule_create'?result!=='1':kind==='rule_configure'?!(BigInt(result)===BigInt(expected!)||BigInt(result)===BigInt(expected!)+1n):BigInt(result)!==BigInt(expected!)+1n)return alertInvalid();return {operation_id:alertID(r.operation_id),actor:alertID(r.actor),kind,target_id:target,target_key:r.target_key,canonical_digest:r.canonical_digest,expected_revision:expected,expected_result_revision:result}}
export function alertIdentity(command:AlertCommandIdentity):AlertCommandIdentity{return parseAlertIdentity(Object.fromEntries(['operation_id','actor','kind','target_id','target_key','canonical_digest','expected_revision','expected_result_revision'].map(k=>[k,command[k as keyof AlertCommandIdentity]])))}
export function alertRole(role:string){return ['admin','finance_operator','finance_approver'].includes(role)}
export const alertKindLabels:Record<AlertKind,string>={channel_quarantined:'渠道持久隔离',procurement_budget_low:'采购登记预算不足',customer_reconciliation_aged:'客户待核对积压',procurement_unresolved_aged:'采购未知与争议积压'}
export const alertFreshnessLabels:Record<AlertFreshness,string>={disabled:'规则已停用',retired:'规则已退役',paused:'评估已暂停',never_evaluated:'尚无当前版本样本',fresh:'样本仍新鲜',stale:'样本已陈旧'}
export const alertEventLabels:Record<string,string>={opened:'发现告警',acknowledged:'平台共同确认',recovered:'条件恢复',rule_changed:'规则配置变更',rule_disabled:'规则停用',rule_retired:'规则退役'}
export function alertCount(sample:Extract<AlertSample,{observed_count:string}>){return `${sample.count_is_lower_bound?'至少 ':''}${sample.observed_count.replace(/\B(?=(\d{3})+(?!\d))/g,',')} 条`}
