import test from 'node:test'
import assert from 'node:assert/strict'
import { PeriodicBudgetError, parsePeriodicError, periodicRequest, periodicUnknownWrite } from '../src/periodic-budget-transport.ts'
const id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', path = `/api/keys/${id}/periodic-budget`
const json = (value, status = 200, headers = {}) => new Response(typeof value === 'string' ? value : JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json', ...headers } })
const error = code => ({ error: { code, message: 'controlled server text' }, request_id: 'test-request' })
const body = { operation_id: id, expected_revision: '0', period_kind: 'utc_day', enabled: true, limit_amount: '9007199254740993' }
test('only exact error/status pairs classify a PUT failure as known', () => {
  for (const [code, status] of [['invalid_request',400],['invalid_credentials',401],['reauth_required',403],['not_found',404],['periodic_budget_conflict',409],['request_too_large',413],['rate_limited',429]]) assert.equal(periodicUnknownWrite(parsePeriodicError(error(code), status, '3')), false)
  for (const [code, status] of [['periodic_budget_unavailable',503],['periodic_budget_outcome_unknown',503]]) assert.equal(periodicUnknownWrite(parsePeriodicError(error(code),status)), true)
  for (const [raw, status] of [[error('forbidden'),409],[error('unknown_code'),400],[{...error('forbidden'),extra:true},403],[{error:{code:'forbidden',message:'x',extra:1},request_id:'r'},403],[{...error('forbidden'),request_id:''},403]]) assert.throws(() => parsePeriodicError(raw,status))
  assert.equal(parsePeriodicError(error('rate_limited'),429,'7').retryAfterMs,7000)
  assert.equal(periodicUnknownWrite(new Error('network')),true)
})
test('transport sends one canonical PUT with exact string amounts and bound abort signal', async () => {
  let calls = 0
  const value = await periodicRequest(path, { method:'PUT',body,csrf:'test-csrf',decode:raw=>raw,fetcher:async (url, init) => { calls++; assert.equal(url,path); assert.equal(init.credentials,'include'); assert.equal(init.cache,'no-store'); assert.equal(init.redirect,'error'); assert.equal(init.headers['X-CSRF-Token'],'test-csrf'); assert.equal(JSON.parse(init.body).limit_amount,'9007199254740993'); assert.ok(init.signal instanceof AbortSignal); return json({data:'ok',request_id:'r'},201) } })
  assert.equal(value.data,'ok'); assert.equal(calls,1)
})
test('malformed, truncated, oversized, wrong status and binding failures remain unknown', async () => {
  const cases = [() => json('{"error":{"code":"forbidden","code":"forbidden","message":"x"},"request_id":"r"}',403), () => json('{"error":',400), () => json('x'.repeat(131073),400), () => new Response(new Uint8Array([0xff]),{status:400,headers:{'Content-Type':'application/json'}}), () => json(error('not_found'),200), () => json(error('forbidden'),409), () => new Response('{}',{status:200}), () => json('{}',202)]
  for (const response of cases) await assert.rejects(periodicRequest(path,{method:'PUT',body,csrf:'x',decode:()=>{throw new Error('binding mismatch')},fetcher:async()=>response()}),cause=>cause instanceof PeriodicBudgetError && periodicUnknownWrite(cause))
})
test('timeout and external abort actually abort fetch without retrying', async () => {
  let calls=0, observed
  const fetcher=async (_url,init)=>{calls++;observed=init.signal;return new Promise(()=>{})}
  await assert.rejects(periodicRequest(path,{method:'PUT',body,csrf:'x',decode:x=>x,fetcher,timeoutMs:5}),cause=>periodicUnknownWrite(cause))
  assert.equal(observed.aborted,true);assert.equal(calls,1)
  const controller=new AbortController();const pending=periodicRequest(path,{signal:controller.signal,decode:x=>x,fetcher,timeoutMs:1000});controller.abort()
  await assert.rejects(pending,cause=>periodicUnknownWrite(cause));assert.equal(observed.aborted,true);assert.equal(calls,2)
  const before=new AbortController();before.abort();await assert.rejects(periodicRequest(path,{signal:before.signal,decode:x=>x,fetcher}));assert.equal(calls,2)
})
test('local preflight cannot terminate an already durable pending operation',async()=>{
  let calls=0
  await assert.rejects(periodicRequest(path,{method:'PUT',body,decode:x=>x,fetcher:async()=>{calls++;return json({})}}),cause=>cause instanceof PeriodicBudgetError && periodicUnknownWrite(cause))
  assert.equal(calls,0)
  await assert.rejects(periodicRequest(path,{decode:x=>x,fetcher:async()=>new Response(new Uint8Array([0xef,0xbb,0xbf,0x7b,0x7d]),{headers:{'Content-Type':'application/json'}})}),cause=>periodicUnknownWrite(cause))
})
test('opaque cursor is encoded once and unsupported paths / queries never fetch', async () => {
  let calls=0;const fetcher=async()=>{calls++;return json({data:[],request_id:'r',next_cursor:''})}
  await periodicRequest(`${path}/windows?cursor=${encodeURIComponent('pbc1.public-token.signature')}`,{decode:x=>x,fetcher})
  for(const bad of [path+'?actor='+id,path+'/windows?cursor=x&cursor=y',path+'/windows?cursor=',path+'/windows?cursor=%ZZ','/api/wallet/periodic-budget?x=1','/api/keys/'+id.toUpperCase()+'/periodic-budget'])await assert.rejects(periodicRequest(bad,{decode:x=>x,fetcher}))
  await assert.rejects(periodicRequest('/api/wallet/periodic-budget',{method:'PUT',body,csrf:'x',decode:x=>x,fetcher}))
  assert.equal(calls,1)
})
