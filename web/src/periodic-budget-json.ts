// Decode before DTO validation: JSON.parse alone accepts duplicate object keys
// and isolated UTF-16 surrogates. This boundary never returns a partial document.
export const PERIODIC_RESPONSE_BYTES = 131072
export function parsePeriodicJSON(raw: string): unknown {
  if (new TextEncoder().encode(raw).length > PERIODIC_RESPONSE_BYTES) throw new Error('周期预算响应超过大小上限。')
  let at = 0
  const bad = (): never => { throw new Error('周期预算响应不是严格 JSON。') }
  const white = () => { while (/[\t\r\n ]/.test(raw[at] || '!')) at++ }
  function string(): string {
    const start = at++
    while (at < raw.length) {
      const c = raw[at++]
      if (c === '"') {
        let value: unknown
        try { value = JSON.parse(raw.slice(start, at)) } catch { return bad() }
        if (typeof value !== 'string' || [...value].some(c => { const n = c.codePointAt(0)!; return n >= 0xd800 && n <= 0xdfff })) return bad()
        return value
      }
      if (c === '\\') at++
    }
    return bad()
  }
  function value(depth: number): unknown {
    if (depth > 16) return bad()
    white()
    const c = raw[at]
    if (c === '"') return string()
    if (c === '{') {
      at++; white()
      const result: Record<string, unknown> = Object.create(null)
      if (raw[at] === '}') { at++; return result }
      while (true) {
        white(); if (raw[at] !== '"') return bad()
        const key = string(); if (Object.hasOwn(result, key)) return bad()
        white(); if (raw[at++] !== ':') return bad()
        result[key] = value(depth + 1); white()
        const separator = raw[at++]
        if (separator === '}') return result
        if (separator !== ',') return bad()
      }
    }
    if (c === '[') {
      at++; white(); const result: unknown[] = []
      if (raw[at] === ']') { at++; return result }
      while (true) { result.push(value(depth + 1)); white(); const separator = raw[at++]; if (separator === ']') return result; if (separator !== ',') return bad() }
    }
    for (const [literal, result] of [['true', true], ['false', false], ['null', null]] as const) {
      if (raw.startsWith(literal, at)) { at += literal.length; return result }
    }
    const number = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/.exec(raw.slice(at))
    if (!number || !/^(?:0|-?[1-9][0-9]*)$/.test(number[0])) return bad()
    at += number[0].length
    const result = Number(number[0]); if (!Number.isSafeInteger(result)) return bad()
    return result
  }
  const result = value(0); white(); if (at !== raw.length) return bad()
  return result
}
