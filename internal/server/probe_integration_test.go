package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

type probeHTTPResult struct {
	status int
	body   []byte
	err    error
}

const probeJSON = `{"id":"probe-result","object":"chat.completion","model":"provider-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`

func probeAdmin(t *testing.T, f *gatewayFixture) (string, string) {
	t.Helper()
	id := uuid.NewString()
	authExec(t, f.pool, "INSERT INTO users(id,email,password_hash,platform_role,mfa_enabled) VALUES($1,$2,'unused','admin',true)", id, id+"@probe.invalid")
	return authSession(t, f.server, f.pool, id, "", true, true)
}

func probeCall(ctx context.Context, f *gatewayFixture, channel, raw, csrf string) probeHTTPResult {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+"/api/platform/channels/"+channel+"/probe", nil)
	if err != nil {
		return probeHTTPResult{err: err}
	}
	r.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
	r.Header.Set("Origin", f.server.Config.Origin)
	r.Header.Set("X-CSRF-Token", csrf)
	response, err := f.http.Client().Do(r)
	if err != nil {
		return probeHTTPResult{err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return probeHTTPResult{response.StatusCode, body, err}
}

func probeChannels(t *testing.T, f *gatewayFixture) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), "SELECT id FROM channels ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		result = append(result, id)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	return result
}

func probeSetCapacity(t *testing.T, f *gatewayFixture, channel string, capacity int) {
	t.Helper()
	var revision int64
	if err := f.pool.QueryRow(context.Background(), "SELECT active_revision FROM models WHERE id=$1", f.model).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.Catalog.Publish(context.Background(), "probe-test", f.model, revision, []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: capacity}}); err != nil {
		t.Fatal(err)
	}
}

func probeAssertWalletUntouched(t *testing.T, f *gatewayFixture) {
	t.Helper()
	w, err := f.server.Billing.Wallet(context.Background(), billing.Query{TenantID: f.tenant})
	if err != nil || w.Available != 1000 || w.Held != 0 {
		t.Fatalf("platform probe affected wallet: %+v %v", w, err)
	}
	var spent, held, records int64
	if err = f.pool.QueryRow(context.Background(), "SELECT spent,held,(SELECT count(*) FROM request_records) FROM api_keys WHERE id=$1", f.key).Scan(&spent, &held, &records); err != nil || spent != 0 || held != 0 || records != 0 {
		t.Fatalf("platform probe created customer money state: spent=%d held=%d requests=%d err=%v", spent, held, records, err)
	}
}

func probeWaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not enter")
	}
}

func TestPostgresProbeUsesPublishedVersionAndPlatformAudit(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeJSON)
	}}, nil)
	channel := probeChannels(t, f)[0]
	raw, csrf := probeAdmin(t, f)
	var publishedVersion string
	if err := f.pool.QueryRow(context.Background(), "SELECT current_version FROM channels WHERE id=$1", channel).Scan(&publishedVersion); err != nil {
		t.Fatal(err)
	}
	var unpublishedCalls atomic.Int32
	unpublished := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unpublishedCalls.Add(1)
		w.WriteHeader(500)
	}))
	defer unpublished.Close()
	if _, err := f.server.Catalog.SaveChannel(context.Background(), "probe-test", channel, catalog.ChannelInput{Name: "unpublished version", BaseURL: unpublished.URL + "/v1", Secret: "unpublished-secret", Region: "global", AllowedCIDRs: []string{"127.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := probeCall(ctx, f, channel, raw, csrf)
	if result.err != nil || result.status != 200 || !strings.Contains(string(result.body), `"ok":true`) || f.posts.Load() != 1 || unpublishedCalls.Load() != 0 {
		t.Fatalf("probe mixed unpublished endpoint: %+v posts=%d unpublished=%d", result, f.posts.Load(), unpublishedCalls.Load())
	}
	var version, status, usage string
	var audits int
	err := f.pool.QueryRow(ctx, `SELECT p.channel_version_id,p.status,p.usage_state,(SELECT count(*) FROM audit_events a WHERE a.action='channel.probe' AND a.target=p.id) FROM platform_probe_attempts p`).Scan(&version, &status, &usage, &audits)
	if err != nil || version != publishedVersion || status != "completed" || usage != "confirmed_final" || audits != 1 || len(f.server.global) != 0 || len(f.server.probes) != 0 {
		t.Fatalf("probe audit/version/resources: %s %s %s audit=%d err=%v", version, status, usage, audits, err)
	}
	probeAssertWalletUntouched(t, f)
}

func TestPostgresCustomerCapacityBlocksProbe(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeJSON)
	}}, nil)
	channel := probeChannels(t, f)[0]
	probeSetCapacity(t, f, channel, 1)
	raw, csrf := probeAdmin(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	customer := make(chan probeHTTPResult, 1)
	go func() {
		r := f.request(t, ctx, false, false)
		b, e := io.ReadAll(r.Body)
		customer <- probeHTTPResult{r.StatusCode, b, e}
	}()
	probeWaitSignal(t, started)
	result := probeCall(ctx, f, channel, raw, csrf)
	if result.err != nil || result.status != 503 || !strings.Contains(string(result.body), "probe_channel_unavailable") || f.posts.Load() != 1 || len(f.server.global) != 1 || len(f.server.probes) != 0 {
		t.Fatalf("probe bypassed customer capacity: %+v posts=%d global=%d probes=%d", result, f.posts.Load(), len(f.server.global), len(f.server.probes))
	}
	var probes int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM platform_probe_attempts").Scan(&probes); err != nil || probes != 0 {
		t.Fatalf("blocked probe was marked dispatched: count=%d err=%v", probes, err)
	}
	close(release)
	result = <-customer
	if result.err != nil || result.status != 200 {
		t.Fatalf("customer completion: %+v", result)
	}
}

func TestPostgresProbeCapacityBlocksCustomer(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeJSON)
	}}, nil)
	channel := probeChannels(t, f)[0]
	probeSetCapacity(t, f, channel, 1)
	raw, csrf := probeAdmin(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := make(chan probeHTTPResult, 1)
	go func() { probe <- probeCall(ctx, f, channel, raw, csrf) }()
	probeWaitSignal(t, started)
	r := f.request(t, ctx, false, false)
	body, err := io.ReadAll(r.Body)
	if err != nil || r.StatusCode != 503 || f.posts.Load() != 1 || len(f.server.global) != 1 || len(f.server.probes) != 1 {
		t.Fatalf("customer bypassed probe lease: HTTP%d %s err=%v posts=%d", r.StatusCode, body, err, f.posts.Load())
	}
	close(release)
	result := <-probe
	if result.err != nil || result.status != 200 || len(f.server.global) != 0 || len(f.server.probes) != 0 {
		t.Fatalf("probe completion/resources: %+v", result)
	}
	wallet, err := f.server.Billing.Wallet(ctx, billing.Query{TenantID: f.tenant})
	if err != nil || wallet.Available != 1000 || wallet.Held != 0 {
		t.Fatalf("failed customer should release, platform probe should not charge: %+v %v", wallet, err)
	}
}

func TestPostgresProbeGlobalBudgetAcrossChannels(t *testing.T) {
	started, release := make(chan struct{}, 4), make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeJSON)
	}
	f := newGatewayFixture(t, []http.HandlerFunc{handler, handler, handler, handler, handler}, nil)
	channels := probeChannels(t, f)
	raw, csrf := probeAdmin(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan probeHTTPResult, 4)
	for _, channel := range channels[:4] {
		go func(id string) { results <- probeCall(ctx, f, id, raw, csrf) }(channel)
	}
	for range 4 {
		probeWaitSignal(t, started)
	}
	denied := probeCall(ctx, f, channels[4], raw, csrf)
	if denied.err != nil || denied.status != 429 || !strings.Contains(string(denied.body), "probe_capacity_limited") || f.posts.Load() != 4 || len(f.server.global) != 4 || len(f.server.probes) != 4 {
		t.Fatalf("probe global budget exceeded: %+v posts=%d global=%d probes=%d", denied, f.posts.Load(), len(f.server.global), len(f.server.probes))
	}
	close(release)
	for range 4 {
		r := <-results
		if r.err != nil || r.status != 200 {
			t.Fatalf("probe failed: %+v", r)
		}
	}
	if len(f.server.global) != 0 || len(f.server.probes) != 0 {
		t.Fatal("probe permits leaked")
	}
	probeAssertWalletUntouched(t, f)
}

func TestPostgresProbeSharesGlobalGenerationLimit(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	channel := probeChannels(t, f)[0]
	raw, csrf := probeAdmin(t, f)
	for range cap(f.server.global) {
		f.server.global <- struct{}{}
	}
	defer func() {
		for range cap(f.server.global) {
			<-f.server.global
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := probeCall(ctx, f, channel, raw, csrf)
	if result.err != nil || result.status != 429 || f.posts.Load() != 0 || len(f.server.probes) != 0 {
		t.Fatalf("probe ignored shared global budget or leaked its own permit: %+v", result)
	}
	probeAssertWalletUntouched(t, f)
}

func TestPostgresProbeLeadershipRecheckedBeforeNetwork(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	channel := probeChannels(t, f)[0]
	raw, csrf := probeAdmin(t, f)
	var calls atomic.Int32
	f.server.Guard = func(context.Context) error {
		if calls.Add(1) > 1 {
			return errors.New("test leadership lost")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := probeCall(ctx, f, channel, raw, csrf)
	var failed int
	err := f.pool.QueryRow(ctx, "SELECT count(*) FROM platform_probe_attempts WHERE status='failed'").Scan(&failed)
	if result.err != nil || result.status != 503 || calls.Load() != 2 || f.posts.Load() != 0 || err != nil || failed != 1 || len(f.server.global) != 0 || len(f.server.probes) != 0 {
		t.Fatalf("lost leadership dispatched or leaked: %+v calls=%d posts=%d failed=%d err=%v", result, calls.Load(), f.posts.Load(), failed, err)
	}
	probeAssertWalletUntouched(t, f)
}

func TestPostgresProbeCancellationClosesSocketAndReleasesSharedLease(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(closed)
		case <-time.After(5 * time.Second):
			t.Error("cancelled probe kept upstream socket alive")
		}
	}}, nil)
	channel := probeChannels(t, f)[0]
	raw, csrf := probeAdmin(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan probeHTTPResult, 1)
	go func() { results <- probeCall(ctx, f, channel, raw, csrf) }()
	probeWaitSignal(t, started)
	cancel()
	result := <-results
	if result.err == nil {
		t.Fatal("cancelled caller unexpectedly received a complete response")
	}
	probeWaitSignal(t, closed)
	var failed int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM platform_probe_attempts WHERE status='failed' AND usage_state='missing'").Scan(&failed); err != nil {
			t.Fatal(err)
		}
		if failed == 1 && len(f.server.global) == 0 && len(f.server.probes) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var credential string
	if err := f.pool.QueryRow(context.Background(), "SELECT credential_id FROM channel_versions WHERE channel_id=$1", channel).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	health := f.server.Routing.Snapshot(credential+":"+f.model, channel)
	if failed != 1 || f.posts.Load() != 1 || len(f.server.global) != 0 || len(f.server.probes) != 0 || health.Inflight != 0 || health.ConsecutiveFailures != 0 {
		t.Fatalf("cancelled probe leaked/retried/polluted health: failed=%d posts=%d global=%d probes=%d health=%+v", failed, f.posts.Load(), len(f.server.global), len(f.server.probes), health)
	}
	probeAssertWalletUntouched(t, f)
}
