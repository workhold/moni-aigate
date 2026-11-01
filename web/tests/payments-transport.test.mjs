import test from 'node:test'
import assert from 'node:assert/strict'
import { PaymentError,parsePaymentError,paymentRequest,paymentUnknownWrite } from '../src/payments-transport.ts'
import { createPaymentEpoch } from '../src/payments-state.ts'
const id='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',path='/api/wallet/payment-orders',body={operation_id:id,sku_version_id:id}
const response=(value,status=200,headers={})=>new Response(typeof value==='string'?value:JSON.stringify(value),{status,headers:{'Content-Type':'application/json',...headers}})
const error=code=>({error:{code,message:'server text is deliberately not displayed'},request_id:'r'})
test('only fixed matching precommit error pairs may release a write journal',()=>{
 for(const [code,status] of [['invalid_request',400],['unauthorized',401],['csrf_invalid',403],['reauth_required',403],['commerce_operation_conflict',409],['payments_runtime_disabled',409],['body_too_large',413],['rate_limited',429]])assert.equal(paymentUnknownWrite(parsePaymentError(error(code),status)),false)
 for(const [code,status] of [['not_found',404],['commerce_unavailable',503],['commerce_outcome_unknown',503],['payment_policy_conflict',409],['recovery_mode',503]])assert.equal(paymentUnknownWrite(parsePaymentError(error(code),status)),true)
 for(const [value,status] of [[error('forbidden'),409],[error('invented'),400],[{...error('forbidden'),extra:true},403]])assert.throws(()=>parsePaymentError(value,status))
 assert.equal(parsePaymentError(error('forbidden'),403).message.includes('server text'),false)
})
test('a human write sends one exact request and never retries',async()=>{
 let calls=0
 const out=await paymentRequest(path,{method:'POST',body,csrf:'test-csrf',decode:x=>x,fetcher:async(url,init)=>{calls++;assert.equal(url,path);assert.equal(init.credentials,'include');assert.equal(init.cache,'no-store');assert.equal(init.redirect,'error');assert.equal(init.headers['X-CSRF-Token'],'test-csrf');assert.deepEqual(JSON.parse(init.body),body);assert.ok(init.signal instanceof AbortSignal);return response({data:'ok',request_id:'r'},202)}})
 assert.equal(out.data,'ok');assert.equal(calls,1)
 await assert.rejects(paymentRequest(path,{method:'POST',body,csrf:'x',decode:x=>x,fetcher:async()=>{calls++;throw new Error('private transport')}}),paymentUnknownWrite);assert.equal(calls,2)
})
test('bounded raw response and strict DTO failure retain unknown write',async()=>{
 const replies=[()=>response('{"error":{"code":"forbidden","code":"forbidden","message":"x"},"request_id":"r"}',403),()=>response('{"data":',200),()=>response('x'.repeat(131073),400),()=>new Response(new Uint8Array([255]),{status:400,headers:{'Content-Type':'application/json'}}),()=>response(error('not_found'),404),()=>response(error('forbidden'),409),()=>response({},201),()=>response({},200,{'Content-Encoding':'gzip'}),()=>new Response('{}',{status:200})]
 for(const reply of replies)await assert.rejects(paymentRequest(path,{method:'POST',body,csrf:'x',decode:()=>{throw new Error('DTO binding mismatch')},fetcher:async()=>reply()}),cause=>cause instanceof PaymentError&&paymentUnknownWrite(cause))
})
test('12s local deadline invalidates ticket, aborts fetch and ignores late success',async()=>{
 const gate=createPaymentEpoch();gate.auth('actor:tenant:owner');const ticket=gate.ticket('mutation');let resolveFetch,seen,decode=0,expired=0
 const promise=paymentRequest(path,{method:'POST',body,csrf:'x',signal:ticket.signal,timeoutMs:5,onDeadline:()=>{expired++;gate.expire(ticket)},decode:x=>{decode++;return x},fetcher:async(_url,init)=>{seen=init.signal;return new Promise(resolve=>{resolveFetch=resolve})}})
 await assert.rejects(promise,paymentUnknownWrite);assert.equal(expired,1);assert.equal(seen.aborted,true);assert.equal(gate.accepts(ticket),false);resolveFetch(response({data:'late'},202));await new Promise(resolve=>setTimeout(resolve,0));assert.equal(decode,0)
})
test('path/query/method and local preflight never touch fetch or release durable lock',async()=>{
 let calls=0;const fetcher=async()=>{calls++;return response({})}
 for(const bad of ['/api/payments/sandbox/notifications',path+'?cursor=x&cursor=y',path+'?limit=50','/api/wallet/payment-policy','/api/platform/payment-orders/'+id+'/cancel',path+'?cursor=%ZZ',path+'#x'])await assert.rejects(paymentRequest(bad,{decode:x=>x,fetcher}))
 await assert.rejects(paymentRequest('/api/platform/payment-orders',{method:'POST',body,csrf:'x',decode:x=>x,fetcher}));await assert.rejects(paymentRequest(path,{method:'POST',body,decode:x=>x,fetcher}),paymentUnknownWrite);assert.equal(calls,0)
 const before=new AbortController();before.abort();await assert.rejects(paymentRequest(path,{signal:before.signal,decode:x=>x,fetcher}),paymentUnknownWrite);assert.equal(calls,0)
})
