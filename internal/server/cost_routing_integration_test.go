package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func publishCostTestRoute(t *testing.T, f *gatewayFixture, policy routing.CostPolicy, caps map[string]*string) []string {
	t.Helper()
	channels := probeChannels(t, f)
	bindings := make([]catalog.BindingInput, 0, len(channels))
	for _, ch := range channels {
		bindings = append(bindings, catalog.BindingInput{ChannelID: ch, UpstreamModel: "provider-model", Priority: 0, Weight: 1, MaxInflight: 4, MaxInputCostRate: caps[ch], MaxOutputCostRate: caps[ch]})
	}
	if _, err := f.server.Catalog.PublishWithCostPolicy(context.Background(), "cost-test", f.model, 1, bindings, policy); err != nil {
		t.Fatal(err)
	}
	return channels
}

func TestCostRoutingSelectsLowerCostAndKeepsRetailPrice(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t), gatewaySuccess(t), gatewaySuccess(t)}, nil)
	channels := publishCostTestRoute(t, f, routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}, nil)
	for i, rate := range []int64{9000000, 1000000} {
		publishCostFixture(t, f, channels[i], "provider-model", "contract", costRate(rate), costRate(rate), time.Time{})
	}
	response := f.request(t, context.Background(), false, false)
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || strings.Contains(string(raw), "cost_version") || strings.Contains(string(raw), "contract") {
		t.Fatal(response.StatusCode, string(raw))
	}
	state := f.state(t, response.Header.Get("X-Request-ID"))
	if state.charge != 20 || state.spent != 20 || state.held != 0 || state.attempts != 1 || f.posts.Load() != 1 {
		t.Fatal("cost strategy changed retail funds", state)
	}
	var selected, cost, basis string
	var decisions []byte
	if err := f.pool.QueryRow(context.Background(), `SELECT a.channel_id,a.cost_amount::text,s.cost_basis,a.route_decisions FROM upstream_attempts a JOIN attempt_meter_snapshots s ON s.attempt_id=a.id WHERE a.request_id=$1`, response.Header.Get("X-Request-ID")).Scan(&selected, &cost, &basis, &decisions); err != nil || selected != channels[1] || cost != "15" || basis != "contract" {
		t.Fatal("cheaper procurement channel not used", selected, cost, basis, err)
	}
	var ds []routing.Decision
	if json.Unmarshal(decisions, &ds) != nil {
		t.Fatal("invalid explanations")
	}
	reasons := map[string]string{}
	for _, d := range ds {
		reasons[d.ChannelID] = d.Reason
	}
	if reasons[channels[0]] != "higher_cost" || reasons[channels[1]] != "selected" || reasons[channels[2]] != "cost_unknown" {
		t.Fatal(reasons)
	}
	if v, err := f.server.Billing.VerifyLedger(context.Background(), billing.Query{}); err != nil || !v.Balanced {
		t.Fatal(v, err)
	}
}

func TestCostRoutingUnknownAndRateCapsApplyToAllSixProtocols(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(http.ResponseWriter, *http.Request) { t.Error("cost-excluded route reached supplier") }}, nil, 8, protocol)
			cap := "0"
			channels := probeChannels(t, f)
			publishCostTestRoute(t, f, routing.DefaultCostPolicy(), map[string]*string{channels[0]: &cap})
			path, raw := keyPolicyProtocolInput(t, protocol)
			call := func() *httptest.ResponseRecorder {
				return keyPolicyHTTP(t, f.server.Engine(), "POST", path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
			}
			for _, stage := range []string{"unknown", "over-cap"} {
				if stage == "over-cap" {
					publishCostFixture(t, f, channels[0], "provider-model", "contract", costRate(1), costRate(0), time.Time{})
				}
				got := call()
				if got.Code != 503 || strings.Contains(got.Body.String(), "cost_version") || f.posts.Load() != 0 {
					t.Fatal(stage, got.Code, got.Body.String(), f.posts.Load())
				}
				// Cost-only exclusion is detected by routing after Reserve; it
				// must release that request rather than retain customer funds.
				var held, spent int64
				if err := f.pool.QueryRow(context.Background(), "SELECT held,spent FROM api_keys WHERE id=$1", f.key).Scan(&held, &spent); err != nil || held != 0 || spent != 0 {
					t.Fatal("cost exclusion stranded customer funds", held, spent, err)
				}
			}
		})
	}
}

func TestCostRoutingQueuedQuoteChangeDoesNotSendOrReplan(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t), gatewaySuccess(t)}, nil)
	channels := publishCostTestRoute(t, f, routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}, nil)
	for i, ch := range channels {
		publishCostFixture(t, f, ch, "provider-model", "contract", costRate(int64(i+1)*1000000), costRate(int64(i+1)*1000000), time.Time{})
	}
	procurementMeterApplication(t, f)
	forbidScalarQuoteReads(t, f)
	f.http.Close()
	f.http = httptest.NewServer(f.server.Engine())
	t.Cleanup(f.http.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := f.server.admission.acquire(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan probeHTTPResult, 1)
	path, raw := keyPolicyProtocolInput(t, provider.ProtocolChat)
	req, err := http.NewRequestWithContext(ctx, "POST", f.http.URL+path, strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	go func() {
		response, err := f.http.Client().Do(req)
		if err != nil {
			done <- probeHTTPResult{err: err}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		done <- probeHTTPResult{status: response.StatusCode, body: body, err: err}
	}()
	admissionAwait(t, "quoted request in admission queue", func() bool { return admissionQueueDepth(&f.server.admission, f.key) == 2 })
	publishCostFixture(t, f, channels[0], "provider-model", "contract", costRate(9000000), costRate(9000000), time.Time{})
	release()
	got := <-done
	if got.err != nil || got.status != 503 || !strings.Contains(string(got.body), "cost_quote_stale") || f.posts.Load() != 0 {
		t.Fatal("stale quote sent/replanned", got, f.posts.Load())
	}
	var attempts, held, spent int64
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM upstream_attempts),held,spent FROM api_keys WHERE id=$1`, f.key).Scan(&attempts, &held, &spent); err != nil || attempts != 0 || held != 0 || spent != 0 {
		t.Fatal("stale quote financial cleanup", attempts, held, spent, err)
	}
}

func TestCostVersionHTTPStrictCASAndSafeProjection(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	session, csrf := probeAdmin(t, f)
	channel := probeChannels(t, f)[0]
	var revision string
	if err := f.pool.QueryRow(context.Background(), "SELECT management_revision::text FROM channels WHERE id=$1", channel).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	var sid, digest string
	if err := f.pool.QueryRow(context.Background(), `SELECT schema_id,encode(schema_digest,'hex') FROM model_capability_versions WHERE model_id=$1`, f.model).Scan(&sid, &digest); err != nil {
		t.Fatal(err)
	}
	valid := fmt.Sprintf(`{"operation_id":%q,"channel_id":%q,"schema_id":%q,"schema_digest":%q,"variant_key":"default","upstream_model":"provider-model","input_rate":"9223372036854775807","output_rate":"0","basis":"contract","effective_at":"2026-11-01T00:00:00.000001Z","expected_channel_revision":%q}`, uuid.NewString(), channel, sid, digest, revision)
	call := func(raw string) *httptest.ResponseRecorder {
		f.server.pricing = pricingAdmission{}
		return keyPolicyHTTP(t, f.server.Engine(), "POST", "/api/platform/cost-versions", raw, session, csrf, f.server.Config.Origin, "", nil)
	}
	for _, raw := range []string{
		strings.Replace(valid, fmt.Sprintf(`"schema_id":%q,`, sid), "", 1),
		strings.Replace(valid, fmt.Sprintf(`"schema_digest":%q`, digest), `"schema_digest":"bad"`, 1),
		strings.Replace(valid, `"variant_key":"default"`, `"variant_key":"other"`, 1),
		strings.Replace(valid, `"basis":"contract"`, `"basis":"contract","basis":"invoice"`, 1),
		strings.Replace(valid, `"basis":"contract"`, `"BASIS":"contract"`, 1),
		strings.Replace(valid, `"basis":"contract"`, `"basis":"contract","secret":"marker"`, 1),
		strings.Replace(valid, `"output_rate":"0"`, `"output_rate":0`, 1),
		strings.Replace(valid, `"output_rate":"0"`, `"output_rate":"00"`, 1),
		strings.Replace(valid, `"output_rate":"0"`, `"output_rate":"-1"`, 1),
		valid + `{}`,
	} {
		if got := call(raw); got.Code != 400 {
			t.Fatal("noncanonical cost input accepted", got.Code, got.Body.String())
		}
	}
	got := call(valid)
	if got.Code != 201 || !strings.Contains(got.Body.String(), "channel_revision") || !strings.Contains(got.Body.String(), sid) || !strings.Contains(got.Body.String(), digest) {
		t.Fatal("valid cost not published", got.Code, got.Body.String())
	}
	if got = call(valid); got.Code != 200 {
		t.Fatal("same cost operation did not replay original receipt", got.Code)
	}
	view := authHTTP(t, f.server, "GET", "/api/platform/cost-versions", session, csrf, "", nil)
	if view.Code != 200 || !strings.Contains(view.Body.String(), `"rates"`) || strings.Contains(view.Body.String(), `"input_rate"`) || strings.Contains(view.Body.String(), `"output_rate"`) || !strings.Contains(view.Body.String(), sid) || !strings.Contains(view.Body.String(), digest) || strings.Contains(view.Body.String(), "local-fixture-secret") {
		t.Fatal("cost projection lost precision or leaked secrets", view.Code, view.Body.String())
	}
	models := authHTTP(t, f.server, "GET", "/api/platform/models", session, csrf, "", nil)
	if models.Code != 200 || !strings.Contains(models.Body.String(), sid) || !strings.Contains(models.Body.String(), digest) || !strings.Contains(models.Body.String(), `"variant_key":"default"`) {
		t.Fatal("model selector omitted immutable cost domain", models.Code, models.Body.String())
	}
}

func TestCostQuoteActivationDuringCredentialWaitRecordsNotSentZeroCost(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	channels := publishCostTestRoute(t, f, routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}, nil)
	publishCostFixture(t, f, channels[0], "provider-model", "contract", costRate(1000000), costRate(1000000), time.Time{})
	procurementMeterApplication(t, f)
	forbidScalarQuoteReads(t, f)
	f.http.Close()
	f.http = httptest.NewServer(f.server.Engine())
	t.Cleanup(f.http.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var effective time.Time
	costID := publishCostFixture(t, f, channels[0], "provider-model", "contract", costRate(9000000), costRate(9000000), time.Now().Add(time.Second))
	if err := f.pool.QueryRow(ctx, `SELECT effective_at FROM cost_versions WHERE id=$1`, costID).Scan(&effective); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var pid int
	if err = blocker.QueryRow(ctx, `SELECT pg_backend_pid() FROM channel_credentials WHERE channel_id=$1 FOR UPDATE`, channels[0]).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := admissionStartHTTP(f.http.Client(), admissionHTTPRequest(t, f, ctx, false))
	admissionAwait(t, "dispatch awaiting credential lock", func() bool {
		var blocked bool
		if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		return blocked
	})
	admissionAwait(t, "scheduled quote activation", func() bool {
		var active bool
		if err := f.pool.QueryRow(ctx, "SELECT clock_timestamp()>=$1", effective).Scan(&active); err != nil {
			t.Fatal(err)
		}
		return active
	})
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	got := admissionAwaitHTTP(t, done)
	if got.err != nil || got.response.StatusCode != 503 || f.posts.Load() != 0 {
		t.Fatal("quote changed before provider I/O", got.err, f.posts.Load())
	}
	var state, cost, reason string
	var charge, held, spent int64
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT a.delivery_state,a.cost_amount::text,f.transport_end_reason,s.charged_amount,k.held,k.spent,
 (SELECT count(*) FROM upstream_attempts) FROM upstream_attempts a
 JOIN request_records r ON r.id=a.request_id JOIN finalization_records f ON f.id=r.latest_finalization_id
 JOIN settlements s ON s.request_id=r.id JOIN api_keys k ON k.id=r.key_id`).Scan(&state, &cost, &reason, &charge, &held, &spent, &count); err != nil || state != "failed" || cost != "0" || reason != "not_sent" || charge != 0 || held != 0 || spent != 0 || count != 1 {
		t.Fatal("unsent admission left cost or funds unknown", state, cost, reason, charge, held, spent, count, err)
	}
}

func TestCostRouteHTTPStrictPublicationAndCustomerPrivacy(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	session, csrf := probeAdmin(t, f)
	channel := probeChannels(t, f)[0]
	valid := fmt.Sprintf(`{"expected_revision":1,"cost_policy":{"mode":"cost_first","unknown_cost_policy":"deny"},"bindings":[{"channel_id":%q,"upstream_model":"provider-model","priority":0,"weight":1,"max_inflight":4,"max_input_cost_rate":"9223372036854775807","max_output_cost_rate":"9223372036854775807"}]}`, channel)
	call := func(raw string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, f.server.Engine(), "PUT", "/api/platform/models/"+f.model+"/bindings", raw, session, csrf, f.server.Config.Origin, "", nil)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"mode":"cost_first"`, `"mode":"cost_first","mode":"manual"`, 1),
		strings.Replace(valid, `"unknown_cost_policy":"deny"`, `"unknown_cost_policy":"allow"`, 1),
		strings.Replace(valid, `"unknown_cost_policy":"deny"`, `"UNKNOWN_COST_POLICY":"deny"`, 1),
		strings.Replace(valid, `"weight":1`, `"weight":1,"cost_policy":null`, 1),
		strings.Replace(valid, `"mode":"cost_first"`, `"mode":"cost_first","max_inflight":4`, 1),
		strings.Replace(valid, `"max_output_cost_rate":"9223372036854775807"`, `"max_output_cost_rate":"-1"`, 1),
		strings.Replace(valid, `"max_output_cost_rate":"9223372036854775807"`, `"max_output_cost_rate":0`, 1),
		strings.Replace(valid, `"max_output_cost_rate":"9223372036854775807"`, `"max_output_cost_rate":"01"`, 1),
		valid + `{}`,
	} {
		if got := call(raw); got.Code != 400 {
			t.Fatal("noncanonical route accepted", got.Code, got.Body.String())
		}
	}
	if got := call(valid); got.Code != 200 {
		t.Fatal("cost route publication failed", got.Code, got.Body.String())
	}
	if got := call(valid); got.Code != 409 {
		t.Fatal("cost route old CAS accepted", got.Code)
	}
	adminView := authHTTP(t, f.server, "GET", "/api/platform/models", session, csrf, "", nil)
	if adminView.Code != 200 || !strings.Contains(adminView.Body.String(), `"cost_policy":{"mode":"cost_first","unknown_cost_policy":"deny"}`) || !strings.Contains(adminView.Body.String(), `"max_input_cost_rate":"9223372036854775807"`) {
		t.Fatal("route projection lost cost policy or rate precision", adminView.Code, adminView.Body.String())
	}
	ownerSession, ownerCSRF := keyPolicyOwner(t, f)
	for _, path := range []string{"/api/models", "/api/prices"} {
		got := authHTTP(t, f.server, "GET", path, ownerSession, ownerCSRF, "", nil)
		if got.Code != 200 || strings.Contains(got.Body.String(), "cost_policy") || strings.Contains(got.Body.String(), "max_input_cost_rate") || strings.Contains(got.Body.String(), "cost_version") {
			t.Fatal("customer projection leaked procurement settings", got.Code)
		}
	}
}
