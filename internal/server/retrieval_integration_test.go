package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

const retrievalEmbeddingJSON = `{"object":"list","model":"provider-model","data":[{"object":"embedding","index":0,"embedding":[0.1,-0.2],"debug":"private-vector"}],"usage":{"prompt_tokens":10,"total_tokens":10},"debug":"private-metadata"}`
const retrievalRerankJSON = `{"id":"rank_fixture","model":"provider-model","results":[{"index":1,"relevance_score":0.9,"document":{"text":"forged-document"}},{"index":0,"relevance_score":0.4}],"usage":{"total_tokens":10},"metadata":{"secret":"private-metadata"}}`

func retrievalBody(protocol string) map[string]any {
	if protocol == provider.ProtocolEmbeddings {
		return map[string]any{"model": "retail-model", "input": "hello", "encoding_format": "float", "dimensions": 2}
	}
	return map[string]any{"model": "retail-model", "query": "hello", "documents": []string{"first-original", "second-original"}, "top_n": 2, "return_documents": true}
}

func retrievalPath(protocol string) string {
	if protocol == provider.ProtocolEmbeddings {
		return "/v1/embeddings"
	}
	return "/v1/rerank"
}

func retrievalJSON(protocol string) string {
	if protocol == provider.ProtocolEmbeddings {
		return retrievalEmbeddingJSON
	}
	return retrievalRerankJSON
}

func retrievalHandler(t *testing.T, result string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		var request map[string]json.RawMessage
		if err != nil || json.Unmarshal(raw, &request) != nil || string(request["model"]) != `"provider-model"` || bytes.Contains(raw, []byte("mg_")) {
			t.Error("retrieval model/credential boundary failed")
		}
		for _, field := range []string{"stream", "stream_options", "max_tokens", "max_output_tokens", "tools"} {
			if _, exists := request[field]; exists {
				t.Errorf("generation field %s injected into retrieval", field)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, result)
	}
}

func retrievalResourcesReleased(t *testing.T, f *gatewayFixture) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.server.mu.Lock()
		released := len(f.server.global) == 0 && len(f.server.tenantPermits) == 0 && len(f.server.keyPermits) == 0
		f.server.mu.Unlock()
		if released && f.server.Streams.Load() == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("retrieval retained request permits")
		}
		time.Sleep(time.Millisecond)
	}
}

func retrievalAssertSettled(t *testing.T, f *gatewayFixture, request string) {
	t.Helper()
	retrievalResourcesReleased(t, f)
	v := f.state(t, request)
	if v.billing != "settled" || v.reservation != "settled" || v.amount != 100 || v.charge != 10 || v.settlements != 1 || v.attempts != 1 || v.available != 990 || v.held != 0 || v.spent != 10 || v.keyHeld != 0 || v.usage != "confirmed_final" || f.posts.Load() != 1 {
		t.Fatalf("retrieval final charge/hold: %+v posts=%d", v, f.posts.Load())
	}
	var input, output, total, outputLimit, outputRate int64
	err := f.pool.QueryRow(context.Background(), `SELECT meter_event_quantity(e.id,'text_input_token'),meter_event_quantity(e.id,'text_output_token'),meter_event_quantity(e.id,'text_input_token'),s.output_bound,meter_price_million(s.price_version_id,'text_output_token') FROM request_records r JOIN finalization_records z ON z.id=r.latest_finalization_id JOIN metering_events e ON e.id=z.event_id JOIN request_meter_snapshots s ON s.request_id=r.id WHERE r.id=$1`, request).Scan(&input, &output, &total, &outputLimit, &outputRate)
	if err != nil || input != 10 || output != 0 || total != 10 || outputLimit != 0 || outputRate != 0 {
		t.Fatalf("retrieval durable usage %d/%d/%d limit/rate=%d/%d err=%v", input, output, total, outputLimit, outputRate, err)
	}
	ledger, err := f.server.Billing.VerifyLedger(context.Background(), billing.Query{})
	if err != nil || !ledger.Balanced {
		t.Fatalf("retrieval ledger: %+v %v", ledger, err)
	}
}

func TestRetrievalCommercialRoundTrip(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolEmbeddings, provider.ProtocolRerank} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{retrievalHandler(t, retrievalJSON(protocol))}, nil, 8, protocol)
			res := nativeRequest(t, f, retrievalPath(protocol), retrievalBody(protocol), http.Header{"Authorization": []string{"Bearer " + f.token}})
			body, err := io.ReadAll(res.Body)
			if err != nil || res.StatusCode != 200 || !bytes.Contains(body, []byte(`"model":"retail-model"`)) || !bytes.Contains(body, []byte(`"total_tokens":10`)) {
				t.Fatalf("retrieval HTTP %d %s %v", res.StatusCode, body, err)
			}
			for _, secret := range []string{"provider-model", "private-metadata", "private-vector", "forged-document", "local-fixture-secret"} {
				if bytes.Contains(body, []byte(secret)) {
					t.Fatalf("retrieval response leaked %s", secret)
				}
			}
			if protocol == provider.ProtocolRerank && (!bytes.Contains(body, []byte("first-original")) || !bytes.Contains(body, []byte("second-original"))) {
				t.Fatal("rerank did not use original documents")
			}
			retrievalAssertSettled(t, f, res.Header.Get("X-Request-ID"))
		})
	}
}

func TestRetrievalEmbeddingBatchBase64RoundTrip(t *testing.T) {
	// AACAPwAAAMA= is little-endian float32 [1,-2].
	result := `{"object":"list","data":[{"object":"embedding","index":1,"embedding":"AACAPwAAAMA="},{"object":"embedding","index":0,"embedding":"AACAPwAAAMA="}],"usage":{"prompt_tokens":10,"total_tokens":10}}`
	f := newGatewayFixtureConfigured(t, []http.HandlerFunc{retrievalHandler(t, result)}, nil, 8, provider.ProtocolEmbeddings)
	body := retrievalBody(provider.ProtocolEmbeddings)
	body["input"], body["encoding_format"] = []string{"one", "two"}, "base64"
	res := nativeRequest(t, f, "/v1/embeddings", body, http.Header{"Authorization": []string{"Bearer " + f.token}})
	raw, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 200 || bytes.Count(raw, []byte("AACAPwAAAMA=")) != 2 {
		t.Fatalf("batch base64 HTTP %d %s %v", res.StatusCode, raw, err)
	}
	retrievalAssertSettled(t, f, res.Header.Get("X-Request-ID"))
}

func TestRetrievalUnknownUsageWithholdsResultAndKeepsHold(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolEmbeddings, provider.ProtocolRerank} {
		for _, usage := range []string{"missing", "conflicting", "unknown_dimension", "duplicate"} {
			t.Run(protocol+"/"+usage, func(t *testing.T) {
				var result map[string]json.RawMessage
				_ = json.Unmarshal([]byte(retrievalJSON(protocol)), &result)
				switch usage {
				case "missing":
					delete(result, "usage")
				case "conflicting":
					result["usage"] = json.RawMessage(`{"prompt_tokens":10,"total_tokens":11}`)
				case "unknown_dimension":
					result["usage"] = json.RawMessage(`{"prompt_tokens":10,"total_tokens":10,"search_units":1}`)
				}
				raw, _ := json.Marshal(result)
				if usage == "duplicate" {
					raw = append(raw[:len(raw)-1], []byte(`,"usage":{"prompt_tokens":10,"total_tokens":10}}`)...)
				}
				f := newGatewayFixtureConfigured(t, []http.HandlerFunc{retrievalHandler(t, string(raw))}, nil, 8, protocol)
				res := nativeRequest(t, f, retrievalPath(protocol), retrievalBody(protocol), http.Header{"Authorization": []string{"Bearer " + f.token}})
				body, err := io.ReadAll(res.Body)
				if err != nil || res.StatusCode != 503 || !bytes.Contains(body, []byte("billing_pending")) || bytes.Contains(body, []byte(`"embedding":`)) || bytes.Contains(body, []byte(`"results":`)) {
					t.Fatalf("unconfirmed retrieval HTTP %d %s %v", res.StatusCode, body, err)
				}
				retrievalResourcesReleased(t, f)
				v := f.state(t, res.Header.Get("X-Request-ID"))
				want := "conflicting"
				if usage == "missing" {
					want = "missing"
				}
				if v.billing != "pending_reconciliation" || v.reservation != "pending_reconciliation" || v.settlements != 0 || v.attempts != 1 || v.content || v.usage != want || v.available != 900 || v.held != 100 || v.spent != 0 || v.keyHeld != 100 || f.posts.Load() != 1 {
					t.Fatalf("unconfirmed retrieval hold: %+v posts=%d", v, f.posts.Load())
				}
			})
		}
	}
}

func TestRetrievalAdmissionRejectsBeforeFinancialState(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolEmbeddings, provider.ProtocolRerank} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) { t.Error("rejected retrieval dispatched") }}, nil, 8, protocol)
			header := http.Header{"Authorization": []string{"Bearer " + f.token}}
			for _, field := range []string{"stream", "max_tokens", "tools"} {
				body := retrievalBody(protocol)
				body[field] = false
				res := nativeRequest(t, f, retrievalPath(protocol), body, header)
				if res.StatusCode != 400 {
					t.Fatalf("%s accepted %s: %d", protocol, field, res.StatusCode)
				}
			}
			res := nativeRequest(t, f, "/v1/chat/completions", nativeBody(provider.ProtocolMessages, false), header)
			if res.StatusCode != 400 {
				t.Fatal("retrieval model accepted at generation endpoint", res.StatusCode)
			}
			res = nativeRequest(t, f, retrievalPath(protocol), retrievalBody(protocol), http.Header{})
			if res.StatusCode != 401 {
				t.Fatal("retrieval accepted unauthenticated", res.StatusCode)
			}
			if _, err := f.pool.Exec(context.Background(), "UPDATE api_keys SET scopes='{}' WHERE id=$1", f.key); err != nil {
				t.Fatal(err)
			}
			res = nativeRequest(t, f, retrievalPath(protocol), retrievalBody(protocol), header)
			if res.StatusCode != 403 {
				t.Fatal("retrieval accepted without model scope", res.StatusCode)
			}
			probeAssertWalletUntouched(t, f)
			if f.posts.Load() != 0 {
				t.Fatal("rejected retrieval posted upstream")
			}
		})
	}
}

func TestRetrievalIdempotencyPreventsSecondPostAndCharge(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolEmbeddings, provider.ProtocolRerank} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{retrievalHandler(t, retrievalJSON(protocol))}, nil, 8, protocol)
			header := http.Header{"Authorization": []string{"Bearer " + f.token}, "Idempotency-Key": []string{"retrieval-same-request"}}
			first := nativeRequest(t, f, retrievalPath(protocol), retrievalBody(protocol), header)
			_, _ = io.Copy(io.Discard, first.Body)
			if first.StatusCode != 200 {
				t.Fatal(first.StatusCode)
			}
			second := nativeRequest(t, f, retrievalPath(protocol), retrievalBody(protocol), header)
			body, _ := io.ReadAll(second.Body)
			if second.StatusCode != 409 || !bytes.Contains(body, []byte("request_exists")) || second.Header.Get("X-Original-Request-ID") != first.Header.Get("X-Request-ID") {
				t.Fatalf("retrieval duplicate HTTP %d %s", second.StatusCode, body)
			}
			retrievalAssertSettled(t, f, first.Header.Get("X-Request-ID"))
		})
	}
}

func TestRetrievalProbeVerifiesUsageWithoutCustomerFunds(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolEmbeddings, provider.ProtocolRerank} {
		for _, confirmed := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/confirmed=%t", protocol, confirmed), func(t *testing.T) {
				result := retrievalEmbeddingJSON
				if protocol == provider.ProtocolRerank {
					result = `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":10}}`
				}
				if !confirmed {
					var object map[string]json.RawMessage
					_ = json.Unmarshal([]byte(result), &object)
					delete(object, "usage")
					raw, _ := json.Marshal(object)
					result = string(raw)
				}
				f := newGatewayFixtureConfigured(t, []http.HandlerFunc{retrievalHandler(t, result)}, nil, 8, protocol)
				raw, csrf := probeAdmin(t, f)
				response := probeCall(context.Background(), f, probeChannels(t, f)[0], raw, csrf)
				if response.err != nil || response.status != 200 || !bytes.Contains(response.body, []byte(fmt.Sprintf(`"ok":%t`, confirmed))) {
					t.Fatalf("probe %d %s %v", response.status, response.body, response.err)
				}
				var status, usage string
				if err := f.pool.QueryRow(context.Background(), "SELECT status,usage_state FROM platform_probe_attempts").Scan(&status, &usage); err != nil {
					t.Fatal(err)
				}
				wantStatus, wantUsage := "completed", "confirmed_final"
				if !confirmed {
					wantStatus, wantUsage = "failed", "missing"
				}
				if status != wantStatus || usage != wantUsage || f.posts.Load() != 1 {
					t.Fatal("probe status", status, usage, f.posts.Load())
				}
				probeAssertWalletUntouched(t, f)
			})
		}
	}
}

func TestRetrievalCatalogRejectsGenerationConfigurationAndOutputPrice(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolEmbeddings, provider.ProtocolRerank} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) { t.Error("catalog test dispatched") }}, nil, 8, protocol)
			ctx := context.Background()
			if _, err := f.server.Catalog.PublishPrice(ctx, "catalog-test", f.model, "1", "1", time.Time{}); !errors.Is(err, catalog.ErrInvalid) {
				t.Fatal("input-only accepted output price", err)
			}
			for i, change := range []func(*provider.Profile){
				func(p *provider.Profile) { p.OutputLimit = 1 },
				func(p *provider.Profile) { p.DefaultOutputLimit = 1 },
				func(p *provider.Profile) { p.OutputLimitField = "max_tokens" },
				func(p *provider.Profile) { p.SupportsTools = true },
				func(p *provider.Profile) { p.SupportsParallelToolCalls = true },
			} {
				p := &provider.Profile{Protocol: protocol, InputTokenUpperBound: 100, ContextLimit: 100}
				change(p)
				_, err := f.server.Catalog.CreateModel(ctx, "catalog-test", catalog.ModelInput{PublicName: fmt.Sprintf("invalid-%d", i), Profile: p})
				if !errors.Is(err, catalog.ErrInvalid) {
					t.Fatalf("invalid retrieval configuration %d accepted: %v", i, err)
				}
			}
		})
	}
}

func TestRetrievalMalformedResponseDoesNotLeakOrRetry(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolEmbeddings, provider.ProtocolRerank} {
		t.Run(protocol, func(t *testing.T) {
			result := strings.Replace(retrievalJSON(protocol), `"index":0`, `"index":999`, 1)
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{retrievalHandler(t, result)}, nil, 8, protocol)
			res := nativeRequest(t, f, retrievalPath(protocol), retrievalBody(protocol), http.Header{"Authorization": []string{"Bearer " + f.token}})
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != 502 || bytes.Contains(body, []byte("private-metadata")) || bytes.Contains(body, []byte(`"embedding":`)) || bytes.Contains(body, []byte(`"results":`)) {
				t.Fatalf("malformed response HTTP %d %s", res.StatusCode, body)
			}
			retrievalResourcesReleased(t, f)
			v := f.state(t, res.Header.Get("X-Request-ID"))
			if v.billing != "released" || v.reservation != "released" || v.available != 1000 || v.held != 0 || v.spent != 0 || v.keyHeld != 0 || v.attempts != 1 || f.posts.Load() != 1 {
				t.Fatalf("malformed response release: %+v posts=%d", v, f.posts.Load())
			}
		})
	}
}
