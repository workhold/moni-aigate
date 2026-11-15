import { parseRetailPrice, pricingID } from './pricing.ts'
import type { RetailPrice } from './pricing.ts'
export interface PricedModel { id: string; public_name: string; aliases?: string[]; protocol?: string; enabled?: boolean; context_limit?: number; output_limit?: number; default_output_limit?: number; retail_price?: RetailPrice; reservation_strategy?: string; supports_tools?: boolean; profile?: Record<string, unknown> }
export function parsePricedModels(value: unknown): PricedModel[] {
  if (!Array.isArray(value) || value.length > 50) throw new Error('模型目录响应无效。')
  const rows = value.map(raw => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('模型目录响应无效。')
    const r = raw as PricedModel
    pricingID(r.id)
    if (typeof r.public_name !== 'string' || !r.public_name) throw new Error('模型名称响应无效。')
    if (r.aliases !== undefined && (!Array.isArray(r.aliases) || r.aliases.some(alias => typeof alias !== 'string' || !alias || /[\s\p{Cc}]/u.test(alias) || new TextEncoder().encode(alias).length > 100))) throw new Error('模型别名响应无效。')
    if (Object.hasOwn(r, 'input_rate') || Object.hasOwn(r, 'output_rate')) throw new Error('模型目录仍返回旧价格标量，请刷新核查。')
    if (Object.hasOwn(r, 'retail_price')) {
      const price = parseRetailPrice(r.retail_price, r.id)
      if (['openai_embeddings','jina_rerank'].includes(String(r.protocol ?? r.profile?.protocol)) && price.status === 'available' && price.rates!.rates[1]!.numerator !== '0') throw new Error('输入计费模型返回了非零输出价，请联系运营核查。')
      return { ...r, retail_price: price }
    }
    return { ...r }
  })
  if (new Set(rows.map(row => row.id)).size !== rows.length) throw new Error('模型目录包含重复标识。')
  return rows
}
export function applyRetailQuote(rows: PricedModel[], quote: RetailPrice): PricedModel[] {
  const safe = parseRetailPrice(quote)
  return rows.map(row => row.id === safe.model_id ? { ...row, retail_price: safe } : row)
}
