package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

// These fixtures prove the 035 phase-0 external upstream contract end to end:
// one logical model served through direct, new_api and sub2api channel kinds
// over the shared OpenAI-compatible relay path, with the commercial chain
// (reserve, attempt, usage, cost, settlement) generated only inside aigate.

type relayUpstream struct {
	server *httptest.Server
	kind   string
	secret string

	mu        sync.Mutex
	behavior  string
	requests  int
	lastModel string
	lastAuth  string
}

func (u *relayUpstream) setBehavior(behavior string) {
	u.mu.Lock()
	u.behavior = behavior
	u.mu.Unlock()
}

func (u *relayUpstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.requests
}

func (u *relayUpstream) observed() (string, string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastModel, u.lastAuth
}

// newRelayUpstream stands in for a downstream New API or Sub2API gateway (or
// a direct supplier). It speaks the same OpenAI-compatible chat shape all
// three topologies share.
func newRelayUpstream(t *testing.T, kind, secret string) *relayUpstream {
	t.Helper()
	u := &relayUpstream{kind: kind, secret: secret}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected relay request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var head struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &head)
		u.mu.Lock()
		u.requests++
		u.lastModel = head.Model
		u.lastAuth = r.Header.Get("Authorization")
		behavior := u.behavior
		u.mu.Unlock()
		switch behavior {
		case "reject_429":
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
		case "http_500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"downstream failure"}}`)
		case "drop_after_headers":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, _ := hijacker.Hijack()
				_ = conn.Close()
				return
			}
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, gatewayJSON)
		}
	}))
	t.Cleanup(u.server.Close)
	return u
}

type relayFixture struct {
	server                                        *Server
	pool                                          *pgxpool.Pool
	http                                          *httptest.Server
	tenant, key, model, token, operator, approver string
	newAPI, sub2api, direct                       *relayUpstream
	newAPIChannel, sub2apiChannel, directChannel  string
}

func newRelayFixture(t *testing.T) *relayFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for real PostgreSQL external relay tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "relay_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrations.ApplyCurrent(ctx, pool); err != nil {
		t.Fatal(err)
	}
	secrets := &security.Secrets{Active: "test", Keys: map[string][]byte{"test": bytes.Repeat([]byte{0x51}, 32)}, Pepper: bytes.Repeat([]byte{0x67}, 32)}
	s := New(config.Config{Origin: "http://console.test", Secrets: secrets, MediaProviderProfile: config.MediaProviderProfileDevelopment, MediaDeterministicMock: true}, pool, fstest.MapFS{}, nil)
	s.Ready(true)
	f := &relayFixture{server: s, pool: pool, tenant: uuid.NewString(), key: uuid.NewString(), token: "mg_" + security.Random(24)}
	user := uuid.NewString()
	f.operator, f.approver = uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO tenants(id,name) VALUES($1,'external relay fixture')", f.tenant)
	for _, u := range []struct{ id, role string }{{user, "none"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}} {
		exec("INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)", u.id, u.id+"@relay.invalid", u.role)
	}
	exec("INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", f.tenant, user)
	profile := &provider.Profile{Model: "retail-model", Protocol: provider.ProtocolChat, InputTokenUpperBound: 100, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 20, NoConsumptionStatuses: []int{429}, UsageSemantics: "final"}
	f.model, err = s.Catalog.CreateModel(ctx, f.operator, catalog.ModelInput{PublicName: "retail-model", Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, f.model)
	exec("INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'relay fixture','mg_test',$4,$5,1000)", f.key, f.tenant, user, secrets.Digest(f.token), []string{f.model})
	meterquote.PublishDefaultPrice(t, ctx, pool, meterquote.DefaultPrice{ModelID: f.model, Actor: f.operator, InputRate: 1000000, OutputRate: 2000000, EffectiveAt: time.Now().Add(-time.Hour)})
	f.newAPI = newRelayUpstream(t, "new_api", "newapi-relay-secret")
	f.sub2api = newRelayUpstream(t, "sub2api", "sub2api-relay-secret")
	f.direct = newRelayUpstream(t, "direct", "direct-supplier-secret")
	// The external gateways carry their own model names; the binding's
	// upstream_model is the mapping aigate sends downstream.
	f.newAPIChannel = f.saveChannel(t, "external-new-api", f.newAPI, "new_api", "newapi-relay-secret", "relay-newapi-model")
	f.sub2apiChannel = f.saveChannel(t, "external-sub2api", f.sub2api, "sub2api", "sub2api-relay-secret", "relay-sub2api-model")
	f.directChannel = f.saveChannel(t, "direct-supplier", f.direct, "direct", "direct-supplier-secret", "provider-model")
	credit, err := s.Billing.CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: uuid.NewString(), ProofRef: "receipt:relay-fixture", Amount: 100000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 100000, IdempotencyKey: uuid.NewString(), RequestHMAC: "fixture-credit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Billing.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	f.http = httptest.NewServer(s.Engine())
	t.Cleanup(f.http.Close)
	return f
}

func (f *relayFixture) saveChannel(t *testing.T, name string, upstream *relayUpstream, kind, secret, upstreamModel string) string {
	t.Helper()
	ctx := context.Background()
	id, err := f.server.Catalog.SaveChannel(ctx, f.operator, "", catalog.ChannelInput{Name: name, BaseURL: upstream.server.URL + "/v1", Secret: secret, Region: "global", ChannelKind: kind, AllowedCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	// Publish the supplier cost for the mapped downstream model so attempts
	// keep an explicit procurement projection instead of an unknown cost.
	inputRate, outputRate := int64(500000), int64(500000)
	meterquote.PublishCost(t, ctx, f.pool, meterquote.CostPrice{ChannelID: id, UpstreamModel: upstreamModel, ModelID: f.model, Actor: f.approver, Basis: "contract", InputRate: &inputRate, OutputRate: &outputRate, EffectiveAt: time.Now().Add(-time.Hour)})
	return id
}

func (f *relayFixture) publish(t *testing.T, bindings ...catalog.BindingInput) int64 {
	t.Helper()
	var current int64
	if err := f.pool.QueryRow(context.Background(), "SELECT active_revision FROM models WHERE id=$1", f.model).Scan(&current); err != nil {
		t.Fatal(err)
	}
	revision, err := f.server.Catalog.Publish(context.Background(), f.operator, f.model, current, bindings)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func (f *relayFixture) activate(t *testing.T, expected, target int64) {
	t.Helper()
	if _, err := f.server.Catalog.ActivateRouteRevision(context.Background(), f.operator, f.model, expected, target); err != nil {
		t.Fatal(err)
	}
}

func (f *relayFixture) request(t *testing.T) (string, int, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"model": "retail-model", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 20})
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.token)
	response, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.Header.Get("X-Request-Id"), response.StatusCode, raw
}

// requestFacts asserts the 035 commercial boundary for one customer request:
// money facts exist only inside aigate, with an explicit cost projection on
// every upstream attempt.
func (f *relayFixture) requestFacts(t *testing.T, requestID string) (attempts int, settled bool, routeRevision int64) {
	t.Helper()
	var settlements int
	var billingStatus string
	var requestedName *string
	err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM upstream_attempts a WHERE a.request_id=r.id),(SELECT count(*) FROM settlements s WHERE s.request_id=r.id),r.route_revision,r.requested_model_name,r.billing_status FROM request_records r WHERE r.id=$1`, requestID).Scan(&attempts, &settlements, &routeRevision, &requestedName, &billingStatus)
	if err != nil {
		t.Fatal(err)
	}
	settled = billingStatus == "settled"
	rows, err := f.pool.Query(context.Background(), `SELECT cost_status,(cost_amount IS NOT NULL) FROM upstream_attempts WHERE request_id=$1 ORDER BY sequence`, requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var hasAmount bool
		if err = rows.Scan(&status, &hasAmount); err != nil {
			t.Fatal(err)
		}
		if status == "unknown" && hasAmount || status != "unknown" && !hasAmount {
			t.Fatalf("attempt cost projection is not explicit: %s/%v", status, hasAmount)
		}
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	return attempts, settled, routeRevision
}

func TestExternalRelayNewAPIEndToEndSlice(t *testing.T) {
	f := newRelayFixture(t)
	f.publish(t, catalog.BindingInput{ChannelID: f.newAPIChannel, UpstreamModel: "relay-newapi-model", Priority: 1, Weight: 1, MaxInflight: 4})
	requestID, status, raw := f.request(t)
	if status != http.StatusOK {
		t.Fatalf("new_api relay request returned HTTP %d: %s", status, raw)
	}
	var decoded struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Model != "retail-model" {
		t.Fatalf("relay response did not echo the customer model name: %s", raw)
	}
	upstreamModel, auth := f.newAPI.observed()
	if upstreamModel != "relay-newapi-model" {
		t.Fatalf("model mapping was not applied downstream: %q", upstreamModel)
	}
	if auth != "Bearer newapi-relay-secret" {
		t.Fatalf("relay credential was not applied: %q", auth)
	}
	attempts, settled, revision := f.requestFacts(t, requestID)
	if attempts != 1 || !settled || revision != 1 {
		t.Fatalf("new_api slice facts: attempts=%d settled=%v revision=%d", attempts, settled, revision)
	}
	if f.sub2api.count()+f.direct.count() != 0 {
		t.Fatal("new_api slice leaked requests to other channels")
	}
}

func TestExternalRelaySub2APIReusesSameContract(t *testing.T) {
	f := newRelayFixture(t)
	f.publish(t, catalog.BindingInput{ChannelID: f.sub2apiChannel, UpstreamModel: "relay-sub2api-model", Priority: 1, Weight: 1, MaxInflight: 4})
	requestID, status, raw := f.request(t)
	if status != http.StatusOK {
		t.Fatalf("sub2api relay request returned HTTP %d: %s", status, raw)
	}
	upstreamModel, _ := f.sub2api.observed()
	if upstreamModel != "relay-sub2api-model" {
		t.Fatalf("sub2api model mapping was not applied: %q", upstreamModel)
	}
	attempts, settled, _ := f.requestFacts(t, requestID)
	if attempts != 1 || !settled {
		t.Fatalf("sub2api slice facts: attempts=%d settled=%v", attempts, settled)
	}
}

func TestExternalRelayNoConsumptionSwitchesAcrossChannelKinds(t *testing.T) {
	f := newRelayFixture(t)
	f.newAPI.setBehavior("reject_429")
	f.publish(t,
		catalog.BindingInput{ChannelID: f.newAPIChannel, UpstreamModel: "relay-newapi-model", Priority: 2, Weight: 1, MaxInflight: 4},
		catalog.BindingInput{ChannelID: f.directChannel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 4})
	requestID, status, raw := f.request(t)
	if status != http.StatusOK {
		t.Fatalf("cross-kind failover returned HTTP %d: %s", status, raw)
	}
	if f.newAPI.count() != 1 || f.direct.count() != 1 {
		t.Fatalf("429 no-consumption failover dispatch counts: new_api=%d direct=%d", f.newAPI.count(), f.direct.count())
	}
	attempts, settled, _ := f.requestFacts(t, requestID)
	if attempts != 2 || !settled {
		t.Fatalf("cross-kind failover facts: attempts=%d settled=%v", attempts, settled)
	}
}

func TestExternalRelayUnknownConsumptionNeverReplays(t *testing.T) {
	for _, behavior := range []string{"http_500", "drop_after_headers"} {
		t.Run(behavior, func(t *testing.T) {
			f := newRelayFixture(t)
			f.newAPI.setBehavior(behavior)
			f.publish(t,
				catalog.BindingInput{ChannelID: f.newAPIChannel, UpstreamModel: "relay-newapi-model", Priority: 2, Weight: 1, MaxInflight: 4},
				catalog.BindingInput{ChannelID: f.directChannel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 4})
			requestID, status, _ := f.request(t)
			if status == http.StatusOK {
				t.Fatalf("%s must not report success", behavior)
			}
			if f.newAPI.count() != 1 || f.direct.count() != 0 {
				t.Fatalf("%s replayed or switched a possibly-consumed request: new_api=%d direct=%d", behavior, f.newAPI.count(), f.direct.count())
			}
			attempts, settled, _ := f.requestFacts(t, requestID)
			if attempts != 1 || settled {
				t.Fatalf("%s facts: attempts=%d settled=%v", behavior, attempts, settled)
			}
		})
	}
}

func TestExternalRelayEmbeddedAdapterSliceRollsBackByRouteRevision(t *testing.T) {
	f := newRelayFixture(t)
	// Revision 1 serves the capability through the external new_api relay.
	relayRevision := f.publish(t, catalog.BindingInput{ChannelID: f.newAPIChannel, UpstreamModel: "relay-newapi-model", Priority: 1, Weight: 1, MaxInflight: 4})
	beforeID, status, _ := f.request(t)
	if status != http.StatusOK || f.newAPI.count() != 1 {
		t.Fatalf("relay revision did not serve: HTTP %d new_api=%d", status, f.newAPI.count())
	}
	_, settled, beforeRevision := f.requestFacts(t, beforeID)
	if !settled || beforeRevision != relayRevision {
		t.Fatalf("relay revision facts: settled=%v revision=%d", settled, beforeRevision)
	}
	// Revision 2 migrates the same capability to aigate's embedded direct
	// adapter; this is the external_relay -> embedded_adapter slice.
	embeddedRevision := f.publish(t, catalog.BindingInput{ChannelID: f.directChannel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 4})
	embeddedID, status, _ := f.request(t)
	if status != http.StatusOK || f.direct.count() != 1 {
		t.Fatalf("embedded revision did not serve: HTTP %d direct=%d", status, f.direct.count())
	}
	_, settled, afterRevision := f.requestFacts(t, embeddedID)
	if !settled || afterRevision != embeddedRevision {
		t.Fatalf("embedded revision facts: settled=%v revision=%d", settled, afterRevision)
	}
	// Route revision rollback returns the capability to the external relay.
	f.activate(t, embeddedRevision, relayRevision)
	rollbackID, status, _ := f.request(t)
	if status != http.StatusOK || f.newAPI.count() != 2 {
		t.Fatalf("rollback did not return traffic to the relay: HTTP %d new_api=%d", status, f.newAPI.count())
	}
	_, settled, rollbackRevision := f.requestFacts(t, rollbackID)
	if !settled || rollbackRevision != relayRevision {
		t.Fatalf("rollback facts: settled=%v revision=%d", settled, rollbackRevision)
	}
	// The request admitted before the rollback keeps its frozen revision.
	if beforeRevision != relayRevision || afterRevision != embeddedRevision {
		t.Fatalf("frozen revisions drifted: before=%d embedded=%d", beforeRevision, embeddedRevision)
	}
}

func TestExternalRelayChannelKindLifecycle(t *testing.T) {
	f := newRelayFixture(t)
	ctx := context.Background()
	// A channel's kind is visible in the platform list.
	var kind, mode string
	if err := f.pool.QueryRow(ctx, `SELECT v.channel_kind,v.integration_mode FROM channel_versions v WHERE v.channel_id=$1`, f.newAPIChannel).Scan(&kind, &mode); err != nil {
		t.Fatal(err)
	}
	if kind != "new_api" || mode != "external_relay" {
		t.Fatalf("new_api channel contract: kind=%s mode=%s", kind, mode)
	}
	if err := f.pool.QueryRow(ctx, `SELECT channel_kind,integration_mode FROM channel_versions WHERE channel_id=$1`, f.directChannel).Scan(&kind, &mode); err != nil {
		t.Fatal(err)
	}
	if kind != "direct" || mode != "embedded_adapter" {
		t.Fatalf("direct channel contract: kind=%s mode=%s", kind, mode)
	}
	// Invalid kinds are rejected before any write.
	if _, err := f.server.Catalog.SaveChannel(ctx, f.operator, "", catalog.ChannelInput{Name: "invalid-kind", BaseURL: f.direct.server.URL + "/v1", Secret: "direct-supplier-secret", Region: "global", ChannelKind: "openrouter", AllowedCIDRs: []string{"127.0.0.0/8"}}); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("invalid channel kind accepted: %v", err)
	}
	// An existing channel's topology is fixed; migration happens through a
	// different channel plus a route revision switch, never by redefining a
	// relay identity in place.
	if _, err := f.server.Catalog.SaveChannel(ctx, f.operator, f.newAPIChannel, catalog.ChannelInput{Name: "external-new-api", BaseURL: f.newAPI.server.URL + "/v1", Secret: "newapi-relay-secret", Region: "global", ChannelKind: "direct", AllowedCIDRs: []string{"127.0.0.0/8"}}); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("channel kind change accepted: %v", err)
	}
}

func TestExternalRelayRouteRevisionActivationContract(t *testing.T) {
	f := newRelayFixture(t)
	ctx := context.Background()
	relayRevision := f.publish(t, catalog.BindingInput{ChannelID: f.newAPIChannel, UpstreamModel: "relay-newapi-model", Priority: 1, Weight: 1, MaxInflight: 4})
	embeddedRevision := f.publish(t, catalog.BindingInput{ChannelID: f.directChannel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 4})
	// Stale CAS is a conflict.
	if _, err := f.server.Catalog.ActivateRouteRevision(ctx, f.operator, f.model, relayRevision, relayRevision); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("stale activation accepted: %v", err)
	}
	// Unknown revisions are refused.
	if _, err := f.server.Catalog.ActivateRouteRevision(ctx, f.operator, f.model, embeddedRevision, 99); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("unpublished activation accepted: %v", err)
	}
	// Unknown models are refused.
	if _, err := f.server.Catalog.ActivateRouteRevision(ctx, f.operator, uuid.NewString(), embeddedRevision, relayRevision); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("unknown model activation accepted: %v", err)
	}
	// A route whose entire frozen credential pool was revoked cannot be
	// reactivated, even though its channel and version remain enabled.
	if _, err := f.pool.Exec(ctx, "UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE channel_id=$1", f.newAPIChannel); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.Catalog.ActivateRouteRevision(ctx, f.operator, f.model, embeddedRevision, relayRevision); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("revoked-credential route activation accepted: %v", err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE channel_credentials SET revoked_at=NULL WHERE channel_id=$1", f.newAPIChannel); err != nil {
		t.Fatal(err)
	}
	// A route whose channel was disabled cannot be reactivated.
	if _, err := f.pool.Exec(ctx, "UPDATE channels SET enabled=false WHERE id=$1", f.newAPIChannel); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.Catalog.ActivateRouteRevision(ctx, f.operator, f.model, embeddedRevision, relayRevision); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("dead route activation accepted: %v", err)
	}
}

func TestExternalRelayRouteActivationRechecksCredentialRevocation(t *testing.T) {
	f := newRelayFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	relayRevision := f.publish(t, catalog.BindingInput{ChannelID: f.newAPIChannel, UpstreamModel: "relay-newapi-model", Priority: 1, Weight: 1, MaxInflight: 4})
	embeddedRevision := f.publish(t, catalog.BindingInput{ChannelID: f.directChannel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 4})
	var credential string
	if err := f.pool.QueryRow(ctx, `SELECT v.credential_id FROM channel_versions v JOIN channels c ON c.current_version=v.id WHERE c.id=$1`, f.newAPIChannel).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	holder, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, "SELECT id FROM channel_credentials WHERE id=$1 FOR UPDATE", credential); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, activateErr := f.server.Catalog.ActivateRouteRevision(ctx, f.operator, f.model, embeddedRevision, relayRevision)
		done <- activateErr
	}()
	select {
	case err = <-done:
		t.Fatalf("activation did not wait for the credential writer: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = holder.Exec(ctx, "UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1", credential); err != nil {
		t.Fatal(err)
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, catalog.ErrInvalid) {
			t.Fatalf("activation committed across credential revocation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("activation remained blocked after credential writer committed")
	}
}

func TestExternalRelayProbeReportsEvidenceDimensions(t *testing.T) {
	f := newRelayFixture(t)
	f.publish(t,
		catalog.BindingInput{ChannelID: f.newAPIChannel, UpstreamModel: "relay-newapi-model", Priority: 2, Weight: 1, MaxInflight: 4},
		catalog.BindingInput{ChannelID: f.sub2apiChannel, UpstreamModel: "relay-sub2api-model", Priority: 1, Weight: 1, MaxInflight: 4})
	fixture := &gatewayFixture{server: f.server, pool: f.pool, http: f.http}
	raw, csrf := probeAdmin(t, fixture)
	result := probeCall(context.Background(), fixture, f.newAPIChannel, raw, csrf)
	if result.status != http.StatusOK {
		t.Fatalf("probe returned HTTP %d: %s", result.status, result.body)
	}
	var decoded struct {
		Data struct {
			OK       bool `json:"ok"`
			Evidence struct {
				ChannelKind     string `json:"channel_kind"`
				IntegrationMode string `json:"integration_mode"`
				UpstreamModel   string `json:"upstream_model"`
				ProtocolSuccess bool   `json:"protocol_success"`
				UsageStatus     string `json:"usage_status"`
				CostBasis       string `json:"cost_basis"`
				ErrorClass      string `json:"error_class"`
				Consumption     string `json:"consumption"`
			} `json:"evidence"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result.body, &decoded); err != nil {
		t.Fatalf("probe response was not JSON (HTTP %d, err=%v): %q", result.status, result.err, result.body)
	}
	if !decoded.Data.OK || decoded.Data.Evidence.ChannelKind != "new_api" || decoded.Data.Evidence.IntegrationMode != "external_relay" || decoded.Data.Evidence.UpstreamModel != "relay-newapi-model" || !decoded.Data.Evidence.ProtocolSuccess || decoded.Data.Evidence.UsageStatus != string(provider.UsageConfirmed) || decoded.Data.Evidence.CostBasis != "contract" {
		t.Fatalf("probe evidence dimensions incomplete: %+v", decoded.Data.Evidence)
	}
	// A failing probe separates the failure classification from usage and cost
	// evidence instead of collapsing them into one boolean. Probes are rate
	// limited per channel, so the failing probe uses the sibling sub2api
	// channel rather than repeating the same channel within one minute.
	f.sub2api.setBehavior("http_500")
	result = probeCall(context.Background(), fixture, f.sub2apiChannel, raw, csrf)
	if result.status != http.StatusOK {
		t.Fatalf("failing probe returned HTTP %d: %s", result.status, result.body)
	}
	if err := json.Unmarshal(result.body, &decoded); err != nil {
		t.Fatalf("probe response was not JSON (HTTP %d, err=%v): %q", result.status, result.err, result.body)
	}
	if decoded.Data.OK || decoded.Data.Evidence.ProtocolSuccess || decoded.Data.Evidence.UsageStatus == string(provider.UsageConfirmed) || decoded.Data.Evidence.Consumption != "unknown" {
		t.Fatalf("failing probe evidence misclassified: %+v", decoded.Data.Evidence)
	}
}
