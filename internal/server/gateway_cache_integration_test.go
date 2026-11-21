package server

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func enableGatewayExactCache(t *testing.T, f *gatewayFixture) {
	t.Helper()
	var binding catalog.BindingInput
	if err := f.pool.QueryRow(context.Background(), `SELECT b.channel_id,b.upstream_model,b.priority,b.weight,b.max_inflight FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id WHERE p.model_id=$1 AND p.revision=1`, f.model).Scan(&binding.ChannelID, &binding.UpstreamModel, &binding.Priority, &binding.Weight, &binding.MaxInflight); err != nil {
		t.Fatal(err)
	}
	if revision, err := f.server.Catalog.PublishWithDataPlanePolicy(context.Background(), "cache-test", f.model, 1, []catalog.BindingInput{binding}, routing.DefaultCostPolicy(), routing.DefaultBreakerConfig(), 60); err != nil || revision != 2 {
		t.Fatalf("cache route revision=%d err=%v", revision, err)
	}
}

func TestGatewayPostgresExactCacheHitSameChargeAndZeroUpstream(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	enableGatewayExactCache(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := f.request(t, ctx, false, false)
	firstBody, err := io.ReadAll(first.Body)
	if err != nil || first.StatusCode != http.StatusOK {
		t.Fatalf("cache miss HTTP %d err=%v", first.StatusCode, err)
	}
	second := f.request(t, ctx, false, false)
	secondBody, err := io.ReadAll(second.Body)
	if err != nil || second.StatusCode != http.StatusOK || string(secondBody) != string(firstBody) {
		t.Fatalf("cache hit HTTP %d body=%q err=%v", second.StatusCode, secondBody, err)
	}
	if f.posts.Load() != 1 {
		t.Fatalf("cache hit touched upstream: posts=%d", f.posts.Load())
	}
	firstID, secondID := first.Header.Get("X-Request-ID"), second.Header.Get("X-Request-ID")
	if firstID == secondID {
		t.Fatal("cache hit reused the original request identity")
	}
	var attempts, events, finals, reservations, settlements, cacheRows int
	var source string
	var firstCharge, secondCharge int64
	if err = f.pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM upstream_attempts WHERE request_id=$1),(SELECT count(*) FROM metering_events WHERE request_id=$1),
 (SELECT count(*) FROM finalization_records WHERE request_id=$1),(SELECT count(*) FROM reservations WHERE request_id=$1),
 (SELECT count(*) FROM settlements WHERE request_id=$1),(SELECT count(*) FROM text_response_cache),
 (SELECT source FROM finalization_records WHERE request_id=$1),
 (SELECT charged_amount FROM settlements WHERE request_id=$2),(SELECT charged_amount FROM settlements WHERE request_id=$1)`,
		secondID, firstID).Scan(&attempts, &events, &finals, &reservations, &settlements, &cacheRows, &source, &firstCharge, &secondCharge); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || events != 0 || finals != 1 || reservations != 1 || settlements != 1 || cacheRows != 1 || source != "cache" || firstCharge != secondCharge {
		t.Fatalf("cache-hit cardinality/charge mismatch: attempts=%d events=%d finals=%d reservations=%d settlements=%d rows=%d source=%s charges=%d/%d", attempts, events, finals, reservations, settlements, cacheRows, source, firstCharge, secondCharge)
	}
	ledger, err := f.server.Billing.VerifyLedger(ctx, billing.Query{TenantID: f.tenant})
	if err != nil || !ledger.Balanced {
		t.Fatalf("cache-hit ledger mismatch: %+v err=%v", ledger, err)
	}
}
