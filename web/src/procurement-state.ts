import { ApiError } from './api.ts'
export { createKeyAccessScope as createProcurementScope, keyAccessContext as procurementContext } from './key-access-policy.ts'

export interface ProcurementCommand {
  readonly operationID: string
  readonly target: string
  readonly path: string
  readonly method: 'POST' | 'PUT'
  readonly body: Readonly<Record<string, unknown>>
  readonly lookupPath: string
}
export type ProcurementCommandState = 'submitting' | 'unknown' | 'conflict' | 'rejected' | 'confirmed'
export interface ProcurementCommandReview {
  readonly command: ProcurementCommand
  readonly state: ProcurementCommandState
  readonly message?: string
  readonly failureCode?: string
  readonly result?: unknown
}

export function procurementOperationID(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) || value === '00000000-0000-0000-0000-000000000000') throw new Error('操作 ID 须为规范非零 UUID。')
  return value
}
export function unknownProcurementWrite(error: unknown): boolean {
  return !(error instanceof ApiError) || error.status === 0 || error.status >= 500 || error.status === 408 || error.code === 'invalid_response'
}

// Explicitly copy the safe JSON projection: Vue proxies cannot be cloned with
// structuredClone. Nothing here is persisted to browser storage.
function copy<T>(value: T): T {
  if (Array.isArray(value)) return value.map(copy) as T
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, copy(item)])) as T
  return value
}
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') { Object.values(value).forEach(freeze); Object.freeze(value) }
  return value
}
function proposalProgress(previous: unknown, next: unknown): boolean {
  if (!previous || !next || typeof previous !== 'object' || typeof next !== 'object') return false
  const before = previous as Record<string, unknown>, after = next as Record<string, unknown>
  if (before.state !== 'proposed' || after.state !== 'approved') return false
  return JSON.stringify({ ...before, state: after.state, approver: after.approver, approval_operation_id: after.approval_operation_id, approved_at: after.approved_at }) === JSON.stringify(after)
}

export function createProcurementJournal() {
  const actors = new Map<string, Map<string, ProcurementCommandReview>>()
  function entries(context: string) {
    let values = actors.get(context)
    if (!values) { values = new Map(); actors.set(context, values) }
    return values
  }
  function current(context: string, operationID: string): ProcurementCommandReview {
    const value = entries(context).get(procurementOperationID(operationID))
    if (!value) throw new Error('未找到原操作，请使用已保存的操作 ID 只读查询。')
    return value
  }
  return {
    list(context: string): ProcurementCommandReview[] { return [...entries(context).values()].reverse().map(copy) },
    get(context: string, operationID: string): ProcurementCommandReview { return copy(current(context, operationID)) },
    begin(context: string, input: ProcurementCommand): ProcurementCommandReview {
      const operationID = procurementOperationID(input.operationID), values = entries(context)
      if (!context || !input.target || input.body.operation_id !== operationID) throw new Error('原操作上下文或操作 ID 不一致。')
      if (values.has(operationID)) throw new Error('操作 ID 已使用，请只读查询原回执。')
      if ([...values.values()].some(value => value.command.target === input.target && ['submitting', 'unknown'].includes(value.state))) throw new Error('该对象已有结果待确认的操作，请先查询原回执，不能换 ID 重复提交。')
      const value = freeze({ command: copy(input), state: 'submitting' as const })
      values.set(operationID, value)
      return copy(value)
    },
    fail(context: string, operationID: string, error: unknown, message: string): ProcurementCommandReview {
      const value = current(context, operationID)
      if (value.state !== 'submitting') return copy(value)
      const state: ProcurementCommandState = unknownProcurementWrite(error) ? 'unknown' : error instanceof ApiError && error.status === 409 ? 'conflict' : 'rejected'
      const next = freeze({ command: value.command, state, message, failureCode: error instanceof ApiError ? error.code : undefined })
      entries(context).set(operationID, next)
      return copy(next)
    },
    confirm(context: string, operationID: string, result: unknown): ProcurementCommandReview {
      const value = current(context, operationID), safe = copy(result)
      if (value.state === 'confirmed' && JSON.stringify(value.result) !== JSON.stringify(safe) && !proposalProgress(value.result, safe)) throw new Error('原操作回执不一致，请联系运营核查。')
      const next = freeze({ command: value.command, state: 'confirmed' as const, result: safe })
      entries(context).set(operationID, next)
      return copy(next)
    },
  }
}
export const procurementJournal = createProcurementJournal()
