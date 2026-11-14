function formatUnits(value: unknown, precision: number, prefix: string, suffix: string): string {
  if (value === null || value === undefined || value === '') return '—'
  try {
    const amount = BigInt(String(value)); const negative = amount < 0n; const magnitude = negative ? -amount : amount
    const whole = (magnitude / 1_000_000n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
    const fraction = (magnitude % 1_000_000n).toString().padStart(6, '0').slice(0, precision)
    return `${negative ? '−' : ''}${prefix}${whole}${fraction ? `.${fraction}` : ''}${suffix}`
  } catch { return '—' }
}
export function money(value: unknown, precision = 6): string { return formatUnits(value, precision, '¥', '') }
export function coin(value: unknown, precision = 6): string { return formatUnits(value, precision, '', ' 币') }
export function cnyToMicro(value: string): string {
  const normalized = value.trim()
  if (!/^\d+(\.\d{1,6})?$/.test(normalized)) throw new Error('金额须为非负数，最多 6 位小数。')
  const [whole, fraction = ''] = normalized.split('.')
  const result = BigInt(whole!) * 1_000_000n + BigInt(fraction.padEnd(6, '0'))
  if (result > 9_223_372_036_854_775_807n) throw new Error('金额超过可接受范围。')
  return result.toString()
}
export function time(value: unknown): string {
  if (!value) return '—'
  const date = new Date(String(value))
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}
export function integer(value: unknown): string { return value === null || value === undefined ? '—' : String(value).replace(/\B(?=(\d{3})+(?!\d))/g, ',') }
export function shortId(value: unknown): string { const s = String(value || ''); return s.length > 18 ? `${s.slice(0, 8)}…${s.slice(-6)}` : s || '—' }
const labels: Record<string, string> = {
  active: '启用', enabled: '启用', disabled: '停用', suspended: '暂停', revoked: '已撤销', expired: '已过期',
  accepted: '已接收', dispatching: '转发中', streaming: '生成中', succeeded: '成功', completed: '已完成', failed: '失败', cancelled: '已取消',
  reserved: '已冻结', held: '冻结中', settled: '已结算', released: '已释放', pending_reconciliation: '待核对', pending: '待审批', proposed: '待复核', approved: '已批准', rejected: '已拒绝',
  confirmed_final: '最终量已确认', provisional: '临时累计量', estimated: '估算', missing: '用量缺失', conflicting: '用量冲突', unknown: '待核对',
  contract: '合同依据', invoice: '账单依据',
  credit: '人工核款入账', funding: '钱包入账', voucher_grant: '运营赠额入账', sandbox_capture: '本地模拟入账', wallet_opening: '初始余额导入（非现金）', reserve: '费用冻结', settlement: '消费结算', settle: '消费结算', release: '冻结释放', refund: '退回钱包',
  owner: '所有者', member: '成员', billing_viewer: '账单查看者', admin: '平台管理员', platform_admin: '平台管理员', finance: '财务', finance_operator: '财务制单', finance_approver: '财务审批', none: '客户账号',
}
export function label(value: unknown): string { const s = String(value ?? ''); return labels[s] || s || '—' }
export function tagType(value: unknown): 'success' | 'warning' | 'danger' | 'info' | 'primary' {
  const s = String(value)
  if (['active', 'enabled', 'succeeded', 'completed', 'settled', 'approved', 'confirmed_final'].includes(s)) return 'success'
  if (['failed', 'revoked', 'expired', 'rejected', 'conflicting', 'suspended'].includes(s)) return 'danger'
  if (['pending', 'proposed', 'pending_reconciliation', 'reserved', 'held', 'missing', 'unknown'].includes(s)) return 'warning'
  return 'info'
}
