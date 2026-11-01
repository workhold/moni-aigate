import { parsePaymentPending, paymentID } from './payments.ts'
import type { PaymentPending } from './payments.ts'
export const PAYMENTS_JOURNAL_DB='moni-aigate-payments-v1', PAYMENTS_JOURNAL_STORE='pending', PAYMENTS_JOURNAL_LIMIT=128
export interface PaymentJournal { list(actor:string,scope:string,signal?:AbortSignal):Promise<PaymentPending[]>; begin(actor:string,value:PaymentPending,signal?:AbortSignal):Promise<{inserted:boolean;pending:PaymentPending}>; finish(actor:string,value:PaymentPending,signal?:AbortSignal):Promise<boolean>; subscribe(callback:()=>void):()=>void; close():void }
type Key=[string,string,string,string]
const unavailable=()=>new Error('无法可靠保存或读取原操作编号；本次不会发送新命令。请恢复浏览器存储后核查原编号。')
const corrupt=()=>new Error('支付待核查标识损坏、已满或身份不一致；请保留原编号人工核查，不能清空后重发。')
function namespace(actor:string,scope:string):void{paymentID(actor);if(scope!=='platform')paymentID(scope)}
function key(actor:string,p:PaymentPending):Key{namespace(actor,p.scope);return [actor,p.scope,p.kind,p.target_id]}
function match(raw:unknown,lookup:IDBValidKey,actor:string,scope:string):PaymentPending{const p=parsePaymentPending(raw);if(p.scope!==scope||JSON.stringify(lookup)!==JSON.stringify(key(actor,p)))throw corrupt();return p}
export function paymentJournalAdmission(values:PaymentPending[],pending:PaymentPending):{inserted:boolean;pending:PaymentPending}{
 if(values.length>PAYMENTS_JOURNAL_LIMIT)throw corrupt();const ids=new Set<string>(),targets=new Set<string>()
 for(const v of values){parsePaymentPending(v);const target=JSON.stringify([v.kind,v.target_id]);if(v.scope!==pending.scope||ids.has(v.operation_id)||targets.has(target))throw corrupt();ids.add(v.operation_id);targets.add(target)}
 const current=values.find(v=>v.kind===pending.kind&&v.target_id===pending.target_id);if(current)return {inserted:false,pending:current}
 if(values.length>=PAYMENTS_JOURNAL_LIMIT||ids.has(pending.operation_id))throw corrupt();return {inserted:true,pending}
}
export function createPaymentJournal(factory?:IDBFactory):PaymentJournal{
 let dbPromise:Promise<IDBDatabase>|undefined,closed=false,channel:BroadcastChannel|undefined
 const listeners=new Set<()=>void>()
 function hint(){try{channel?.postMessage('refresh')}catch{/* The durable transaction remains authoritative. */}}
 function open():Promise<IDBDatabase>{if(closed)return Promise.reject(unavailable());if(dbPromise)return dbPromise
  dbPromise=new Promise((resolve,reject)=>{const idb=factory||globalThis.indexedDB;if(!idb){reject(unavailable());return}let request:IDBOpenDBRequest;try{request=idb.open(PAYMENTS_JOURNAL_DB,1)}catch{reject(unavailable());return}let settled=false;const timer=setTimeout(fail,4000)
   function fail(){if(!settled){settled=true;clearTimeout(timer);reject(unavailable())}}
   request.onerror=request.onblocked=fail
   request.onupgradeneeded=event=>{if((event as IDBVersionChangeEvent).oldVersion!==0||request.result.objectStoreNames.length!==0){request.transaction?.abort();return}request.result.createObjectStore(PAYMENTS_JOURNAL_STORE)}
   request.onsuccess=()=>{const db=request.result;if(settled||closed){db.close();fail();return}try{if(db.version!==1||db.objectStoreNames.length!==1||!db.objectStoreNames.contains(PAYMENTS_JOURNAL_STORE))throw unavailable();const store=db.transaction(PAYMENTS_JOURNAL_STORE,'readonly').objectStore(PAYMENTS_JOURNAL_STORE);if(store.keyPath!==null||store.autoIncrement||store.indexNames.length!==0)throw unavailable();db.onversionchange=()=>{closed=true;db.close()};if(typeof window!=='undefined'&&globalThis.BroadcastChannel){channel=new BroadcastChannel(PAYMENTS_JOURNAL_DB);channel.onmessage=()=>{for(const fn of listeners)fn()}}settled=true;clearTimeout(timer);resolve(db)}catch{db.close();fail()}}
  });return dbPromise
 }
 async function transaction<T>(mode:IDBTransactionMode,signal:AbortSignal|undefined,action:(store:IDBObjectStore,set:(v:T)=>void,abort:(e:Error)=>void)=>void):Promise<T>{
  if(signal?.aborted)throw unavailable();const db=await open();if(closed||signal?.aborted)throw unavailable()
  return new Promise((resolve,reject)=>{let tx:IDBTransaction,value:T,hasValue=false,error:Error|undefined;try{tx=db.transaction(PAYMENTS_JOURNAL_STORE,mode)}catch{reject(unavailable());return}
   const abort=(cause:Error)=>{error=cause;try{tx.abort()}catch{reject(cause)}},cancel=()=>abort(unavailable()),timer=setTimeout(cancel,4000),done=()=>{clearTimeout(timer);signal?.removeEventListener('abort',cancel)}
   signal?.addEventListener('abort',cancel,{once:true});tx.oncomplete=()=>{done();if(hasValue){resolve(value);if(mode==='readwrite')hint()}else reject(unavailable())};tx.onabort=tx.onerror=()=>{done();reject(error||unavailable())}
   try{action(tx.objectStore(PAYMENTS_JOURNAL_STORE),next=>{value=next;hasValue=true},abort)}catch{abort(unavailable())}
  })
 }
 function scan(store:IDBObjectStore,actor:string,scope:string,finish:(rows:PaymentPending[])=>void,abort:(e:Error)=>void){
  namespace(actor,scope);const rows:PaymentPending[]=[],request=store.openCursor(IDBKeyRange.bound([actor,scope],[actor,scope+'\u0000'],false,true))
  request.onsuccess=()=>{const cursor=request.result;if(!cursor){finish(rows);return}try{rows.push(match(cursor.value,cursor.key,actor,scope));if(rows.length>PAYMENTS_JOURNAL_LIMIT)throw corrupt();cursor.continue()}catch{abort(corrupt())}}
 }
 return {
  async list(actor,scope,signal){namespace(actor,scope);return transaction('readonly',signal,(store,set,abort)=>scan(store,actor,scope,rows=>{const ids=new Set(rows.map(v=>v.operation_id));if(ids.size!==rows.length){abort(corrupt());return}set(rows.sort((a,b)=>b.created_at.localeCompare(a.created_at)))},abort))},
  async begin(actor,raw,signal){const pending=parsePaymentPending(raw);namespace(actor,pending.scope);return transaction('readwrite',signal,(store,set,abort)=>scan(store,actor,pending.scope,rows=>{try{const result=paymentJournalAdmission(rows,pending);if(result.inserted)store.add(pending,key(actor,pending));set(result)}catch{abort(corrupt())}},abort))},
  async finish(actor,raw,signal){const pending=parsePaymentPending(raw),lookup=key(actor,pending);return transaction('readwrite',signal,(store,set,abort)=>{const request=store.get(lookup);request.onsuccess=()=>{try{if(signal?.aborted){abort(unavailable());return}if(request.result===undefined){set(true);return}const current=match(request.result,lookup,actor,pending.scope);if(JSON.stringify(current)!==JSON.stringify(pending)){set(false);return}store.delete(lookup);set(true)}catch{abort(corrupt())}}})},
  subscribe(callback){listeners.add(callback);return()=>listeners.delete(callback)},
  close(){closed=true;listeners.clear();channel?.close();void dbPromise?.then(db=>db.close()).catch(()=>{})},
 }
}
