import { ApiError } from './api.ts'
import { commerceKinds, commerceTime, commerceUUID, parseCommerceReceipt } from './commerce.ts'
import type { CommerceExpectation, CommerceKind, CommerceReceipt } from './commerce.ts'
export { createKeyAccessScope as createCommerceScope, keyAccessContext as commerceContext } from './key-access-policy.ts'

// Only this allowlisted metadata survives navigation/reload. Never accept a
// request body, result envelope, voucher code, fingerprint, or secret here.
export interface SafeCommerceCommand { operation_id: string; kind: CommerceKind; scope: string; resource_id: string | null; created_at: string }
export interface CommerceStorage { getItem(key: string): string | null; setItem(key: string, value: string): void }
const prefix = 'moni-commerce-v1:'
function storage(): CommerceStorage { return window.sessionStorage }
function key(actor: string, scope: string) { commerceUUID(actor); if (scope !== 'platform') commerceUUID(scope); return `${prefix}${actor}:${scope}` }
function safe(value: SafeCommerceCommand): SafeCommerceCommand {
  if (!commerceKinds.includes(value.kind) || value.kind === 'voucher.redeem' && value.scope === 'platform' || value.kind !== 'voucher.redeem' && value.scope !== 'platform') throw new Error('待核查命令作用域无效。')
  if (['voucher.batch.create', 'voucher.redeem'].includes(value.kind) !== (value.resource_id === null)) throw new Error('安全命令目标无效。')
  return { operation_id: commerceUUID(value.operation_id), kind: value.kind, scope: value.scope === 'platform' ? 'platform' : commerceUUID(value.scope), resource_id: value.resource_id === null ? null : commerceUUID(value.resource_id), created_at: commerceTime(value.created_at) }
}
export function commerceTarget(command: SafeCommerceCommand): string { return command.resource_id ?? command.kind }
export function unknownCommerceWrite(error: unknown): boolean { return !(error instanceof ApiError) || error.status === 0 || error.status >= 500 || error.status === 408 || error.code === 'invalid_response' }
export function createCommerceJournal(adapter: () => CommerceStorage = storage) {
  function read(actor: string, scope: string): SafeCommerceCommand[] {
    let raw: unknown
    try { const text = adapter().getItem(key(actor, scope)); if (text !== null && text.length > 65536) throw new Error(); raw = text === null ? [] : JSON.parse(text) }
    catch { throw new Error('无法读取安全操作记录，请恢复浏览器会话存储后再提交；可以使用原操作 ID 只读核查。') }
    if (!Array.isArray(raw) || raw.length > 128) throw new Error('安全操作记录损坏，请勿重复提交。')
    const rows = raw.map(value => {
      if (!value || typeof value !== 'object' || Object.keys(value).sort().join(',') !== 'created_at,kind,operation_id,resource_id,scope') throw new Error('安全操作记录格式无效，请勿重复提交。')
      return safe(value as SafeCommerceCommand)
    })
    if (rows.some(row => row.scope !== scope) || new Set(rows.map(row => row.operation_id)).size !== rows.length) throw new Error('安全操作记录作用域不一致。')
    return rows
  }
  function write(actor: string, scope: string, rows: SafeCommerceCommand[]) {
    try { adapter().setItem(key(actor, scope), JSON.stringify(rows.map(safe))) }
    catch { throw new Error('无法保存安全操作 ID，本次不能开始提交。请保留原 ID 并恢复会话存储。') }
    if (typeof window !== 'undefined') window.dispatchEvent(new Event('moni:commerce-journal'))
  }
  return {
    list: read,
    begin(actor: string, command: SafeCommerceCommand) {
      const entry = safe(command), rows = read(actor, entry.scope)
      if (rows.length >= 128 || rows.some(row => row.operation_id === entry.operation_id || commerceTarget(row) === commerceTarget(entry))) throw new Error('该对象仍有原操作待核查，请只读查询，不能换 ID 重复提交。')
      write(actor, entry.scope, [...rows, entry]); return entry
    },
    settle(actor: string, command: SafeCommerceCommand, value: unknown): CommerceReceipt {
      const expected: CommerceExpectation = { operation_id: command.operation_id, kind: command.kind, actor, scope: command.scope }
      const receipt = parseCommerceReceipt(value, expected)
      if (receipt.status === 'accepted' && ['voucher.batch.approve', 'voucher.batch.void', 'voucher.revocation.approve'].includes(command.kind) && receipt.resource_id !== command.resource_id) throw new Error('原操作回执返回了不同目标。')
      write(actor, command.scope, read(actor, command.scope).filter(row => row.operation_id !== command.operation_id))
      return receipt
    },
    definiteFailure(actor: string, command: SafeCommerceCommand, error: unknown) {
      if (unknownCommerceWrite(error)) return
      write(actor, command.scope, read(actor, command.scope).filter(row => row.operation_id !== command.operation_id))
    },
  }
}
export const commerceJournal = createCommerceJournal()

export function createCommerceDeliveryGate() {
  let generation = 0, phase: 'idle' | 'submitting' | 'unknown' | 'settled' = 'idle'
  return {
    begin() { if (phase === 'submitting' || phase === 'unknown') throw new Error('原操作仍待核查。'); phase = 'submitting'; return ++generation },
    accepts(ticket: number) { return ticket === generation && phase === 'submitting' },
    unknown(ticket: number) { if (ticket === generation && phase === 'submitting') phase = 'unknown' },
    settled(ticket: number) { if (ticket === generation && phase === 'submitting') phase = 'settled' },
    clear() { generation++; phase = 'idle' },
  }
}

// A local deadline never proves server rollback. Its late response must be
// ignored by the writer's epoch, and the journal is resolved only by GET.
export async function commerceDeadline<T>(promise: Promise<T>, milliseconds = 12000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  try { return await Promise.race([promise, new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error('提交结果待核查。请只读查询原操作 ID，不要重复提交。')), milliseconds) })]) }
  finally { clearTimeout(timer) }
}
