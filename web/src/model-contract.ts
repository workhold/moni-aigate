import { protocolInfo } from './inference.ts'

export function modelGenerationSettings(protocol: unknown, output: unknown, defaultOutput: unknown, tools: unknown): { output_limit: number; default_output_limit: number; supports_tools: boolean } {
  const info = protocolInfo(protocol)
  if (!info) throw new Error('请选择受支持的调用协议。')
  if (info.inputOnly) return { output_limit: 0, default_output_limit: 0, supports_tools: false }
  const maximum = Number(output); const initial = Number(defaultOutput)
  if (!Number.isSafeInteger(maximum) || maximum < 1 || !Number.isSafeInteger(initial) || initial < 1 || initial > maximum) throw new Error('生成模型的输出上限须为正整数，默认输出不能超过最大输出。')
  return { output_limit: maximum, default_output_limit: initial, supports_tools: info.id === 'gemini_generate_content' ? false : !!tools }
}

// A retrieval model forces zero, but that automatic zero is not a deliberate
// price for a generation model selected afterward. Require a fresh entry.
export function outputPriceAfterProtocolChange(current: string, wasInputOnly: boolean, inputOnly: boolean): string {
  if (inputOnly) return '0'
  return wasInputOnly ? '' : current
}
