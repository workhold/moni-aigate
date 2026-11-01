import { ApiError } from './api.ts'
import { parsePublicCatalogPage, parsePublication, publicationWrite, publicFilters, publicCatalogErrorText } from './public-catalog.ts'
import type { PublicCatalogPage, PublicFilters, PublicModel, ModelPublication, PublicationRequest } from './public-catalog.ts'

export interface ObservedPublicModel { model: PublicModel; as_of: string }
export interface CatalogTicket { readonly epoch: number; readonly sequence: number; readonly filters: PublicFilters; readonly cursor: string }
export interface CatalogState { filters: PublicFilters; rows: ObservedPublicModel[]; loading: boolean; loaded: boolean; error: string; next_cursor: string; last_as_of: string }
export function createPublicCatalogPager() {
  let epoch = 0, sequence = 0, disposed = false
  let seenCursors = new Set<string>()
  let state: CatalogState = { filters: { q: '', protocol: '' }, rows: [], loading: false, loaded: false, error: '', next_cursor: '', last_as_of: '' }
  const accepts = (ticket: CatalogTicket) => !disposed && ticket.epoch === epoch && ticket.sequence === sequence && state.loading
  return {
    snapshot(): CatalogState { return { ...state, filters: { ...state.filters }, rows: [...state.rows] } },
    begin(filters: PublicFilters, reset = true): CatalogTicket | null {
      if (disposed) return null
      const normalized = publicFilters(filters.q, filters.protocol)
      if (reset) { epoch++; seenCursors = new Set(['']); state = { filters: normalized, rows: [], loading: false, loaded: false, error: '', next_cursor: '', last_as_of: '' } }
      else if (state.loading || !state.next_cursor || JSON.stringify(normalized) !== JSON.stringify(state.filters)) return null
      state = { ...state, loading: true, error: '' }; sequence++
      return Object.freeze({ epoch, sequence, filters: Object.freeze({ ...normalized }), cursor: reset ? '' : state.next_cursor })
    },
    accepts,
    accept(ticket: CatalogTicket, value: PublicCatalogPage): boolean {
      if (!accepts(ticket)) return false
      const page = parsePublicCatalogPage(value)
      if (page.next_cursor && seenCursors.has(page.next_cursor)) throw new Error('目录分页响应没有前进，请刷新目录。')
      const known = new Set(state.rows.map(row => row.model.id)), additions = page.data.filter(row => !known.has(row.id)).map(model => ({ model, as_of: page.as_of }))
      if (page.next_cursor) seenCursors.add(page.next_cursor)
      state = { ...state, loading: false, loaded: true, rows: [...state.rows, ...additions], next_cursor: page.next_cursor, last_as_of: page.as_of, error: '' }
      return true
    },
    fail(ticket: CatalogTicket, error: unknown): boolean {
      if (!accepts(ticket)) return false
      state = { ...state, loading: false, error: publicCatalogErrorText(error) }; return true
    },
    invalidate(error?: unknown) { epoch++; seenCursors.clear(); state = { ...state, rows: [], loading: false, loaded: false, next_cursor: '', last_as_of: '', error: error === undefined ? '' : publicCatalogErrorText(error) } },
    dispose() { disposed = true; epoch++; state = { ...state, loading: false } },
  }
}

export interface PublicationReview {
  readonly view: ModelPublication; readonly mode: 'editing' | 'submitting' | 'conflict' | 'unknown'
  readonly submitted?: PublicationRequest; readonly observed?: ModelPublication
}
export function newPublicationReview(value: unknown): PublicationReview { return { view: parsePublication(value), mode: 'editing' } }
export function submitPublication(review: PublicationReview, visible: boolean): PublicationReview {
  if (review.mode !== 'editing') throw new Error('请先只读核查并明确开始新编辑。')
  return { ...review, mode: 'submitting', submitted: publicationWrite(review.view, visible) }
}
export function failPublication(review: PublicationReview, error: unknown): PublicationReview {
  if (review.mode !== 'submitting') throw new Error('没有待确认的公开配置提交。')
  const unknown = !(error instanceof ApiError) || error.status === 0 || error.status >= 500 || error.status === 408 || error.code === 'invalid_response'
  return { ...review, mode: error instanceof ApiError && error.status === 409 ? 'conflict' : unknown ? 'unknown' : 'editing', observed: undefined }
}
export function acceptPublication(review: PublicationReview, value: unknown): PublicationReview {
  if (review.mode !== 'submitting' || !review.submitted) throw new Error('没有待确认的公开配置提交。')
  const view = parsePublication(value, review.view.id), before = BigInt(review.submitted.body.expected_revision)
  const expected = before + (review.submitted.body.public_visible === review.view.public_visible ? 0n : 1n)
  if (view.public_visible !== review.submitted.body.public_visible || BigInt(view.public_revision) !== expected) throw new Error('保存响应与原公开设置或版本不一致，请只读核查。')
  return newPublicationReview(view)
}
export function observePublication(review: PublicationReview, value: unknown): PublicationReview {
  if (!['conflict', 'unknown'].includes(review.mode)) throw new Error('当前没有待核查的公开配置。')
  const observed = parsePublication(value, review.view.id), before = BigInt(review.view.public_revision), revision = BigInt(observed.public_revision)
  if (revision < before || revision === before && observed.public_visible !== review.view.public_visible) throw new Error('核查响应版本不一致，请再次只读读取。')
  return { ...review, observed }
}
export function resumePublication(review: PublicationReview): PublicationReview {
  if (!review.observed || !['conflict', 'unknown'].includes(review.mode)) throw new Error('请先只读核查当前配置。')
  return newPublicationReview(review.observed)
}

// A closed/recreated editor must not silently forget an uncertain PUT.
// Only safe publication metadata is retained, scoped by actor/tenant/role.
export function createPublicationJournal() {
  let sequence = 0
  const entries = new Map<string, { ticket: number; review: PublicationReview }>()
  const key = (context: string, id: string) => JSON.stringify([context, id])
  return {
    read(context: string, id: string): PublicationReview | undefined {
      const review = entries.get(key(context, id))?.review
      return review && { ...review, mode: review.mode === 'submitting' ? 'unknown' : review.mode, observed: undefined }
    },
    begin(context: string, review: PublicationReview): number {
      if (review.mode !== 'submitting' || !review.submitted) throw new Error('没有待发送的公开配置。')
      const id = key(context, review.view.id)
      if (entries.has(id)) throw new Error('原公开配置仍待核查，请明确开始新编辑。')
      if (entries.size >= 128) throw new Error('待核查公开配置过多，请先处理已有结果。')
      const ticket = ++sequence; entries.set(id, { ticket, review }); return ticket
    },
    finish(context: string, modelID: string, ticket: number, review?: PublicationReview) {
      const id = key(context, modelID)
      if (entries.get(id)?.ticket !== ticket) return
      if (review && ['unknown', 'conflict'].includes(review.mode)) entries.set(id, { ticket, review })
      else entries.delete(id)
    },
    restart(context: string, review: PublicationReview): PublicationReview {
      const next = resumePublication(review); entries.delete(key(context, review.view.id)); return next
    },
  }
}
export const publicationJournal = createPublicationJournal()
