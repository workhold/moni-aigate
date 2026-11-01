import { computed, reactive } from 'vue'
import { clearOperationKeys, request, setCsrf } from './api.ts'

export interface Tenant { id: string; name: string; role: string }
export interface User { id: string; email: string; platform_role: string; mfa_enabled: boolean }
export interface Me { user: User; tenant: Tenant | null; tenants: Tenant[]; csrf_token: string; reauthenticated_until?: string; mfa_required?: boolean; enrollment_required?: boolean }
export const session = reactive<{ me: Me | null; ready: boolean; mfaPending: boolean; enrollmentRequired: boolean }>({ me: null, ready: false, mfaPending: false, enrollmentRequired: false })
let sessionEpoch = 0
let inFlight: { epoch: number; promise: Promise<boolean> } | undefined
export async function loadMe(force = false): Promise<boolean> {
  if (!force && session.ready) return !!session.me
  if (inFlight?.epoch === sessionEpoch) return inFlight.promise
  const epoch = sessionEpoch
  const promise = (async () => {
    try {
      const { data } = await request<Me>('/me', 'GET', undefined, undefined, () => epoch === sessionEpoch)
      if (epoch !== sessionEpoch) return false
      session.me = data
      setCsrf(data.csrf_token)
      session.mfaPending = !!data.mfa_required
      session.enrollmentRequired = !!data.enrollment_required
      return true
    } catch { if (epoch === sessionEpoch) { session.me = null; session.mfaPending = false; session.enrollmentRequired = false; setCsrf('') }; return false }
    finally { if (epoch === sessionEpoch) session.ready = true; if (inFlight?.epoch === epoch) inFlight = undefined }
  })()
  inFlight = { epoch, promise }
  return promise
}
export function clearSession() { sessionEpoch++; inFlight = undefined; session.me = null; session.ready = true; session.mfaPending = false; session.enrollmentRequired = false; setCsrf(''); clearOperationKeys() }
export const isAdmin = computed(() => ['admin', 'platform_admin'].includes(session.me?.user.platform_role || ''))
export const isFinance = computed(() => ['finance', 'finance_operator', 'finance_approver', 'finance_admin', 'finance_reviewer'].includes(session.me?.user.platform_role || ''))
export const isApprover = computed(() => ['finance', 'finance_approver', 'finance_admin', 'finance_reviewer'].includes(session.me?.user.platform_role || ''))
export const hasPlatform = computed(() => isAdmin.value || isFinance.value)
export const canManageKeys = computed(() => ['owner', 'member'].includes(session.me?.tenant?.role || ''))
export const canViewWallet = computed(() => ['owner', 'billing_viewer'].includes(session.me?.tenant?.role || ''))
