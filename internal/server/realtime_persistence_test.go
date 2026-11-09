package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/realtime"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestPostgresRealtimePersistenceLifecycleAndTenantIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if !strings.Contains(dsn, ":55439/moni_test") {
		t.Skip("realtime PostgreSQL acceptance requires TEST_DATABASE_URL on 127.0.0.1:55439/moni_test")
	}
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx := context.Background()

	var owner, profile, routeID string
	var revision int64
	if err := f.pool.QueryRow(ctx, "SELECT owner_id FROM api_keys WHERE tenant_id=$1 AND id=$2", f.tenant, f.key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT profile_version,active_revision FROM models WHERE id=$1", f.model).Scan(&profile, &revision); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT id FROM route_policy_versions WHERE model_id=$1 AND revision=$2", f.model, revision).Scan(&routeID); err != nil {
		t.Fatal(err)
	}

	requestID := uuid.NewString()
	reservation, err := f.server.Billing.Reserve(ctx, billing.ReserveInput{
		RouteRevision: revision,
		Principal:     billing.Principal{TenantID: f.tenant, UserID: owner, KeyID: f.key},
		RequestID:     requestID, ModelID: f.model, IdempotencyKey: uuid.NewString(),
		RequestHMAC: strings.Repeat("a", 64), ProfileVersion: profile,
		InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := f.server.Catalog.Candidates(ctx, f.model, revision)
	if err != nil || len(candidates) == 0 {
		t.Fatalf("candidates=%d err=%v", len(candidates), err)
	}
	candidate := candidates[0]
	attempt, err := f.server.Billing.RecordAttempt(ctx, billing.AttemptInput{
		TenantID: f.tenant, RequestID: requestID, ChannelID: candidate.ChannelID,
		ChannelVersionID: candidate.VersionID, CredentialID: candidate.CredentialID,
		UpstreamModel: candidate.UpstreamModel, Sequence: 1,
		RouteDecisions: []routing.Decision{{ChannelID: candidate.ChannelID, VersionID: candidate.VersionID, Priority: candidate.Priority, Weight: uint64(candidate.Weight), Reason: "selected"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	store := newPostgresRealtimePersistence(f.pool, f.tenant)
	adapter := provider.NewMockRealtimeAdapter()
	service, err := realtime.NewService(realtime.ServiceOptions{Store: store, Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, turnID := uuid.NewString(), uuid.NewString()
	binding := realtime.TicketBinding{
		SessionID: sessionID, TenantID: f.tenant, ActorID: owner, APIKeyID: f.key,
		ModelID: f.model, ProfileID: profile, RouteID: routeID, Origin: "http://console.test",
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	ticket, err := service.IssueTicket(ctx, binding, at, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateSession(ctx, ticket.Token, binding, realtime.SessionConfig{
		ID: sessionID, TenantID: f.tenant, ActorID: owner, APIKeyID: f.key,
		ModelID: f.model, ProfileID: profile, RouteID: routeID, Origin: binding.Origin,
		Limits: realtime.SessionLimits{MaxTurns: 2, SessionTTL: 10 * time.Minute, TurnTTL: time.Minute, MaxFrameBytes: 1 << 20, MaxQueueEvents: 32, MaxQueueBytes: 4 << 20, MaxForwardedBytes: 64 << 20},
	}, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	refs := realtime.CommercialRefs{RequestID: requestID, ReservationID: reservation.ID, AttemptID: attempt.ID}
	turn, err := service.BeginTurn(ctx, sessionID, realtime.BeginTurnInput{TurnID: turnID, RequestID: requestID, ReservationID: reservation.ID, AttemptID: attempt.ID}, refs, at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	parser, err := realtime.NewEventParser(realtime.EventLimits{})
	if err != nil {
		t.Fatal(err)
	}
	eventID := uuid.NewString()
	event, err := parser.Parse([]byte(`{"event_id":"` + eventID + `","type":"response.create","data":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AcceptEvent(ctx, sessionID, event, at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.AcceptEvent(ctx, sessionID, event, at.Add(3*time.Second))
	if err != nil || duplicate.Disposition != realtime.EventDuplicate {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}

	secondTurn := realtime.TurnRecord{SessionID: sessionID, Refs: refs, Snapshot: realtime.TurnSnapshot{
		ID: uuid.NewString(), RequestID: requestID, ReservationID: reservation.ID, AttemptID: attempt.ID,
		State: realtime.TurnReserved, CreatedAt: at.Add(3 * time.Second), UpdatedAt: at.Add(3 * time.Second),
	}}
	if err = store.SaveTurn(ctx, secondTurn); err == nil {
		t.Fatal("database accepted two inflight turns")
	}

	if _, err = service.Dispatch(ctx, sessionID, turn.ID, realtime.ProviderPayload{EventID: event.ID, EventType: event.Type}, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	// A fresh persistence and provider adapter must recover the durable
	// provider session/turn identities without dispatching a second turn.
	restartedStore := newPostgresRealtimePersistence(f.pool, f.tenant)
	restartedService, err := realtime.NewService(realtime.ServiceOptions{Store: restartedStore, Adapter: provider.NewMockRealtimeAdapter()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restartedService.Receive(ctx, sessionID, turn.ID, at.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	proof := realtime.SettlementProof{FinalizationID: uuid.NewString(), SettlementID: uuid.NewString(), RetailUsageDigest: strings.Repeat("a", 64), RetailUsageState: realtime.UsageConfirmedFinal, SupplierUsageState: realtime.UsageUnknown}
	if err = restartedService.Settle(ctx, sessionID, turn.ID, proof, at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = restartedService.Settle(ctx, sessionID, turn.ID, proof, at.Add(7*time.Second)); err != nil {
		t.Fatalf("idempotent settle: %v", err)
	}

	var sessionState, turnState, storedRequest, storedReservation, storedAttempt string
	if err = f.pool.QueryRow(ctx, "SELECT state FROM realtime_sessions WHERE tenant_id=$1 AND id=$2", f.tenant, sessionID).Scan(&sessionState); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT state,request_id,reservation_id,attempt_id FROM realtime_turns WHERE tenant_id=$1 AND session_id=$2 AND id=$3", f.tenant, sessionID, turn.ID).Scan(&turnState, &storedRequest, &storedReservation, &storedAttempt); err != nil {
		t.Fatal(err)
	}
	var eventCount int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM realtime_events WHERE tenant_id=$1 AND session_id=$2 AND event_id=$3", f.tenant, sessionID, event.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if sessionState != string(realtime.SessionOpen) || turnState != string(realtime.TurnSettled) || storedRequest != requestID || storedReservation != reservation.ID || storedAttempt != attempt.ID || eventCount != 1 {
		t.Fatalf("stored realtime facts session=%q turn=%q refs=%q/%q/%q events=%d", sessionState, turnState, storedRequest, storedReservation, storedAttempt, eventCount)
	}

	otherTenant := newPostgresRealtimePersistence(f.pool, uuid.NewString())
	if _, err = otherTenant.LoadSession(ctx, sessionID); !errors.Is(err, realtime.ErrNotFound) {
		t.Fatalf("cross-tenant session read err=%v", err)
	}
	if _, err = otherTenant.FindEvent(ctx, sessionID, event.ID); !errors.Is(err, realtime.ErrNotFound) {
		t.Fatalf("cross-tenant event read err=%v", err)
	}
}

func TestPostgresRealtimeServerSelectsDatabasePersistence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if !strings.Contains(dsn, ":55439/moni_test") {
		t.Skip("realtime PostgreSQL acceptance requires TEST_DATABASE_URL on 127.0.0.1:55439/moni_test")
	}
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	service, err := f.server.realtimeServiceForTenant(f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	if service == nil {
		t.Fatal("database realtime service is nil")
	}
}
