import { parseTextRates, pricingDecimal, pricingID, pricingObject, pricingSchemaDigest } from './pricing.ts'
import type { TextRates } from './pricing.ts'

export type MeterSide = 'retail' | 'supplier'
export type MeterQuantityStatus = 'confirmed_final' | 'provisional' | 'missing' | 'conflicting'

export interface TextBoundLine {
  dimension: 'text_input_token' | 'text_output_token'
  unit: 'token'
  quantity: string
}
export interface TextBounds<S extends MeterSide> {
  schema_id: string
  version: '1'
  side: S
  bounds: TextBoundLine[]
}
export interface TextQuantityLine {
  dimension: 'text_input_token' | 'text_output_token'
  unit: 'token'
  status: MeterQuantityStatus
  quantity: string | null
}
export interface TextQuantityWireLine {
  dimension: 'text_input_token' | 'text_output_token'
  unit: 'token'
  quantity: string
}
export interface TextQuantities<S extends MeterSide> {
  schema_id: string
  version: '1'
  side: S
  quantities: TextQuantityLine[]
}
export type TextRetailBounds = TextBounds<'retail'>
export type TextSupplierBounds = TextBounds<'supplier'>
export type TextRetailQuantities = TextQuantities<'retail'>
export type TextSupplierQuantities = TextQuantities<'supplier'>
export interface TextQuantitiesWire<S extends MeterSide> {
  schema_id: string
  version: '1'
  side: S
  quantities: TextQuantityWireLine[]
}
export type TextRetailQuantitiesWire = TextQuantitiesWire<'retail'>

function bad(): never { throw new Error('计量向量响应无效，请刷新核查；不会降级为标量或免费。') }
function side<S extends MeterSide>(value: unknown, expected: S): S { if (value !== expected) return bad(); return expected }
function vectorRows(value: unknown): unknown[] {
  if (!Array.isArray(value) || value.length !== 2) return bad()
  return value
}

export function parseTextBounds<S extends MeterSide>(value: unknown, schema: string, expectedSide: S): TextBounds<S> {
  const r = pricingObject(value, ['schema_id', 'version', 'side', 'bounds'])
  if (pricingID(r.schema_id) !== pricingID(schema) || r.version !== '1') return bad()
  const rows = vectorRows(r.bounds)
  const bounds = rows.map((raw, index): TextBoundLine => {
    const line = pricingObject(raw, ['dimension', 'unit', 'quantity'])
    const dimension = index === 0 ? 'text_input_token' as const : 'text_output_token' as const
    if (line.dimension !== dimension || line.unit !== 'token') return bad()
    return { dimension, unit: 'token', quantity: pricingDecimal(line.quantity) }
  })
  return { schema_id: schema, version: '1', side: side(r.side, expectedSide), bounds }
}

export function parseTextQuantities<S extends MeterSide>(value: unknown, schema: string, expectedSide: S): TextQuantities<S> {
  const r = pricingObject(value, ['schema_id', 'version', 'side', 'quantities'])
  if (pricingID(r.schema_id) !== pricingID(schema) || r.version !== '1') return bad()
  const rows = vectorRows(r.quantities)
  const quantities = rows.map((raw, index): TextQuantityLine => {
    const line = pricingObject(raw, ['dimension', 'unit', 'status', 'quantity'])
    const dimension = index === 0 ? 'text_input_token' as const : 'text_output_token' as const
    const statuses: MeterQuantityStatus[] = ['confirmed_final', 'provisional', 'missing', 'conflicting']
    if (line.dimension !== dimension || line.unit !== 'token' || typeof line.status !== 'string' || !statuses.includes(line.status as MeterQuantityStatus)) return bad()
    const status = line.status as MeterQuantityStatus
    const quantity = status === 'missing' || status === 'conflicting' ? null : pricingDecimal(line.quantity)
    if ((status === 'missing' || status === 'conflicting') && line.quantity !== null) return bad()
    return { dimension, unit: 'token', status, quantity }
  })
  return { schema_id: schema, version: '1', side: side(r.side, expectedSide), quantities }
}

export function parseTextQuantitiesWire<S extends MeterSide>(value: unknown, schema: string, expectedSide: S): TextQuantitiesWire<S> {
  const r = pricingObject(value, ['schema_id', 'version', 'side', 'quantities'])
  if (pricingID(r.schema_id) !== pricingID(schema) || r.version !== '1') return bad()
  const rows = vectorRows(r.quantities)
  const quantities = rows.map((raw, index): TextQuantityWireLine => {
    const line = pricingObject(raw, ['dimension', 'unit', 'quantity'])
    const dimension = index === 0 ? 'text_input_token' as const : 'text_output_token' as const
    if (line.dimension !== dimension || line.unit !== 'token') return bad()
    return { dimension, unit: 'token', quantity: pricingDecimal(line.quantity) }
  })
  return { schema_id: schema, version: '1', side: side(r.side, expectedSide), quantities }
}

export function parseTextRetailQuantitiesWire(value: unknown, schema: string): TextRetailQuantitiesWire {
  return parseTextQuantitiesWire(value, schema, 'retail')
}

export function parseTextQuantityLines(value: unknown, schema: string): TextRetailQuantityLines {
  if (value === null) return null
  pricingID(schema)
  if (!Array.isArray(value) || value.length !== 2) return bad()
  const quantities = value.map((raw, index): TextQuantityLine => {
    const line = pricingObject(raw, ['dimension', 'unit', 'status', 'quantity'])
    const dimension = index === 0 ? 'text_input_token' as const : 'text_output_token' as const
    const statuses: MeterQuantityStatus[] = ['confirmed_final', 'provisional', 'missing', 'conflicting']
    if (line.dimension !== dimension || line.unit !== 'token' || typeof line.status !== 'string' || !statuses.includes(line.status as MeterQuantityStatus)) return bad()
    const status = line.status as MeterQuantityStatus
    if (status === 'missing' || status === 'conflicting') {
      if (line.quantity !== null) return bad()
      return { dimension, unit: 'token', status, quantity: null }
    }
    return { dimension, unit: 'token', status, quantity: pricingDecimal(line.quantity) }
  })
  return quantities
}
export type TextRetailQuantityLines = TextQuantityLine[] | null

export function quantityFor(lines: TextRetailQuantityLines, dimension: TextQuantityLine['dimension']): TextQuantityLine | undefined {
  return lines?.find(line => line.dimension === dimension)
}

export function rateFor<S extends MeterSide>(rates: TextRates<S> | null | undefined, dimension: TextQuantityLine['dimension']) {
  return rates?.rates.find(line => line.dimension === dimension)
}

export function ratePerMillionText<S extends MeterSide>(rates: TextRates<S> | null | undefined, dimension: TextQuantityLine['dimension']): string {
  const line = rateFor(rates, dimension)
  if (!line) return '—'
  let numerator = BigInt(line.numerator) * 1_000_000n
  let denominator = BigInt(line.denominator)
  const gcd = (a: bigint, b: bigint): bigint => { while (b) { const next = a % b; a = b; b = next }; return a }
  const factor = gcd(numerator, denominator); numerator /= factor; denominator /= factor
  if (denominator === 1n) {
    const whole = numerator / 1_000_000n, fraction = (numerator % 1_000_000n).toString().padStart(6, '0')
    return `¥${whole.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')}.${fraction}`
  }
  return `¥${numerator.toString()}/${denominator.toString()} 微元 / 百万 Tokens`
}

export function rateIsZero<S extends MeterSide>(rates: TextRates<S> | null | undefined, dimension: TextQuantityLine['dimension']): boolean {
  return rateFor(rates, dimension)?.numerator === '0'
}

export interface MeterSnapshotProjection {
  schema_id: string
  schema_digest: string
  variant_key: 'default'
  retail_rates: TextRates<'retail'>
  retail_bounds: TextRetailBounds
  usage_status: MeterQuantityStatus
  retail_quantities: TextRetailQuantityLines
}

export function parseMeterSnapshotProjection(value: unknown): MeterSnapshotProjection {
  const r = pricingObject(value, ['schema_id', 'schema_digest', 'variant_key', 'retail_rates', 'retail_bounds', 'usage_status', 'retail_quantities'])
  const schema_id = pricingID(r.schema_id)
  const usage = r.usage_status
  if (typeof usage !== 'string' || !['confirmed_final', 'provisional', 'missing', 'conflicting'].includes(usage)) return bad()
  const quantities = parseTextQuantityLines(r.retail_quantities, schema_id)
  if (usage === 'confirmed_final' && quantities === null) return bad()
  if (usage !== 'confirmed_final' && quantities !== null && quantities.some(line => line.status === 'confirmed_final')) return bad()
  return {
    schema_id,
    schema_digest: pricingSchemaDigest(r.schema_digest),
    variant_key: r.variant_key === 'default' ? 'default' : bad(),
    retail_rates: parseTextRates(r.retail_rates, schema_id, 'retail'),
    retail_bounds: parseTextBounds(r.retail_bounds, schema_id, 'retail'),
    usage_status: usage as MeterQuantityStatus,
    retail_quantities: quantities,
  }
}
