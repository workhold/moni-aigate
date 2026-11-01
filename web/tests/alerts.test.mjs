import assert from 'node:assert/strict'
import test from 'node:test'
import { createHash } from 'node:crypto'
import { alertCommand, alertCount, alertCursor, alertID, alertLabel, alertMicros, alertQuery, alertRole, alertSigned, alertTime, alertUint, parseAlertCapabilities, parseAlertEnvelope, parseAlertEpisode, parseAlertEvent, parseAlertIdentity, parseAlertPage, parseAlertParameters, parseAlertReceipt, parseAlertRule, parseAlertSample } from '../src/alerts.ts'
import { parseAlertJSON } from '../src/alerts-json.ts'
const actor='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',resource='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',ruleID='cccccccc-cccc-4ccc-8ccc-cccccccccccc',episodeID='dddddddd-dddd-4ddd-8ddd-dddddddddddd',op='eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee',at='2026-11-01T00:00:00.123456Z',huge='9007199254740993'
const channel=patch=>({kind:'channel_quarantined',as_of:at,enabled:false,quarantined:true,...patch})
const runtime=patch=>({next_evaluation_at:at,last_attempt_at:at,last_success_at:at,evaluated_revision:huge,last_sample:channel(),last_error_code:'source_query_timeout',current_episode_id:episodeID,freshness:'fresh',observed_at:at,...patch})
const rule=patch=>({id:ruleID,kind:'channel_quarantined',scope_type:'channel',scope_id:resource,label:'渠道告警',state:'enabled',parameters:{},configuration_revision:huge,created_by:actor,updated_by:actor,created_at:at,updated_at:at,retired_at:null,runtime:runtime(),...patch})
const episode=patch=>({id:episodeID,rule_id:ruleID,generation:huge,rule_revision:huge,rule_snapshot:{kind:'channel_quarantined',scope_type:'channel',scope_id:resource,label:'冻结名称',parameters:{}},status:'open',action_revision:'1',opened_at:at,initial_sample:channel(),last_seen_at:at,latest_sample:channel(),acknowledged_by:null,acknowledged_at:null,resolved_at:null,resolve_reason:null,evaluation_freshness:'stale',observed_at:at,...patch})
const cap=patch=>({deployment_enabled:false,recovery_mode:false,evaluation_available:false,reason:'deployment_disabled',max_enabled_rules:'64',evaluation_interval_seconds:'60',stale_after_seconds:'180',observed_at:at,...patch})
const envelope=data=>({data,request_id:'safe-request'})
const cursor=patch=>Buffer.from(JSON.stringify({v:1,t:at,id:ruleID,filter_digest:'a'.repeat(64),...patch})).toString('base64url')
const createInput={kind:'channel_quarantined',scope_type:'channel',scope_id:resource,label:'渠道告警',state:'disabled',parameters:{}}
const receipt=(c,patch={})=>({operation_id:c.operation_id,kind:c.kind,status:'applied',reason:'',resource_id:c.target_id||ruleID,result_revision:c.expected_result_revision,created_at:at,...patch})

test('strict UUID, canonical decimal and Unicode labels enforce separate bounds without Number conversion',()=>{
 assert.equal(alertUint(huge),huge);assert.equal(alertUint('9223372036854775807'),'9223372036854775807');assert.equal(alertSigned('-'+'9'.repeat(101)),'-'+'9'.repeat(101));assert.equal(alertLabel('中'.repeat(53)),'中'.repeat(53));assert.equal(alertLabel('x'.repeat(80)),'x'.repeat(80))
 for(const v of [1,0,null,'01','-0','+1','1e1','1.0','9223372036854775808'])assert.throws(()=>alertUint(v))
 for(const v of ['00','-0','+'+'1','9'.repeat(102)])assert.throws(()=>alertSigned(v))
 for(const v of ['', '  ', 'x\n','\u0085x','x\u3000','\ud800','中'.repeat(54),'x'.repeat(81)])assert.throws(()=>alertLabel(v))
 for(const v of [actor.toUpperCase(),'00000000-0000-0000-0000-000000000000',resource+' '])assert.throws(()=>alertID(v))
 assert(alertRole('finance_operator'));assert(alertRole('finance_approver'));for(const role of ['owner','platform_admin','finance','none'])assert(!alertRole(role))
})
test('UTC microseconds preserve pre-epoch and single-microsecond boundaries; no browser freshness inference',()=>{
 assert.equal(alertTime(at),at);assert.equal(alertMicros(at)-alertMicros('2026-11-01T00:00:00.123455Z'),1n);assert.equal(alertMicros('1969-12-31T23:59:59.999999Z'),-1n)
 for(const v of ['2026-02-29T00:00:00.000000Z','2026-11-01T00:00:00Z','2026-11-01T00:00:00.1234567Z','2026-11-01T00:00:00.123456+00:00','0000-01-01T00:00:00.000000Z'])assert.throws(()=>alertTime(v))
 const parsed=parseAlertRule(rule());assert.equal(parsed.runtime.freshness,'fresh');assert.equal(parsed.runtime.last_error_code,'source_query_timeout')
})
test('parameters use exact kind unions, hysteresis and bounded integer strings',()=>{
 assert.deepEqual(parseAlertParameters('channel_quarantined',{}),{});assert.deepEqual(parseAlertParameters('procurement_budget_low',{threshold_amount:'0',recovery_amount:huge}),{threshold_amount:'0',recovery_amount:huge})
 assert.throws(()=>parseAlertParameters('procurement_budget_low',{threshold_amount:'2',recovery_amount:'1'}));assert.throws(()=>parseAlertParameters('channel_quarantined',{enabled:true}))
 for(const p of [{minimum_age_seconds:'599',trigger_count:'1',recovery_count:'0'},{minimum_age_seconds:'604801',trigger_count:'1',recovery_count:'0'},{minimum_age_seconds:'600',trigger_count:'1001',recovery_count:'0'},{minimum_age_seconds:'600',trigger_count:'2',recovery_count:'2'}])assert.throws(()=>parseAlertParameters('customer_reconciliation_aged',p))
})
test('budget sample preserves 100-digit projections and negative 101-digit available arithmetic',()=>{
 const large='9'.repeat(100),s={kind:'procurement_budget_low',as_of:at,account_state:'paused',limit_amount:huge,held_amount:large,spent_amount:large,available_amount:(BigInt(huge)-2n*BigInt(large)).toString(),currency:'CNY'}
 assert.deepEqual(parseAlertSample(s),s)
 for(const p of [{available_amount:'0'},{held_amount:'1'.repeat(101)},{spent_amount:42},{currency:'USD'},{cash_balance:'0'}])assert.throws(()=>parseAlertSample({...s,...p}))
})
test('aging 1001 always means at least; zero has explicit nullable oldest fields and age is creation age',()=>{
 const s={kind:'customer_reconciliation_aged',as_of:at,minimum_age_seconds:'600',observed_count:'1001',count_is_lower_bound:true,oldest_created_at:'2026-10-31T23:50:00.123456Z',oldest_reference_id:resource}
 assert.equal(alertCount(parseAlertSample(s)),'至少 1,001 条');assert.throws(()=>parseAlertSample({...s,count_is_lower_bound:false}));assert.throws(()=>parseAlertSample({...s,oldest_created_at:'2026-10-31T23:50:00.123457Z'}));assert.throws(()=>parseAlertSample({...s,observed_count:'1002'}))
 assert.equal(parseAlertSample({...s,observed_count:'0',count_is_lower_bound:false,oldest_created_at:null,oldest_reference_id:null}).oldest_created_at,null)
 assert.throws(()=>parseAlertSample({...s,observed_count:'0',count_is_lower_bound:false}));assert.throws(()=>parseAlertSample({...s,kind:'procurement_budget_low'}))
})
test('capabilities preserve default-off and recovery priority, never call disabled worker healthy',()=>{
 assert.equal(parseAlertCapabilities(cap()).reason,'deployment_disabled');assert.equal(parseAlertCapabilities(cap({recovery_mode:true,reason:'recovery_mode'})).recovery_mode,true)
 for(const p of [{evaluation_available:true},{reason:'available'},{max_enabled_rules:64},{stale_after_seconds:'181'},{deployment_enabled:true}])assert.throws(()=>parseAlertCapabilities(cap(p)))
})
test('strict rules and episodes preserve sample/error/history identities and nullable lifecycle pairs',()=>{
 assert.equal(parseAlertEpisode(episode()).rule_snapshot.label,'冻结名称');assert.equal(parseAlertRule(rule()).configuration_revision,huge)
 for(const p of [{runtime:{...runtime(),last_success_at:null}},{retired_at:at},{endpoint:'secret'},{state:'disabled'}])assert.throws(()=>parseAlertRule(rule(p)))
 for(const p of [{acknowledged_by:actor},{status:'acknowledged'},{resolved_at:at},{resolve_reason:'recovered'},{initial_sample:channel({kind:'other'})},{generation:42}])assert.throws(()=>parseAlertEpisode(episode(p)))
 const resolved=episode({status:'resolved',acknowledged_by:actor,acknowledged_at:at,resolved_at:at,resolve_reason:'rule_disabled',action_revision:'3'});assert.equal(parseAlertEpisode(resolved).acknowledged_by,actor)
 const event={id:op,episode_id:episodeID,action_revision:'1',type:'opened',actor:'system',observed_at:at,safe_sample:channel(),created_at:at};assert.equal(parseAlertEvent(event,episodeID).type,'opened');assert.throws(()=>parseAlertEvent({...event,actor},episodeID));assert.throws(()=>parseAlertEvent({...event,type:'acknowledged'},episodeID));assert.throws(()=>parseAlertEvent(event,ruleID))
})
test('nonempty pages call row parsers without exposing Array.map indices as expected IDs',()=>{
 const rules=parseAlertPage({...envelope([rule()]),next_cursor:null},parseAlertRule,r=>r.id)
 const episodes=parseAlertPage({...envelope([episode()]),next_cursor:null},parseAlertEpisode,r=>r.id)
 assert.equal(rules.rows[0].id,ruleID);assert.equal(episodes.rows[0].id,episodeID)
})
test('strict raw JSON detects nested and escaped duplicate fields, trailing data, bad UTF8 surrogate and excessive depth',()=>{
 assert.equal(parseAlertJSON('{"label":"\\ud83d\\ude00"}').label,'😀')
 for(const raw of ['{"x":1,"\\u0078":2}','{"a":{"x":1,"x":2}}','{"label":"\\ud800"}','{} {}','[1,]','{"x":1.0}','{"x":1e0}','['.repeat(14)+'0'+']'.repeat(14)])assert.throws(()=>parseAlertJSON(raw))
 assert.throws(()=>parseAlertJSON(JSON.stringify('x'.repeat(262145))))
})
test('list pagination has explicit null terminal and exact canonical cursor schema, never 023 empty string',()=>{
 const c=cursor();assert.equal(alertCursor(c),c);assert.equal(alertCursor(null),null);assert.equal(parseAlertPage({...envelope([]),next_cursor:null},parseAlertRule,r=>r.id).next,null)
 for(const v of ['',undefined,c+'=','AB',Buffer.from('not json').toString('base64url'),cursor({t:'2026-11-01T00:00:00Z'}),cursor({v:2})])assert.throws(()=>alertCursor(v))
 for(const p of [{next_cursor:''},{data:[rule(),rule()]},{limit:50},{next_cursor:undefined}])assert.throws(()=>parseAlertPage({...envelope([]),next_cursor:null,...p},parseAlertRule,r=>r.id))
 assert.throws(()=>parseAlertEnvelope({...envelope(rule()),next_cursor:null},parseAlertRule));assert.throws(()=>parseAlertPage({...envelope(Array.from({length:51},()=>rule())),next_cursor:null},parseAlertRule,r=>r.id))
 assert.equal(alertQuery('episodes',{status:'resolved',kind:'channel_quarantined'},null),'/platform/alerts?status=resolved&kind=channel_quarantined');assert.equal(alertQuery('events',{},c,episodeID),`/platform/alerts/${episodeID}/events?cursor=${c}`)
 assert.throws(()=>alertQuery('rules',{scope_id:resource}));assert.throws(()=>alertQuery('rules',{kind:'all'}));assert.throws(()=>alertQuery('episodes',{limit:'10'}))
})
test('command digest matches independent frozen JSON order and escaping; operation ID is excluded',async()=>{
 const c=await alertCommand('rule_create',op,actor,createInput),other=await alertCommand('rule_create',resource,actor,createInput)
 const independent=`{"v":1,"actor":"${actor}","kind":"rule_create","target_id":null,"body":{"kind":"channel_quarantined","scope_type":"channel","scope_id":"${resource}","label":"渠道告警","state":"disabled","parameters":{}}}`
 assert.equal(c.canonical_digest,createHash('sha256').update(independent).digest('hex'));assert.equal(other.canonical_digest,c.canonical_digest);assert.deepEqual(c.body,{operation_id:op,...createInput});assert(Object.isFrozen(c.body.parameters));assert.equal(c.expected_result_revision,'1')
 const special=await alertCommand('rule_create',op,actor,{...createInput,label:'<>&\u2028中'});assert.equal(special.canonical_digest,createHash('sha256').update(independent.replace('渠道告警','<>&\\u2028中')).digest('hex'))
 await assert.rejects(alertCommand('rule_create',op,actor,{...createInput,scope_type:'tenant'}));await assert.rejects(alertCommand('rule_create',op,actor,{...createInput,extra:true}))
})
test('configure/no-op/retire/ack preserve CAS above 2^53 and unified target locks',async()=>{
 const previous=parseAlertRule(rule()),same=await alertCommand('rule_configure',op,actor,{expected_revision:huge,label:previous.label,state:previous.state,parameters:{}},ruleID,previous),changed=await alertCommand('rule_configure',op,actor,{expected_revision:huge,label:'新名称',state:'disabled',parameters:{}},ruleID,previous),retire=await alertCommand('rule_retire',op,actor,{expected_revision:huge},ruleID),ack=await alertCommand('acknowledge',op,actor,{expected_action_revision:huge},episodeID)
 assert.equal(same.expected_result_revision,huge);assert.equal(changed.expected_result_revision,'9007199254740994');assert.equal(retire.target_key,changed.target_key);assert.equal(ack.expected_result_revision,'9007199254740994');assert.equal(changed.method,'PUT')
 assert.equal(parseAlertReceipt(receipt(changed),changed).status,'applied');assert.throws(()=>parseAlertReceipt(receipt(changed,{result_revision:huge}),changed))
 assert.equal(parseAlertReceipt(receipt(changed,{status:'rejected',reason:'alert_revision_stale',result_revision:null}),changed).status,'rejected')
 for(const patch of [{kind:'rule_retire'},{operation_id:resource},{resource_id:resource},{status:'rejected',reason:'unexpected',result_revision:null},{status:'rejected',reason:'alert_revision_stale'},{status:'applied',reason:'ok'}])assert.throws(()=>parseAlertReceipt(receipt(changed,patch),changed))
 const identity=Object.fromEntries(['operation_id','actor','kind','target_id','target_key','canonical_digest','expected_revision','expected_result_revision'].map(k=>[k,ack[k]]));assert.equal(parseAlertIdentity(identity).expected_result_revision,'9007199254740994');assert.throws(()=>parseAlertIdentity({...identity,expected_result_revision:huge}))
})
