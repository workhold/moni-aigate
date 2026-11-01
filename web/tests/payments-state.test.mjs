import test from 'node:test'
import assert from 'node:assert/strict'
import { createPaymentEpoch,emptyPaymentReview,resumePaymentReview,failPaymentReview,paymentPollDelay,paymentPageCursor } from '../src/payments-state.ts'
import { PaymentError } from '../src/payments-transport.ts'
import { createPaymentJournal,paymentJournalAdmission,PAYMENTS_JOURNAL_LIMIT } from '../src/payments-journal.ts'
const id='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',actor='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',scope='cccccccc-cccc-4ccc-8ccc-cccccccccccc'
const pending={operation_id:id,kind:'payment.order.create',scope,target_id:id,created_at:'2026-11-01T08:00:00.000000Z'}
test('identity, route, replacement lane, deadline and dispose reject old tickets',()=>{
 const gate=createPaymentEpoch();gate.auth('actor-a:tenant-a:owner:csrf');const old=gate.ticket('mutation'),read=gate.ticket('orders');assert.equal(gate.accepts(old),true);gate.auth('actor-a:tenant-b:billing_viewer:csrf');assert.equal(old.signal.aborted,true);assert.equal(read.signal.aborted,true);assert.equal(gate.accepts(old),false)
 const one=gate.ticket('receipt'),two=gate.ticket('receipt');assert.equal(one.signal.aborted,true);assert.equal(gate.expire(one),false);assert.equal(gate.expire(two),true);assert.equal(gate.accepts(two),false)
 const live=gate.ticket('journal');gate.dispose();assert.equal(live.signal.aborted,true);assert.equal(gate.accepts(live),false)
})
test('reload stays unknown; only received precommit rejection is known; polling is bounded',()=>{
 assert.equal(emptyPaymentReview().phase,'idle');const restored=resumePaymentReview(pending);assert.equal(restored.phase,'unknown');assert.equal(restored.cleanupBlocked,true)
 const submitting={...restored,phase:'submitting'};for(const cause of [new PaymentError('commerce_outcome_unknown',503),new PaymentError('not_found',404),new Error('offline')])assert.equal(failPaymentReview(submitting,cause).phase,'unknown')
 assert.equal(failPaymentReview(submitting,new PaymentError('forbidden',403,true)).phase,'known_failure');assert.deepEqual(Array.from({length:7},(_,i)=>paymentPollDelay(i)),[1000,2000,5000,10000,20000,null,null]);assert.throws(()=>paymentPageCursor('same','same',[]));assert.throws(()=>paymentPageCursor('old','new',['old']))
})
test('journal admission restores original target, enforces 128 and rejects corruption',()=>{
 assert.equal(paymentJournalAdmission([pending],{...pending,operation_id:actor}).inserted,false);assert.equal(paymentJournalAdmission([pending],{...pending,operation_id:actor}).pending.operation_id,id)
 const rows=Array.from({length:PAYMENTS_JOURNAL_LIMIT},()=>({...pending,operation_id:crypto.randomUUID(),target_id:crypto.randomUUID()}))
 assert.throws(()=>paymentJournalAdmission(rows,{...pending,operation_id:crypto.randomUUID(),target_id:crypto.randomUUID()}));assert.throws(()=>paymentJournalAdmission([{...pending,body:'secret'}],pending));assert.throws(()=>paymentJournalAdmission([pending,{...pending,target_id:actor}],{...pending,target_id:scope}));assert.equal(paymentJournalAdmission(rows,rows[127]).inserted,false)
})
test('unavailable IndexedDB never falls back or permits a write',async()=>{
 assert.equal(globalThis.indexedDB,undefined);const journal=createPaymentJournal();await assert.rejects(journal.begin(actor,pending),/不会发送新命令/);await assert.rejects(journal.list(actor,scope),/不会发送新命令/);journal.close()
})
