// Run in a real browser on the console origin. These are synthetic metadata
// records only; cleanup touches their random actor/key, never the whole DB.
import { createPeriodicJournal, PERIODIC_JOURNAL_DB } from '../src/periodic-budget-journal.ts'
const check = (condition, message) => { if (!condition) throw new Error(message) }
export function publicPeriodicPending(actor, tenant, target, operation = crypto.randomUUID()) { return {version:1,operation_id:operation,kind:'key_periodic_budget.configure',actor,identity_scope:tenant,target:`/api/keys/${target}/periodic-budget`,input_fingerprint:'f'.repeat(64),state:'pending'} }
export async function runPeriodicIndexedDBQA() {
  const actor=crypto.randomUUID(),tenant=crypto.randomUUID(),key=crypto.randomUUID(),first=publicPeriodicPending(actor,tenant,key),second={...first,operation_id:crypto.randomUUID()}
  let completes=0
  const factory={open(name,version){const request=indexedDB.open(name,version);request.addEventListener('success',()=>{const db=request.result,original=db.transaction.bind(db);db.transaction=(...args)=>{const tx=original(...args);if(args[1]==='readwrite')tx.addEventListener('complete',()=>completes++);return tx}});return request}}
  const a=createPeriodicJournal(factory),b=createPeriodicJournal(),c=createPeriodicJournal()
  let rawDB
  try {
    const before=completes
    const [left,right]=await Promise.all([a.begin(first),b.begin(second)])
    check(Number(left.inserted)+Number(right.inserted)===1,'native concurrent connections admitted two pending operations')
    check(left.pending.operation_id===right.pending.operation_id,'connections did not restore the same original ID')
    check(completes>before,'begin resolved before its native readwrite transaction complete')
    const winner=left.pending,loser=winner.operation_id===first.operation_id?second:first
    const restored=await c.load(actor,tenant,first.target);check(restored?.operation_id===winner.operation_id,'reopened connection lost pending')
    check(await c.finish(loser)===false,'late result deleted a different original ID')
    check((await b.load(actor,tenant,first.target))?.operation_id===winner.operation_id,'late terminal cleanup erased pending')
    check(await a.finish(winner),'matching terminal cleanup failed')
    check(await b.load(actor,tenant,first.target)===null,'terminal cleanup was not durable')
    rawDB=await new Promise((resolve,reject)=>{const r=indexedDB.open(PERIODIC_JOURNAL_DB,1);r.onsuccess=()=>resolve(r.result);r.onerror=()=>reject(new Error('native raw DB unavailable'))})
    await new Promise((resolve,reject)=>{const tx=rawDB.transaction('pending','readwrite');tx.objectStore('pending').put({...first,body:{forbidden:true}});tx.oncomplete=resolve;tx.onabort=()=>reject(new Error('synthetic corrupt fixture failed'))})
    let rejected=false;try{await c.load(actor,tenant,first.target)}catch{rejected=true}check(rejected,'corrupt metadata silently accepted')
    rejected=false;try{await b.begin(second)}catch{rejected=true}check(rejected,'corrupt pending silently overwritten')
    return {nativeIndexedDB:true,atomicConcurrentAdmission:true,completeBeforeSendBoundary:true,durableReopen:true,lateIDCannotDelete:true,corruptFailsClosed:true}
  } finally {
    if(!rawDB)rawDB=await new Promise((resolve,reject)=>{const r=indexedDB.open(PERIODIC_JOURNAL_DB,1);r.onsuccess=()=>resolve(r.result);r.onerror=()=>reject(new Error('QA cleanup DB unavailable'))})
    await new Promise((resolve,reject)=>{const tx=rawDB.transaction('pending','readwrite');tx.objectStore('pending').delete([actor,tenant,first.target]);tx.oncomplete=resolve;tx.onabort=()=>reject(new Error('QA metadata cleanup failed'))})
    rawDB.close();a.close();b.close();c.close()
  }
}
