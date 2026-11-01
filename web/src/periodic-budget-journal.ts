import { parsePeriodicPending, periodicID } from './periodic-budget.ts'
import type { PeriodicPending } from './periodic-budget.ts'

export const PERIODIC_JOURNAL_DB = 'moni-aigate-periodic-budget-v1'
export const PERIODIC_JOURNAL_STORE = 'pending'
type Key = [string, string, string]
export interface PeriodicJournal { load(actor: string, identityScope: string, target: string): Promise<PeriodicPending | null>; begin(value: PeriodicPending): Promise<{ inserted: boolean; pending: PeriodicPending }>; finish(value: PeriodicPending): Promise<boolean>; close(): void }
const unavailable = () => new Error('无法可靠读写原操作标识；本次不会发送新命令。请恢复浏览器存储后再核查，不能清空标识后重发。')
const corrupt = () => new Error('待核查预算标识损坏或身份不一致；请按原操作编号人工核查，不能自动重置后重发。')
function key(actor: string, identityScope: string, target: string): Key { periodicID(actor); if (identityScope !== 'platform') periodicID(identityScope); if (typeof target !== 'string' || !target.startsWith('/api/') || target.length > 200) throw corrupt(); return [actor, identityScope, target] }
function matching(raw: unknown, expected: Key): PeriodicPending {
  let value: PeriodicPending
  try { value = parsePeriodicPending(raw) } catch { throw corrupt() }
  if (value.actor !== expected[0] || value.identity_scope !== expected[1] || value.target !== expected[2]) throw corrupt()
  return value
}
// IndexedDB is the production synchronization primitive. There is deliberately
// no memory, sessionStorage or localStorage fallback when durable storage fails.
export function createPeriodicJournal(factory?: IDBFactory): PeriodicJournal {
  let database: Promise<IDBDatabase> | undefined, closed = false
  function open(): Promise<IDBDatabase> {
    if (closed) return Promise.reject(unavailable())
    if (database) return database
    database = new Promise((resolve, reject) => {
      const idb = factory || globalThis.indexedDB
      if (!idb) { reject(unavailable()); return }
      let request: IDBOpenDBRequest
      try { request = idb.open(PERIODIC_JOURNAL_DB, 1) } catch { reject(unavailable()); return }
      let settled = false
      const fail = () => { if (!settled) { settled = true; clearTimeout(timer); reject(unavailable()) } }
      const timer = setTimeout(fail, 4000)
      request.onerror = fail; request.onblocked = fail
      request.onupgradeneeded = event => {
        if ((event as IDBVersionChangeEvent).oldVersion !== 0 || request.result.objectStoreNames.length !== 0) { request.transaction?.abort(); return }
        request.result.createObjectStore(PERIODIC_JOURNAL_STORE, { keyPath: ['actor', 'identity_scope', 'target'] })
      }
      request.onsuccess = () => {
        const db = request.result
        if (settled || closed) { db.close(); fail(); return }
        try {
          if (db.version !== 1 || db.objectStoreNames.length !== 1 || !db.objectStoreNames.contains(PERIODIC_JOURNAL_STORE)) throw unavailable()
          const store = db.transaction(PERIODIC_JOURNAL_STORE, 'readonly').objectStore(PERIODIC_JOURNAL_STORE)
          if (JSON.stringify(store.keyPath) !== JSON.stringify(['actor', 'identity_scope', 'target']) || store.autoIncrement || store.indexNames.length !== 0) throw unavailable()
          db.onversionchange = () => { closed = true; db.close() }
          settled = true; clearTimeout(timer); resolve(db)
        } catch { db.close(); fail() }
      }
    })
    return database
  }
  async function transaction<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore, set: (value: T) => void, abort: (cause: Error) => void) => void): Promise<T> {
    const db = await open()
    if (closed) throw unavailable()
    return new Promise((resolve, reject) => {
      let tx: IDBTransaction, value: T, hasValue = false, failure: Error | undefined
      try { tx = db.transaction(PERIODIC_JOURNAL_STORE, mode) } catch { reject(unavailable()); return }
      // No request success resolves the promise: only transaction completion is
      // a durable admission/terminal deletion boundary, including across tabs.
      tx.oncomplete = () => { if (hasValue) resolve(value); else reject(unavailable()) }
      tx.onabort = tx.onerror = () => reject(failure || unavailable())
      const abort = (cause: Error) => { failure = cause; try { tx.abort() } catch { reject(cause) } }
      try { action(tx.objectStore(PERIODIC_JOURNAL_STORE), next => { value = next; hasValue = true }, abort) } catch { abort(unavailable()) }
    })
  }
  return {
    async load(actor, identityScope, target) {
      const lookup = key(actor, identityScope, target)
      return transaction('readonly', (store, set, abort) => { const request = store.get(lookup); request.onsuccess = () => { try { set(request.result === undefined ? null : matching(request.result, lookup)) } catch { abort(corrupt()) } } })
    },
    async begin(raw) {
      const pending = parsePeriodicPending(raw), lookup = key(pending.actor, pending.identity_scope, pending.target)
      return transaction('readwrite', (store, set, abort) => {
        const request = store.get(lookup)
        request.onsuccess = () => {
          try {
            if (request.result !== undefined) { set({ inserted: false, pending: matching(request.result, lookup) }); return }
            store.add(pending); set({ inserted: true, pending })
          } catch { abort(corrupt()) }
        }
      })
    },
    async finish(raw) {
      const pending = parsePeriodicPending(raw), lookup = key(pending.actor, pending.identity_scope, pending.target)
      return transaction('readwrite', (store, set, abort) => {
        const request = store.get(lookup)
        request.onsuccess = () => {
          try {
            if (request.result === undefined) { set(true); return }
            const current = matching(request.result, lookup)
            if (current.operation_id !== pending.operation_id || current.input_fingerprint !== pending.input_fingerprint || current.kind !== pending.kind) { set(false); return }
            store.delete(lookup); set(true)
          } catch { abort(corrupt()) }
        }
      })
    },
    close() { closed = true; void database?.then(db => db.close(), () => {}) },
  }
}
