import test from 'node:test'
import assert from 'node:assert/strict'
import { createPeriodicEpoch, editingPeriodicReview, failPeriodicReview, periodicPageTransition, periodicPollDelay, periodicReviewBlocked, resumePeriodicReview } from '../src/periodic-budget-state.ts'
import { createPeriodicJournal } from '../src/periodic-budget-journal.ts'
import { PeriodicBudgetError } from '../src/periodic-budget-transport.ts'
const pending={version:1,operation_id:'dddddddd-dddd-4ddd-8ddd-dddddddddddd',kind:'key_periodic_budget.configure',actor:'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',identity_scope:'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',target:'/api/keys/cccccccc-cccc-4ccc-8ccc-cccccccccccc/periodic-budget',input_fingerprint:'f'.repeat(64),state:'pending'}
test('auth, target, lane replacement and command epoch cancel actual old requests',()=>{
  const gate=createPeriodicEpoch();gate.auth('actor-a:tenant-a:owner:csrf');gate.view('target-a')
  const oldView=gate.ticket('view'),oldList=gate.ticket('windows'),oldCommand=gate.ticket('command')
  gate.command();assert.equal(oldCommand.signal.aborted,true);assert.equal(gate.accepts(oldCommand),false);assert.equal(gate.accepts(oldView),true)
  const nextList=gate.ticket('windows');assert.equal(oldList.signal.aborted,true);assert.equal(gate.accepts(nextList),true)
  gate.view('target-b');assert.equal(oldView.signal.aborted,true);assert.equal(nextList.signal.aborted,true)
  const operation=gate.ticket('operation');gate.auth('actor-b:tenant-b:member:csrf');assert.equal(operation.signal.aborted,true);assert.equal(gate.accepts(operation),false)
  const live=gate.ticket('view');gate.dispose();assert.equal(live.signal.aborted,true);assert.equal(gate.accepts(live),false)
})
test('refresh pending always resumes unknown and terminal cleanup failure continues blocking',()=>{
  assert.equal(periodicReviewBlocked(editingPeriodicReview()),false)
  const restored=resumePeriodicReview(pending);assert.equal(restored.state,'unknown');assert.equal(periodicReviewBlocked(restored),true)
  const submitting={...restored,state:'submitting'}
  const unknown=failPeriodicReview(submitting,new PeriodicBudgetError('periodic_budget_unavailable',503));assert.equal(unknown.state,'unknown');assert.equal(unknown.pending.operation_id,pending.operation_id)
  const rejected=failPeriodicReview(submitting,new PeriodicBudgetError('periodic_budget_conflict',409,true));assert.equal(rejected.state,'known_failure');assert.equal(periodicReviewBlocked(rejected),true)
  assert.equal(periodicReviewBlocked({...rejected,cleanupBlocked:false}),false)
  assert.equal(periodicReviewBlocked({...restored,state:'confirmed',cleanupBlocked:true}),true)
  assert.deepEqual([0,1,2,3,20].map(i=>periodicPollDelay(i)),[1000,2000,5000,5000,5000]);assert.equal(periodicPollDelay(0,9000),9000)
})
test('unavailable IndexedDB fails closed without memory or sessionStorage fallback',async()=>{
  assert.equal(globalThis.indexedDB,undefined)
  const journal=createPeriodicJournal()
  await assert.rejects(journal.begin(pending),/不会发送新命令/)
  await assert.rejects(journal.load(pending.actor,pending.identity_scope,pending.target),/不会发送新命令/)
  journal.close()
})
test('page 1→2→3→2→3 follows the current path while real cursor loops fail',()=>{
  let path=periodicPageTransition([], '', 'cursor-1', 'first')
  path=periodicPageTransition(path,'cursor-1','cursor-2','next')
  path=periodicPageTransition(path,'cursor-2','cursor-3','next')
  assert.deepEqual(path,['cursor-1','cursor-2'])
  path=periodicPageTransition(path,'cursor-1','cursor-2','previous')
  assert.deepEqual(path,['cursor-1'])
  path=periodicPageTransition(path,'cursor-2','cursor-3','next')
  assert.deepEqual(path,['cursor-1','cursor-2'])
  assert.throws(()=>periodicPageTransition(path,'cursor-3','cursor-1','next'))
  assert.throws(()=>periodicPageTransition(path,'cursor-3','cursor-3','next'))
})
