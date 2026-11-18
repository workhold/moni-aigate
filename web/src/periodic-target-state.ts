import { reactive } from 'vue'
import { errorText } from './api.ts'
import { readPeriodicTargetChoices } from './periodic-target.ts'
import type { PeriodicTargetChoice } from './periodic-target.ts'

export interface PeriodicTargetLoadState { choices: PeriodicTargetChoice[]; loading: boolean; error: string }
export type PeriodicTargetContent = 'error' | 'loading' | 'empty' | 'search_empty' | 'rows'
export interface PeriodicTargetEmptyAction { label: string; path: string }

export function createPeriodicTargetLoader(load = readPeriodicTargetChoices) {
  let generation = 0
  const state = reactive<PeriodicTargetLoadState>({ choices: [], loading: false, error: '' })
  async function reload() {
    const ticket = ++generation
    state.loading = true; state.error = ''
    try {
      const rows = await load(() => ticket === generation)
      if (ticket === generation) state.choices = rows
    } catch (cause) {
      if (ticket === generation) { state.choices = []; state.error = errorText(cause) }
    } finally { if (ticket === generation) state.loading = false }
  }
  function reset() { state.choices = []; state.error = ''; state.loading = false; return reload() }
  function dispose() { generation++; state.loading = false }
  return { state, reload, reset, dispose }
}

export function periodicTargetContent(state: PeriodicTargetLoadState, total: number): PeriodicTargetContent {
  if (state.error) return 'error'
  if (state.loading) return 'loading'
  if (!state.choices.length) return 'empty'
  if (!total) return 'search_empty'
  return 'rows'
}

export function periodicTargetEmptyActions(admin: boolean): { message: string; actions: PeriodicTargetEmptyAction[] } {
  if (!admin) return { message: '当前还没有可选择的预算目标，请联系平台管理员创建客户或采购账户。', actions: [] }
  return { message: '还没有可选择的预算目标。请先创建客户或采购账户。', actions: [{ label: '管理客户', path: '/platform/tenants' }, { label: '管理采购账户', path: '/platform/procurement' }] }
}
