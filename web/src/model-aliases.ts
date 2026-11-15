import { newOperationKey, request, stableOperationKey } from './api.ts'

export type ModelAliasState = 'active' | 'retired'
export interface ModelAlias {
  alias: string
  model_id: string
  state: ModelAliasState
  revision: string
  operation_id: string
  created_by: string
  created_at: string
  retired_at: string | null
}

function invalid(): Error { return new Error('模型别名响应格式无效。') }
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw invalid()
  return value as Record<string, unknown>
}
function id(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value)) throw invalid()
  return value
}
function name(value: unknown): string {
  if (typeof value !== 'string' || !value || new TextEncoder().encode(value).length > 100 || /[\s\p{Cc}]/u.test(value)) throw invalid()
  return value
}
function revision(value: unknown): string {
  if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) throw invalid()
  return value
}
function time(value: unknown, nullable = false): string | null {
  if (value === null && nullable) return null
  if (typeof value !== 'string' || !value.includes('T') || !Number.isFinite(Date.parse(value))) throw invalid()
  return value
}
export function parseModelAlias(value: unknown): ModelAlias {
  const raw = object(value)
  const keys = ['alias', 'model_id', 'state', 'revision', 'operation_id', 'created_by', 'created_at', 'retired_at']
  if (Object.keys(raw).length !== keys.length || keys.some(key => !Object.hasOwn(raw, key))) throw invalid()
  if (raw.state !== 'active' && raw.state !== 'retired') throw invalid()
  const retiredAt = time(raw.retired_at, true)
  if ((raw.state === 'retired') !== (retiredAt !== null)) throw invalid()
  return { alias: name(raw.alias), model_id: id(raw.model_id), state: raw.state, revision: revision(raw.revision), operation_id: id(raw.operation_id), created_by: id(raw.created_by), created_at: time(raw.created_at)!, retired_at: retiredAt }
}
export function parseModelAliases(value: unknown): ModelAlias[] {
  if (!Array.isArray(value) || value.length > 100) throw invalid()
  const rows = value.map(parseModelAlias)
  if (new Set(rows.map(row => row.alias)).size !== rows.length) throw invalid()
  return rows
}
export function modelAliasCreate(modelID: string, alias: string, operationID = newOperationKey()) {
  const idValue = id(modelID), nameValue = name(alias)
  if (!idValue || !nameValue || !id(operationID)) throw invalid()
  return Object.freeze({ path: `/platform/models/${encodeURIComponent(idValue)}/aliases`, method: 'POST' as const, body: { operation_id: operationID, alias: nameValue } })
}
export function modelAliasRetire(alias: string, expectedRevision: string, operationID = newOperationKey()) {
  const nameValue = name(alias), revisionValue = revision(expectedRevision), op = id(operationID)
  return Object.freeze({ path: `/platform/model-aliases/${encodeURIComponent(nameValue)}/retire`, method: 'POST' as const, body: { operation_id: op, expected_revision: revisionValue } })
}
export async function fetchModelAliases(modelID: string): Promise<ModelAlias[]> {
  const response = await request<unknown>(`/platform/models/${encodeURIComponent(id(modelID))}/aliases`)
  return parseModelAliases(response.data)
}
export function stableAliasCreate(modelID: string, alias: string) {
  const operation = modelAliasCreate(modelID, alias)
  return { ...operation, body: { ...operation.body, operation_id: stableOperationKey(operation.path, { alias: operation.body.alias }) } }
}
export function stableAliasRetire(alias: string, expectedRevision: string) {
  const operation = modelAliasRetire(alias, expectedRevision)
  return { ...operation, body: { ...operation.body, operation_id: stableOperationKey(operation.path, { expected_revision: operation.body.expected_revision }) } }
}
