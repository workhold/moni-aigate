import { ApiError } from './api.ts'
import { parsePricingIdentity, parsePricingReceipt, pricingID, pricingIdentity } from './pricing.ts'
import type { PricingCommand, PricingCommandIdentity, PricingPage, PricingReceipt } from './pricing.ts'

export type PricingReview = { command: PricingCommandIdentity | PricingCommand; state: 'submitting' | 'unknown' | 'confirmed' | 'rejected'; receipt?: PricingReceipt; message?: string }
export function pricingContext(actor: string | undefined, tenant: string | undefined, role: string, csrf: string, mfa: boolean): string { return JSON.stringify([actor || '', tenant || '', role, csrf, mfa]) }
export function createPricingEpoch() {
  let context = '', epoch = 0, sequence = 0, disposed = false
  return {
    reset(next = context) { context = next; epoch++; sequence = 0 },
    ticket() { return Object.freeze({ context, epoch, sequence: ++sequence }) },
    accepts(ticket: { context: string; epoch: number; sequence: number }) { return !disposed && ticket.context === context && ticket.epoch === epoch && ticket.sequence === sequence },
    dispose() { disposed = true; epoch++ },
  }
}
export function createPricingPager<T>() {
  const gate = createPricingEpoch()
  let rows: T[] = [], next = '', loading = false, loaded = false, message = '', page = 0, context: PricingPage<T>['context'], seen = new Set<string>()
  return {
    reset(identity: string) { gate.reset(identity); rows = []; next = ''; loading = false; loaded = false; message = ''; page = 0; context = undefined; seen = new Set() },
    begin(first = true) {
      if (loading || (!first && !next)) return null
      if (first) { gate.reset(); seen = new Set(); page = 0 }
      const ticket = { ...gate.ticket(), cursor: first ? '' : next, first }
      loading = true; message = ''; return ticket
    },
    accepts(ticket: Parameters<typeof gate.accepts>[0]) { return gate.accepts(ticket) },
    accept(ticket: Parameters<typeof gate.accepts>[0] & { cursor: string; first: boolean }, value: PricingPage<T>) {
      if (!gate.accepts(ticket)) return false
      if (value.next && (value.next === ticket.cursor || seen.has(value.next))) throw new Error('分页游标未前进，请刷新第一页重新核查。')
      if (value.next) seen.add(value.next)
      rows = [...value.rows]; next = value.next; context = value.context; loading = false; loaded = true; page = ticket.first ? 1 : page + 1; return true
    },
    fail(ticket: Parameters<typeof gate.accepts>[0], error: unknown) { if (!gate.accepts(ticket)) return false; loading = false; message = error instanceof Error ? error.message : '读取未完成。'; return true },
    snapshot() { return { rows: [...rows], next, loading, loaded, message, page, context } },
    dispose() { gate.dispose(); rows = []; next = ''; context = undefined; loading = false },
  }
}
export function unknownPricingWrite(error: unknown) { return !(error instanceof ApiError) || error.status === 0 || error.status === 408 || error.status >= 500 || error.code === 'invalid_response' }
interface StorageLike { getItem(key: string): string | null; setItem(key: string, value: string): void; removeItem(key: string): void }
const prefix = 'moni:pricing:pending:v1:'
function immutable<T>(value: T): T { if (value && typeof value === 'object') { Object.values(value).forEach(immutable); Object.freeze(value) }; return value }
function prune(map: Map<string, PricingReview>) { const terminal = [...map].filter(([, value]) => ['confirmed','rejected'].includes(value.state)); terminal.slice(0, Math.max(0, terminal.length - 64)).forEach(([id]) => map.delete(id)); return map }
export function createPricingJournal(storage?: StorageLike) {
  const actors = new Map<string, Map<string, PricingReview>>()
  function values(actor: string) {
    pricingID(actor)
    let map = actors.get(actor)
    if (!map) {
      map = new Map()
      if (storage) {
        let raw: string | null
        try { raw = storage.getItem(prefix + actor) } catch { throw new Error('无法读取待核查售价命令，请恢复会话存储后再操作。') }
        if (raw) {
          if (raw.length > 65536) throw new Error('待核查售价命令存储损坏，请由运营核对原操作，不能自动重置后重发。')
          let envelope: unknown
          try { envelope = JSON.parse(raw) } catch { throw new Error('待核查售价命令存储损坏，请由运营核对原操作，不能自动重置后重发。') }
          const e = envelope as { v?: unknown; commands?: unknown }
          if (!e || typeof e !== 'object' || Object.keys(e).length !== 2 || e.v !== 1 || !Array.isArray(e.commands) || e.commands.length > 64) throw new Error('待核查售价命令存储无效，不能自动重置后重发。')
          for (const value of e.commands) {
            const command = parsePricingIdentity(value)
            if (command.actor !== actor || map.has(command.operation_id)) throw new Error('待核查售价命令身份不一致。')
            map.set(command.operation_id, { command: Object.freeze(command), state: 'unknown' })
          }
        }
      }
      actors.set(actor, map)
    }
    return map
  }
  function persist(actor: string, map: Map<string, PricingReview>) {
    if (!storage) return
    const commands = [...map.values()].filter(review => ['submitting','unknown'].includes(review.state)).map(review => pricingIdentity(review.command))
    try { if (commands.length) storage.setItem(prefix + actor, JSON.stringify({ v: 1, commands })); else storage.removeItem(prefix + actor) }
    catch { throw new Error('无法保存原操作核查标识，本次不允许新提交。') }
  }
  function entry(actor: string, operation: string) { const value = values(actor).get(pricingID(operation)); if (!value) throw new Error('未找到原售价命令，请仅按原操作 ID 查询。'); return value }
  return {
    list(actor: string): PricingReview[] { return [...values(actor).values()].reverse().map(review => ({ ...review })) },
    blocked(actor: string, target: string) { return [...values(actor).values()].some(review => review.command.target === target && ['submitting','unknown'].includes(review.state)) },
    begin(command: PricingCommand): PricingReview {
      const identity = pricingIdentity(command), map = values(identity.actor)
      if (map.has(identity.operation_id) || this.blocked(identity.actor, identity.target)) throw new Error('该目标已有结果待核查的命令，只能查询原操作，不能换 ID 重发。')
      if ([...map.values()].filter(review => ['submitting','unknown'].includes(review.state)).length >= 64) throw new Error('待核查命令已达上限，请先核对原回执。')
      const value: PricingReview = { command, state: 'submitting' }, next = new Map(map).set(identity.operation_id, value)
      persist(identity.actor, next); actors.set(identity.actor, prune(next)); return { ...value }
    },
    fail(actor: string, operation: string, error: unknown): PricingReview {
      const value = entry(actor, operation)
      if (value.state !== 'submitting') return { ...value }
      const review: PricingReview = { command: value.command, state: unknownPricingWrite(error) ? 'unknown' : 'rejected', message: unknownPricingWrite(error) ? '结果待核查，只能查询原回执；404不证明提交失败。' : error instanceof Error ? error.message : '操作未通过。' }
      const next = new Map(values(actor)).set(operation, review); persist(actor, next); actors.set(actor, prune(next)); return { ...review }
    },
    confirm(actor: string, operation: string, raw: unknown): PricingReview {
      const value = entry(actor, operation), receipt = immutable(parsePricingReceipt(raw, value.command))
      if (value.state === 'confirmed' && JSON.stringify(value.receipt) !== JSON.stringify(receipt)) throw new Error('不可变原回执发生变化，请联系运营核查。')
      const review: PricingReview = { command: value.command, state: 'confirmed', receipt }, next = new Map(values(actor)).set(operation, review)
      persist(actor, next); actors.set(actor, prune(next)); return { ...review }
    },
  }
}
// Only safe identity/fingerprint metadata is stored; no body/label/quote/result or CSRF token.
export const pricingJournal = createPricingJournal({ getItem: key => sessionStorage.getItem(key), setItem: (key, value) => sessionStorage.setItem(key, value), removeItem: key => sessionStorage.removeItem(key) })
