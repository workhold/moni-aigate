package server

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestPostgresQuotaIsolationPersistsWithoutChannelCircuitQuarantine(t *testing.T) {
	quota := func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"insufficient_quota"}}`)
	}
	f := newGatewayFixture(t, []http.HandlerFunc{quota, gatewaySuccess(t)}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response := f.request(t, ctx, false, false)
	_, _ = io.ReadAll(response.Body)
	state := f.state(t, response.Header.Get("X-Request-ID"))
	if state.attempts != 1 || state.reservation != "released" || f.posts.Load() != 1 {
		t.Fatalf("unknown quota consumption was replayed or charged: %+v", state)
	}
	var revoked, audits int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_credentials WHERE revoked_at IS NOT NULL),(SELECT count(*) FROM audit_events WHERE action='credential.quota_isolated')`).Scan(&revoked, &audits); err != nil || revoked != 1 || audits != 1 {
		t.Fatalf("quota was not persistently isolated: revoked=%d audits=%d err=%v", revoked, audits, err)
	}
	// Resetting volatile process health cannot reactivate the persisted credential.
	f.server.Routing = routing.NewEngine(routing.Config{})
	response = f.request(t, ctx, false, false)
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || f.posts.Load() != 2 {
		t.Fatalf("persisted quota isolation was bypassed after reset: HTTP %d body=%q posts=%d err=%v", response.StatusCode, body, f.posts.Load(), err)
	}
	f.assertSettled(t, response.Header.Get("X-Request-ID"), 1)
}
