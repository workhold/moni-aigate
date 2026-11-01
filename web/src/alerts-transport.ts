import { ApiError } from './api.ts'
import { parseAlertJSON } from './alerts-json.ts'

export interface AlertRequestOptions { method?:'GET'|'POST'|'PUT';body?:unknown;csrf?:string;current?:()=>boolean;fetcher?:typeof fetch;timeoutMs?:number }
const messages:Record<string,string>={invalid_request:'告警请求未通过校验。',unauthorized:'请重新登录。',forbidden:'当前账号没有此告警权限。',reauth_required:'请完成敏感操作再认证。',csrf_invalid:'会话校验失败，请刷新身份后重试。',mfa_required:'请完成账号认证。',alert_not_found:'当前读取未找到该资源或原操作。',alert_operation_conflict:'原操作 ID 与请求不一致，请核查原回执。',alert_rate_limited:'告警请求过于频繁，请稍后手动重试。',alert_unavailable:'告警服务暂不可用；写操作结果需要按原 ID 核查。'}
function invalid(status:number,request=''):ApiError{return new ApiError('invalid_response','告警响应不符合合同；写操作请仅查询原操作回执。',request,status)}
function requestID(value:unknown):string {return typeof value==='string'&&value.length>0&&value.length<=200&&!/\p{Cc}/u.test(value)?value:''}
async function boundedBody(response:Response):Promise<string>{
  if(!response.body)throw invalid(response.status)
  const reader=response.body.getReader(),chunks:Uint8Array[]=[],decoder=new TextDecoder('utf-8',{fatal:true});let length=0
  try{while(true){const result=await reader.read();if(result.done)break;length+=result.value.length;if(length>262144){await reader.cancel();throw invalid(response.status)}chunks.push(result.value)}const bytes=new Uint8Array(length);let at=0;for(const chunk of chunks){bytes.set(chunk,at);at+=chunk.length}return decoder.decode(bytes)}finally{reader.releaseLock()}
}
export async function alertsRequest(path:string,options:AlertRequestOptions={}):Promise<unknown>{
  if(!/^\/platform\/(?:alerts(?:\/capabilities|\/[0-9a-f-]{36}(?:\/(?:events|acknowledge))?)?|alert-rules(?:\/[0-9a-f-]{36}(?:\/retire)?)?|alert-operations\/[0-9a-f-]{36})(?:\?[^#]*)?$/.test(path))throw new Error('告警接口路径无效。')
  const method=options.method||'GET',headers:Record<string,string>={Accept:'application/json'},current=options.current||(()=>true),controller=new AbortController()
  let body:string|undefined
  if(method!=='GET'){if(!options.csrf)throw new ApiError('csrf_invalid',messages.csrf_invalid!,'',403);headers['X-CSRF-Token']=options.csrf;headers['Content-Type']='application/json';body=JSON.stringify(options.body);if(!body||new TextEncoder().encode(body).length>8192)throw new ApiError('invalid_request',messages.invalid_request!,'',400)}
  else if(options.body!==undefined)throw new Error('告警读取不能携带正文。')
  let timer:ReturnType<typeof setTimeout>|undefined
  const deadline=new Promise<never>((_,reject)=>{timer=setTimeout(()=>{controller.abort();reject(new ApiError('network_error','告警请求等待超时；写操作结果待核查。','',0))},options.timeoutMs??(method==='GET'?6000:8000))})
  try{return await Promise.race([(async()=>{let response:Response;try{response=await(options.fetcher||fetch)(`/api${path}`,{method,headers,credentials:'include',cache:'no-store',redirect:'error',signal:controller.signal,body})}catch{throw new ApiError('network_error','无法连接告警服务；写操作请查询原操作回执。','',0)}
    let payload:unknown;try{payload=parseAlertJSON(await boundedBody(response))}catch(cause){if(cause instanceof ApiError)throw cause;throw invalid(response.status,requestID(response.headers.get('X-Request-ID')))}
    if(!response.ok){const raw=payload&&typeof payload==='object'?payload as Record<string,unknown>:{},err=raw.error&&typeof raw.error==='object'?raw.error as Record<string,unknown>:{},code=typeof err.code==='string'&&Object.hasOwn(messages,err.code)?err.code:'alert_unavailable',error=new ApiError(code,messages[code]!,requestID(raw.request_id),response.status);if(current()&&typeof window!=='undefined'){if(response.status===401)window.dispatchEvent(new CustomEvent('moni:unauthenticated'));if(code==='reauth_required')window.dispatchEvent(new CustomEvent('moni:reauth'))}throw error}
    if(response.status!==200)throw invalid(response.status)
    return payload
  })(),deadline])}finally{clearTimeout(timer)}
}
