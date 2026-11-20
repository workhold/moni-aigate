package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

func firstEventError(t *testing.T, code string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: error\ndata: {\"error\":{\"code\":\""+code+"\"}}\n\n")
	}
}

func TestGatewayPostgresTrustedFirstEventErrorFailsOverBeforeCommit(t *testing.T) {
	f := newGatewayFixtureProfileConfigured(t, []http.HandlerFunc{firstEventError(t, "trusted_rejection"), gatewayStream(t, true)}, nil, 8, provider.ProtocolChat, func(profile *provider.Profile) {
		profile.NoConsumptionEventCodes = []string{"trusted_rejection"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response := f.request(t, ctx, true, true)
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "hello") {
		t.Fatalf("first-event fallback HTTP %d body=%q err=%v", response.StatusCode, body, err)
	}
	f.assertSettled(t, response.Header.Get("X-Request-ID"), 2)
	if f.posts.Load() != 2 {
		t.Fatalf("trusted first event did not select exactly one fallback: posts=%d", f.posts.Load())
	}
}

func TestGatewayPostgresUnknownFirstEventErrorDoesNotReplay(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{firstEventError(t, "untrusted_error"), gatewayStream(t, true)}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response := f.request(t, ctx, true, true)
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode < 400 || strings.Contains(string(body), "hello") {
		t.Fatalf("unknown first event unexpectedly committed/failed over: HTTP %d body=%q err=%v", response.StatusCode, body, err)
	}
	state := f.state(t, response.Header.Get("X-Request-ID"))
	if state.attempts != 1 || f.posts.Load() != 1 || state.settlements != 1 || state.charge != 0 || state.reservation != "released" {
		t.Fatalf("unknown first event replay/charge mismatch: %+v posts=%d", state, f.posts.Load())
	}
}
