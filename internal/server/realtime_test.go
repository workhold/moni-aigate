package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/realtime"
	"golang.org/x/net/websocket"
)

type realtimeTestEvent struct {
	EventID string          `json:"event_id"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
}

func newRealtimeTestServer(t *testing.T, origin string, timeout time.Duration) (*httptest.Server, *provider.MockRealtimeAdapter) {
	t.Helper()
	adapter := provider.NewMockRealtimeAdapter()
	service, err := realtime.NewService(realtime.ServiceOptions{Store: realtime.NewMemoryStore(), Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Config:          config.Config{Origin: origin},
		Realtime:        service,
		RealtimeAdapter: adapter,
		RealtimeTimeout: timeout,
	}
	r := gin.New()
	r.GET("/v1/realtime", func(c *gin.Context) {
		c.Set("principal", identity.Principal{TenantID: "tenant-1", UserID: "user-1", KeyID: "key-1"})
		c.Set("realtime_model", catalog.Model{ID: "model-1", Name: "mock-realtime", ProfileVersion: "profile-1", Revision: 1})
		s.realtime(c)
	})
	return httptest.NewServer(r), adapter
}

func dialRealtime(t *testing.T, httpURL, origin string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(httpURL, "http") + "/v1/realtime"
	config, err := websocket.NewConfig(wsURL, origin)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := websocket.DialConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func readRealtimeEvent(t *testing.T, conn *websocket.Conn) realtimeTestEvent {
	t.Helper()
	var raw string
	if err := websocket.Message.Receive(conn, &raw); err != nil {
		t.Fatal(err)
	}
	var event realtimeTestEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func sendRealtimeEvent(t *testing.T, conn *websocket.Conn, raw string) {
	t.Helper()
	if err := websocket.Message.Send(conn, raw); err != nil {
		t.Fatal(err)
	}
}

func eventDataString(t *testing.T, event realtimeTestEvent, key string) string {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	value, ok := data[key].(string)
	if !ok {
		t.Fatalf("event %s data missing %s: %s", event.Type, key, event.Data)
	}
	return value
}

func TestRealtimeWebSocketAuthenticatesOriginAndRunsOneTurn(t *testing.T) {
	server, adapter := newRealtimeTestServer(t, "https://console.example", time.Second)
	defer server.Close()
	conn := dialRealtime(t, server.URL, "https://console.example")
	defer conn.Close()

	if got := readRealtimeEvent(t, conn).Type; got != "session.created" {
		t.Fatalf("session event type = %q", got)
	}
	input := `{"event_id":"input-1","type":"input.text","data":{"text":"hello"}}`
	sendRealtimeEvent(t, conn, input)
	if got := readRealtimeEvent(t, conn).Type; got != "event.accepted" {
		t.Fatalf("input event type = %q", got)
	}
	sendRealtimeEvent(t, conn, `{"event_id":"create-1","type":"response.create","data":{}}`)
	if got := readRealtimeEvent(t, conn).Type; got != "event.accepted" {
		t.Fatalf("create event type = %q", got)
	}
	if got := readRealtimeEvent(t, conn).Type; got != "response.created" {
		t.Fatalf("created event type = %q", got)
	}
	if got := readRealtimeEvent(t, conn).Type; got != "response.delta" {
		t.Fatalf("delta event type = %q", got)
	}
	pending := readRealtimeEvent(t, conn)
	if pending.Type != "response.pending" || eventDataString(t, pending, "reason") != "settlement_unwired" {
		t.Fatalf("pending event = %+v", pending)
	}

	// Replaying the same event is acknowledged without a second provider send.
	sendRealtimeEvent(t, conn, `{"event_id":"create-1","type":"response.create","data":{}}`)
	if got := readRealtimeEvent(t, conn).Type; got != "event.duplicate" {
		t.Fatalf("duplicate event type = %q", got)
	}
	if got := len(adapter.SendCalls()); got != 1 {
		t.Fatalf("provider send count = %d", got)
	}

	// A distinct charge-bearing event cannot start a second turn.
	sendRealtimeEvent(t, conn, `{"event_id":"input-2","type":"input.text","data":{"text":"again"}}`)
	if got := readRealtimeEvent(t, conn).Type; got != "error" {
		t.Fatalf("second turn event type = %q", got)
	}
	deadline := time.Now().Add(time.Second)
	for len(adapter.CancelCalls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(adapter.CancelCalls()); got != 1 {
		t.Fatalf("disconnect cancel count = %d", got)
	}
}

func TestRealtimeWebSocketRejectsConflictingEventIdentity(t *testing.T) {
	server, _ := newRealtimeTestServer(t, "https://console.example", time.Second)
	defer server.Close()
	conn := dialRealtime(t, server.URL, "https://console.example")
	defer conn.Close()
	_ = readRealtimeEvent(t, conn)
	sendRealtimeEvent(t, conn, `{"event_id":"same-1","type":"input.text","data":{"text":"one"}}`)
	if got := readRealtimeEvent(t, conn).Type; got != "event.accepted" {
		t.Fatalf("first event type = %q", got)
	}
	sendRealtimeEvent(t, conn, `{"event_id":"same-1","type":"input.text","data":{"text":"two"}}`)
	conflict := readRealtimeEvent(t, conn)
	if conflict.Type != "error" || eventDataString(t, conflict, "code") != "event_conflict" {
		t.Fatalf("conflict event = %+v", conflict)
	}
}

func TestRealtimeWebSocketRejectsCrossOriginAndTimesOut(t *testing.T) {
	server, _ := newRealtimeTestServer(t, "https://console.example", 30*time.Millisecond)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime"
	badConfig, err := websocket.NewConfig(wsURL, "https://evil.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = websocket.DialConfig(badConfig); err == nil {
		t.Fatal("cross-origin websocket was accepted")
	}

	conn := dialRealtime(t, server.URL, "https://console.example")
	defer conn.Close()
	if got := readRealtimeEvent(t, conn).Type; got != "session.created" {
		t.Fatalf("session event type = %q", got)
	}
	deadline := time.Now().Add(time.Second)
	if err = conn.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = websocket.Message.Receive(conn, &raw); err != nil {
		t.Fatal("timeout error frame was not sent:", err)
	}
	var timeoutEvent realtimeTestEvent
	if err = json.Unmarshal([]byte(raw), &timeoutEvent); err != nil || timeoutEvent.Type != "error" || eventDataString(t, timeoutEvent, "code") != "timeout" {
		t.Fatalf("timeout event = %s", raw)
	}
	if err = websocket.Message.Receive(conn, &raw); err == nil {
		t.Fatal("idle websocket did not time out")
	}
}

func TestRealtimeWebSocketCancelAndMalformedEventCloseWithoutDispatch(t *testing.T) {
	server, adapter := newRealtimeTestServer(t, "https://console.example", time.Second)
	defer server.Close()
	conn := dialRealtime(t, server.URL, "https://console.example")
	defer conn.Close()
	if got := readRealtimeEvent(t, conn).Type; got != "session.created" {
		t.Fatalf("session event type = %q", got)
	}
	sendRealtimeEvent(t, conn, `{"event_id":"cancel-1","type":"response.cancel","data":{}}`)
	if got := readRealtimeEvent(t, conn).Type; got != "event.accepted" {
		t.Fatalf("cancel event type = %q", got)
	}
	var raw string
	if err := websocket.Message.Receive(conn, &raw); err == nil {
		t.Fatal("cancelled connection stayed open")
	}
	if got := len(adapter.SendCalls()); got != 0 {
		t.Fatalf("provider send count = %d", got)
	}

	server, _ = newRealtimeTestServer(t, "https://console.example", time.Second)
	defer server.Close()
	conn = dialRealtime(t, server.URL, "https://console.example")
	defer conn.Close()
	if got := readRealtimeEvent(t, conn).Type; got != "session.created" {
		t.Fatalf("session event type = %q", got)
	}
	sendRealtimeEvent(t, conn, `{"event_id":"bad-1","type":"input.commit","data":{}}`)
	bad := readRealtimeEvent(t, conn)
	if bad.Type != "error" || eventDataString(t, bad, "code") != "invalid_event" {
		t.Fatalf("malformed event = %+v", bad)
	}
	if err := websocket.Message.Receive(conn, &raw); err == nil {
		t.Fatal("malformed connection stayed open")
	}
}
