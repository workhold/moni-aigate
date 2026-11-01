import { identityCompletionBody, identityEmail, identityErrorText, identityUnknownResult, IdentityInputError, IdentityPublicError } from './identity-public.ts'
import type { IdentityProof, IdentityPurpose } from './identity-proof.ts'
import type { IdentityCapabilities, IdentityCompletion } from './identity-public.ts'
import { identityCompletionPath } from './identity-proof.ts'

export type IdentityPhase = 'ready' | 'submitting' | 'accepted' | 'completed' | 'proof_invalid' | 'error' | 'unknown' | 'missing_proof'
export interface IdentityActionSnapshot { phase: IdentityPhase; error: string }
export interface IdentityTicket { readonly epoch: number; readonly sequence: number }
export function createIdentityAction(purpose: IdentityPurpose, proof?: IdentityProof, completion = false) {
  let token = proof?.purpose === purpose && proof.path === identityCompletionPath(purpose) ? proof.token : ''
  let epoch = 0, sequence = 0, disposed = false
  let state: IdentityActionSnapshot = { phase: completion && !token ? 'missing_proof' : 'ready', error: '' }
  const accepts = (ticket: IdentityTicket) => !disposed && ticket.epoch === epoch && ticket.sequence === sequence && state.phase === 'submitting'
  const start = (available: boolean) => {
    if (disposed || !['ready', 'error', 'proof_invalid'].includes(state.phase)) throw new IdentityInputError('当前操作不能重复提交，请先核查原结果。')
    if (!available) throw new IdentityInputError('此项自助功能当前不可用，请先刷新能力状态。')
    state = { phase: 'submitting', error: '' }; return Object.freeze({ epoch, sequence: ++sequence })
  }
  return {
    snapshot(): IdentityActionSnapshot { return { ...state } },
    accepts,
    beginRequest(email: string, available: boolean) { if (completion) throw new IdentityInputError('请在邮件请求页操作。'); const normalized = identityEmail(email); const ticket = start(available); return { ticket, email: normalized } },
    beginCompletion(input: { password: string; tenant_name?: string; mfa_code?: string }, available: boolean): { ticket: IdentityTicket; body: IdentityCompletion } {
      if (!completion || !token) throw new IdentityInputError('请重新打开最新邮件中的链接。')
      const body = identityCompletionBody(purpose, token, input), ticket = start(available); return { ticket, body }
    },
    accept(ticket: IdentityTicket): boolean {
      if (!accepts(ticket)) return false
      state = { phase: completion ? 'completed' : 'accepted', error: '' }; token = ''; return true
    },
    fail(ticket: IdentityTicket, error: unknown): boolean {
      if (!accepts(ticket)) return false
      const unknown = identityUnknownResult(error)
      state = { phase: unknown ? 'unknown' : completion && error instanceof IdentityPublicError && error.code === 'identity_proof_invalid' ? 'proof_invalid' : 'error', error: identityErrorText(error) }
      if (unknown) token = ''
      return true
    },
    newMailRequest() { if (completion || disposed || state.phase === 'submitting') throw new IdentityInputError('不能在当前页面重新发起邮件请求。'); epoch++; state = { phase: 'ready', error: '' } },
    dispose() { disposed = true; epoch++; token = ''; state = { phase: completion ? 'missing_proof' : 'ready', error: '' } },
  }
}

export function createIdentityCapabilitiesReader() {
  let epoch = 0, disposed = false
  let state: { loading: boolean; capabilities?: IdentityCapabilities; error: string } = { loading: false, error: '' }
  const accepts = (ticket: number) => !disposed && ticket === epoch && state.loading
  return {
    snapshot() { return { ...state } },
    begin() { const ticket = ++epoch; if (!disposed) state = { loading: true, error: '' }; return ticket },
    accept(ticket: number, capabilities: IdentityCapabilities) { if (!accepts(ticket)) return false; state = { loading: false, capabilities, error: '' }; return true },
    fail(ticket: number) { if (!accepts(ticket)) return false; state = { loading: false, error: '暂时无法读取自助功能状态，请手动刷新后重试。' }; return true },
    invalidate() { epoch++; state = { loading: false, error: '' } },
    dispose() { disposed = true; epoch++; state = { loading: false, error: '' } },
  }
}
