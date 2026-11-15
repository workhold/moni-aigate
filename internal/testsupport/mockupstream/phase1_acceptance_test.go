package mockupstream_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routecost"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

func phase1Candidate(id string, priority int, weight uint64) routing.Candidate {
	schema, err := metering.ParseSchema([]byte(`{"schema_id":"10000000-0000-4000-8000-000000000001","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"9223372036854775807"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"9223372036854775807"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`))
	if err != nil {
		panic(err)
	}
	quote, err := routecost.NewQuote(routecost.Input{ChannelID: id, UpstreamModel: "qa-text", Basis: "unknown", VariantKey: "default", Schema: schema})
	if err != nil {
		panic(err)
	}
	return routing.Candidate{
		ChannelID: id, VersionID: "phase1-v1", CredentialID: id + "-credential",
		Model: "qa-text", Region: "global", HealthKey: id + ":qa-text",
		Priority: priority, Weight: weight, MaxInflight: 10, Enabled: true,
		CostPolicy: routing.DefaultCostPolicy(), CostQuote: quote,
		InputUpperBound: 100, OutputLimit: 20,
	}
}

func phase1Plan(t *testing.T, candidates ...routing.Candidate) *routing.Plan {
	t.Helper()
	plan, err := routing.NewPlan(candidates, 2)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func phase1Admit(context.Context, routing.Candidate, int) error { return nil }

func TestPhase1ThreeToOneWeightDistribution(t *testing.T) {
	candidates := []routing.Candidate{phase1Candidate("a", 10, 3), phase1Candidate("b", 10, 1)}
	draws := 0
	random := rand.New(rand.NewPCG(31, 2026))
	engine := routing.NewEngine(routing.Config{RandN: func(n uint64) uint64 {
		if n == 4 {
			draws++
		}
		return random.Uint64N(n)
	}})
	counts := map[string]int{}
	const requests = 4000
	for i := 0; i < requests; i++ {
		plan := phase1Plan(t, candidates...)
		attempt, err := plan.Start(context.Background(), engine, phase1Admit)
		if err != nil {
			t.Fatal(err)
		}
		counts[attempt.Candidate.ChannelID]++
		if plan.Attempts() != 1 {
			t.Fatalf("request %d used %d attempts", i, plan.Attempts())
		}
		attempt.Finish(routing.Neutral)
	}
	// Four standard errors at p=0.75, N=4000 is about 110 requests. A fixed
	// PCG seed makes the statistical assertion reproducible without replacing
	// the production selector or forcing individual routing decisions.
	const expected, tolerance = 3000, 120
	difference := counts["a"] - expected
	if difference < 0 {
		difference = -difference
	}
	if draws != requests || counts["a"]+counts["b"] != requests || difference > tolerance {
		t.Fatalf("3:1 distribution or one-attempt invariant failed: draws=%d counts=%v tolerance=%d", draws, counts, tolerance)
	}
}

func phase1ChatRequest(t *testing.T, nonce string, stream bool) *provider.Request {
	t.Helper()
	profile := providerProfile(provider.ProtocolChat)
	profile.NoConsumptionStatuses = []int{429}
	body := fmt.Sprintf(`{"model":"qa-text","messages":[{"role":"user","content":"phase1 [[mock:nonce=%s]]"}],"max_tokens":2,"stream":%t}`, nonce, stream)
	request, err := provider.Parse(provider.ProtocolChat, []byte(body), profile)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestPhase1TrustedRejectionAThenBHasTwoReceipts(t *testing.T) {
	service, err := mockupstream.New(mockupstream.Config{Candidates: map[string]mockupstream.Candidate{
		"a": {Secret: "a-secret", Models: []string{"qa-text"}, OutputTokens: 8, DefaultFault: "reject_429"},
		"b": {Secret: "b-secret", Models: []string{"qa-text"}, OutputTokens: 8},
	}}, mockupstream.Options{AllowDirectives: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	a := phase1Candidate("a", 2, 1)
	a.Endpoint = endpointFor(t, server.URL+"/c/a/v1/chat/completions", "qa-text", "a-secret")
	b := phase1Candidate("b", 1, 1)
	b.Endpoint = endpointFor(t, server.URL+"/c/b/v1/chat/completions", "qa-text", "b-secret")
	plan := phase1Plan(t, a, b)
	engine := routing.NewEngine(routing.Config{})
	client := provider.NewClient(provider.ClientOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := phase1ChatRequest(t, "trusted-a-b", false)

	first, err := plan.Start(ctx, engine, phase1Admit)
	if err != nil {
		t.Fatal(err)
	}
	_, callErr := client.Do(ctx, first.Candidate.Endpoint, request)
	first.Finish(routing.Neutral)
	if callErr == nil || !provider.SafeToRetry(callErr) || !plan.CanRetry(ctx, callErr) {
		t.Fatalf("trusted rejection was not safe to retry: %v", callErr)
	}
	second, err := plan.Start(ctx, engine, phase1Admit)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(ctx, second.Candidate.Endpoint, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = response.DecodeJSON(); err != nil {
		t.Fatal(err)
	}
	second.Finish(routing.Success)
	if plan.Attempts() != 2 || plan.CanRetry(ctx, callErr) {
		t.Fatalf("attempt budget exceeded: attempts=%d", plan.Attempts())
	}
	if _, err = plan.Start(ctx, engine, phase1Admit); !errors.Is(err, routing.ErrAttemptBudget) {
		t.Fatalf("third attempt was admitted: %v", err)
	}
	receipts := service.Receipts(0)
	if len(receipts) != 2 || receipts[0].Candidate != "a" || receipts[1].Candidate != "b" || receipts[0].Nonce != "trusted-a-b" || receipts[1].Nonce != "trusted-a-b" {
		t.Fatalf("trusted a->b must have exactly two ordered receipts: %#v", receipts)
	}
}

func TestPhase1ConnectionRefusedUsesAtMostTwoAttempts(t *testing.T) {
	refusedAddress := func() string {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		if err = listener.Close(); err != nil {
			t.Fatal(err)
		}
		return address
	}
	service, err := mockupstream.New(mockupstream.Config{Candidates: map[string]mockupstream.Candidate{
		"b": {Secret: "b-secret", Models: []string{"qa-text"}, OutputTokens: 8},
	}}, mockupstream.Options{AllowDirectives: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	firstCandidate := phase1Candidate("refused-a", 3, 1)
	firstCandidate.Endpoint = endpointFor(t, "http://"+refusedAddress()+"/c/a/v1/chat/completions", "qa-text", "a-secret")
	secondCandidate := phase1Candidate("refused-b", 2, 1)
	secondCandidate.Endpoint = endpointFor(t, "http://"+refusedAddress()+"/c/b/v1/chat/completions", "qa-text", "b-secret")
	standby := phase1Candidate("b", 1, 1)
	standby.Endpoint = endpointFor(t, server.URL+"/c/b/v1/chat/completions", "qa-text", "b-secret")
	plan := phase1Plan(t, firstCandidate, secondCandidate, standby)
	engine := routing.NewEngine(routing.Config{})
	client := provider.NewClient(provider.ClientOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := phase1ChatRequest(t, "connection-refused", false)

	for sequence := 1; sequence <= 2; sequence++ {
		attempt, err := plan.Start(ctx, engine, phase1Admit)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Sequence != sequence {
			t.Fatalf("attempt sequence=%d, want %d", attempt.Sequence, sequence)
		}
		_, callErr := client.Do(ctx, attempt.Candidate.Endpoint, request)
		attempt.Finish(routing.Neutral)
		if callErr == nil || !provider.SafeToRetry(callErr) {
			t.Fatalf("connection refusal %d was not classified not-sent: %v", sequence, callErr)
		}
		if sequence < 2 && !plan.CanRetry(ctx, callErr) {
			t.Fatalf("first not-sent refusal did not permit the second attempt: %v", callErr)
		}
		if sequence == 2 && plan.CanRetry(ctx, callErr) {
			t.Fatal("second not-sent refusal permitted a third attempt")
		}
	}
	if plan.Attempts() != 2 {
		t.Fatalf("connection-refused request used %d attempts, want exactly two", plan.Attempts())
	}
	if _, err = plan.Start(ctx, engine, phase1Admit); !errors.Is(err, routing.ErrAttemptBudget) {
		t.Fatalf("third attempt was admitted after two connection refusals: %v", err)
	}
	if receipts := service.Receipts(0); len(receipts) != 0 {
		t.Fatalf("third healthy candidate was dispatched after the two-attempt budget: %#v", receipts)
	}
}

func TestPhase1StreamFaultsHaveOneReceiptAndNoReplay(t *testing.T) {
	for _, tc := range []struct {
		fault, wantProtocol, wantErrorCode string
		wantUsage                          provider.UsageStatus
		wantEvents                         int
		wantContent                        bool
	}{
		{fault: "drop_after_events:2", wantProtocol: "failed", wantUsage: provider.UsageMissing, wantEvents: 2, wantContent: true},
		{fault: "error_after_usage", wantProtocol: "failed", wantUsage: provider.UsageProvisional, wantContent: true},
		{fault: "conflicting_usage", wantProtocol: "completed", wantUsage: provider.UsageConflicting, wantContent: true},
		{fault: "stall_first_event", wantProtocol: "failed", wantUsage: provider.UsageMissing, wantErrorCode: "upstream_first_event_timeout"},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			service, err := mockupstream.New(mockupstream.Config{Candidates: map[string]mockupstream.Candidate{
				"a": {Secret: "a-secret", Models: []string{"qa-text"}, OutputTokens: 8},
			}}, mockupstream.Options{AllowDirectives: true})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(service.Handler())
			defer server.Close()
			client := provider.NewClient(provider.ClientOptions{ConnectTimeout: time.Second, HeaderTimeout: time.Second, FirstEventTimeout: 75 * time.Millisecond, IdleTimeout: time.Second, ProgressTimeout: time.Second})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			profile := providerProfile(provider.ProtocolChat)
			body := fmt.Sprintf(`{"model":"qa-text","messages":[{"role":"user","content":"phase1 [[mock:nonce=fault-%s]] [[mock:fault=%s]]"}],"max_tokens":2,"stream":true}`, tc.fault, tc.fault)
			request, err := provider.Parse(provider.ProtocolChat, []byte(body), profile)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(ctx, endpointFor(t, server.URL+"/c/a/v1/chat/completions", "qa-text", "a-secret"), request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Close()
			events := 0
			sawContent := false
			var terminal error
			for {
				event, readErr := response.ReadEvent()
				if event != nil {
					events++
					sawContent = sawContent || event.Content
				}
				if readErr != nil {
					terminal = readErr
					break
				}
			}
			if tc.wantProtocol == "completed" {
				if !errors.Is(terminal, io.EOF) {
					t.Fatalf("conflicting usage did not reach a clean protocol EOF: %v", terminal)
				}
			} else {
				if terminal == nil || errors.Is(terminal, io.EOF) || provider.SafeToRetry(terminal) {
					t.Fatalf("fault was successful or retryable: %v", terminal)
				}
			}
			if tc.wantErrorCode != "" {
				var providerErr *provider.Error
				if !errors.As(terminal, &providerErr) || providerErr.Code != tc.wantErrorCode {
					t.Fatalf("fault error = %v, want code %s", terminal, tc.wantErrorCode)
				}
			}
			if tc.wantEvents != 0 && events != tc.wantEvents {
				t.Fatalf("drop_after_events delivered %d events, want %d", events, tc.wantEvents)
			}
			if tc.wantContent && !sawContent {
				t.Fatal("fault directive did not produce a commit-capable content event")
			}
			if response.ProtocolOutcome != tc.wantProtocol || response.Usage.Status != tc.wantUsage {
				t.Fatalf("terminal mismatch: outcome=%s usage=%s error=%v", response.ProtocolOutcome, response.Usage.Status, terminal)
			}
			if response.Usage.Status == provider.UsageConflicting {
				if _, err = provider.UsageJSON(response.Usage); err == nil {
					t.Fatal("conflicting usage crossed the settlement evidence boundary")
				}
			}
			receipts := service.Receipts(0)
			if len(receipts) != 1 || receipts[0].Candidate != "a" || receipts[0].Nonce != "fault-"+tc.fault || receipts[0].Fault != tc.fault {
				t.Fatalf("possibly-consumed fault was replayed or lost: %#v", receipts)
			}
		})
	}
}
