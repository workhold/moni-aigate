package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

const probeSecondarySecret = "local-probe-secondary-secret"

func newProbePoolFixture(t *testing.T, connections int32, handler http.HandlerFunc) (*gatewayFixture, string, catalog.CredentialPoolView) {
	t.Helper()
	f := newGatewayFixtureWithPool(t, []http.HandlerFunc{gatewaySuccess(t)}, nil, connections)
	channel := probeChannels(t, f)[0]
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected pooled probe request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.posts.Add(1)
		handler(w, r)
	}))
	t.Cleanup(upstream.Close)
	ctx := context.Background()
	if _, err := f.server.Catalog.SaveChannel(ctx, "probe-pool-test", channel, catalog.ChannelInput{BaseURL: upstream.URL + "/v1", AllowedCIDRs: []string{"127.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	old, err := f.server.Catalog.CredentialPool(ctx, channel, "")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := f.server.Catalog.UpdateCredentialPool(ctx, "probe-pool-test", channel, catalog.CredentialPoolInput{ExpectedVersion: old.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{
		{CredentialID: old.Members[0].CredentialID, Label: "primary", Weight: 1, MaxInflight: 1},
		{Secret: probeSecondarySecret, Label: "secondary", Weight: 3, MaxInflight: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probeSetCapacity(t, f, channel, 4)
	return f, channel, pool
}

func assertProbePoolResult(t *testing.T, result probeHTTPResult, credential string, verified bool) {
	t.Helper()
	var body struct {
		Data struct {
			CredentialID string `json:"credential_id"`
			OK           bool   `json:"ok"`
			Message      string `json:"message"`
		} `json:"data"`
	}
	if result.err != nil || result.status != 200 || json.Unmarshal(result.body, &body) != nil || body.Data.CredentialID != credential || body.Data.OK != verified || !strings.Contains(body.Data.Message, "该凭据本次") {
		t.Fatalf("pooled probe response: HTTP%d %s err=%v", result.status, result.body, result.err)
	}
}

func TestPostgresProbePoolUsesFrozenSecondaryWithOneConnection(t *testing.T) {
	var secondaryCalls atomic.Int32
	f, channel, pool := newProbePoolFixture(t, 1, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Header.Get("Authorization") != "Bearer "+probeSecondarySecret {
			t.Error("probe did not use the selected secondary secret")
		}
		secondaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeJSON)
	})
	f.server.Routing = routing.NewEngine(routing.Config{RandN: func(n uint64) uint64 { return n - 1 }})
	var unpublishedCalls atomic.Int32
	unpublished := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unpublishedCalls.Add(1)
		w.WriteHeader(500)
	}))
	t.Cleanup(unpublished.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Both endpoint metadata and pool membership change without publishing.
	if _, err := f.server.Catalog.SaveChannel(ctx, "probe-pool-test", channel, catalog.ChannelInput{BaseURL: unpublished.URL + "/v1", AllowedCIDRs: []string{"127.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	current, err := f.server.Catalog.CredentialPool(ctx, channel, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Catalog.UpdateCredentialPool(ctx, "probe-pool-test", channel, catalog.CredentialPoolInput{ExpectedVersion: current.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{{CredentialID: pool.Members[0].CredentialID, Label: "current primary only", Weight: 1, MaxInflight: 1}}}); err != nil {
		t.Fatal(err)
	}
	raw, csrf := probeAdmin(t, f)
	result := probeCall(ctx, f, channel, raw, csrf)
	assertProbePoolResult(t, result, pool.Members[1].CredentialID, true)
	var version, credential, status string
	var audits int
	err = f.pool.QueryRow(ctx, `SELECT p.channel_version_id,p.credential_id,p.status,(SELECT count(*) FROM audit_events a WHERE a.action='channel.probe' AND a.target=p.id) FROM platform_probe_attempts p`).Scan(&version, &credential, &status, &audits)
	if err != nil || version != pool.ChannelVersionID || credential != pool.Members[1].CredentialID || status != "completed" || audits != 1 || secondaryCalls.Load() != 1 || unpublishedCalls.Load() != 0 {
		t.Fatalf("frozen pooled probe identity: version=%s credential=%s status=%s audit=%d calls=%d unpublished=%d err=%v", version, credential, status, audits, secondaryCalls.Load(), unpublishedCalls.Load(), err)
	}
	if f.server.Routing.CredentialInflight(credential) != 0 || len(f.server.global) != 0 || len(f.server.probes) != 0 {
		t.Fatal("pooled probe leaked capacity")
	}
	probeAssertWalletUntouched(t, f)
}

func TestPostgresProbePoolRevokedPrimaryUsesLiveSecondary(t *testing.T) {
	f, channel, pool := newProbePoolFixture(t, 2, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Header.Get("Authorization") != "Bearer "+probeSecondarySecret {
			t.Error("revoked primary was dispatched")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeJSON)
	})
	f.server.Routing = routing.NewEngine(routing.Config{RandN: func(uint64) uint64 { return 0 }})
	if err := f.server.Catalog.RevokeCredential(context.Background(), "probe-pool-test", channel, pool.Members[0].CredentialID); err != nil {
		t.Fatal(err)
	}
	raw, csrf := probeAdmin(t, f)
	result := probeCall(context.Background(), f, channel, raw, csrf)
	assertProbePoolResult(t, result, pool.Members[1].CredentialID, true)
	var credential string
	if err := f.pool.QueryRow(context.Background(), "SELECT credential_id FROM platform_probe_attempts").Scan(&credential); err != nil || credential != pool.Members[1].CredentialID || f.posts.Load() != 1 {
		t.Fatal("secondary probe identity missing", credential, f.posts.Load(), err)
	}
	probeAssertWalletUntouched(t, f)
}

func TestPostgresProbePoolPrimaryFailureDoesNotTryAnotherKey(t *testing.T) {
	f, channel, pool := newProbePoolFixture(t, 2, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Header.Get("Authorization") != "Bearer local-fixture-secret" {
			t.Error("probe switched to a secondary credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"local fixture rejected this credential"}}`)
	})
	f.server.Routing = routing.NewEngine(routing.Config{RandN: func(uint64) uint64 { return 0 }})
	raw, csrf := probeAdmin(t, f)
	result := probeCall(context.Background(), f, channel, raw, csrf)
	assertProbePoolResult(t, result, pool.Members[0].CredentialID, false)
	var count int
	var credential, status string
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) OVER(),credential_id,status FROM platform_probe_attempts`).Scan(&count, &credential, &status); err != nil || count != 1 || credential != pool.Members[0].CredentialID || status != "failed" || f.posts.Load() != 1 {
		t.Fatal("failed probe retried or lost identity", count, credential, status, f.posts.Load(), err)
	}
	if f.server.Routing.Snapshot(pool.Members[1].CredentialID+":"+f.model, channel).State != routing.Closed {
		t.Fatal("failed primary probe affected the secondary health")
	}
	probeAssertWalletUntouched(t, f)
}

func TestPostgresProbePoolSharesCredentialAndChannelCapacity(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f, channel, pool := newProbePoolFixture(t, 2, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Header.Get("Authorization") != "Bearer "+probeSecondarySecret {
			t.Error("probe bypassed the occupied primary credential")
		}
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeJSON)
	})
	f.server.Routing = routing.NewEngine(routing.Config{RandN: func(uint64) uint64 { return 0 }})
	primary := routing.Candidate{ChannelID: channel, VersionID: pool.ChannelVersionID, CredentialID: pool.Members[0].CredentialID, Model: "provider-model", Region: "global", HealthKey: pool.Members[0].CredentialID + ":" + f.model, Priority: 1, Weight: 1, MaxInflight: 4, CredentialWeight: 1, CredentialMaxInflight: 1, Enabled: true}
	plan, err := routing.NewPlan([]routing.Candidate{primary}, 1)
	if err != nil {
		t.Fatal(err)
	}
	occupied, err := plan.Start(context.Background(), f.server.Routing, func(context.Context, routing.Candidate, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Finish(routing.Neutral)
	raw, csrf := probeAdmin(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan probeHTTPResult, 1)
	go func() { results <- probeCall(ctx, f, channel, raw, csrf) }()
	probeWaitSignal(t, started)
	if f.server.Routing.CredentialInflight(primary.CredentialID) != 1 || f.server.Routing.CredentialInflight(pool.Members[1].CredentialID) != 1 || f.server.Routing.Snapshot(primary.HealthKey, channel).Inflight != 2 || len(f.server.global) != 1 || len(f.server.probes) != 1 {
		t.Error("probe did not hold both shared capacities and platform permits")
	}
	close(release)
	assertProbePoolResult(t, <-results, pool.Members[1].CredentialID, true)
	if f.server.Routing.CredentialInflight(primary.CredentialID) != 1 || f.server.Routing.CredentialInflight(pool.Members[1].CredentialID) != 0 || f.server.Routing.Snapshot(primary.HealthKey, channel).Inflight != 1 {
		t.Fatal("probe cleanup released another execution's capacity")
	}
	occupied.Finish(routing.Neutral)
	probeAssertWalletUntouched(t, f)
}

func TestPostgresProbePoolRechecksSelectedSecondaryAfterCredentialLock(t *testing.T) {
	f, channel, pool := newProbePoolFixture(t, 4, gatewaySuccess(t))
	f.server.Routing = routing.NewEngine(routing.Config{RandN: func(n uint64) uint64 { return n - 1 }})
	raw, csrf := probeAdmin(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var blockerPID int
	if err = blocker.QueryRow(ctx, "SELECT pg_backend_pid() FROM channel_credentials WHERE id=$1 FOR UPDATE", pool.Members[1].CredentialID).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	results := make(chan probeHTTPResult, 1)
	go func() { results <- probeCall(ctx, f, channel, raw, csrf) }()
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", blockerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked || f.server.Routing.CredentialInflight(pool.Members[1].CredentialID) != 1 || f.posts.Load() != 0 {
		t.Fatal("probe did not lock the actual secondary before dispatch", blocked, f.posts.Load())
	}
	// Fixture-level update models a revocation completed before the selected
	// credential's SHARE lock. No customer or review database is touched.
	if _, err = blocker.Exec(ctx, "UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1", pool.Members[1].CredentialID); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-results
	if result.err != nil || result.status != 503 || !strings.Contains(string(result.body), "probe_channel_unavailable") || f.posts.Load() != 0 {
		t.Fatalf("revoked secondary passed dispatch barrier: HTTP%d %s err=%v posts=%d", result.status, result.body, result.err, f.posts.Load())
	}
	var probes, audits int
	if err = f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM platform_probe_attempts),(SELECT count(*) FROM audit_events WHERE action='channel.probe')").Scan(&probes, &audits); err != nil || probes != 0 || audits != 0 || f.server.Routing.CredentialInflight(pool.Members[1].CredentialID) != 0 || f.server.Routing.Snapshot(pool.Members[1].CredentialID+":"+f.model, channel).Inflight != 0 || len(f.server.global) != 0 || len(f.server.probes) != 0 {
		t.Fatal("denied secondary left a dispatched record or leaked capacity", probes, audits, err)
	}
	probeAssertWalletUntouched(t, f)
}
