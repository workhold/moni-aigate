import { PAYMENTS_RESPONSE_BYTES, parsePaymentsJSON } from './payments-json.ts'
import { parsePaymentInput, paymentDecimal, paymentID, paymentObject, paymentRequestID, paymentText } from './payments.ts'
import type { PaymentKind } from './payments.ts'
const statuses:Record<string,number>={invalid_request:400,invalid_cursor:400,unauthorized:401,invalid_credentials:401,forbidden:403,csrf_invalid:403,mfa_required:403,reauth_required:403,not_found:404,commerce_operation_conflict:409,payments_runtime_disabled:409,payment_policy_conflict:409,body_too_large:413,rate_limited:429,commerce_unavailable:503,commerce_outcome_unknown:503,payment_unavailable:503,payment_outcome_unknown:503,recovery_mode:503,instance_unavailable:503}
const known=new Set(['invalid_request','invalid_cursor','unauthorized','invalid_credentials','forbidden','csrf_invalid','mfa_required','reauth_required','commerce_operation_conflict','payments_runtime_disabled','body_too_large','rate_limited'])
const messages:Record<string,string>={invalid_request:'支付请求未通过校验。',invalid_cursor:'分页位置无效，请刷新第一页。',unauthorized:'请重新登录后核查原操作。',invalid_credentials:'登录已失效，请重新登录后核查原操作。',forbidden:'当前身份没有此操作权限。',csrf_invalid:'会话校验失败，请刷新当前身份。',mfa_required:'请完成账号已配置的认证。',reauth_required:'请完成敏感操作再认证。',not_found:'尚未查到记录；这不能证明原提交失败，原编号仍然保留。',commerce_operation_conflict:'原操作编号与当前命令不一致，请保留原编号核查。',payments_runtime_disabled:'新售已关闭；历史订单与原回执仍可只读核查。',payment_policy_conflict:'配置版本已改变，请重新读取当前配置。',body_too_large:'支付请求超过大小上限。',rate_limited:'查询过于频繁，请稍后按原编号核查。',commerce_unavailable:'商业操作暂不可用；提交结果可能尚未确认。',commerce_outcome_unknown:'原操作结果未知，只能查询原操作回执。',payment_unavailable:'支付服务暂不可用。',payment_outcome_unknown:'支付接收结果待核查。',recovery_mode:'系统处于恢复模式，暂不接受新变更。',instance_unavailable:'实例暂不可用，请保留原操作编号。',invalid_response:'支付响应不符合合同；已提交操作的结果待核查。',network_error:'连接中断或等待超时；已提交操作的结果待核查。'}
export class PaymentError extends Error{readonly code:string;readonly status:number;readonly knownFailure:boolean;readonly retryAfterMs:number;constructor(code:string,status=0,knownFailure=false,retryAfterMs=0){super(messages[code]||messages.invalid_response);this.name='PaymentError';this.code=code;this.status=status;this.knownFailure=knownFailure;this.retryAfterMs=retryAfterMs}}
const invalid=(status=0)=>new PaymentError('invalid_response',status)
export function parsePaymentError(raw:unknown,status:number):PaymentError{try{const v=paymentObject(raw,['error','request_id']),e=paymentObject(v.error,['code','message']);paymentRequestID(v.request_id);paymentText(e.message,1000);if(typeof e.code!=='string'||!Object.hasOwn(statuses,e.code)||statuses[e.code]!==status)throw invalid(status);return new PaymentError(e.code,status,known.has(e.code),status===429?1000:0)}catch{throw invalid(status)}}
export function paymentUnknownWrite(cause:unknown):boolean{return !(cause instanceof PaymentError)||!cause.knownFailure}
export function paymentIdentityFailure(cause:unknown):boolean{return cause instanceof PaymentError&&[401,403].includes(cause.status)}
interface Route{kind?:PaymentKind;policy?:boolean;statuses:number[]}
export function paymentRoute(path:string,method:'GET'|'POST'|'PUT'):Route{
 if(path.length>5000||path.includes('#'))throw invalid();const [resource,query,extra]=path.split('?');if(!resource||extra!==undefined)throw invalid()
 const m=/^\/api\/(wallet|platform)\/(payment-capabilities|payment-policy|payment-skus|payment-orders|commerce-operations)(?:\/([0-9a-f-]{36}))?(?:\/(active|cancel|observations))?$/.exec(resource)
 if(!m)throw invalid();const [,scope,group,id,action]=m;if(id)paymentID(id)
 let list=false,kind:PaymentKind|undefined,policy=false,allowed=false
 if(method==='GET'){
  if(group==='payment-capabilities')allowed=!id&&!action
  if(group==='payment-policy')allowed=scope==='platform'&&!id&&!action
  if(group==='payment-skus')allowed=!action&&(scope==='platform'||!id)
  if(group==='payment-orders')allowed=!action||scope==='platform'&&!!id&&action==='observations'
  if(group==='commerce-operations')allowed=!!id&&!action
  list=(group==='payment-skus'||group==='payment-orders')&&(!id||action==='observations')
 }else if(method==='POST'){
  if(scope==='platform'&&group==='payment-skus'&&!id&&!action){allowed=true;kind='payment.sku.publish'}
  if(scope==='wallet'&&group==='payment-orders'&&!id&&!action){allowed=true;kind='payment.order.create'}
  if(scope==='wallet'&&group==='payment-orders'&&id&&action==='cancel'){allowed=true;kind='payment.order.cancel'}
 }else{
  if(scope==='platform'&&group==='payment-skus'&&id&&action==='active'){allowed=true;kind='payment.sku.activate'}
  if(scope==='platform'&&group==='payment-policy'&&!id&&!action){allowed=true;policy=true}
 }
 if(!allowed)throw invalid()
 if(query!==undefined){if(method!=='GET'||!list||!query||/%(?![0-9a-fA-F]{2})/.test(query))throw invalid();try{decodeURIComponent(query.replace(/\+/g,' '))}catch{throw invalid()};const q=[...new URLSearchParams(query)];if(q.length!==1||q[0]![0]!=='cursor'||!q[0]![1]||q[0]![1].length>1024||!/^[A-Za-z0-9_-]+$/.test(q[0]![1]))throw invalid()}
 return {kind,policy,statuses:method==='GET'?[200]:kind==='payment.sku.publish'?[200,201]:kind==='payment.order.create'?[200,202]:[200]}
}
async function bounded(response:Response,signal:AbortSignal):Promise<string>{
 if(!response.body)throw invalid(response.status);const reader=response.body.getReader(),chunks:Uint8Array[]=[];let length=0
 try{while(true){if(signal.aborted)throw new PaymentError('network_error');const part=await reader.read();if(part.done)break;length+=part.value.length;if(length>PAYMENTS_RESPONSE_BYTES){void reader.cancel().catch(()=>{});throw invalid(response.status)}chunks.push(part.value)}const raw=new Uint8Array(length);let offset=0;for(const chunk of chunks){raw.set(chunk,offset);offset+=chunk.length}return new TextDecoder('utf-8',{fatal:true,ignoreBOM:true}).decode(raw)}finally{reader.releaseLock()}
}
export interface PaymentRequestOptions<T>{method?:'GET'|'POST'|'PUT';body?:unknown;csrf?:string;signal?:AbortSignal;decode:(value:unknown)=>T;fetcher?:typeof fetch;timeoutMs?:number;onDeadline?:()=>void}
export async function paymentRequest<T>(path:string,options:PaymentRequestOptions<T>):Promise<T>{
 const method=options.method||'GET',route=paymentRoute(path,method),headers:Record<string,string>={Accept:'application/json'};let body:string|undefined
 if(method!=='GET'){
  let value:unknown
  if(route.kind)value=parsePaymentInput(route.kind,options.body)
  else{const v=paymentObject(options.body,['expected_revision','enabled']);if(typeof v.enabled!=='boolean')throw invalid();value={expected_revision:paymentDecimal(v.expected_revision),enabled:v.enabled}}
  body=JSON.stringify(value);if(!options.csrf)throw new PaymentError('csrf_invalid',403);if(new TextEncoder().encode(body).length>8192)throw new PaymentError('body_too_large',413);headers['Content-Type']='application/json';headers['X-CSRF-Token']=options.csrf
 }else if(options.body!==undefined)throw invalid()
 const controller=new AbortController();let rejectAbort:((error:PaymentError)=>void)|undefined
 const abortPromise=new Promise<never>((_,reject)=>{rejectAbort=reject}),abort=()=>{controller.abort();rejectAbort?.(new PaymentError('network_error'))}
 if(options.signal?.aborted)abort();else options.signal?.addEventListener('abort',abort,{once:true})
 const timer=setTimeout(()=>{try{options.onDeadline?.()}finally{abort()}},options.timeoutMs??12000)
 const work=async()=>{if(controller.signal.aborted)throw new PaymentError('network_error');let response:Response;try{response=await(options.fetcher||fetch)(path,{method,headers,credentials:'include',cache:'no-store',redirect:'error',signal:controller.signal,body})}catch{throw new PaymentError('network_error')}
  let raw:unknown;try{if(!/^application\/json(?:\s*;\s*charset\s*=\s*utf-8)?\s*$/i.test(response.headers.get('Content-Type')||'')||!['','identity'].includes(response.headers.get('Content-Encoding')||''))throw invalid(response.status);raw=parsePaymentsJSON(await bounded(response,controller.signal))}catch{throw invalid(response.status)}
  if(controller.signal.aborted)throw new PaymentError('network_error');if(!response.ok)throw parsePaymentError(raw,response.status);if(!route.statuses.includes(response.status))throw invalid(response.status);try{return options.decode(raw)}catch{throw invalid(response.status)}
 }
 try{return await Promise.race([work(),abortPromise])}finally{clearTimeout(timer);options.signal?.removeEventListener('abort',abort)}
}
