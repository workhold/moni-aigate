import test from 'node:test'
import assert from 'node:assert/strict'
import { parsePaymentsJSON,PAYMENTS_RESPONSE_BYTES } from '../src/payments-json.ts'
import { parsePaymentCapabilities,parsePaymentPolicy,parsePaymentSKUVersion,parsePaymentSKU,parsePaymentOrder,parsePaymentObservation,parsePaymentPending,parsePaymentReceipt,parsePaymentWrite,parsePaymentSingle,parsePaymentPage,paymentTime,paymentReference,paymentCanRead } from '../src/payments.ts'
const id='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',tenant='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',actor='cccccccc-cccc-4ccc-8ccc-cccccccccccc',versionID='dddddddd-dddd-4ddd-8ddd-dddddddddddd',resource='eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee',time='2026-11-01T08:00:00.123456Z'
const version=()=>({id:versionID,sku_id:resource,version:'9007199254740993',display_name:'模拟商品',amount:'9007199254740993',wallet_credit_amount:'9007199254740993',currency:'CNY',environment:'sandbox',merchant_id:'merchant:qa',payee_label:'本地模拟',checkout_ttl_seconds:900,created_at:time})
const sku=()=>({id:resource,revision:'1',enabled:true,current_version_id:versionID,current_version:version(),created_at:time})
const order=()=>({id,tenant_id:tenant,creator:actor,sku_version_id:versionID,display_name:'模拟商品',amount:'9007199254740993',wallet_credit_amount:'9007199254740993',currency:'CNY',environment:'sandbox',merchant_id:'merchant:qa',payee_label:'本地模拟',created_at:time,expires_at:'2026-11-01T08:15:00.123456Z',checkout_state:'pending',creation_state:'queued',checkout_ref:null,funding_state:'unconfirmed',review_required:false,review_reason:null,funding_receipt_id:null,journal_id:null,credited_at:null,observed_at:time})
const pending=(kind='payment.order.create')=>({operation_id:id,kind,scope:kind.startsWith('payment.sku.')?'platform':tenant,target_id:kind==='payment.order.create'?versionID:resource,created_at:time})
const receipt=(p=pending())=>({operation_id:p.operation_id,kind:p.kind,scope:p.scope,actor,status:'accepted',reason:'accepted',resource_id:p.kind==='payment.order.create'?id:resource,amount:null,funding_receipt_id:null,journal_id:null,created_at:time})
test('raw JSON rejects duplicate aliases, invalid UTF16, number coercion and byte overflow',()=>{
 for(const raw of ['{"x":1,"x":2}','{"x":1,"\\u0078":2}','{"x":"\\ud800"}','{"x":"\\udc00"}','{"x":-0}','{"x":1.0}','{"x":1e0}','{"x":9007199254740993}','{} {}','{"a":1,}'])assert.throws(()=>parsePaymentsJSON(raw))
 assert.equal(parsePaymentsJSON('{"名字":"\\ud83d\\ude80","ttl":900}').ttl,900)
 assert.throws(()=>parsePaymentsJSON(' '.repeat(PAYMENTS_RESPONSE_BYTES)+'{}'))
 assert.equal(parsePaymentsJSON(' '.repeat(PAYMENTS_RESPONSE_BYTES-2)+'{}')!==null,true)
})
test('SKU exact shape preserves bigint strings and binds current immutable version',()=>{
 assert.equal(parsePaymentSKUVersion(version()).amount,'9007199254740993');assert.equal(parsePaymentSKU(sku()).current_version.id,versionID)
 for(const bad of [{...version(),amount:9007199254740992},{...version(),amount:'01'},{...version(),amount:'9223372036854775808'},{...version(),wallet_credit_amount:'1'},{...version(),display_name:'中'.repeat(34)},{...version(),checkout_ttl_seconds:901},{...version(),extra:true}])assert.throws(()=>parsePaymentSKUVersion(bad))
 for(const bad of [{...sku(),current_version_id:null},{...sku(),current_version:{...version(),sku_id:id}},{...sku(),current_version_id:id},{...sku(),current_version:null,current_version_id:null}])assert.throws(()=>parsePaymentSKU(bad))
 assert.equal(parsePaymentSKU({...sku(),enabled:false,current_version:null,current_version_id:null}).enabled,false)
})
test('order tenant/TTL/review/funding null matrix rejects partial or invented credit',()=>{
 assert.equal(parsePaymentOrder(order(),tenant,id).funding_state,'unconfirmed')
 const credited={...order(),funding_state:'credited',funding_receipt_id:resource,journal_id:versionID,credited_at:time,review_required:true,review_reason:'transaction_conflict'};assert.equal(parsePaymentOrder(credited,tenant).review_required,true)
 for(const bad of [{...order(),tenant_id:actor},{...order(),expires_at:'2026-11-01T08:15:00.123457Z'},{...order(),expires_at:'2026-11-01T08:14:00.123456Z'},{...order(),funding_state:'credited'},{...order(),funding_receipt_id:resource},{...order(),funding_state:'verified',journal_id:resource},{...order(),review_required:true},{...order(),review_reason:'unknown_order'},{...order(),checkout_ref:'https://pay.invalid/secret'},{...order(),creation_state:'success'},{...order(),environment:'production'}])assert.throws(()=>parsePaymentOrder(bad,tenant,id))
})
test('public format and capabilities validate calendar, ref and role boundaries',()=>{
 for(const bad of ['0000-01-01T00:00:00.000000Z','2026-02-29T00:00:00.000000Z','2026-11-01T24:00:00.000000Z','2026-11-01T00:00:00Z'])assert.throws(()=>paymentTime(bad))
 assert.equal(paymentTime('0001-01-01T00:00:00.000000Z'),'0001-01-01T00:00:00.000000Z');for(const bad of ['BearerSecret','SK-key','https://pay.invalid','x\u0000','symbol:②'])assert.throws(()=>paymentReference(bad))
 assert.equal(parsePaymentCapabilities({mode:'off',environment:'sandbox',payments_enabled:false,local_simulation:true}).mode,'off')
 for(const bad of [{mode:'off',environment:null,payments_enabled:true,local_simulation:false},{mode:'sandbox',environment:null,payments_enabled:false,local_simulation:false},{mode:'live',environment:null,payments_enabled:false,local_simulation:false}])assert.throws(()=>parsePaymentCapabilities(bad))
 assert.throws(()=>parsePaymentPolicy({enabled:true,revision:'1',runtime_allowed:false,effective_enabled:true}))
 assert.equal(paymentCanRead(true,'admin','member'),true);assert.equal(paymentCanRead(true,'platform_admin','owner'),false);assert.equal(paymentCanRead(false,'admin','member'),false);assert.equal(paymentCanRead(false,'none','billing_viewer'),true)
})
test('four command receipts bind actor/scope/op and only provable resource links',()=>{
 for(const kind of ['payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel']){const p=pending(kind),r=receipt(p);assert.equal(parsePaymentReceipt(r,p,actor).kind,kind);assert.throws(()=>parsePaymentReceipt({...r,actor:resource},p,actor));assert.throws(()=>parsePaymentReceipt({...r,operation_id:versionID},p,actor));assert.throws(()=>parsePaymentReceipt({...r,amount:'1'},p,actor));assert.throws(()=>parsePaymentReceipt({...r,reason:'maybe'},p,actor))}
 const p=pending();assert.equal(parsePaymentWrite({data:receipt(p),order:order(),request_id:'r'},p,actor).order.id,id)
 assert.throws(()=>parsePaymentWrite({data:receipt(p),request_id:'r'},p,actor));assert.throws(()=>parsePaymentWrite({data:receipt(p),order:{...order(),sku_version_id:resource},request_id:'r'},p,actor))
 const rejected={...receipt(p),status:'rejected',reason:'payments_disabled',resource_id:null};assert.equal(parsePaymentWrite({data:rejected,request_id:'r'},p,actor).order,null);assert.throws(()=>parsePaymentWrite({data:rejected,order:order(),request_id:'r'},p,actor));assert.throws(()=>parsePaymentReceipt({...rejected,reason:'payment_revision_conflict'},p,actor))
 assert.throws(()=>parsePaymentPending({...p,amount:'1'}));assert.throws(()=>parsePaymentPending({...p,actor}));assert.throws(()=>parsePaymentPending({...p,scope:'platform'}))
})
test('observations support unknown logical order and paged envelopes keep explicit null',()=>{
 const observed={id:resource,order_id:id,kind:'capture',status:'processing_review',reason:'unknown_order',occurred_at:null,received_at:time};assert.equal(parsePaymentObservation(observed,id).reason,'unknown_order');assert.throws(()=>parsePaymentObservation(observed,tenant));assert.throws(()=>parsePaymentObservation({...observed,reason:'raw-provider-secret'},id))
 assert.equal(parsePaymentPage({data:[],next_cursor:null,request_id:'r'},parsePaymentSKU).next_cursor,null);assert.throws(()=>parsePaymentPage({data:[],next_cursor:'',request_id:'r'},parsePaymentSKU));assert.throws(()=>parsePaymentPage({data:[sku(),sku()],next_cursor:null,request_id:'r'},parsePaymentSKU));assert.throws(()=>parsePaymentSingle({data:sku(),request_id:'r',extra:1},parsePaymentSKU))
})
