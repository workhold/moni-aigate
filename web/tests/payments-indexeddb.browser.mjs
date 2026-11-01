// Run on the console's real origin; only these random actor namespaces are
// touched during cleanup. Nothing clears the database or real pending records.
import { createPaymentJournal,PAYMENTS_JOURNAL_DB,PAYMENTS_JOURNAL_STORE } from '../src/payments-journal.ts'
const check=(condition,message)=>{if(!condition)throw new Error(message)}
export async function runPaymentsIndexedDBQA(){
 const actor=crypto.randomUUID(),scope=crypto.randomUUID(),otherActor=crypto.randomUUID(),target=crypto.randomUUID()
 const make=(id=crypto.randomUUID(),resource=target)=>({operation_id:id,kind:'payment.order.create',scope,target_id:resource,created_at:'2026-11-01T08:00:00.000000Z'})
 let complete=0,armedAdd,armedDelete
 const factory={open(name,version){const request=indexedDB.open(name,version);request.addEventListener('success',()=>{const db=request.result,transact=db.transaction.bind(db);db.transaction=(...args)=>{const tx=transact(...args);if(args[1]==='readwrite'){
  tx.addEventListener('complete',()=>{complete++});const objectStore=tx.objectStore.bind(tx);tx.objectStore=(...names)=>{const store=objectStore(...names),add=store.add.bind(store),remove=store.delete.bind(store);store.add=(...values)=>{const request=add(...values);if(armedAdd){const controller=armedAdd;armedAdd=undefined;controller.abort()}return request};store.delete=(...values)=>{const request=remove(...values);if(armedDelete){const controller=armedDelete;armedDelete=undefined;controller.abort()}return request};return store}
 }return tx}});return request}}
 const a=createPaymentJournal(factory),b=createPaymentJournal(),reopened=createPaymentJournal();let rawDB
 try{
  const first=make(),second=make(),before=complete
  const [left,right]=await Promise.all([a.begin(actor,first),b.begin(actor,second)])
  check(Number(left.inserted)+Number(right.inserted)===1,'two connections admitted concurrent writes to one target');check(left.pending.operation_id===right.pending.operation_id,'concurrent admission lost original ID');check(complete>before,'begin resolved before native transaction completion')
  const winner=left.pending,loser=winner.operation_id===first.operation_id?second:first
  check((await reopened.list(actor,scope))[0]?.operation_id===winner.operation_id,'reopened connection lost pending')
  check((await reopened.list(otherActor,scope)).length===0,'another actor saw pending')
  check(await b.finish(actor,loser)===false,'late terminal erased another ID')
  const abortDelete=new AbortController();armedDelete=abortDelete;let rejected=false;try{await a.finish(actor,winner,abortDelete.signal)}catch{rejected=true};check(rejected,'epoch cancellation did not abort terminal deletion');check((await b.list(actor,scope))[0]?.operation_id===winner.operation_id,'aborted cleanup deleted original journal')
  check(await b.finish(actor,winner),'matching terminal cleanup failed');check((await reopened.list(actor,scope)).length===0,'terminal cleanup was not durable')
  const abortAdd=new AbortController();armedAdd=abortAdd;rejected=false;try{await a.begin(actor,make(),abortAdd.signal)}catch{rejected=true};check(rejected,'aborted admission resolved success');check((await b.list(actor,scope)).length===0,'aborted admission leaked a record')
  await Promise.all(Array.from({length:128},()=>a.begin(actor,make(crypto.randomUUID(),crypto.randomUUID()))));check((await b.list(actor,scope)).length===128,'128 records did not remain durable')
  rejected=false;try{await b.begin(actor,make(crypto.randomUUID(),crypto.randomUUID()))}catch{rejected=true};check(rejected,'129th pending was admitted');check((await b.list(actor,scope)).length===128,'full journal silently replaced a record')
  await b.begin(otherActor,make());check((await reopened.list(otherActor,scope)).length===1,'quota was not scoped to actor')
  rawDB=await new Promise((resolve,reject)=>{const request=indexedDB.open(PAYMENTS_JOURNAL_DB,1);request.onsuccess=()=>resolve(request.result);request.onerror=()=>reject(new Error('raw QA database unavailable'))})
  const row=(await b.list(actor,scope))[0]
  // Every suffix in the exact actor/scope namespace must be inspected, even
  // keys that sort beyond all normal string kind names.
  for(const suffix of ['\uffffx',[],23]){
   const damaged=[actor,scope,suffix,row.target_id]
   await new Promise((resolve,reject)=>{const tx=rawDB.transaction(PAYMENTS_JOURNAL_STORE,'readwrite');tx.objectStore(PAYMENTS_JOURNAL_STORE).put(row,damaged);tx.oncomplete=resolve;tx.onabort=()=>reject(new Error('damaged key fixture failed'))})
   rejected=false;try{await reopened.list(actor,scope)}catch{rejected=true};check(rejected,'namespace scan missed damaged suffix')
   rejected=false;try{await b.begin(actor,row)}catch{rejected=true};check(rejected,'damaged suffix bypassed atomic admission')
   await new Promise((resolve,reject)=>{const tx=rawDB.transaction(PAYMENTS_JOURNAL_STORE,'readwrite');tx.objectStore(PAYMENTS_JOURNAL_STORE).delete(damaged);tx.oncomplete=resolve;tx.onabort=()=>reject(new Error('damaged key cleanup failed'))})
  }
  await new Promise((resolve,reject)=>{const tx=rawDB.transaction(PAYMENTS_JOURNAL_STORE,'readwrite');tx.objectStore(PAYMENTS_JOURNAL_STORE).put({...row,body:'forbidden'},[actor,scope,row.kind,row.target_id]);tx.oncomplete=resolve;tx.onabort=()=>reject(new Error('synthetic corrupt write failed'))})
  rejected=false;try{await reopened.list(actor,scope)}catch{rejected=true};check(rejected,'corrupt journal was accepted')
  return {nativeIndexedDB:true,concurrentTargetUnique:true,transactionCompleteBeforeSend:true,durableReopen:true,actorIsolation:true,lateIDCannotDelete:true,epochAbortPreservesPending:true,abortAdmissionNeverSucceeds:true,quota128:true,corruptFailsClosed:true,allNamespaceSuffixesChecked:true}
 }finally{
  if(!rawDB)rawDB=await new Promise((resolve,reject)=>{const request=indexedDB.open(PAYMENTS_JOURNAL_DB,1);request.onsuccess=()=>resolve(request.result);request.onerror=()=>reject(new Error('QA cleanup database unavailable'))})
  for(const who of [actor,otherActor])await new Promise((resolve,reject)=>{const tx=rawDB.transaction(PAYMENTS_JOURNAL_STORE,'readwrite'),request=tx.objectStore(PAYMENTS_JOURNAL_STORE).openCursor(IDBKeyRange.bound([who,scope],[who,scope+'\u0000'],false,true));request.onsuccess=()=>{const cursor=request.result;if(cursor){cursor.delete();cursor.continue()}};tx.oncomplete=resolve;tx.onabort=()=>reject(new Error('random QA namespace cleanup failed'))})
  rawDB.close();a.close();b.close();reopened.close()
 }
}
