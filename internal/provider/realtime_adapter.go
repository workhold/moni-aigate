package provider

import "github.com/moni-ai/moni-aigate/internal/realtime"

// RealtimeAdapter is the provider-side name for the single realtime network
// boundary. The realtime service owns state and commercial references. A
// provider implementation only implements these transport calls.
type RealtimeAdapter = realtime.ProviderRealtimeAdapter

type RealtimeOpenInput = realtime.ProviderOpenInput
type RealtimeSession = realtime.ProviderSession
type RealtimeSendInput = realtime.ProviderSendInput
type RealtimePayload = realtime.ProviderPayload
type RealtimeDispatch = realtime.ProviderDispatch
type RealtimeReceiveInput = realtime.ProviderReceiveInput
type RealtimeReceiveResult = realtime.ProviderReceiveResult
type RealtimeCancelInput = realtime.ProviderCancelInput
