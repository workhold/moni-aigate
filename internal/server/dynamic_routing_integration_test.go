package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func dynamicGatewayRequest(t *testing.T, f *gatewayFixture, route map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"model": "retail-model", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 20, "route": route})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.http.URL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func republishDynamicGateway(t *testing.T, f *gatewayFixture, policy routing.DynamicPolicy) {
	t.Helper()
	available, err := f.server.Catalog.CandidateSnapshot(context.Background(), f.model, 1)
	if err != nil || len(available) == 0 {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	bindings := []catalog.BindingInput{}
	for _, candidate := range available {
		if seen[candidate.BindingID] {
			continue
		}
		seen[candidate.BindingID] = true
		bindings = append(bindings, catalog.BindingInput{ChannelID: candidate.ChannelID, PhysicalModelID: candidate.PhysicalModelID, UpstreamModel: candidate.UpstreamModel, Priority: 1, Weight: candidate.Weight, MaxInflight: candidate.MaxInflight})
	}
	if _, err = f.server.Catalog.PublishDynamic(context.Background(), "dynamic-test", f.model, 1, bindings, policy, catalog.ReleaseTestOnly); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicRouteHTTPOrderAndUnknownSelector(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t), gatewaySuccess(t), gatewaySuccess(t)}, nil)
	republishDynamicGateway(t, f, routing.DefaultDynamicPolicy())
	channels := probeChannels(t, f)
	response := dynamicGatewayRequest(t, f, map[string]any{"order": []string{channels[2]}, "allow_fallbacks": false})
	if response.StatusCode != 200 {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	requestID := response.Header.Get("X-Request-ID")
	var selected, physical string
	if err := f.pool.QueryRow(context.Background(), `SELECT a.channel_id,d.selected_physical_model_id FROM upstream_attempts a JOIN route_decisions d ON d.attempt_id=a.id WHERE a.request_id=$1`, requestID).Scan(&selected, &physical); err != nil || selected != channels[2] || physical == "" {
		t.Fatalf("selected=%s physical=%s err=%v", selected, physical, err)
	}
	f.assertSettled(t, requestID, 1)

	before := f.posts.Load()
	response = dynamicGatewayRequest(t, f, map[string]any{"only": []string{"unknown-supplier"}})
	if response.StatusCode != 400 || f.posts.Load() != before {
		t.Fatalf("unknown selector status=%d", response.StatusCode)
	}
}

func TestDynamicRouteStatsRequireFiftyRealSamples(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	if _, err := f.pool.Exec(context.Background(), `UPDATE api_keys SET lifetime_limit=1000000 WHERE id=$1`, f.key); err != nil {
		t.Fatal(err)
	}
	var operator, approver string
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT id FROM users WHERE platform_role='finance_operator' LIMIT 1),(SELECT id FROM users WHERE platform_role='finance_approver' LIMIT 1)`).Scan(&operator, &approver); err != nil {
		t.Fatal(err)
	}
	operation := uuid.NewString()
	credit, err := f.server.Billing.CreateCredit(context.Background(), billing.CreditInput{TenantID: f.tenant, ActorID: operator, Amount: 10000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 10000, BusinessRef: operation, ProofRef: "route-stats-fixture", IdempotencyKey: operation, RequestHMAC: operation})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Billing.ApproveCredit(context.Background(), billing.ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: approver}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		response := f.request(t, context.Background(), false, false)
		if response.StatusCode != 200 {
			t.Fatalf("sample %d status=%d", i, response.StatusCode)
		}
		time.Sleep(1050 * time.Millisecond)
	}
	candidates, err := f.server.Catalog.CandidateSnapshot(context.Background(), f.model, 1)
	if err != nil || len(candidates) != 1 {
		t.Fatal(err)
	}
	stats := candidates[0].Stats
	if stats.SampleCount != 50 || stats.SuccessCount != 50 || stats.P50FirstContentUS == nil || stats.P95FirstContentUS == nil || stats.ThroughputMicroS == nil || *stats.ThroughputMicroS != 55555 {
		t.Fatalf("real route stats=%+v", stats)
	}
	if err = f.server.Catalog.RefreshRouteStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	var samples int64
	var p50 *int64
	if err = f.pool.QueryRow(context.Background(), `SELECT sample_count,p50_first_content_us FROM route_stats_windows WHERE physical_model_id=$1 AND channel_id=$2`, candidates[0].PhysicalModelID, candidates[0].ChannelID).Scan(&samples, &p50); err != nil || samples != 50 || p50 == nil {
		t.Fatalf("persisted route stats samples=%d p50=%v err=%v", samples, p50, err)
	}
}

func TestDynamicRouteAutoUsesSingleRetailSettlement(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	var operator string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_operator' LIMIT 1`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	bindings := []catalog.BindingInput{}
	for _, item := range []struct {
		name, upstream, tier string
		rate                 int64
	}{{"premium", "premium-model", "premium", 10000000}, {"economy", "economy-model", "economy", 1000000}} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, gatewayJSON)
		}))
		t.Cleanup(upstream.Close)
		channel, err := f.server.Catalog.SaveChannel(context.Background(), operator, "", catalog.ChannelInput{Name: item.name, BaseURL: upstream.URL + "/v1", Secret: "local-fixture-secret", Region: "global", AllowedCIDRs: []string{"127.0.0.0/8"}})
		if err != nil {
			t.Fatal(err)
		}
		capability := routing.PhysicalCapability{ContextLimit: 120, InputUpperBound: 100, OutputLimit: 20, UsageSemantics: "final", Modalities: []string{"text"}, QualityTier: item.tier, MaxConcurrency: 4, SupportedParameters: []string{}}
		bindings = append(bindings, catalog.BindingInput{ChannelID: channel, UpstreamModel: item.upstream, Capability: &capability, Priority: 1, Weight: 1, MaxInflight: 4})
		meterquote.PublishCost(t, context.Background(), f.pool, meterquote.CostPrice{ChannelID: channel, UpstreamModel: item.upstream, ModelID: f.model, Actor: operator, Basis: "contract", InputRate: &item.rate, OutputRate: &item.rate, EffectiveAt: time.Now().UTC().Add(-time.Hour)})
	}
	policy := routing.DefaultDynamicPolicy()
	policy.Scoring = routing.ScoringPolicy{Auto: true, AllowDegradation: true}
	if _, err := f.server.Catalog.PublishDynamic(context.Background(), operator, f.model, 1, bindings, policy, catalog.ReleaseTestOnly); err != nil {
		t.Fatal(err)
	}
	response := dynamicGatewayRequest(t, f, map[string]any{"sort": "price"})
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		t.Fatalf("auto status=%d body=%s", response.StatusCode, body)
	}
	var result struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &result) != nil || result.Model != "retail-model" {
		t.Fatalf("auto response model=%s body=%s", result.Model, body)
	}
	requestID := response.Header.Get("X-Request-ID")
	var tier, degradation string
	if err := f.pool.QueryRow(context.Background(), `SELECT pm.capability->>'quality_tier',d.degradation FROM route_decisions d JOIN physical_models pm ON pm.id=d.selected_physical_model_id WHERE d.request_id=$1`, requestID).Scan(&tier, &degradation); err != nil || tier != "economy" || degradation != "tier_changed" {
		t.Fatalf("auto tier=%s degradation=%s err=%v", tier, degradation, err)
	}
	f.assertSettled(t, requestID, 1)
}

func TestDynamicRouteNoCandidatePersistsFailurePoolBeforeRelease(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	policy := routing.DynamicPolicy{CostPolicy: routing.DefaultCostPolicy(), QualityFloor: "premium"}
	republishDynamicGateway(t, f, policy)
	response := f.request(t, context.Background(), false, false)
	if response.StatusCode != 503 || f.posts.Load() != 0 {
		t.Fatalf("no-candidate status=%d posts=%d", response.StatusCode, f.posts.Load())
	}
	requestID := response.Header.Get("X-Request-ID")
	var attempts, decisions, candidates int
	var settlement, reason string
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM upstream_attempts WHERE request_id=$1),(SELECT count(*) FROM route_decisions WHERE request_id=$1 AND attempt_id IS NULL),(SELECT jsonb_array_length(candidates) FROM route_decisions WHERE request_id=$1),(SELECT status FROM settlements WHERE request_id=$1),(SELECT candidates->0->>'reason' FROM route_decisions WHERE request_id=$1)`, requestID).Scan(&attempts, &decisions, &candidates, &settlement, &reason); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || decisions != 1 || candidates != 1 || settlement != "released" || reason != "quality_below_floor" {
		t.Fatalf("attempts=%d decisions=%d candidates=%d settlement=%s reason=%s", attempts, decisions, candidates, settlement, reason)
	}
}
