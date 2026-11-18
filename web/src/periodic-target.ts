import { parseProcurementAccount, procurementIdentity, procurementText, readProcurementRows } from './procurement.ts'

export interface PeriodicTargetChoice {
  kind: 'tenant' | 'procurement'
  id: string
  name: string
  provider: string
  state: string
}

function tenantChoice(value: unknown): PeriodicTargetChoice {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('客户列表响应格式无效。')
  const row = value as Record<string, unknown>
  return { kind: 'tenant', id: procurementIdentity(row.id), name: procurementText(row.name, '客户名称', 200), provider: '客户工作空间', state: procurementText(row.status, '客户状态', 40) }
}

export async function readPeriodicTargetChoices(active: () => boolean): Promise<PeriodicTargetChoice[]> {
  const [tenants, accounts] = await Promise.all([
    readProcurementRows('/platform/tenants', tenantChoice, active),
    readProcurementRows('/platform/procurement-accounts', row => {
      const account = parseProcurementAccount(row)
      return { kind: 'procurement' as const, id: account.id, name: account.label, provider: account.provider, state: account.state }
    }, active),
  ])
  return [...tenants, ...accounts].sort((a, b) => a.name.localeCompare(b.name) || a.kind.localeCompare(b.kind) || a.id.localeCompare(b.id))
}

export function periodicTargetPage(choices: PeriodicTargetChoice[], search: string, page: number, pageSize = 20): { rows: PeriodicTargetChoice[]; total: number } {
  if (!Number.isSafeInteger(page) || page < 1 || !Number.isSafeInteger(pageSize) || pageSize < 1 || pageSize > 100) throw new Error('预算目标分页无效。')
  const needle = search.trim().toLowerCase()
  const filtered = needle ? choices.filter(choice => [choice.name, choice.provider, choice.id, choice.state, choice.kind].some(value => value.toLowerCase().includes(needle))) : choices
  return { rows: filtered.slice((page - 1) * pageSize, page * pageSize), total: filtered.length }
}

export function periodicTargetChoicePath(choice: PeriodicTargetChoice): string {
  const id = procurementIdentity(choice.id)
  if (choice.kind !== 'tenant' && choice.kind !== 'procurement') throw new Error('预算目标类型无效。')
  return `/platform/periodic-budget?${choice.kind}=${encodeURIComponent(id)}`
}
