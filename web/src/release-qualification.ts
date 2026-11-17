export type ReleaseQualification = 'mock_verified' | 'external_flow_verified' | 'supplier_profile_qualified'
export interface ReleaseEvidence {
  id: string; channel_id: string; channel_version_id: string; model_id: string; profile_version: string
  upstream_model: string; variant_key: 'default'; supplier: string; protocol: string; qualification: ReleaseQualification
  observed_at: string; evidence_commit: string; evidence_ref: string; evidence_sha256: string
  source_mode: 'reference_only' | 'vendored'; source_repository: string; source_commit: string
  license_ref: string; notice_ref: string; modification_ref: string; boundary_ref: string; usage_ref: string; retry_ref: string; cost_ref: string
  cost_status: 'known' | 'unknown'; open_issue_codes: string[]; credential_ids: string[]
  withdrawn: boolean; withdrawal_reason?: string; withdrawn_at?: string; stale: boolean; created_at: string
}
const required=['id','channel_id','channel_version_id','model_id','profile_version','upstream_model','variant_key','supplier','protocol','qualification','observed_at','evidence_commit','evidence_ref','evidence_sha256','source_mode','source_repository','source_commit','license_ref','notice_ref','modification_ref','boundary_ref','usage_ref','retry_ref','cost_ref','cost_status','open_issue_codes','credential_ids','withdrawn','stale','created_at'] as const
const optional=['withdrawal_reason','withdrawn_at'] as const
const invalid=()=>new Error('发布资格证据响应格式无效，请刷新后重试。')
function object(value:unknown):Record<string,unknown>{if(!value||typeof value!=='object'||Array.isArray(value))throw invalid();const raw=value as Record<string,unknown>,keys=Object.keys(raw),allowed=[...required,...optional] as string[];if(keys.some(k=>!allowed.includes(k))||required.some(k=>!keys.includes(k)))throw invalid();return raw}
function id(value:unknown):string{if(typeof value!=='string'||!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value))throw invalid();return value}
function text(value:unknown,max=200):string{if(typeof value!=='string'||!value||new TextEncoder().encode(value).length>max||/[\u0000-\u001f\u007f]/.test(value))throw invalid();return value}
function ref(value:unknown):string{const v=text(value);if(!/^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$/.test(v))throw invalid();return v}
function hex(value:unknown,length:number):string{if(typeof value!=='string'||!new RegExp(`^[0-9a-f]{${length}}$`).test(value))throw invalid();return value}
function timestamp(value:unknown):string{if(typeof value!=='string'||!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z$/.test(value)||!Number.isFinite(new Date(value).getTime()))throw invalid();return value}
function bool(value:unknown):boolean{if(typeof value!=='boolean')throw invalid();return value}
function codes(value:unknown):string[]{if(!Array.isArray(value)||value.length>32||value.some(v=>typeof v!=='string'||!/^[a-z][a-z0-9_]{0,63}$/.test(v))||new Set(value).size!==value.length)throw invalid();return [...value]}
function credentials(value:unknown):string[]{if(!Array.isArray(value)||value.length>32||new Set(value).size!==value.length)throw invalid();return value.map(id)}
export function parseReleaseEvidence(value:unknown):ReleaseEvidence{
  const r=object(value),qualification=r.qualification,sourceMode=r.source_mode,costStatus=r.cost_status,variant=r.variant_key
  if(!['mock_verified','external_flow_verified','supplier_profile_qualified'].includes(String(qualification))||!['reference_only','vendored'].includes(String(sourceMode))||!['known','unknown'].includes(String(costStatus))||variant!=='default')throw invalid()
  if(typeof r.source_repository!=='string'||!/^https:\/\/[A-Za-z0-9.-]+\/[A-Za-z0-9._/-]+$/.test(r.source_repository))throw invalid()
  const withdrawn=bool(r.withdrawn),reason=r.withdrawal_reason,at=r.withdrawn_at
  if(withdrawn&&(typeof reason!=='string'||!/^[a-z][a-z0-9_]{0,63}$/.test(reason))||!withdrawn&&(reason!==undefined||at!==undefined))throw invalid()
  if(withdrawn)timestamp(at)
  return{id:id(r.id),channel_id:id(r.channel_id),channel_version_id:id(r.channel_version_id),model_id:id(r.model_id),profile_version:text(r.profile_version),upstream_model:text(r.upstream_model),variant_key:'default',supplier:text(r.supplier,120),protocol:text(r.protocol,80),qualification:qualification as ReleaseQualification,observed_at:timestamp(r.observed_at),evidence_commit:hex(r.evidence_commit,40),evidence_ref:ref(r.evidence_ref),evidence_sha256:hex(r.evidence_sha256,64),source_mode:sourceMode as ReleaseEvidence['source_mode'],source_repository:String(r.source_repository),source_commit:hex(r.source_commit,40),license_ref:ref(r.license_ref),notice_ref:ref(r.notice_ref),modification_ref:ref(r.modification_ref),boundary_ref:ref(r.boundary_ref),usage_ref:ref(r.usage_ref),retry_ref:ref(r.retry_ref),cost_ref:ref(r.cost_ref),cost_status:costStatus as ReleaseEvidence['cost_status'],open_issue_codes:codes(r.open_issue_codes),credential_ids:credentials(r.credential_ids),withdrawn,...(withdrawn?{withdrawal_reason:String(reason),withdrawn_at:String(at)}:{}),stale:bool(r.stale),created_at:timestamp(r.created_at)}
}
export function parseReleaseEvidenceList(value:unknown,nextCursor:unknown):{rows:ReleaseEvidence[];next:string}{if(!Array.isArray(value)||typeof nextCursor!=='string')throw invalid();return{rows:value.map(parseReleaseEvidence),next:nextCursor}}
export function releaseEvidencePath(filter:{channelID?:string;modelID?:string},cursor=''):string{const query=new URLSearchParams();if(filter.channelID)query.set('channel_id',id(filter.channelID));if(filter.modelID)query.set('model_id',id(filter.modelID));if(cursor)query.set('cursor',cursor);const suffix=query.toString();return'/platform/release-evidence'+(suffix?'?'+suffix:'')}
export function releaseEvidenceDetailPath(value:string):string{return`/platform/release-evidence/${encodeURIComponent(id(value))}`}
export function releaseQualificationLabel(value:ReleaseQualification):string{return value==='supplier_profile_qualified'?'供应商 profile 资格记录':value==='external_flow_verified'?'外部流程已验证（非供应商资格）':'Mock 已验证（非供应商资格）'}
