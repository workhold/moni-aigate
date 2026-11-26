package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

const attributionSecondarySecret = "pool-secondary-secret"

// poolChannel wires the fixture's single channel to a two-credential pool with
// MaxInflight 1 per member and republishes the route, so concurrent requests
// deterministically land on different credentials while both hold inflight.
func poolChannel(t *testing.T, f *gatewayFixture) string {
	t.Helper()
	ctx := context.Background()
	var channelID, version string
	if err := f.pool.QueryRow(ctx, `SELECT v.channel_id,v.id FROM channel_versions v JOIN model_bindings b ON b.channel_version_id=v.id JOIN route_policy_versions p ON p.id=b.route_policy_version_id WHERE p.model_id=$1 AND p.revision=1`, f.model).Scan(&channelID, &version); err != nil {
		t.Fatal(err)
	}
	view, err := f.server.Catalog.CredentialPool(ctx, channelID, "")
	if err != nil || len(view.Members) == 0 {
		t.Fatalf("pool view: %+v err=%v", view, err)
	}
	if _, err = f.server.Catalog.UpdateCredentialPool(ctx, "route-test", channelID, catalog.CredentialPoolInput{ExpectedVersion: version, Members: []catalog.CredentialPoolMemberInput{
		{CredentialID: view.Members[0].CredentialID, Label: "primary", Weight: 1, MaxInflight: 1},
		{Secret: attributionSecondarySecret, Label: "secondary", Weight: 1, MaxInflight: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	var binding catalog.BindingInput
	if err = f.pool.QueryRow(ctx, `SELECT b.channel_id,b.upstream_model,b.priority,b.weight,b.max_inflight FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id WHERE p.model_id=$1 AND p.revision=1`, f.model).Scan(&binding.ChannelID, &binding.UpstreamModel, &binding.Priority, &binding.Weight, &binding.MaxInflight); err != nil {
		t.Fatal(err)
	}
	revision, err := f.server.Catalog.PublishWithDataPlanePolicy(ctx, "route-test", f.model, 1, []catalog.BindingInput{binding}, routing.DefaultCostPolicy(), routing.DefaultBreakerConfig(), 0)
	if err != nil || revision != 2 {
		t.Fatalf("pool route revision=%d err=%v", revision, err)
	}
	return channelID
}

func attributionKey(channelID string) string {
	return channelID + ":provider-model"
}

// fireRequest is the goroutine-safe request helper; t.Fatal is never called
// outside the test goroutine.
func fireRequest(t *testing.T, f *gatewayFixture, ctx context.Context) (int, []byte, string) {
	t.Helper()
	body := map[string]any{"model": "retail-model", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 20}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Errorf("marshal: %v", err)
		return 0, nil, ""
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		t.Errorf("request: %v", err)
		return 0, nil, ""
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.token)
	response, err := f.http.Client().Do(r)
	if err != nil {
		t.Errorf("do: %v", err)
		return 0, nil, ""
	}
	defer response.Body.Close()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.Errorf("read: %v", err)
		return response.StatusCode, nil, ""
	}
	return response.StatusCode, b, response.Header.Get("X-Request-ID")
}

func TestGatewayOutcomeMapsOverloadedWithoutExternalChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	overloaded := &provider.Error{Code: "upstream_rejected", HTTPStatus: 502, Class: provider.ClassOverloaded, Consumption: provider.ConsumptionUnknown, UpstreamStatus: 529}
	if got := outcome(ctx, overloaded); got != routing.Overloaded {
		t.Fatalf("529 must map to the overloaded outcome, got %s", got)
	}
	if detail := failureDetail(overloaded); detail.Class != provider.ClassOverloaded || detail.UpstreamStatus != 529 {
		t.Fatalf("failure detail lost machine signals: %+v", detail)
	}
	plain := &provider.Error{Code: "upstream_rejected", HTTPStatus: 502, Class: provider.ClassUpstream, Consumption: provider.ConsumptionUnknown, UpstreamStatus: 500}
	if got := outcome(ctx, plain); got != routing.Failure {
		t.Fatalf("bare 5xx must keep counting, got %s", got)
	}
	if got := outcome(ctx, &provider.Error{Code: "x", HTTPStatus: 429, Class: provider.ClassRateLimit, Consumption: provider.ConsumptionUnknown, UpstreamStatus: 429}); got != routing.Neutral {
		t.Fatalf("429 must stay neutral, got %s", got)
	}
}

func TestPostgresAttributionWithdrawsPairedUpstreamRejections(t *testing.T) {
	var index, pending atomic.Int32
	gate := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		switch i := index.Add(1); i {
		case 1, 4:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, gatewayJSON)
		case 2, 3:
			// Hold both concurrent rejections in flight so each occupies a
			// different pool member (MaxInflight 1) before answering.
			if n := pending.Add(1); n == 2 {
				close(gate)
			} else {
				select {
				case <-gate:
				case <-time.After(5 * time.Second):
					t.Error("paired rejection barrier timed out")
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			_, _ = io.WriteString(w, `{"error":{"message":"secret-canary"}}`)
		default:
			t.Errorf("unexpected request #%d", i)
			w.WriteHeader(500)
		}
	}
	f := newGatewayFixture(t, []http.HandlerFunc{handler}, nil)
	key := attributionKey(poolChannel(t, f))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	status, _, _ := fireRequest(t, f, ctx)
	if status != 200 {
		t.Fatalf("warmup success failed: %d", status)
	}
	type result struct {
		status int
		body   []byte
		id     string
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for k := range results {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			s, b, id := fireRequest(t, f, ctx)
			results[slot] = result{s, b, id}
		}(k)
	}
	wg.Wait()
	for _, r := range results {
		if r.status != 502 || !strings.Contains(string(r.body), "upstream_rejected") || strings.Contains(string(r.body), "secret-canary") {
			t.Fatalf("paired rejection response mismatch: %d %s", r.status, r.body)
		}
		v := f.state(t, r.id)
		if v.billing != "released" || v.settlements != 1 || v.charge != 0 || v.attempts != 1 {
			t.Fatalf("rejected request must release with one attempt: %+v", v)
		}
	}
	// The identical pair from two credentials with recent success is the
	// request's fault: no ladder, no open, route still usable.
	if got := f.server.Routing.Snapshot(key, strings.SplitN(key, ":", 2)[0]); got.State != routing.Closed || got.ConsecutiveFailures != 0 {
		t.Fatalf("attribution must withdraw the paired count: %+v", got)
	}
	status, body, id := fireRequest(t, f, ctx)
	if status != 200 {
		t.Fatalf("route unusable after withdrawal: %d %s", status, body)
	}
	v := f.state(t, id)
	// Two successes so far (warmup + this one): wallet 960, spent 40.
	if v.billing != "settled" || v.reservation != "settled" || v.settlements != 1 || v.attempts != 1 || v.charge != 20 || v.available != 960 || v.spent != 40 || v.usage != "confirmed_final" {
		t.Fatalf("post-withdrawal success settled wrongly: %+v", v)
	}
	if f.posts.Load() != 4 {
		t.Fatalf("post count=%d want 4", f.posts.Load())
	}
}

func TestPostgresAttributionWithoutRecentSuccessStillOpens(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `{"error":{"message":"secret-canary"}}`)
	}
	f := newGatewayFixture(t, []http.HandlerFunc{handler}, nil)
	key := attributionKey(poolChannel(t, f))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		status, _, _ := fireRequest(t, f, ctx)
		if status != 502 {
			t.Fatalf("request %d status %d", i, status)
		}
	}
	channel := strings.SplitN(key, ":", 2)[0]
	if got := f.server.Routing.Snapshot(key, channel); got.State != routing.Open || got.ConsecutiveFailures != 3 {
		t.Fatalf("without recent success the ladder must still open: %+v", got)
	}
	status, body, _ := fireRequest(t, f, ctx)
	if status != 503 || !strings.Contains(string(body), "no_available_channel") {
		t.Fatalf("open circuit must reject: %d %s", status, body)
	}
	if f.posts.Load() != 3 {
		t.Fatalf("post count=%d want 3", f.posts.Load())
	}
}

func TestPostgresRateLimitCooldownIsCredentialScoped(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		if r.Header.Get("Authorization") == "Bearer local-fixture-secret" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, gatewayJSON)
	}
	f := newGatewayFixture(t, []http.HandlerFunc{handler}, nil)
	key := attributionKey(poolChannel(t, f))
	// Deterministic pool draws: roll 0 always picks the first (throttled)
	// member while it is eligible.
	f.server.Routing = routing.NewEngine(routing.Config{RandN: func(n uint64) uint64 { return 0 }})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	status, _, id := fireRequest(t, f, ctx)
	if status != 429 {
		t.Fatalf("throttled primary must surface 429, got %d", status)
	}
	v := f.state(t, id)
	if v.billing != "released" || v.attempts != 1 || v.charge != 0 {
		t.Fatalf("throttled request must release: %+v", v)
	}
	status, body, id := fireRequest(t, f, ctx)
	if status != 200 {
		t.Fatalf("sibling credential must stay usable: %d %s", status, body)
	}
	f.assertSettled(t, id, 1)
	channel := strings.SplitN(key, ":", 2)[0]
	if got := f.server.Routing.Snapshot(key, channel); got.State != routing.Closed {
		t.Fatalf("credential cooldown must not open the channel: %+v", got)
	}
	var revoked bool
	if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM channel_credentials k JOIN channel_version_credentials cp ON cp.credential_id=k.id WHERE cp.channel_version_id=(SELECT current_version FROM channels WHERE id=$1) AND k.revoked_at IS NOT NULL)", channel).Scan(&revoked); err != nil || revoked {
		t.Fatalf("429 must not revoke credentials: %v err=%v", revoked, err)
	}
	// With both members cooling the whole binding is excluded and the route
	// decision records the reason.
	var credIDs []string
	rows, err := f.pool.Query(ctx, "SELECT credential_id FROM channel_version_credentials WHERE channel_version_id=(SELECT current_version FROM channels WHERE id=$1) ORDER BY position", channel)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		credIDs = append(credIDs, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(credIDs) != 2 {
		t.Fatalf("pool members %v err=%v", credIDs, err)
	}
	f.server.Routing.CredentialCooldown(channel, "provider-model", credIDs[1], time.Minute)
	status, _, id = fireRequest(t, f, ctx)
	if status != 503 {
		t.Fatalf("fully cooled binding must reject: %d", status)
	}
	var raw []byte
	if err := f.pool.QueryRow(ctx, "SELECT candidates FROM route_decisions WHERE request_id=$1", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var decisions []routing.CandidateDecision
	if json.Unmarshal(raw, &decisions) != nil || len(decisions) == 0 || decisions[0].FilterReason != "credential_cooldown" {
		t.Fatalf("route decision must expose credential_cooldown: %s", raw)
	}
	if strings.Contains(string(raw), "local-fixture-secret") || strings.Contains(string(raw), attributionSecondarySecret) {
		t.Fatal("route decision leaked a secret")
	}
}

func TestPostgresOverloadedRestsFixedWithoutLadder(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(529)
		_, _ = io.WriteString(w, `{"error":{"type":"overloaded_error"}}`)
	}
	f := newGatewayFixture(t, []http.HandlerFunc{handler}, nil)
	var channelID string
	if err := f.pool.QueryRow(context.Background(), `SELECT channel_id FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id WHERE p.model_id=$1 AND p.revision=1`, f.model).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	key := attributionKey(channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	status, body, id := fireRequest(t, f, ctx)
	if status != 502 || !strings.Contains(string(body), "upstream_rejected") {
		t.Fatalf("529 must keep the external mapping: %d %s", status, body)
	}
	v := f.state(t, id)
	if v.billing != "released" || v.attempts != 1 || v.charge != 0 || v.usage != "missing" {
		t.Fatalf("overloaded request must release unchanged: %+v", v)
	}
	got := f.server.Routing.Snapshot(key, channelID)
	if got.State != routing.Open || got.ConsecutiveFailures != 0 || !got.OpenUntil.After(time.Now().Add(25*time.Second)) {
		t.Fatalf("overloaded rest must be fixed and ladder-free: %+v", got)
	}
	status, _, id = fireRequest(t, f, ctx)
	if status != 503 {
		t.Fatalf("resting channel must reject: %d", status)
	}
	v = f.state(t, id)
	if v.billing != "released" || v.attempts != 0 {
		t.Fatalf("blocked request must not consume attempts: %+v", v)
	}
	if f.posts.Load() != 1 {
		t.Fatalf("post count=%d want 1", f.posts.Load())
	}
	var revoked bool
	if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM channel_credentials WHERE revoked_at IS NOT NULL)").Scan(&revoked); err != nil || revoked {
		t.Fatalf("overloaded must not revoke credentials: %v err=%v", revoked, err)
	}
}
