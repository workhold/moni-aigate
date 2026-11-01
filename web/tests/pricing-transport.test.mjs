import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError, setCsrf } from '../src/api.ts'
import { pricingRequest } from '../src/pricing.ts'
import { unknownPricingWrite } from '../src/pricing-state.ts'
test('new transport performs one exact request with CSRF, and never auto retries 5xx/unknown', async () => {
  const original=globalThis.fetch; const calls=[];setCsrf('test-csrf')
  globalThis.fetch=async(url,options)=>{calls.push({url,options});return new Response(JSON.stringify({error:{code:'pricing_busy',message:'busy'},request_id:'r'}),{status:503,headers:{'Content-Type':'application/json'}})}
  try { await assert.rejects(pricingRequest('/platform/price-groups','POST',{operation_id:'safe-id',label:'safe-label'}),e=>e instanceof ApiError&&unknownPricingWrite(e));assert.equal(calls.length,1);assert.equal(calls[0].url,'/api/platform/price-groups');assert.equal(calls[0].options.headers['X-CSRF-Token'],'test-csrf');assert.equal(calls[0].options.credentials,'include');assert(!('Idempotency-Key' in calls[0].options.headers)) } finally {globalThis.fetch=original;setCsrf('')}
})
test('identity epoch and local deadline both suppress late authentication events', async () => {
  const originalFetch=globalThis.fetch,originalWindow=globalThis.window,originalTimeout=globalThis.setTimeout,events=[]
  globalThis.window={dispatchEvent:event=>events.push(event)}
  let deliver;globalThis.fetch=()=>new Promise(resolve=>{deliver=resolve})
  let current=true
  try {
    const pending=pricingRequest('/platform/pricing-operations/original','GET',undefined,()=>current);current=false
    deliver(new Response(JSON.stringify({error:{code:'reauth_required',message:'reauth'},request_id:'r'}),{status:403}));await assert.rejects(pending);assert.equal(events.length,0)
    globalThis.setTimeout=(fn,ms,...args)=>originalTimeout(fn,ms===10500?1:ms,...args)
    current=true;const timed=pricingRequest('/platform/price-groups','POST',{label:'safe'},()=>current)
    await assert.rejects(timed,/等待超时/)
    deliver(new Response(JSON.stringify({error:{code:'reauth_required',message:'reauth'},request_id:'r'}),{status:403}));await new Promise(resolve=>originalTimeout(resolve,5));assert.equal(events.length,0)
  } finally {globalThis.fetch=originalFetch;globalThis.window=originalWindow;globalThis.setTimeout=originalTimeout}
})
