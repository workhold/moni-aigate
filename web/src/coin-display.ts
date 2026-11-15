import { reactive } from 'vue'
import { request } from './api.ts'

export const coinDisplayConfirmation = 'CONFIRM_COIN_DISPLAY_CHANGE'
export type CoinDisplayCurrency = 'CNY' | 'USD'
export interface CoinDisplayPublic { version: string; display_name: string; fiat_currency: CoinDisplayCurrency | null; rate_num: string | null; rate_den: string | null }
export interface CoinDisplaySetting extends CoinDisplayPublic { operation_id: string; actor: string | null; reason: string; created_at: string }
export interface CoinDisplaySettings { current: CoinDisplaySetting; versions: CoinDisplaySetting[] }
export interface CoinDisplayCommand { operation_id: string; expected_revision: string; display_name: string; fiat_currency: CoinDisplayCurrency | null; rate_num: string | null; rate_den: string | null; reason: string; confirmation: string }

export const coinDisplayState = reactive({ version: '1', displayName: '币', fiatCurrency: null as CoinDisplayCurrency | null, rateNum: null as string | null, rateDen: null as string | null, loaded: false })
function object(value: unknown, fields: readonly string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('展示配置响应格式无效。')
  const raw = value as Record<string, unknown>
  if (Object.keys(raw).length !== fields.length || fields.some(field => !Object.hasOwn(raw, field))) throw new Error('展示配置字段与合同不一致。')
  return raw
}
function decimal(value: unknown, positive = true): string {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)$/.test(value) || positive && value === '0' || BigInt(value) > 9223372036854775807n) throw new Error('展示版本或比例须为规范 int64 十进制字符串。')
  return value
}
function text(value: unknown, max: number): string {
  if (typeof value !== 'string' || !value.length || value.trim() !== value || new TextEncoder().encode(value).length > max || /\p{Cc}/u.test(value)) throw new Error('展示文本无效。')
  return value
}
function uuid(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) || value === '00000000-0000-0000-0000-000000000000') throw new Error('展示操作标识无效。')
  return value
}
function settingTime(value: unknown): string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) throw new Error('展示配置时间无效。')
  const [whole, fraction = ''] = value.slice(0, -1).split('.'), milliseconds = `${whole}.${fraction.padEnd(3, '0').slice(0, 3)}Z`, date = new Date(milliseconds)
  if (!Number.isFinite(date.getTime()) || date.toISOString() !== milliseconds) throw new Error('展示配置时间无效。')
  return value
}
function currency(value: unknown): CoinDisplayCurrency | null {
  if (value === null) return null
  if (value !== 'CNY' && value !== 'USD') throw new Error('约合币种无效。')
  return value
}
function rate(value: unknown): string | null { return value === null ? null : decimal(value) }
function validateCombo(result: CoinDisplayPublic): CoinDisplayPublic {
  const values = [result.fiat_currency, result.rate_num, result.rate_den], disabled = values.every(value => value === null), enabled = result.fiat_currency !== null && values.every(value => value !== null)
  if (!disabled && !enabled) throw new Error('约合币种与比例必须同时为空或同时完整。')
  return result
}
export function parseCoinDisplayPublic(value: unknown): CoinDisplayPublic {
  const raw = object(value, ['version', 'display_name', 'fiat_currency', 'rate_num', 'rate_den'])
  return validateCombo({ version: decimal(raw.version), display_name: text(raw.display_name, 32), fiat_currency: currency(raw.fiat_currency), rate_num: rate(raw.rate_num), rate_den: rate(raw.rate_den) })
}
export function parseCoinDisplaySetting(value: unknown): CoinDisplaySetting {
  const raw = object(value, ['operation_id', 'version', 'display_name', 'fiat_currency', 'rate_num', 'rate_den', 'actor', 'reason', 'created_at'])
  const result = { ...parseCoinDisplayPublic({ version: raw.version, display_name: raw.display_name, fiat_currency: raw.fiat_currency, rate_num: raw.rate_num, rate_den: raw.rate_den }), operation_id: uuid(raw.operation_id), actor: raw.actor === null ? null : uuid(raw.actor), reason: text(raw.reason, 200), created_at: settingTime(raw.created_at) }
  if (result.version === '1' ? result.actor !== null : result.actor === null) throw new Error('展示版本与操作者不一致。')
  return result
}
export function parseCoinDisplaySettings(value: unknown): CoinDisplaySettings {
  const raw = object(value, ['current', 'versions'])
  if (!Array.isArray(raw.versions) || raw.versions.length < 1 || raw.versions.length > 50) throw new Error('展示配置历史无效。')
  const current = parseCoinDisplaySetting(raw.current), versions = raw.versions.map(parseCoinDisplaySetting)
  if (JSON.stringify(current) !== JSON.stringify(versions[0])) throw new Error('当前展示配置与最新版本不一致。')
  if (new Set(versions.map(item => item.operation_id)).size !== versions.length || new Set(versions.map(item => item.version)).size !== versions.length) throw new Error('展示配置历史包含重复版本。')
  for (let index = 1; index < versions.length; index++) if (BigInt(versions[index - 1]!.version) !== BigInt(versions[index]!.version) + 1n) throw new Error('展示配置历史版本不连续。')
  return { current, versions }
}
export function publicCoinDisplay(setting: CoinDisplaySetting): CoinDisplayPublic {
  return { version: setting.version, display_name: setting.display_name, fiat_currency: setting.fiat_currency, rate_num: setting.rate_num, rate_den: setting.rate_den }
}
export function applyCoinDisplay(value: unknown) {
  const setting = parseCoinDisplayPublic(value)
  coinDisplayState.version = setting.version
  coinDisplayState.displayName = setting.display_name
  coinDisplayState.fiatCurrency = setting.fiat_currency
  coinDisplayState.rateNum = setting.rate_num
  coinDisplayState.rateDen = setting.rate_den
  coinDisplayState.loaded = true
}
export function resetCoinDisplay() {
  coinDisplayState.version = '1'; coinDisplayState.displayName = '币'; coinDisplayState.fiatCurrency = null; coinDisplayState.rateNum = null; coinDisplayState.rateDen = null; coinDisplayState.loaded = false
}
export async function loadCoinDisplay(): Promise<CoinDisplayPublic> {
  resetCoinDisplay()
  const response = await request<unknown>('/coin-display')
  const setting = parseCoinDisplayPublic(response.data)
  applyCoinDisplay(setting)
  return setting
}
export function coinDisplayCommand(input: CoinDisplayCommand): CoinDisplayCommand {
  if (input.confirmation !== coinDisplayConfirmation) throw new Error('必须完成展示配置变更的二次确认。')
  const publicValue = validateCombo({ version: input.expected_revision, display_name: text(input.display_name, 32), fiat_currency: currency(input.fiat_currency), rate_num: rate(input.rate_num), rate_den: rate(input.rate_den) })
  return { operation_id: uuid(input.operation_id), expected_revision: decimal(input.expected_revision), display_name: publicValue.display_name, fiat_currency: publicValue.fiat_currency, rate_num: publicValue.rate_num, rate_den: publicValue.rate_den, reason: text(input.reason, 200), confirmation: coinDisplayConfirmation }
}
