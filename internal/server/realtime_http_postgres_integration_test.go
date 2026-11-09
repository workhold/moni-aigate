package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/websocket"
)

func TestRealtimeHTTPPostgresOneSessionOneTurn(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)

	// The API-key middleware runs before the WebSocket upgrade. A request
	// without the fixture key must be rejected at the HTTP boundary.
	unauthorized, err := http.NewRequest(http.MethodGet, f.http.URL+"/v1/realtime?model=retail-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedResponse, err := f.http.Client().Do(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	defer unauthorizedResponse.Body.Close()
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(unauthorizedResponse.Body)
		t.Fatalf("unauthorized realtime status=%d body=%s", unauthorizedResponse.StatusCode, body)
	}

	wsURL := "ws" + strings.TrimPrefix(f.http.URL, "http") + "/v1/realtime?model=retail-model"
	wsConfig, err := websocket.NewConfig(wsURL, "http://console.test")
	if err != nil {
		t.Fatal(err)
	}
	wsConfig.Header = http.Header{"Authorization": []string{"Bearer " + f.token}}
	conn, err := websocket.DialConfig(wsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	created := readRealtimeEvent(t, conn)
	if created.Type != "session.created" {
		var sessions, turns, mediaRequests, mediaSettlements int
		if queryErr := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM realtime_sessions),(SELECT count(*) FROM realtime_turns),(SELECT count(*) FROM media_requests WHERE operation='realtime_turn'),(SELECT count(*) FROM media_settlements WHERE request_id IN (SELECT id FROM media_requests WHERE operation='realtime_turn'))`).Scan(&sessions, &turns, &mediaRequests, &mediaSettlements); queryErr != nil {
			t.Fatalf("session event=%+v, diagnostic query: %v", created, queryErr)
		}
		t.Fatalf("session event=%+v realtime sessions=%d turns=%d media requests=%d settlements=%d", created, sessions, turns, mediaRequests, mediaSettlements)
	}
	sessionID := eventDataString(t, created, "session_id")
	turnID := eventDataString(t, created, "turn_id")
	if _, err = uuid.Parse(sessionID); err != nil {
		t.Fatalf("session_id=%q is not a UUID: %v", sessionID, err)
	}
	if _, err = uuid.Parse(turnID); err != nil {
		t.Fatalf("turn_id=%q is not a UUID: %v", turnID, err)
	}

	sendRealtimeEvent(t, conn, `{"event_id":"11111111-1111-4111-8111-111111111101","type":"input.text","data":{"text":"hello"}}`)
	if accepted := readRealtimeEvent(t, conn); accepted.Type != "event.accepted" {
		t.Fatalf("input acknowledgement=%+v", accepted)
	}
	sendRealtimeEvent(t, conn, `{"event_id":"11111111-1111-4111-8111-111111111102","type":"response.create","data":{}}`)
	if accepted := readRealtimeEvent(t, conn); accepted.Type != "event.accepted" {
		t.Fatalf("create acknowledgement=%+v", accepted)
	}
	if createdResponse := readRealtimeEvent(t, conn); createdResponse.Type != "response.created" {
		t.Fatalf("response.created=%+v", createdResponse)
	}
	if delta := readRealtimeEvent(t, conn); delta.Type != "response.delta" {
		t.Fatalf("response.delta=%+v", delta)
	}
	if done := readRealtimeEvent(t, conn); done.Type != "response.done" {
		t.Fatalf("response.done=%+v", done)
	}

	// Replaying the same charge-bearing event is acknowledged from the durable
	// event identity and must not dispatch the mock provider again.
	sendRealtimeEvent(t, conn, `{"event_id":"11111111-1111-4111-8111-111111111102","type":"response.create","data":{}}`)
	if duplicate := readRealtimeEvent(t, conn); duplicate.Type != "event.duplicate" {
		t.Fatalf("duplicate acknowledgement=%+v", duplicate)
	}
	if got := len(f.server.RealtimeAdapter.SendCalls()); got != 1 {
		t.Fatalf("provider sends=%d want=1", got)
	}
	if got := len(f.server.RealtimeAdapter.ReceiveCalls()); got != 1 {
		t.Fatalf("provider receives=%d want=1", got)
	}

	ctx := t.Context()
	var realtimeState, turnState, providerSessionID, providerHandle string
	if err = f.pool.QueryRow(ctx, `SELECT state,provider_session_id FROM realtime_sessions WHERE tenant_id=$1 AND id=$2`, f.tenant, sessionID).Scan(&realtimeState, &providerSessionID); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT state,provider_handle FROM realtime_turns WHERE tenant_id=$1 AND session_id=$2 AND id=$3`, f.tenant, sessionID, turnID).Scan(&turnState, &providerHandle); err != nil {
		t.Fatal(err)
	}
	if realtimeState != "open" || turnState != "finalizing" || providerSessionID == "" || providerHandle == "" {
		t.Fatalf("durable realtime state session=%q turn=%q provider_session=%q provider_handle=%q", realtimeState, turnState, providerSessionID, providerHandle)
	}

	var mediaState, reservationState, attemptState, settlementStatus, usageState string
	var mediaRequests, mediaAttempts, mediaSettlements, mediaObservations int
	if err = f.pool.QueryRow(ctx, `SELECT q.state,r.status,a.delivery_state,s.status,o.usage_state,
 COUNT(DISTINCT q.id),COUNT(DISTINCT a.id),COUNT(DISTINCT s.id),COUNT(DISTINCT o.id)
 FROM media_requests q
 JOIN media_reservations r ON r.tenant_id=q.tenant_id AND r.request_id=q.id
 JOIN media_attempts a ON a.tenant_id=q.tenant_id AND a.request_id=q.id
 LEFT JOIN media_settlements s ON s.tenant_id=q.tenant_id AND s.request_id=q.id
 LEFT JOIN media_observations o ON o.tenant_id=q.tenant_id AND o.request_id=q.id
 WHERE q.tenant_id=$1 AND q.operation='realtime_turn'
 GROUP BY q.state,r.status,a.delivery_state,s.status,o.usage_state`, f.tenant).Scan(&mediaState, &reservationState, &attemptState, &settlementStatus, &usageState, &mediaRequests, &mediaAttempts, &mediaSettlements, &mediaObservations); err != nil {
		t.Fatal(err)
	}
	if mediaState != "settled" || reservationState != "settled" || attemptState != "completed" || settlementStatus != "settled" || usageState != "confirmed_final" || mediaRequests != 1 || mediaAttempts != 1 || mediaSettlements != 1 || mediaObservations != 1 {
		t.Fatalf("media settlement state request=%q reservation=%q attempt=%q settlement=%q usage=%q counts=%d/%d/%d/%d", mediaState, reservationState, attemptState, settlementStatus, usageState, mediaRequests, mediaAttempts, mediaSettlements, mediaObservations)
	}

}
