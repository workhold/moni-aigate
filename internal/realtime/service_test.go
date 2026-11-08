package realtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type serviceAdapter struct {
	mu       sync.Mutex
	open     int
	sends    int
	receives int
	cancels  int
}

func (a *serviceAdapter) Open(context.Context, ProviderOpenInput) (ProviderSession, error) {
	a.mu.Lock()
	a.open++
	a.mu.Unlock()
	return ProviderSession{ID: "provider-session-1"}, nil
}

func (a *serviceAdapter) Send(context.Context, ProviderSendInput) (ProviderDispatch, error) {
	a.mu.Lock()
	a.sends++
	a.mu.Unlock()
	return ProviderDispatch{Sent: true, Handle: "provider-handle-1", ResponseID: "response-1"}, nil
}

func (a *serviceAdapter) Receive(context.Context, ProviderReceiveInput) (ProviderReceiveResult, error) {
	a.mu.Lock()
	a.receives++
	a.mu.Unlock()
	return ProviderReceiveResult{ResponseID: "response-1", Bytes: 12, Final: true}, nil
}

func (a *serviceAdapter) Cancel(context.Context, ProviderCancelInput) error {
	a.mu.Lock()
	a.cancels++
	a.mu.Unlock()
	return nil
}

func serviceFixture(t *testing.T) (*Service, *MemoryStore, *serviceAdapter, TicketBinding, time.Time) {
	t.Helper()
	store := NewMemoryStore()
	adapter := &serviceAdapter{}
	service, err := NewService(ServiceOptions{Store: store, Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	binding := TicketBinding{SessionID: "session-service", TenantID: "tenant-service", ActorID: "actor-service", APIKeyID: "key-service", ModelID: "model-service", ProfileID: "profile-service", RouteID: "route-service", Origin: "https://console.example"}
	at := time.Date(2026, 11, 8, 4, 0, 0, 0, time.UTC)
	return service, store, adapter, binding, at
}

func TestServicePersistsCommercialRefsAndSettlesThroughProvider(t *testing.T) {
	service, store, adapter, binding, at := serviceFixture(t)
	ctx := context.Background()
	ticket, err := service.IssueTicket(ctx, binding, at, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.CreateSession(ctx, ticket.Token, binding, SessionConfig{
		ID: binding.SessionID, TenantID: binding.TenantID, ActorID: binding.ActorID, APIKeyID: binding.APIKeyID,
		ModelID: binding.ModelID, ProfileID: binding.ProfileID, RouteID: binding.RouteID, Origin: binding.Origin,
	}, at.Add(time.Second))
	if err != nil || snapshot.State != SessionOpen {
		t.Fatalf("create session: %+v, %v", snapshot, err)
	}
	refs := CommercialRefs{RequestID: "request-service", ReservationID: "reservation-service", AttemptID: "attempt-service"}
	turn, err := service.BeginTurn(ctx, binding.SessionID, BeginTurnInput{TurnID: "turn-service", RequestID: refs.RequestID, ReservationID: refs.ReservationID, AttemptID: refs.AttemptID}, refs, at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	parser, err := NewEventParser(EventLimits{})
	if err != nil {
		t.Fatal(err)
	}
	event, err := parser.Parse([]byte(`{"event_id":"create-service","type":"response.create","data":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AcceptEvent(ctx, binding.SessionID, event, at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.AcceptEvent(ctx, binding.SessionID, event, at.Add(3*time.Second))
	if err != nil || duplicate.Disposition != EventDuplicate {
		t.Fatalf("duplicate event: %+v, %v", duplicate, err)
	}
	dispatched, err := service.Dispatch(ctx, binding.SessionID, turn.ID, ProviderPayload{EventID: event.ID, EventType: event.Type}, at.Add(4*time.Second))
	if err != nil || dispatched.State != TurnSending || dispatched.ProviderResponseID != "response-1" {
		t.Fatalf("dispatch: %+v, %v", dispatched, err)
	}
	received, err := service.Receive(ctx, binding.SessionID, turn.ID, at.Add(5*time.Second))
	if err != nil || received.State != TurnFinalizing {
		t.Fatalf("receive: %+v, %v", received, err)
	}
	proof := SettlementProof{FinalizationID: "final-service", SettlementID: "settle-service", RetailUsageDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RetailUsageState: UsageConfirmedFinal, SupplierUsageState: UsageUnknown}
	if err = service.Settle(ctx, binding.SessionID, turn.ID, proof, at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := store.LoadTurn(ctx, binding.SessionID, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Refs != refs || stored.Snapshot.State != TurnSettled || stored.Snapshot.SettlementID != proof.SettlementID {
		t.Fatalf("stored turn lost commercial proof: %+v", stored)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.open != 1 || adapter.sends != 1 || adapter.receives != 1 || adapter.cancels != 0 {
		t.Fatalf("provider calls: %+v", adapter)
	}
}

func TestServiceDisconnectLeavesPendingAndCannotResubmit(t *testing.T) {
	service, _, adapter, binding, at := serviceFixture(t)
	ctx := context.Background()
	ticket, err := service.IssueTicket(ctx, binding, at, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateSession(ctx, ticket.Token, binding, SessionConfig{ID: binding.SessionID, TenantID: binding.TenantID, ActorID: binding.ActorID, APIKeyID: binding.APIKeyID, ModelID: binding.ModelID, ProfileID: binding.ProfileID, RouteID: binding.RouteID, Origin: binding.Origin}, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	refs := CommercialRefs{RequestID: "request-pending", ReservationID: "reservation-pending", AttemptID: "attempt-pending"}
	turn, err := service.BeginTurn(ctx, binding.SessionID, BeginTurnInput{TurnID: "turn-pending", RequestID: refs.RequestID, ReservationID: refs.ReservationID, AttemptID: refs.AttemptID}, refs, at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	parser, _ := NewEventParser(EventLimits{})
	event, _ := parser.Parse([]byte(`{"event_id":"create-pending","type":"response.create","data":{}}`))
	if _, err = service.AcceptEvent(ctx, binding.SessionID, event, at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Dispatch(ctx, binding.SessionID, turn.ID, ProviderPayload{EventID: event.ID, EventType: event.Type}, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	closed, err := service.Disconnect(ctx, binding.SessionID, "client_disconnect", at.Add(5*time.Second))
	if err != nil || !closed.CancelUpstream || closed.TurnState != TurnPending {
		t.Fatalf("disconnect: %+v, %v", closed, err)
	}
	if _, err = service.Dispatch(ctx, binding.SessionID, turn.ID, ProviderPayload{EventID: event.ID, EventType: event.Type}, at.Add(6*time.Second)); !errors.Is(err, ErrPendingNoResubmit) {
		t.Fatalf("pending turn was resubmitted: %v", err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.sends != 1 || adapter.cancels != 1 {
		t.Fatalf("provider calls after disconnect: %+v", adapter)
	}
}
