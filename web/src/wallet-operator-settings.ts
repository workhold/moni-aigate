import { commerceDecimal, commerceObject, commerceText, commerceUUID } from './commerce.ts'

export const walletOperatorConfirmation = 'CONFIRM_SINGLE_OPERATOR_CHANGE'

export interface WalletOperatorSetting {
  operation_id: string
  version: string
  single_operator: boolean
  actor: string | null
  reason: string
  created_at: string
}
export interface WalletOperatorSettings {
  current: WalletOperatorSetting
  versions: WalletOperatorSetting[]
}
export interface WalletOperatorCommand {
  operation_id: string
  expected_revision: string
  single_operator: boolean
  reason: string
  confirmation: string
}

function settingTime(value: unknown): string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) throw new Error('运营设置时间无效。')
  const [whole, fraction = ''] = value.slice(0, -1).split('.')
  const milliseconds = `${whole}.${fraction.padEnd(3, '0').slice(0, 3)}Z`, date = new Date(milliseconds)
  if (!Number.isFinite(date.getTime()) || date.toISOString() !== milliseconds) throw new Error('运营设置时间无效。')
  return value
}

export function parseWalletOperatorSetting(value: unknown): WalletOperatorSetting {
  const raw = commerceObject(value, ['operation_id', 'version', 'single_operator', 'actor', 'reason', 'created_at'])
  if (typeof raw.single_operator !== 'boolean') throw new Error('单人运营状态无效。')
  return {
    operation_id: commerceUUID(raw.operation_id),
    version: commerceDecimal(raw.version, true),
    single_operator: raw.single_operator,
    actor: raw.actor === null ? null : commerceUUID(raw.actor),
    reason: commerceText(raw.reason),
    created_at: settingTime(raw.created_at),
  }
}

export function parseWalletOperatorSettings(value: unknown): WalletOperatorSettings {
  const raw = commerceObject(value, ['current', 'versions'])
  if (!Array.isArray(raw.versions) || raw.versions.length < 1 || raw.versions.length > 50) throw new Error('运营设置历史无效。')
  const versions = raw.versions.map(parseWalletOperatorSetting), current = parseWalletOperatorSetting(raw.current)
  const first = versions[0]
  if (JSON.stringify(current) !== JSON.stringify(first)) throw new Error('当前运营设置与最新版本不一致。')
  const operations = new Set(versions.map(item => item.operation_id)), revisions = new Set(versions.map(item => item.version))
  if (operations.size !== versions.length || revisions.size !== versions.length) throw new Error('运营设置历史包含重复版本。')
  for (let index = 1; index < versions.length; index++) {
    if (BigInt(versions[index - 1].version) !== BigInt(versions[index].version) + 1n) throw new Error('运营设置历史版本不连续。')
  }
  return { current, versions }
}

export function walletOperatorCommand(input: WalletOperatorCommand): WalletOperatorCommand {
  if (typeof input.single_operator !== 'boolean' || input.confirmation !== walletOperatorConfirmation) throw new Error('必须完成单人运营风险的二次确认。')
  return {
    operation_id: commerceUUID(input.operation_id),
    expected_revision: commerceDecimal(input.expected_revision, true),
    single_operator: input.single_operator,
    reason: commerceText(input.reason),
    confirmation: walletOperatorConfirmation,
  }
}
