package provider

import (
	"context"
	"testing"
)

func TestMockRealtimeAdapterCompletesOneResponse(t *testing.T) {
	adapter := NewMockRealtimeAdapter()
	ctx := context.Background()
	opened, err := adapter.Open(ctx, RealtimeOpenInput{SessionID: "session-1"})
	if err != nil || opened.ID == "" {
		t.Fatalf("open = %+v, %v", opened, err)
	}
	dispatch, err := adapter.Send(ctx, RealtimeSendInput{ProviderSessionID: opened.ID, SessionID: "session-1", TurnID: "turn-1", Payload: []byte("hello")})
	if err != nil || !dispatch.Sent || dispatch.Handle == "" || dispatch.ResponseID == "" {
		t.Fatalf("send = %+v, %v", dispatch, err)
	}
	received, err := adapter.Receive(ctx, RealtimeReceiveInput{ProviderSessionID: opened.ID, ProviderHandle: dispatch.Handle, SessionID: "session-1", TurnID: "turn-1"})
	if err != nil || received.ResponseID != dispatch.ResponseID || received.Bytes != 1 || !received.Final {
		t.Fatalf("receive = %+v, %v", received, err)
	}
	if err = adapter.Cancel(ctx, RealtimeCancelInput{ProviderSessionID: opened.ID, ProviderHandle: dispatch.Handle, SessionID: "session-1", TurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.OpenCalls()) != 1 || len(adapter.SendCalls()) != 1 || len(adapter.ReceiveCalls()) != 1 || len(adapter.CancelCalls()) != 1 {
		t.Fatalf("unexpected call counts: open=%d send=%d receive=%d cancel=%d", len(adapter.OpenCalls()), len(adapter.SendCalls()), len(adapter.ReceiveCalls()), len(adapter.CancelCalls()))
	}
}

func TestMockRealtimeAdapterUnknownHandleIsAnError(t *testing.T) {
	adapter := NewMockRealtimeAdapter()
	if _, err := adapter.Receive(context.Background(), RealtimeReceiveInput{ProviderHandle: "missing"}); err == nil {
		t.Fatal("missing provider handle was accepted")
	}
}
