package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

// These fixtures exercise the public HTTP API, real provider sockets, catalog
// snapshots and PostgreSQL transactions together. They never call a paid model.
type gatewayFixture struct {
	server                    *Server
	pool                      *pgxpool.Pool
	http                      *httptest.Server
	tenant, key, model, token string
	posts                     atomic.Int32
	profile                   *capacityProfiler
}

func newGatewayFixture(t *testing.T, handlers []http.HandlerFunc, noConsumption []int) *gatewayFixture {
	t.Helper()
	return newGatewayFixtureWithPool(t, handlers, noConsumption, 8)
}

func newGatewayFixtureWithPool(t *testing.T, handlers []http.HandlerFunc, noConsumption []int, maxConnections int32) *gatewayFixture {
	return newGatewayFixtureConfigured(t, handlers, noConsumption, maxConnections, provider.ProtocolChat)
}

func newGatewayFixtureConfigured(t *testing.T, handlers []http.HandlerFunc, noConsumption []int, maxConnections int32, protocol string) *gatewayFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for real PostgreSQL gateway tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "gateway_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// An isolated path also isolates the migrations table from the public schema.
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = maxConnections
	profiler := configureCapacityProfiler(t, cfg)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	secrets := &security.Secrets{Active: "test", Keys: map[string][]byte{"test": bytes.Repeat([]byte{0x51}, 32)}, Pepper: bytes.Repeat([]byte{0x67}, 32)}
	s := New(config.Config{Origin: "http://console.test", Secrets: secrets}, pool, fstest.MapFS{}, nil)
	s.Ready(true)
	f := &gatewayFixture{server: s, pool: pool, tenant: uuid.NewString(), key: uuid.NewString(), token: "mg_" + security.Random(24), profile: profiler}
	user, operator, approver := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO tenants(id,name) VALUES($1,'gateway integration')", f.tenant)
	for _, u := range []struct{ id, role string }{{user, "none"}, {operator, "finance_operator"}, {approver, "finance_approver"}} {
		exec("INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)", u.id, u.id+"@example.invalid", u.role)
	}
	exec("INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", f.tenant, user)
	profile := &provider.Profile{Model: "retail-model", Protocol: protocol, InputTokenUpperBound: 100, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 20, NoConsumptionStatuses: noConsumption, UsageSemantics: "final"}
	outputRate := "2000000"
	if protocol == provider.ProtocolGemini {
		profile.UsageSemantics = "cumulative"
	}
	if profile.InputOnly() {
		profile.OutputLimit, profile.DefaultOutputLimit = 0, 0
		outputRate = "0"
	}
	f.model, err = s.Catalog.CreateModel(ctx, operator, catalog.ModelInput{PublicName: "retail-model", Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, f.model)
	exec("INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'gateway fixture','mg_test',$4,$5,1000)", f.key, f.tenant, user, secrets.Digest(f.token), []string{f.model})
	if _, err = s.Catalog.PublishPrice(ctx, operator, f.model, "1000000", outputRate, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	bindings := make([]catalog.BindingInput, 0, len(handlers))
	for i, handler := range handlers {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			suffix := "/chat/completions"
			prefix := "/v1"
			if protocol == provider.ProtocolResponses {
				suffix = "/responses"
			}
			if protocol == provider.ProtocolMessages {
				suffix = "/messages"
			}
			if protocol == provider.ProtocolEmbeddings {
				suffix = "/embeddings"
			}
			if protocol == provider.ProtocolRerank {
				suffix = "/rerank"
			}
			if protocol == provider.ProtocolGemini {
				prefix = "/v1beta"
				suffix = "/models/provider-model:generateContent"
				if r.URL.RawQuery == "alt=sse" {
					suffix = "/models/provider-model:streamGenerateContent"
				}
			}
			if r.Method != http.MethodPost || r.URL.Path != prefix+suffix {
				t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(404)
				return
			}
			f.posts.Add(1)
			if protocol == provider.ProtocolGemini {
				if r.Header.Get("X-Goog-Api-Key") != "local-fixture-secret" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
					t.Error("Gemini provider authorization was not translated")
				}
			} else if protocol == provider.ProtocolMessages {
				if r.Header.Get("X-Api-Key") != "local-fixture-secret" || r.Header.Get("Authorization") != "" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
					t.Error("native provider authorization was not translated")
				}
			} else if r.Header.Get("Authorization") != "Bearer local-fixture-secret" || r.Header.Get("X-Api-Key") != "" {
				t.Error("provider authorization was not translated")
			}
			handler(w, r)
		}))
		t.Cleanup(upstream.Close)
		basePrefix := "/v1"
		if protocol == provider.ProtocolGemini {
			basePrefix = "/v1beta"
		}
		channel, err := s.Catalog.SaveChannel(ctx, operator, "", catalog.ChannelInput{Name: fmt.Sprintf("upstream-%d", i), BaseURL: upstream.URL + basePrefix, Secret: "local-fixture-secret", Region: "global", AllowedCIDRs: []string{"127.0.0.0/8"}})
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, catalog.BindingInput{ChannelID: channel, UpstreamModel: "provider-model", Priority: len(handlers) - i, Weight: 1, MaxInflight: 4})
	}
	if _, err = s.Catalog.Publish(ctx, operator, f.model, 0, bindings); err != nil {
		t.Fatal(err)
	}
	credit, err := s.Billing.CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: operator, BusinessRef: uuid.NewString(), ProofRef: "receipt:gateway-fixture", Amount: 1000, IdempotencyKey: uuid.NewString(), RequestHMAC: "fixture-credit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Billing.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: approver}); err != nil {
		t.Fatal(err)
	}
	f.http = httptest.NewServer(s.Engine())
	t.Cleanup(f.http.Close)
	return f
}

func (f *gatewayFixture) request(t *testing.T, ctx context.Context, streaming, includeUsage bool) *http.Response {
	t.Helper()
	body := map[string]any{"model": "retail-model", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 20, "stream": streaming}
	if streaming {
		body["stream_options"] = map[string]bool{"include_usage": includeUsage}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.token)
	response, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

const gatewayJSON = `{"id":"local-result","object":"chat.completion","model":"provider-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
const gatewayContent = `{"id":"local-result","object":"chat.completion.chunk","model":"provider-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`
const gatewayFinish = `{"id":"local-result","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
const gatewayUsage = `{"id":"local-result","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

func gatewayValidateUpstream(t *testing.T, r *http.Request) {
	t.Helper()
	var req struct {
		Model         string `json:"model"`
		Stream        bool   `json:"stream"`
		MaxTokens     int64  `json:"max_tokens"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Errorf("upstream JSON: %v", err)
		return
	}
	if req.Model != "provider-model" || req.MaxTokens != 20 || (req.Stream && !req.StreamOptions.IncludeUsage) {
		t.Errorf("incorrect normalized request: %+v", req)
	}
	_, _ = io.Copy(io.Discard, r.Body)
}

func gatewaySuccess(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, gatewayJSON)
	}
}

func gatewayStream(t *testing.T, usage bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{gatewayContent, gatewayFinish} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
		if usage {
			// Identical final snapshots are common; the gateway must expose and
			// charge one final quantity rather than sum repeated snapshots.
			for range 2 {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", gatewayUsage)
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
}

type gatewayState struct {
	billing, reservation, reason, stage, usage string
	amount, available, held, spent, keyHeld    int64
	settlements, attempts, charge              int64
	content                                    bool
}

func (f *gatewayFixture) state(t *testing.T, request string) gatewayState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	v := gatewayState{}
	err := f.pool.QueryRow(ctx, `SELECT r.billing_status,v.status,v.amount,coalesce(z.transport_end_reason,''),coalesce(z.delivery_stage,''),coalesce(z.content_written,false),coalesce(m.retail_usage_state,''),(SELECT count(*) FROM settlements WHERE request_id=r.id),(SELECT count(*) FROM upstream_attempts WHERE request_id=r.id),coalesce((SELECT charged_amount FROM settlements WHERE request_id=r.id),0) FROM request_records r JOIN reservations v ON v.request_id=r.id LEFT JOIN finalization_records z ON z.id=r.latest_finalization_id LEFT JOIN metering_events m ON m.id=z.event_id WHERE r.id=$1`, request).Scan(&v.billing, &v.reservation, &v.amount, &v.reason, &v.stage, &v.content, &v.usage, &v.settlements, &v.attempts, &v.charge)
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := f.server.Billing.Wallet(ctx, billing.Query{TenantID: f.tenant})
	if err != nil {
		t.Fatal(err)
	}
	v.available, v.held = int64(wallet.Available), int64(wallet.Held)
	if err = f.pool.QueryRow(ctx, "SELECT spent,held FROM api_keys WHERE id=$1", f.key).Scan(&v.spent, &v.keyHeld); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *gatewayFixture) assertSettled(t *testing.T, request string, attempts int64) {
	t.Helper()
	v := f.state(t, request)
	// Input 10 * 1 + output 5 * 2 = 20 microcurrency units. Hold was 140.
	if v.billing != "settled" || v.reservation != "settled" || v.amount != 140 || v.charge != 20 || v.settlements != 1 || v.attempts != attempts || v.available != 980 || v.held != 0 || v.spent != 20 || v.keyHeld != 0 || v.usage != "confirmed_final" {
		t.Fatalf("incorrect final charge/hold: %+v", v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ledger, err := f.server.Billing.VerifyLedger(ctx, billing.Query{})
	if err != nil || !ledger.Balanced {
		t.Fatalf("unbalanced ledger: %+v %v", ledger, err)
	}
}

func TestGatewayPostgresNonStreamChargesFinalUsage(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := f.request(t, ctx, false, false)
	body, err := io.ReadAll(r.Body)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("HTTP %d %s %v", r.StatusCode, body, err)
	}
	var result struct {
		Model string `json:"model"`
		Usage struct {
			Total int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &result) != nil || result.Model != "retail-model" || result.Usage.Total != 15 || strings.Contains(string(body), "local-fixture-secret") {
		t.Fatalf("invalid public response: %s", body)
	}
	f.assertSettled(t, r.Header.Get("X-Request-ID"), 1)
	if f.posts.Load() != 1 {
		t.Fatal("ordinary completion was replayed")
	}
}

func TestGatewayPostgresStreamUsageVisibilityAndCharging(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(fmt.Sprintf("include_usage_%t", include), func(t *testing.T) {
			f := newGatewayFixture(t, []http.HandlerFunc{gatewayStream(t, true)}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := f.request(t, ctx, true, include)
			body, err := io.ReadAll(r.Body)
			if err != nil || r.StatusCode != 200 || !strings.HasSuffix(string(body), "data: [DONE]\n\n") {
				t.Fatalf("HTTP %d %s %v", r.StatusCode, body, err)
			}
			wantUsage := 0
			if include {
				wantUsage = 1
			}
			if strings.Count(string(body), `"usage":`) != wantUsage || strings.Contains(string(body), "provider-model") {
				t.Fatalf("usage/model exposure mismatch: %s", body)
			}
			f.assertSettled(t, r.Header.Get("X-Request-ID"), 1)
			if f.posts.Load() != 1 {
				t.Fatal("stream completion was replayed")
			}
		})
	}
}

func TestGatewayPostgresMissingUsageNeverSendsSuccessDone(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewayStream(t, false), gatewayStream(t, true)}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := f.request(t, ctx, true, true)
	body, err := io.ReadAll(r.Body)
	if err != nil || strings.Contains(string(body), "[DONE]") || !strings.Contains(string(body), "billing_pending") {
		t.Fatalf("unconfirmed usage reported success: %s %v", body, err)
	}
	v := f.state(t, r.Header.Get("X-Request-ID"))
	if v.billing != "pending_reconciliation" || v.reservation != "pending_reconciliation" || v.settlements != 0 || v.attempts != 1 || !v.content || v.usage != "missing" || v.available != 860 || v.held != 140 || v.spent != 0 || v.keyHeld != 140 || f.posts.Load() != 1 {
		t.Fatalf("missing usage hold/retry policy: %+v posts=%d", v, f.posts.Load())
	}
}

func TestGatewayPostgresClientCancellationKeepsUnknownUsagePending(t *testing.T) {
	upstreamClosed := make(chan struct{})
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", gatewayContent)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(upstreamClosed)
		case <-time.After(5 * time.Second):
			t.Error("gateway did not close cancelled upstream socket")
		}
	}, gatewaySuccess(t)}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := f.request(t, ctx, true, true)
	line, err := bufio.NewReader(r.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, "hello") {
		t.Fatalf("initial content missing: %s %v", line, err)
	}
	requestID := r.Header.Get("X-Request-ID")
	cancel()
	_ = r.Body.Close()
	select {
	case <-upstreamClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream cancellation was not propagated")
	}
	var v gatewayState
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v = f.state(t, requestID)
		if v.billing == "pending_reconciliation" && f.server.Streams.Load() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if v.billing != "pending_reconciliation" || v.reason != "client_cancelled" || v.settlements != 0 || v.attempts != 1 || v.usage != "missing" || v.held != 140 || v.spent != 0 || f.posts.Load() != 1 || f.server.Streams.Load() != 0 || len(f.server.global) != 0 {
		t.Fatalf("cancelled request state/resources: %+v posts=%d streams=%d permits=%d", v, f.posts.Load(), f.server.Streams.Load(), len(f.server.global))
	}
}

func TestGatewayPostgresSafeFailoverHasOneChargeAndTwoPosts(t *testing.T) {
	rejected := func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
	}
	f := newGatewayFixture(t, []http.HandlerFunc{rejected, gatewaySuccess(t), gatewaySuccess(t)}, []int{429})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := f.request(t, ctx, false, false)
	body, err := io.ReadAll(r.Body)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("HTTP %d %s %v", r.StatusCode, body, err)
	}
	f.assertSettled(t, r.Header.Get("X-Request-ID"), 2)
	if f.posts.Load() != 2 {
		t.Fatalf("POST count=%d want2", f.posts.Load())
	}
}

func TestGatewayPostgresRetryBudgetAndUnsafeFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		noConsumption []int
		wantAttempts  int64
	}{
		{"safe_rejection_budget", 429, []int{429}, 2},
		{"unknown_consumption_500", 500, nil, 1},
		{"unverified_rejection_429", 429, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failed := func(w http.ResponseWriter, r *http.Request) {
				gatewayValidateUpstream(t, r)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"message":"local-fixture-secret"}}`)
			}
			f := newGatewayFixture(t, []http.HandlerFunc{failed, failed, gatewaySuccess(t)}, tc.noConsumption)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := f.request(t, ctx, false, false)
			body, err := io.ReadAll(r.Body)
			if err != nil || r.StatusCode < 400 || strings.Contains(string(body), "local-fixture-secret") {
				t.Fatalf("HTTP %d %s %v", r.StatusCode, body, err)
			}
			v := f.state(t, r.Header.Get("X-Request-ID"))
			if v.billing != "released" || v.settlements != 1 || v.charge != 0 || v.attempts != tc.wantAttempts || int64(f.posts.Load()) != tc.wantAttempts || v.available != 1000 || v.held != 0 || v.spent != 0 || v.keyHeld != 0 {
				t.Fatalf("retry budget/release mismatch: %+v posts=%d", v, f.posts.Load())
			}
		})
	}
}
