import { money } from './format.ts'

// A populated amount alone cannot turn an unresolved cost fact into known cost.
export function providerCostText(amount: unknown, status: unknown): string {
  if (!['contract', 'invoice'].includes(String(status)) || amount === null || amount === undefined || amount === '') return '待核对'
  return money(amount)
}
