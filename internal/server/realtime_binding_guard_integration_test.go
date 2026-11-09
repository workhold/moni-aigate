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

func TestPostgresRealtimeTurnRejectsMixedCommercialFactSets(t *testing.T) {
	if !strings.Contains(os.Getenv("TEST_DATABASE_URL"), ":55439/moni_test") {
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
	candidates, err := f.server.Catalog.Candidates(ctx, f.model, revision)
	if err != nil || len(candidates) == 0 {
		t.Fatalf("candidates=%d err=%v", len(candidates), err)
	}
	candidate := candidates[0]
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
	attempt, err := f.server.Billing.RecordAttempt(ctx, billing.AttemptInput{
		TenantID: f.tenant, RequestID: requestID, ChannelID: candidate.ChannelID,
		ChannelVersionID: candidate.VersionID, CredentialID: candidate.CredentialID,
		UpstreamModel: candidate.UpstreamModel, Sequence: 1,
		RouteDecisions: []routing.Decision{{ChannelID: candidate.ChannelID, VersionID: candidate.VersionID, Priority: candidate.Priority, Weight: uint64(candidate.Weight), Reason: "selected"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mediaReservation, err := f.server.Billing.ReserveMedia(ctx, billing.MediaReserveInput{
		TenantID: f.tenant, UserID: owner, KeyID: f.key, RequestID: requestID,
		AttemptID: uuid.NewString(), Operation: "realtime_turn", ModelKey: "mock-realtime",
		ProfileVersion: "44444444-4444-4444-8444-444444444444", IdempotencyKey: uuid.NewString(),
		RequestDigest: []byte(strings.Repeat("b", 32)), QuantityBound: 1,
		Deadline: time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	store := newPostgresRealtimePersistence(f.pool, f.tenant)
	service, err := realtime.NewService(realtime.ServiceOptions{Store: store, Adapter: provider.NewMockRealtimeAdapter()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, mixed := range []struct {
		name          string
		reservationID string
		attemptID     string
	}{
		{name: "media reservation with text attempt", reservationID: mediaReservation.ID, attemptID: attempt.ID},
		{name: "text reservation with media attempt", reservationID: reservation.ID, attemptID: mediaReservation.AttemptID},
	} {
		t.Run(mixed.name, func(t *testing.T) {
			sessionID := uuid.NewString()
			ticket, err := service.IssueTicket(ctx, realtime.TicketBinding{
				SessionID: sessionID, TenantID: f.tenant, ActorID: owner, APIKeyID: f.key,
				ModelID: f.model, ProfileID: profile, RouteID: routeID, Origin: "http://console.test",
			}, now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.CreateSession(ctx, ticket.Token, realtime.TicketBinding{
				SessionID: sessionID, TenantID: f.tenant, ActorID: owner, APIKeyID: f.key,
				ModelID: f.model, ProfileID: profile, RouteID: routeID, Origin: "http://console.test",
			}, realtime.SessionConfig{
				ID: sessionID, TenantID: f.tenant, ActorID: owner, APIKeyID: f.key,
				ModelID: f.model, ProfileID: profile, RouteID: routeID, Origin: "http://console.test",
				Limits: realtime.SessionLimits{MaxTurns: 1, SessionTTL: time.Minute, TurnTTL: time.Minute, MaxFrameBytes: 1 << 20, MaxQueueEvents: 32, MaxQueueBytes: 1 << 20, MaxForwardedBytes: 1 << 20},
			}, now); err != nil {
				t.Fatal(err)
			}
			turnID := uuid.NewString()
			err = store.SaveTurn(ctx, realtime.TurnRecord{
				SessionID: sessionID,
				Refs:      realtime.CommercialRefs{RequestID: requestID, ReservationID: mixed.reservationID, AttemptID: mixed.attemptID},
				Snapshot:  realtime.TurnSnapshot{ID: turnID, RequestID: requestID, ReservationID: mixed.reservationID, AttemptID: mixed.attemptID, State: realtime.TurnReserved, CreatedAt: now, UpdatedAt: now},
			})
			if err == nil || !errors.Is(err, realtime.ErrInvalidInput) && !strings.Contains(err.Error(), "realtime turn binding mismatch") {
				t.Fatalf("mixed commercial facts were accepted: %v", err)
			}
			var turns int
			if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM realtime_turns WHERE tenant_id=$1 AND session_id=$2", f.tenant, sessionID).Scan(&turns); err != nil || turns != 0 {
				t.Fatalf("failed mixed turn left a row: turns=%d err=%v", turns, err)
			}
		})
	}
}
