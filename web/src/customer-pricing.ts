import { parseRetailPrice, pricingDecimal, pricingID } from './pricing.ts'
import type { RetailPrice } from './pricing.ts'
export interface PricedModel { id: string; public_name: string; protocol?: string; enabled?: boolean; context_limit?: number; output_limit?: number; default_output_limit?: number; input_rate?: string | null; output_rate?: string | null; retail_price?: RetailPrice; reservation_strategy?: string; supports_tools?: boolean; profile?: Record<string, unknown> }
export function parsePricedModels(value: unknown): PricedModel[] {
  if (!Array.isArray(value) || value.length > 50) throw new Error('模型目录响应无效。')
  const rows = value.map(raw => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('模型目录响应无效。')
    const r = raw as PricedModel
    pricingID(r.id)
    if (typeof r.public_name !== 'string' || !r.public_name) throw new Error('模型名称响应无效。')
    for (const rate of [r.input_rate, r.output_rate]) if (rate !== undefined && rate !== null) pricingDecimal(rate)
    if (Object.hasOwn(r, 'retail_price')) {
      const price = parseRetailPrice(r.retail_price, r.id)
      if (r.input_rate !== price.input_rate || r.output_rate !== price.output_rate) throw new Error('当前模型报价与金额字段不一致，请刷新核查。')
      if (['openai_embeddings','jina_rerank'].includes(String(r.protocol ?? r.profile?.protocol)) && price.status === 'available' && price.output_rate !== '0') throw new Error('输入计费模型返回了非零输出价，请联系运营核查。')
      return { ...r, retail_price: price }
    }
    return { ...r }
  })
  if (new Set(rows.map(row => row.id)).size !== rows.length) throw new Error('模型目录包含重复标识。')
  return rows
}
export function applyRetailQuote(rows: PricedModel[], quote: RetailPrice): PricedModel[] {
  const safe = parseRetailPrice(quote)
  return rows.map(row => row.id === safe.model_id ? { ...row, input_rate: safe.input_rate, output_rate: safe.output_rate, retail_price: safe } : row)
}
