package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestPostgresRouteDataPlanePolicyIsImmutableAndVersioned(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx := context.Background()
	var binding catalog.BindingInput
	if err := f.pool.QueryRow(ctx, `SELECT b.channel_id,b.upstream_model,b.priority,b.weight,b.max_inflight FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id WHERE p.model_id=$1 AND p.revision=1`, f.model).Scan(&binding.ChannelID, &binding.UpstreamModel, &binding.Priority, &binding.Weight, &binding.MaxInflight); err != nil {
		t.Fatal(err)
	}
	breaker := routing.BreakerConfig{FailureThreshold: 2, OpenDuration: time.Second, HalfOpenTokens: 4}
	revision, err := f.server.Catalog.PublishWithDataPlanePolicy(ctx, "route-test", f.model, 1, []catalog.BindingInput{binding}, routing.DefaultCostPolicy(), breaker, 60)
	if err != nil || revision != 2 {
		t.Fatalf("custom data-plane route publish revision=%d err=%v", revision, err)
	}
	oldSnapshot, err := f.server.Catalog.CandidateSnapshot(ctx, f.model, 1)
	if err != nil || len(oldSnapshot) == 0 || oldSnapshot[0].Breaker != routing.DefaultBreakerConfig() {
		t.Fatalf("historical route changed: %+v err=%v", oldSnapshot, err)
	}
	newSnapshot, err := f.server.Catalog.CandidateSnapshot(ctx, f.model, 2)
	if err != nil || len(newSnapshot) == 0 || newSnapshot[0].Breaker != breaker {
		t.Fatalf("new route breaker snapshot mismatch: %+v err=%v", newSnapshot, err)
	}
	var oldTTL, newTTL, oldOpen, newOpen int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT cache_ttl_seconds FROM route_policy_versions WHERE model_id=$1 AND revision=1),(SELECT cache_ttl_seconds FROM route_policy_versions WHERE model_id=$1 AND revision=2),(SELECT open_ms FROM route_policy_versions WHERE model_id=$1 AND revision=1),(SELECT open_ms FROM route_policy_versions WHERE model_id=$1 AND revision=2)`, f.model).Scan(&oldTTL, &newTTL, &oldOpen, &newOpen); err != nil || oldTTL != 0 || newTTL != 60 || oldOpen != 30000 || newOpen != 1000 {
		t.Fatalf("immutable policy versions mismatch: ttl=%d/%d open=%d/%d err=%v", oldTTL, newTTL, oldOpen, newOpen, err)
	}
}
