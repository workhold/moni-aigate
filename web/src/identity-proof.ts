export type IdentityPurpose = 'registration' | 'password_reset'
export interface IdentityProof { readonly purpose: IdentityPurpose; readonly path: string; readonly token: string }
const paths: Record<IdentityPurpose, string> = { registration: '/register/complete', password_reset: '/password/reset' }
export function identityCompletionPath(purpose: IdentityPurpose): string { return paths[purpose] }
export function canonicalIdentityToken(value: unknown): value is string {
  if (typeof value !== 'string' || !/^ic1_[A-Za-z0-9_-]{43}$/.test(value)) return false
  try {
    const encoded = value.slice(4), decoded = atob(encoded.replace(/-/g, '+').replace(/_/g, '/') + '=')
    return decoded.length === 32 && btoa(decoded).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '') === encoded
  } catch { return false }
}
export function createIdentityProofHandoff(location: Pick<Location, 'pathname' | 'search' | 'hash'>, history: Pick<History, 'replaceState'>) {
  let proof: IdentityProof | undefined
  const purpose = (Object.keys(paths) as IdentityPurpose[]).find(kind => paths[kind] === location.pathname)
  const match = /^#token=(ic1_[A-Za-z0-9_-]{43})$/.exec(location.hash)
  if (purpose && match && canonicalIdentityToken(match[1])) proof = Object.freeze({ purpose, path: location.pathname, token: match[1] })
  // Run before createWebHistory: copying history.state here could retain the
  // old token in current/back/forward and reintroduce it on a later push.
  let decodedFragment = location.hash
  try { decodedFragment = decodeURIComponent(location.hash) } catch { /* Invalid fragments are never proof input. */ }
  const mailFragment = !!purpose || /(?:^#|&)token(?:=|&|$)/i.test(decodedFragment) || decodedFragment.includes('ic1_') || location.hash.includes('ic1_')
  if (mailFragment) history.replaceState(null, '', location.pathname + (purpose ? '' : location.search))
  return {
    take(path: string): IdentityProof | undefined { const current = proof; proof = undefined; return current?.path === path ? current : undefined },
    discardExcept(path: string) { if (proof?.path !== path) proof = undefined },
    discard() { proof = undefined },
  }
}
let initialProof: ReturnType<typeof createIdentityProofHandoff> | undefined
export function captureIdentityProofBeforeHistory() {
  initialProof?.discard()
  initialProof = createIdentityProofHandoff(window.location, window.history)
}
export function takeIdentityProof(path: string) { return initialProof?.take(path) }
export function discardUnclaimedIdentityProof(path: string) { initialProof?.discardExcept(path) }
