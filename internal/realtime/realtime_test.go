package realtime

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func testSession(t *testing.T, limits SessionLimits) (*Session, time.Time) {
	t.Helper()
	if limits == (SessionLimits{}) {
		limits = DefaultSessionLimits()
	}
	at := time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC)
	s, err := NewSession(SessionConfig{
		ID: "session-1", TenantID: "tenant-1", ActorID: "actor-1", APIKeyID: "key-1",
		ModelID: "model-1", ProfileID: "profile-1", RouteID: "route-1",
		Origin: "HTTPS://Console.Example:443", Limits: limits,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Open(at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return s, at.Add(time.Second)
}

func parseEvent(t *testing.T, p EventParser, raw string) ClientEvent {
	t.Helper()
	event, err := p.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return event
}

func TestEventParserCanonicalBoundary(t *testing.T) {
	p, err := NewEventParser(EventLimits{})
	if err != nil {
		t.Fatal(err)
	}
	first := parseEvent(t, p, `{"event_id":"e-1","type":"input.text","data":{"text":"hello"}}`)
	second := parseEvent(t, p, " {\"data\": { \"text\": \"hello\" }, \"type\":\"input.text\", \"event_id\":\"e-1\"} ")
	if first.Fingerprint != second.Fingerprint || first.PayloadDigest != second.PayloadDigest {
		t.Fatal("equivalent event encodings did not produce the same semantic fingerprint")
	}
	if !first.ChargeBearing || !first.TurnRequired || first.PayloadBytes != 5 {
		t.Fatalf("unexpected text metadata: %+v", first)
	}

	audio := parseEvent(t, p, `{"event_id":"e-a","type":"input.audio","data":{"audio_base64":"AQID"}}`)
	if audio.PayloadBytes != 3 || !audio.ChargeBearing || audio.PayloadDigest == "" {
		t.Fatalf("unexpected audio metadata: %+v", audio)
	}
	heartbeat := parseEvent(t, p, `{"event_id":"e-h","type":"heartbeat","data":{}}`)
	if heartbeat.ChargeBearing || heartbeat.TurnRequired || heartbeat.PayloadBytes != 0 {
		t.Fatalf("unexpected heartbeat metadata: %+v", heartbeat)
	}

	bad := []struct {
		name string
		raw  string
	}{
		{"unknown type", `{"event_id":"e","type":"provider.response","data":{}}`},
		{"automatic bypass", `{"event_id":"e","type":"server_vad","data":{}}`},
		{"commit bypass", `{"event_id":"e","type":"input.commit","data":{}}`},
		{"unknown root field", `{"event_id":"e","type":"heartbeat","data":{},"extra":1}`},
		{"unknown data field", `{"event_id":"e","type":"input.text","data":{"text":"x","raw":"x"}}`},
		{"duplicate key", `{"event_id":"e","event_id":"e2","type":"heartbeat","data":{}}`},
		{"trailing JSON", `{"event_id":"e","type":"heartbeat","data":{}} {}`},
		{"noncanonical audio", `{"event_id":"e","type":"input.audio","data":{"audio_base64":"AQI"}}`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.Parse([]byte(tc.raw)); !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("expected boundary rejection, got %v", err)
			}
		})
	}
	tooLong := `{"event_id":"e","type":"input.text","data":{"text":"` + strings.Repeat("x", DefaultMaxTextBytes+1) + `"}}`
	if _, err := p.Parse([]byte(tooLong)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized text was accepted: %v", err)
	}
}

func TestSessionRequiresReservationAndHeartbeatDoesNotCreateTurn(t *testing.T) {
	s, at := testSession(t, SessionLimits{})
	p, err := NewEventParser(EventLimits{})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := parseEvent(t, p, `{"event_id":"hb-1","type":"heartbeat","data":{}}`)
	result, err := s.AcceptEvent(heartbeat, at.Add(time.Second))
	if err != nil || result.Disposition != EventAccepted || result.TurnID != "" {
		t.Fatalf("heartbeat result: %+v, %v", result, err)
	}
	if got := s.Snapshot().TurnCount; got != 0 {
		t.Fatalf("heartbeat created %d turns", got)
	}

	if _, err := s.BeginTurn(BeginTurnInput{TurnID: "turn-bad", RequestID: "req", AttemptID: "attempt"}, at.Add(2*time.Second)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing reservation was accepted: %v", err)
	}
	turn, err := s.BeginTurn(BeginTurnInput{TurnID: "turn-1", RequestID: "req-1", ReservationID: "res-1", AttemptID: "attempt-1"}, at.Add(2*time.Second))
	if err != nil || turn.State != TurnReserved {
		t.Fatalf("begin turn: %+v, %v", turn, err)
	}
	if _, err := s.BeginTurn(BeginTurnInput{TurnID: "turn-2", RequestID: "req-2", ReservationID: "res-2", AttemptID: "attempt-2"}, at.Add(3*time.Second)); !errors.Is(err, ErrTurnInFlight) {
		t.Fatalf("second inflight turn was accepted: %v", err)
	}
	if err := s.StartSending(turn.ID, at.Add(3*time.Second)); !errors.Is(err, ErrNoChargeEvent) {
		t.Fatalf("sending bypassed event gate: %v", err)
	}

	create := parseEvent(t, p, `{"event_id":"create-1","type":"response.create","data":{}}`)
	if _, err := s.AcceptEvent(create, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.AcceptEvent(create, at.Add(4*time.Second))
	if err != nil || duplicate.Disposition != EventDuplicate || duplicate.TurnID != turn.ID {
		t.Fatalf("same event was not idempotent: %+v, %v", duplicate, err)
	}
	conflicting := parseEvent(t, p, `{"event_id":"create-1","type":"input.text","data":{"text":"different"}}`)
	if _, err := s.AcceptEvent(conflicting, at.Add(5*time.Second)); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("same ID with different content was accepted: %v", err)
	}
	if err := s.StartSending(turn.ID, at.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkForwarded(turn.ID, 12, at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.StartReceiving(turn.ID, at.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.BindProviderResponse(turn.ID, "provider-response-1", at.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.BindProviderResponse(turn.ID, "provider-response-1", at.Add(8*time.Second)); err != nil {
		t.Fatalf("same provider response was not idempotent: %v", err)
	}
	if err := s.BeginFinalizing(turn.ID, at.Add(9*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurn(turn.ID, SettlementProof{
		FinalizationID: "final-1", SettlementID: "settle-1", RetailUsageDigest: strings.Repeat("a", 64),
		RetailUsageState: UsageConfirmedFinal, SupplierUsageState: UsageUnknown,
	}, at.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot := s.Snapshot()
	if snapshot.ActiveTurnID != "" || snapshot.TurnCount != 1 || snapshot.Turns[0].State != TurnSettled || snapshot.Turns[0].SupplierUsageState != UsageUnknown {
		t.Fatalf("unexpected settled snapshot: %+v", snapshot)
	}
}

func TestSessionReleasePendingDisconnectAndNoReconnect(t *testing.T) {
	s, at := testSession(t, SessionLimits{})
	turn, err := s.BeginTurn(BeginTurnInput{TurnID: "turn-1", RequestID: "req-1", ReservationID: "res-1", AttemptID: "attempt-1"}, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelTurn(turn.ID, "client_cancel", at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().Turns[0].State; got != TurnReleased {
		t.Fatalf("unforwarded cancellation ended in %s", got)
	}

	turn, err = s.BeginTurn(BeginTurnInput{TurnID: "turn-2", RequestID: "req-2", ReservationID: "res-2", AttemptID: "attempt-2"}, at.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := NewEventParser(EventLimits{})
	input := parseEvent(t, p, `{"event_id":"input-2","type":"input.text","data":{"text":"hello"}}`)
	if _, err := s.AcceptEvent(input, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.StartSending(turn.ID, at.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkForwarded(turn.ID, 1, at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	closeResult, err := s.Disconnect("client_disconnect", at.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !closeResult.CancelUpstream || closeResult.TurnState != TurnPending || s.Snapshot().State != SessionClosed {
		t.Fatalf("disconnect did not preserve in-flight uncertainty: %+v / %+v", closeResult, s.Snapshot())
	}
	if _, err := s.BeginTurn(BeginTurnInput{TurnID: "turn-3", RequestID: "req-3", ReservationID: "res-3", AttemptID: "attempt-3"}, at.Add(8*time.Second)); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session accepted a reconnect turn: %v", err)
	}
	closedInput := parseEvent(t, p, `{"event_id":"input-after-close","type":"input.text","data":{"text":"after"}}`)
	if _, err := s.AcceptEvent(closedInput, at.Add(8*time.Second)); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session accepted an event: %v", err)
	}
}

func TestSessionBackpressureAndDeadlineBounds(t *testing.T) {
	limits := SessionLimits{
		MaxFrameBytes: 8, MaxQueueEvents: 1, MaxQueueBytes: 8, MaxForwardedBytes: 8,
		MaxTurns: 2, SessionTTL: time.Minute, TurnTTL: 10 * time.Second,
	}
	s, at := testSession(t, limits)
	if disposition, err := s.EnqueueOutbound(OutboundEvent{ID: "out-1", Type: "delta", Bytes: 8}, at.Add(time.Second)); err != nil || disposition != QueueAccepted {
		t.Fatalf("enqueue: %s, %v", disposition, err)
	}
	if disposition, err := s.EnqueueOutbound(OutboundEvent{ID: "out-1", Type: "delta", Bytes: 8}, at.Add(time.Second)); err != nil || disposition != QueueDuplicate {
		t.Fatalf("duplicate enqueue: %s, %v", disposition, err)
	}
	if _, err := s.EnqueueOutbound(OutboundEvent{ID: "out-2", Type: "delta", Bytes: 1}, at.Add(2*time.Second)); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("queue overflow was accepted: %v", err)
	}
	if s.Snapshot().State != SessionDraining {
		t.Fatalf("queue overflow did not enter draining: %s", s.Snapshot().State)
	}
	sDraining, atDraining := testSession(t, limits)
	reserved, err := sDraining.BeginTurn(BeginTurnInput{TurnID: "turn-drain", RequestID: "req-drain", ReservationID: "res-drain", AttemptID: "attempt-drain"}, atDraining.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := sDraining.BeginDrain("operator_stop", atDraining.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := sDraining.StartSending(reserved.ID, atDraining.Add(3*time.Second)); !errors.Is(err, ErrSessionDraining) {
		t.Fatalf("draining session allowed new upstream send: %v", err)
	}

	s2, at2 := testSession(t, limits)
	turn, err := s2.BeginTurn(BeginTurnInput{TurnID: "turn-1", RequestID: "req-1", ReservationID: "res-1", AttemptID: "attempt-1"}, at2.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := NewEventParser(EventLimits{})
	input := parseEvent(t, p, `{"event_id":"input-1","type":"input.text","data":{"text":"x"}}`)
	if _, err := s2.AcceptEvent(input, at2.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s2.StartSending(turn.ID, at2.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s2.MarkForwarded(turn.ID, 9, at2.Add(4*time.Second)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("forward overflow was accepted: %v", err)
	}
	if s2.Snapshot().State != SessionDraining || s2.Snapshot().ForwardedBytes != 0 {
		t.Fatalf("forward overflow mutated accounting: %+v", s2.Snapshot())
	}

	s3, at3 := testSession(t, limits)
	turn, err = s3.BeginTurn(BeginTurnInput{TurnID: "turn-1", RequestID: "req-1", ReservationID: "res-1", AttemptID: "attempt-1"}, at3.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := s3.EnforceDeadlines(at3.Add(12 * time.Second))
	if err != nil || !deadline.Released || deadline.TurnState != TurnReleased {
		t.Fatalf("reserved deadline: %+v, %v", deadline, err)
	}

	if _, err := s3.DequeueOutbound(at3.Add(13 * time.Second)); !errors.Is(err, ErrQueueEmpty) {
		t.Fatalf("unexpected outbound queue result: %v", err)
	}
}

func TestTicketIsBoundAndSingleUse(t *testing.T) {
	binding := TicketBinding{
		SessionID: "session-1", TenantID: "tenant-1", ActorID: "actor-1", APIKeyID: "key-1",
		ModelID: "model-1", ProfileID: "profile-1", RouteID: "route-1", Origin: "HTTPS://Console.Example:443",
	}
	book := NewTicketBook()
	at := time.Date(2026, 11, 8, 3, 0, 0, 0, time.UTC)
	issued, err := book.Issue(binding, at, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Claims.Binding.Origin != "https://console.example:443" || issued.Claims.ExpiresAt != at.Add(time.Minute) {
		t.Fatalf("ticket claims were not canonical: %+v", issued.Claims)
	}
	wrong := binding
	wrong.Origin = "https://other.example"
	if _, err := book.Redeem(issued.Token, wrong, at.Add(time.Second)); !errors.Is(err, ErrTicketBinding) {
		t.Fatalf("cross-origin ticket was accepted: %v", err)
	}
	claims, err := book.Redeem(issued.Token, binding, at.Add(2*time.Second))
	if err != nil || claims.TicketID == "" {
		t.Fatalf("ticket redeem: %+v, %v", claims, err)
	}
	if _, err := book.Redeem(issued.Token, binding, at.Add(3*time.Second)); !errors.Is(err, ErrTicketUsed) {
		t.Fatalf("ticket replay was accepted: %v", err)
	}

	expired, err := book.Issue(binding, at, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.Redeem(expired.Token, binding, at.Add(time.Second)); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired ticket result: %v", err)
	}
	if _, err := book.Redeem(expired.Token, binding, at.Add(time.Second)); !errors.Is(err, ErrTicketUsed) {
		t.Fatalf("expired ticket was not consumed: %v", err)
	}
	if _, err := book.Issue(binding, at, MaxTicketTTL+time.Nanosecond); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("long-lived ticket was accepted: %v", err)
	}
	for _, origin := range []string{"*", "null", "https://example.test/path", "ftp://example.test", "https://example.test/"} {
		if _, err := CanonicalOrigin(origin); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid origin %q accepted: %v", origin, err)
		}
	}
}

func TestConcurrentIdempotentHeartbeatDoesNotCreateTurns(t *testing.T) {
	s, at := testSession(t, SessionLimits{})
	p, err := NewEventParser(EventLimits{})
	if err != nil {
		t.Fatal(err)
	}
	event := parseEvent(t, p, `{"event_id":"hb-concurrent","type":"heartbeat","data":{}}`)
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.AcceptEvent(event, at.Add(time.Second))
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if snapshot := s.Snapshot(); snapshot.TurnCount != 0 || len(snapshot.Turns) != 0 {
		t.Fatalf("concurrent heartbeat created a turn: %+v", snapshot)
	}
}
