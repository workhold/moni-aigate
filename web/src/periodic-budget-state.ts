import { periodicUnknownWrite } from './periodic-budget-transport.ts'
import type { PeriodicPending, PeriodicReceipt } from './periodic-budget.ts'
export type PeriodicReviewState = 'editing' | 'submitting' | 'confirmed' | 'known_failure' | 'unknown'
export interface PeriodicReview { state: PeriodicReviewState; pending: PeriodicPending | null; receipt: PeriodicReceipt | null; cleanupBlocked: boolean }
export function editingPeriodicReview(): PeriodicReview { return { state: 'editing', pending: null, receipt: null, cleanupBlocked: false } }
export function resumePeriodicReview(pending: PeriodicPending): PeriodicReview { return { state: 'unknown', pending, receipt: null, cleanupBlocked: true } }
export function failPeriodicReview(review: PeriodicReview, cause: unknown): PeriodicReview {
  if (review.state !== 'submitting' || !review.pending) throw new Error('原命令状态不允许处理提交结果。')
  return { ...review, state: periodicUnknownWrite(cause) ? 'unknown' : 'known_failure', cleanupBlocked: true }
}
export function periodicReviewBlocked(review: PeriodicReview): boolean { return review.state === 'submitting' || review.state === 'unknown' || review.cleanupBlocked }
type Lane = 'view' | 'windows' | 'allocations' | 'command' | 'operation'
export interface PeriodicTicket { readonly authEpoch: number; readonly viewEpoch: number; readonly commandEpoch: number; readonly lane: Lane; readonly sequence: number; readonly signal: AbortSignal }
export function createPeriodicEpoch() {
  let authEpoch = 0, viewEpoch = 0, commandEpoch = 0, context = '', target = '', disposed = false
  const requests = new Map<Lane, { sequence: number; controller: AbortController }>()
  function abortAll() { for (const value of requests.values()) value.controller.abort(); requests.clear() }
  return {
    auth(next: string) { if (next === context && !disposed) return; context = next; authEpoch++; viewEpoch++; commandEpoch++; abortAll() },
    view(next: string) { target = next; viewEpoch++; commandEpoch++; abortAll() },
    command() { commandEpoch++; for (const lane of ['command', 'operation'] as const) { requests.get(lane)?.controller.abort(); requests.delete(lane) } },
    cancel(lane: Lane) { requests.get(lane)?.controller.abort(); requests.delete(lane) },
    ticket(lane: Lane): PeriodicTicket {
      const previous = requests.get(lane); previous?.controller.abort()
      const controller = new AbortController(), sequence = (previous?.sequence || 0) + 1
      requests.set(lane, { sequence, controller })
      if (disposed) controller.abort()
      return Object.freeze({ authEpoch, viewEpoch, commandEpoch, lane, sequence, signal: controller.signal })
    },
    accepts(ticket: PeriodicTicket): boolean { return !disposed && !ticket.signal.aborted && ticket.authEpoch === authEpoch && ticket.viewEpoch === viewEpoch && (ticket.lane !== 'command' && ticket.lane !== 'operation' || ticket.commandEpoch === commandEpoch) && requests.get(ticket.lane)?.sequence === ticket.sequence },
    snapshot() { return { authEpoch, viewEpoch, commandEpoch, context, target } },
    dispose() { disposed = true; authEpoch++; viewEpoch++; commandEpoch++; context = ''; target = ''; abortAll() },
  }
}
export function periodicPollDelay(attempt: number, retryAfterMs = 0): number { return Math.max([1000, 2000, 5000][Math.min(Math.max(0, attempt), 2)]!, retryAfterMs) }
export function periodicPageTransition(path: string[], cursor: string, next: string, direction: 'first' | 'next' | 'previous'): string[] {
  if (next && (next === cursor || direction === 'next' && path.includes(next))) throw new Error('分页没有前进，请刷新第一页重新核查。')
  if (direction === 'first') return []
  if (direction === 'next') { if (!cursor) throw new Error('没有下一页。'); return [...path, cursor] }
  if (!path.length || cursor !== (path[path.length - 2] || '')) throw new Error('上一页路径已改变，请刷新第一页。')
  return path.slice(0, -1)
}
