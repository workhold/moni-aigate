package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

// Read the same defaults the channel form imports. This covers the boundary
// between UI configuration and the database/API defaults, including a paid
// request; separate constant assertions would miss an incompatible region.
func TestChannelFormDefaultRegionMatchesFreshCustomerAndKey(t *testing.T) {
	rawDefaults, err := os.ReadFile("../../web/src/channel-defaults.json")
	if err != nil {
		t.Fatal(err)
	}
	var defaults struct {
		Region string `json:"region"`
	}
	if err = json.Unmarshal(rawDefaults, &defaults); err != nil || defaults.Region == "" || defaults.Region == "*" {
		t.Fatal("channel form must use an explicit region", err)
	}
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx := context.Background()
	admin, _ := authUser(t, f.server, f.pool, "admin")
	adminSession, adminCSRF := authSession(t, f.server, f.pool, admin, "", false, true)
	call := func(method, path string, body any, session, csrf string) json.RawMessage {
		t.Helper()
		response := authHTTP(t, f.server, method, path, session, csrf, f.server.Config.Origin, body)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	createdID := func(data json.RawMessage) string {
		t.Helper()
		var value struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &value); err != nil || value.ID == "" {
			t.Fatal("missing created identity", err)
		}
		return value.ID
	}
	var baseURL string
	if err := f.pool.QueryRow(ctx, "SELECT base_url FROM channel_versions LIMIT 1").Scan(&baseURL); err != nil {
		t.Fatal(err)
	}
	channel := createdID(call("POST", "/api/platform/channels", map[string]any{
		"name": "channel-form-default", "base_url": baseURL, "secret": "local-fixture-secret",
		"region": defaults.Region, "allowed_cidrs": []string{"127.0.0.0/8"},
	}, adminSession, adminCSRF))
	publish := func(revision int) {
		t.Helper()
		call("PUT", "/api/platform/models/"+f.model+"/bindings", map[string]any{
			"expected_revision": revision,
			"bindings":          []map[string]any{{"channel_id": channel, "upstream_model": "provider-model", "priority": 0, "weight": 1, "max_inflight": 10}},
		}, adminSession, adminCSRF)
	}
	publish(1)
	tenant := createdID(call("POST", "/api/platform/tenants", map[string]any{
		"name": "fresh-ui-customer", "owner_email": uuid.NewString() + "@defaults.invalid",
		"owner_password": authTestPassword, "model_ids": []string{f.model},
	}, adminSession, adminCSRF))
	var owner string
	if err := f.pool.QueryRow(ctx, "SELECT user_id FROM memberships WHERE tenant_id=$1 AND role='owner'", tenant).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	ownerSession, ownerCSRF := authSession(t, f.server, f.pool, owner, tenant, false, true)
	var createdKey struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	// These are the KeysView fields; it deliberately sends no region override.
	if err := json.Unmarshal(call("POST", "/api/keys", map[string]any{
		"name": "default-region-app", "scopes": []string{f.model}, "lifetime_limit": nil, "expires_at": nil,
	}, ownerSession, ownerCSRF), &createdKey); err != nil || createdKey.Key == "" || createdKey.ID == "" {
		t.Fatal("fresh UI key creation failed", err)
	}
	var tenantRegions, keyRegions []string
	if err := f.pool.QueryRow(ctx, "SELECT t.allowed_regions,k.allowed_regions FROM tenants t JOIN api_keys k ON k.tenant_id=t.id WHERE k.id=$1", createdKey.ID).Scan(&tenantRegions, &keyRegions); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tenantRegions, []string{defaults.Region}) || !reflect.DeepEqual(keyRegions, []string{defaults.Region}) {
		t.Fatalf("form default does not match fresh grants: tenant=%v key=%v", tenantRegions, keyRegions)
	}
	var operator, approver string
	if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_operator'").Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_approver'").Scan(&approver); err != nil {
		t.Fatal(err)
	}
	credit, err := f.server.Billing.CreateCredit(ctx, billing.CreditInput{TenantID: tenant, ActorID: operator, Amount: 1_000_000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1_000_000, BusinessRef: uuid.NewString(), ProofRef: "fixture:default-region", IdempotencyKey: uuid.NewString(), RequestHMAC: "default-region-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.Billing.ApproveCredit(ctx, billing.ApprovalInput{TenantID: tenant, OrderID: credit.ID, ActorID: approver}); err != nil {
		t.Fatal(err)
	}
	f.tenant, f.key, f.token = tenant, createdKey.ID, createdKey.Key
	response := f.request(t, ctx, false, false)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "hello") || f.posts.Load() != 1 {
		t.Fatalf("default UI path did not reach local model: status=%d posts=%d err=%v", response.StatusCode, f.posts.Load(), err)
	}
	requestID := response.Header.Get("X-Request-ID")
	state := f.state(t, requestID)
	if state.billing != "settled" || state.charge != 20 || state.spent != 20 || state.held != 0 || state.keyHeld != 0 {
		t.Fatalf("default UI request did not settle: %+v", state)
	}
	// A different exact label must remain forbidden. Do not turn global into
	// a wildcard or widen the customer's/key's defaults to make setup pass.
	call("PATCH", "/api/platform/channels/"+channel, map[string]any{"region": "cn"}, adminSession, adminCSRF)
	publish(2)
	response = f.request(t, ctx, false, false)
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "no_available_channel") || f.posts.Load() != 1 {
		t.Fatalf("default region grant permitted a different label: status=%d posts=%d err=%v", response.StatusCode, f.posts.Load(), err)
	}
	var requests int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM request_records WHERE tenant_id=$1", tenant).Scan(&requests); err != nil || requests != 1 {
		t.Fatalf("region rejection created customer requests: %d %v", requests, err)
	}
	after := f.state(t, requestID)
	if after != state {
		t.Fatalf("region rejection changed the settled customer's money: before=%+v after=%+v", state, after)
	}
}
