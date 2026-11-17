export type ChatFinishReason = 'stop' | 'length' | 'tool_calls' | 'content_filter'
export interface ChatToolCallDelta {
  index: number
  id?: string
  type?: 'function'
  name?: string
  arguments?: string
}
export interface ChatToolCallView {
  index: number
  id: string
  type: string
  name: string
  arguments: string
}

// Chat deltas are protocol fragments, not executable tool calls. Only argument
// text is appendable; identity fields may arrive late but may not change.
export class ChatToolCallAssembler {
  private calls = new Map<number, ChatToolCallView>()
  private argumentBytes = 0
  apply(deltas: ChatToolCallDelta[] | undefined): void {
    for (const delta of deltas || []) {
      if (!Number.isSafeInteger(delta.index) || delta.index < 0 || delta.index > 63) throw new Error('工具调用索引无效，请凭请求 ID 查询结果。')
      const current = this.calls.get(delta.index) || { index: delta.index, id: '', type: '', name: '', arguments: '' }
      for (const field of ['id', 'type', 'name'] as const) {
        const incoming = delta[field]
        if (incoming !== undefined) {
          if (current[field] && current[field] !== incoming) throw new Error('工具调用身份在流中发生冲突，请凭请求 ID 查询结果。')
          current[field] = incoming
        }
      }
      if (delta.arguments !== undefined) {
        this.argumentBytes += new TextEncoder().encode(delta.arguments).byteLength
        if (this.argumentBytes > 1 << 20) throw new Error('工具参数超过展示上限，请凭请求 ID 查询结果。')
        current.arguments += delta.arguments
      }
      this.calls.set(delta.index, current)
    }
  }
  list(): ChatToolCallView[] {
    return [...this.calls.values()].sort((a, b) => a.index - b.index).map(call => ({ ...call }))
  }
}
