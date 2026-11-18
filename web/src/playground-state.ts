import type { InferenceResult } from './inference.ts'
import type { ChatFinishReason, ChatToolCallView } from './chat-tools.ts'

export interface PlaygroundResponseState {
  output: string
  toolCalls: ChatToolCallView[]
  finishReason: ChatFinishReason | ''
  usage: Record<string, unknown>
  requestId: string
  metrics: InferenceResult | null
  error: string
  status: string
}

// Results belong to the model that produced them. Refreshing that same model
// preserves the result; changing selection clears only response state, leaving
// the caller's key and request inputs intact.
export function resetResponseForModelChange(state: PlaygroundResponseState, previousId: string | undefined, nextId: string | undefined): void {
  if (previousId === nextId) return
  Object.assign(state, { output: '', toolCalls: [], finishReason: '', usage: {}, requestId: '', metrics: null, error: '', status: '' })
}
