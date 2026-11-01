import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError } from '../src/api.ts'
import { alertCommand } from '../src/alerts.ts'
import { alertContext, createAlertEpoch, createAlertJournal, createAlertPager, submitAlertCommand, unknownAlertWrite } from '../src/alerts-state.ts'
import { alertsRequest } from '../src/alerts-transport.ts'
const actor='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',target='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',operation='cccccccc-cccc-4ccc-8ccc-cccccccccccc',other='dddddddd-dddd-4ddd-8ddd-dddddddddddd',at='2026-11-01T00:00:00.000001Z'
const input={kind:'channel_quarantined',scope_type:'channel',scope_id:target,label:'Private safe label',state:'disabled',parameters:{}}
const command=(op=operation)=>alertCommand('rule_create',op,actor,input)
const receipt=(c,patch={})=>({operation_id:c.operation_id,kind:c.kind,status:'applied',reason:'',resource_id:target,result_revision:c.expected_result_revision,created_at:at,...patch})
function memory(){const data=new Map();return {data,getItem:key=>data.get(key)||null,setItem:(key,value)=>data.set(key,value),removeItem:key=>data.delete(key)}}
function reply(data,status=200){return new Response(JSON.stringify(data),{status,headers:{'Content-Type':'application/json'}})}

test('identity, role, tenant, CSRF, MFA, filters, close and superseding request suppress stale replies',()=>{
 const gate=createAlertEpoch(),ctx=alertContext(actor,target,'admin','csrf',false)
 gate.reset(ctx);const t=gate.ticket();assert(gate.accepts(t));gate.reset(alertContext(actor,target,'finance_operator','csrf',false));assert(!gate.accepts(t))
 for(const changed of [alertContext(actor,other,'admin','csrf',false),alertContext(actor,target,'admin','rotated',false),alertContext(actor,target,'admin','csrf',true),alertContext(actor,target,'admin','csrf',false,true),ctx+'/different-filter']){gate.reset(ctx);const old=gate.ticket();gate.reset(changed);assert(!gate.accepts(old))}
 const older=gate.ticket(),newest=gate.ticket();assert(!gate.accepts(older));assert(gate.accepts(newest));gate.dispose();assert(!gate.accepts(newest))
})
test('pager has explicit null terminal, serial reads, cursor cycle detection and old-identity rejection',()=>{
 const pager=createAlertPager();pager.reset(actor+'/open');const t=pager.begin();assert.equal(pager.begin(),null);pager.accept(t,{rows:[{id:target}],next:'cursor-a'});const n=pager.begin(false);assert.equal(n.cursor,'cursor-a');assert.throws(()=>pager.accept(n,{rows:[],next:'cursor-a'}),/没有前进/);pager.fail(n,new Error('temporary'));assert.equal(pager.snapshot().rows.length,1);const retry=pager.begin(false);pager.accept(retry,{rows:[],next:null});assert.equal(pager.begin(false),null);assert.equal(pager.snapshot().page,2)
 const old=pager.begin();pager.reset(actor+'/resolved');assert.equal(pager.accept(old,{rows:[{id:target}],next:null}),false);assert.equal(pager.fail(old,new Error('late')),false);assert.deepEqual(pager.snapshot().rows,[])
 const current=pager.begin();pager.accept(current,{rows:[{id:target}],next:null});const forbidden=pager.begin();pager.fail(forbidden,new ApiError('forbidden','forbidden','',403));assert.deepEqual(pager.snapshot().rows,[])
})
test('safe unknown journal survives close/reload, never stores body/sample/label/CSRF and 404 never unlocks',async t=>{
 const storage=memory(),journal=createAlertJournal(storage),c=await command();journal.begin(c);journal.fail(actor,operation,new ApiError('alert_unavailable','unknown','',503));assert(journal.blocked(actor,c.target_key));assert.throws(()=>journal.begin({...c,operation_id:other}),/不能换 ID/)
 const stored=[...storage.data.values()].join('');for(const forbidden of ['Private safe label','body','csrf','parameters','last_sample'])assert(!stored.includes(forbidden));const restored=createAlertJournal(storage);assert.equal(restored.list(actor)[0].state,'unknown');assert(!('body'in restored.list(actor)[0].command));assert(restored.blocked(actor,c.target_key))
 t.mock.method(globalThis,'fetch',async()=>reply({error:{code:'alert_not_found',message:'not found'},request_id:'safe'},404));await assert.rejects(alertsRequest(`/platform/alert-operations/${operation}`),e=>e.status===404);assert(restored.blocked(actor,c.target_key));assert.equal(restored.list(actor)[0].state,'unknown')
 restored.confirm(actor,operation,receipt(c));assert(!restored.blocked(actor,c.target_key));assert.equal(storage.data.size,0)
})
test('configure and retire share the same resource lock; mismatched immutable receipt never unlocks',async()=>{
 const journal=createAlertJournal(),c=await alertCommand('rule_retire',operation,actor,{expected_revision:'9007199254740993'},target);journal.begin(c)
 assert.throws(()=>journal.begin({...c,operation_id:other,kind:'rule_configure'}),/不能换 ID/);assert.throws(()=>journal.confirm(actor,operation,receipt(c,{result_revision:'9007199254740993'})));assert(journal.blocked(actor,c.target_key));journal.confirm(actor,operation,receipt(c));assert(Object.isFrozen(journal.list(actor)[0].receipt));assert.throws(()=>journal.confirm(actor,operation,receipt(c,{created_at:'2026-11-02T00:00:00.000001Z'})))
})
test('only known preclaim HTTP failures release; transport, bad success, 404 and operation conflict remain unknown',()=>{
 for(const status of [0,404,408,409,500,503])assert(unknownAlertWrite(new ApiError('alert_unavailable','x','',status)));for(const status of [400,401,403,429])assert(!unknownAlertWrite(new ApiError('forbidden','x','',status)));assert(unknownAlertWrite(new ApiError('invalid_response','x','',200)));assert(unknownAlertWrite(new Error('schema')));assert(unknownAlertWrite(new DOMException('aborted','AbortError')))
})
test('HTTP 200 rejected receipt is a business rejection, never reported as applied',async t=>{
 const c=await command(),journal=createAlertJournal();let writes=0;t.mock.method(globalThis,'fetch',async(url,options)=>{writes++;assert.equal(url,'/api/platform/alert-rules');assert.equal(options.method,'POST');assert.equal(options.headers['X-CSRF-Token'],'csrf');return reply({data:receipt(c,{status:'rejected',reason:'alert_rule_exists',resource_id:null,result_revision:null}),request_id:'safe'})})
 const result=await submitAlertCommand(c,'csrf',()=>true,journal);assert.equal(result.status,'rejected');assert.equal(journal.list(actor)[0].state,'rejected');assert(!journal.blocked(actor,c.target_key));assert.equal(writes,1)
})
test('malformed HTTP 200 stays unknown and never issues automatic retries or a new operation',async t=>{
 const c=await command(),journal=createAlertJournal();let writes=0;t.mock.method(globalThis,'fetch',async()=>{writes++;return reply({data:receipt(c,{operation_id:other}),request_id:'safe'})});await assert.rejects(submitAlertCommand(c,'csrf',()=>true,journal));assert.equal(writes,1);assert.equal(journal.list(actor)[0].command.operation_id,operation);assert(journal.blocked(actor,c.target_key))
})
test('late successful write after close is discarded while original operation remains discoverable',async t=>{
 const c=await command(),journal=createAlertJournal();let complete,current=true;t.mock.method(globalThis,'fetch',()=>new Promise(resolve=>{complete=resolve}));const pending=submitAlertCommand(c,'csrf',()=>current,journal);assert(journal.blocked(actor,c.target_key));current=false;complete(reply({data:receipt(c),request_id:'safe'}));assert.equal(await pending,null);assert.equal(journal.list(actor)[0].state,'unknown');assert(journal.blocked(actor,c.target_key))
})
test('corrupt/unavailable storage fails before POST and preserves pending lock rather than clearing it',async t=>{
 const c=await command(),storage=memory();storage.setItem('moni:alerts:pending:v1:'+actor,'{"v":1,"v":1,"commands":[]}');assert.throws(()=>createAlertJournal(storage).begin(c),/存储损坏/)
 const journal=createAlertJournal({getItem:()=>null,setItem:()=>{throw new Error('quota')},removeItem:()=>{}});let calls=0;t.mock.method(globalThis,'fetch',async()=>{calls++;return reply({})});await assert.rejects(submitAlertCommand(c,'csrf',()=>true,journal),/不允许新提交/);assert.equal(calls,0);assert.equal(journal.list(actor).length,0)
})
test('strict transport has exact 200 success, no-store same-origin credentials and no hidden retries',async()=>{
 let count=0;const response=await alertsRequest('/platform/alerts/capabilities',{fetcher:async(url,options)=>{count++;assert.equal(url,'/api/platform/alerts/capabilities');assert.equal(options.credentials,'include');assert.equal(options.cache,'no-store');assert.equal(options.redirect,'error');assert.equal(options.method,'GET');return reply({data:{},request_id:'safe'})}});assert.equal(response.request_id,'safe');assert.equal(count,1)
 await assert.rejects(alertsRequest('/platform/alerts/capabilities',{fetcher:async()=>reply({data:{},request_id:'safe'},201)}),e=>e.code==='invalid_response'&&e.status===201)
 await assert.rejects(alertsRequest('https://elsewhere.example/api',{fetcher:async()=>{throw new Error('should not call')}}),/路径无效/)
 await assert.rejects(alertsRequest('/platform/alert-rules',{method:'POST',body:{},fetcher:async()=>{throw new Error('should not call')}}),e=>e.status===403)
})
test('transport rejects duplicate JSON, invalid UTF8, oversized streamed response and oversized request',async()=>{
 for(const body of ['{"data":{},"data":{},"request_id":"safe"}',new Uint8Array([0xff])])await assert.rejects(alertsRequest('/platform/alerts/capabilities',{fetcher:async()=>new Response(body,{status:200})}),e=>e.code==='invalid_response')
 let cancelled=false;const stream=new ReadableStream({start(controller){controller.enqueue(new Uint8Array(262145))},cancel(){cancelled=true}});await assert.rejects(alertsRequest('/platform/alerts/capabilities',{fetcher:async()=>new Response(stream,{status:200})}),e=>e.code==='invalid_response');assert(cancelled)
 let called=false;await assert.rejects(alertsRequest('/platform/alert-rules',{method:'POST',csrf:'csrf',body:{label:'x'.repeat(8193)},fetcher:async()=>{called=true;return reply({})}}),e=>e.status===400);assert(!called)
})
test('timeout aborts the actual fetch and remains unknown; stale authentication reply does not dispatch',async t=>{
 let aborted=false;await assert.rejects(alertsRequest('/platform/alerts/capabilities',{timeoutMs:5,fetcher:async(_url,options)=>new Promise(()=>{options.signal.addEventListener('abort',()=>{aborted=true})})}),e=>e.status===0);assert(aborted)
 const old=globalThis.window;let dispatched=0;globalThis.window={dispatchEvent(){dispatched++}};t.after(()=>{if(old===undefined)delete globalThis.window;else globalThis.window=old})
 await assert.rejects(alertsRequest('/platform/alerts/capabilities',{current:()=>false,fetcher:async()=>reply({error:{code:'unauthorized',message:'RAW SECRET SQL'},request_id:'safe'},401)}),e=>!e.message.includes('RAW SECRET'));assert.equal(dispatched,0)
})
