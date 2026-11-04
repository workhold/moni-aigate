package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

const geminiFinalUsage = `{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"cachedContentTokenCount":3,"totalTokenCount":15}`
const geminiResponseJSON = `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"private-thought","thought":true,"thoughtSignature":"private-signature"},{"text":"hello"}]},"finishReason":"STOP","private":"private-candidate"}],"usageMetadata":` + geminiFinalUsage + `,"modelVersion":"provider-model","responseId":"gemini_fixture","metadata":{"secret":"private-metadata"}}`

func geminiBody() map[string]any {
	return map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}, "generationConfig": map[string]any{"maxOutputTokens": 20, "candidateCount": 1}}
}
func geminiPath(stream bool) string {
	if stream {
		return "/v1beta/models/retail-model:streamGenerateContent?alt=sse"
	}
	return "/v1beta/models/retail-model:generateContent"
}
func geminiStream(usage string) string {
	// The provider must split content and finish, then await the trailing usage.
	wire := "data: " + `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"modelVersion":"provider-model","responseId":"gemini_fixture"}` + "\n\n"
	if usage != "" {
		wire += "data: " + `{"usageMetadata":` + usage + `}` + "\n\n"
	}
	return wire
}
func geminiFixture(t *testing.T, handlers []http.HandlerFunc, noConsumption []int) *gatewayFixture {
	t.Helper()
	f := newGatewayFixtureConfigured(t, handlers, noConsumption, 8, provider.ProtocolGemini)
	f.http.Client().Timeout = 8 * time.Second
	return f
}
func geminiValidateUpstream(t *testing.T, r *http.Request, output int64) {
	t.Helper()
	defer func() { _, _ = io.Copy(io.Discard, r.Body); _ = r.Body.Close() }()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
		return
	}
	for _, field := range []string{"model", "stream", "messages", "max_tokens", "max_output_tokens", "tools"} {
		if _, exists := body[field]; exists {
			t.Errorf("non-native Gemini field %s", field)
		}
	}
	var config struct {
		Max int64 `json:"maxOutputTokens"`
		N   int64 `json:"candidateCount"`
	}
	if json.Unmarshal(body["generationConfig"], &config) != nil || config.Max != output || config.N != 1 {
		t.Errorf("Gemini output bound mismatch: %s", body["generationConfig"])
	}
	if strings.Contains(r.URL.RawQuery, "key") || r.Header.Get("X-Goog-Api-Key") != "local-fixture-secret" || r.Header.Get("Authorization") != "" {
		t.Error("Gemini credential boundary failed")
	}
}
func geminiHandler(t *testing.T, stream bool, result string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		geminiValidateUpstream(t, r, 20)
		media := "application/json"
		if stream {
			media = "text/event-stream"
		}
		w.Header().Set("Content-Type", media)
		_, _ = io.WriteString(w, result)
	}
}
func geminiAssertUsage(t *testing.T, f *gatewayFixture, id string, attempts int64) {
	t.Helper()
	retrievalResourcesReleased(t, f)
	f.assertSettled(t, id, attempts)
	var input, output, total, thoughts, cached int64
	err := f.pool.QueryRow(context.Background(), `SELECT (meter_event_usage(e.id)->>'input_tokens')::bigint,(meter_event_usage(e.id)->>'output_tokens')::bigint,(meter_event_usage(e.id)->>'total_tokens')::bigint,(meter_event_usage(e.id)->>'reasoning_tokens')::bigint,(meter_event_usage(e.id)->>'cached_tokens')::bigint FROM request_records r JOIN finalization_records z ON z.id=r.latest_finalization_id JOIN metering_events e ON e.id=z.event_id WHERE r.id=$1`, id).Scan(&input, &output, &total, &thoughts, &cached)
	if err != nil || input != 10 || output != 5 || total != 15 || thoughts != 2 || cached != 3 {
		t.Fatalf("Gemini durable usage %d/%d/%d thinking=%d cache=%d err=%v", input, output, total, thoughts, cached, err)
	}
}
func TestGeminiCommercialJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			wire := geminiResponseJSON
			if stream {
				wire = geminiStream(geminiFinalUsage)
			}
			f := geminiFixture(t, []http.HandlerFunc{geminiHandler(t, stream, wire)}, nil)
			res := nativeRequest(t, f, geminiPath(stream), geminiBody(), http.Header{"Authorization": []string{"Bearer " + f.token}})
			body, err := io.ReadAll(res.Body)
			if err != nil || res.StatusCode != 200 || bytes.Count(body, []byte("hello")) != 1 || !bytes.Contains(body, []byte(`"modelVersion":"retail-model"`)) || !bytes.Contains(body, []byte(`"thoughtsTokenCount":2`)) {
				t.Fatalf("Gemini HTTP %d %s %v", res.StatusCode, body, err)
			}
			for _, private := range []string{"private-", "provider-model", "local-fixture-secret", "[DONE]"} {
				if bytes.Contains(body, []byte(private)) {
					t.Fatalf("Gemini output leaked %s", private)
				}
			}
			if stream && bytes.Count(body, []byte(`"finishReason":"STOP"`)) != 1 {
				t.Fatal("Gemini terminal missing or repeated", string(body))
			}
			geminiAssertUsage(t, f, res.Header.Get("X-Request-ID"), 1)
		})
	}
}
func TestGeminiSSETerminalWaitsForTailUsageEOFAndSettlement(t *testing.T) {
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f := geminiFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		geminiValidateUpstream(t, r, 20)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiStream(""))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: "+`{"usageMetadata":`+geminiFinalUsage+"}\n\n")
	}}, nil)
	res := nativeRequest(t, f, geminiPath(true), geminiBody(), http.Header{"Authorization": []string{"Bearer " + f.token}})
	scanner := bufio.NewScanner(res.Body)
	var first string
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			first = scanner.Text()
			break
		}
	}
	if !strings.Contains(first, "hello") || strings.Contains(first, "finishReason") || strings.Contains(first, "usageMetadata") {
		t.Fatalf("Gemini prematurely delivered terminal or no content: %s %v", first, scanner.Err())
	}
	id := res.Header.Get("X-Request-ID")
	var settled int64
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM settlements WHERE request_id=$1", id).Scan(&settled); err != nil || settled != 0 {
		t.Fatal("Gemini settled before final usage/EOF", settled, err)
	}
	close(release)
	found := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"finishReason":"STOP"`) {
			found = true
			if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM settlements WHERE request_id=$1 AND status='settled'", id).Scan(&settled); err != nil || settled != 1 {
				t.Fatal("Gemini terminal escaped before settlement", settled, err)
			}
		}
	}
	if !found || scanner.Err() != nil {
		t.Fatal("Gemini terminal", found, scanner.Err())
	}
	geminiAssertUsage(t, f, id, 1)
}
func TestGeminiUnknownUsageKeepsHoldWithoutSuccessTerminal(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, mode := range []string{"missing", "mismatch", "unknown", "duplicate", "early_only", "hosted_tool"} {
			if !stream && mode == "early_only" {
				continue
			}
			t.Run(fmt.Sprintf("stream=%t/%s", stream, mode), func(t *testing.T) {
				usage := geminiFinalUsage
				switch mode {
				case "missing", "early_only":
					usage = ""
				case "mismatch":
					usage = strings.Replace(usage, `"totalTokenCount":15`, `"totalTokenCount":16`, 1)
				case "unknown":
					usage = strings.TrimSuffix(usage, "}") + `,"searchUnits":1}`
				}
				var wire string
				if stream {
					wire = geminiStream(usage)
					if mode == "early_only" {
						wire = "data: " + `{"usageMetadata":` + geminiFinalUsage + "}\n\n" + wire
					}
					if mode == "duplicate" {
						wire = strings.Replace(wire, `{"usageMetadata":`+geminiFinalUsage+`}`, `{"usageMetadata":`+geminiFinalUsage+`,"usageMetadata":`+geminiFinalUsage+`}`, 1)
					}
				} else {
					wire = geminiResponseJSON
					if mode == "missing" {
						wire = strings.Replace(wire, `,"usageMetadata":`+geminiFinalUsage, "", 1)
					} else {
						wire = strings.Replace(wire, geminiFinalUsage, usage, 1)
					}
					if mode == "duplicate" {
						wire = strings.TrimSuffix(wire, "}") + `,"usageMetadata":` + geminiFinalUsage + `}`
					}
				}
				if mode == "hosted_tool" {
					wire = strings.Replace(wire, `"finishReason":"STOP"`, `"groundingMetadata":{"searchQueries":["private-search"]},"finishReason":"STOP"`, 1)
				}
				f := geminiFixture(t, []http.HandlerFunc{geminiHandler(t, stream, wire)}, nil)
				res := nativeRequest(t, f, geminiPath(stream), geminiBody(), http.Header{"Authorization": []string{"Bearer " + f.token}})
				body, err := io.ReadAll(res.Body)
				if err != nil || !bytes.Contains(body, []byte("billing_pending")) || bytes.Contains(body, []byte("finishReason")) || (!stream && (res.StatusCode != 503 || bytes.Contains(body, []byte("hello")))) {
					t.Fatalf("Gemini unknown usage HTTP %d %s %v", res.StatusCode, body, err)
				}
				retrievalResourcesReleased(t, f)
				v := f.state(t, res.Header.Get("X-Request-ID"))
				wantUsage := "conflicting"
				if mode == "missing" || mode == "early_only" {
					wantUsage = "missing"
				}
				if v.billing != "pending_reconciliation" || v.held != 140 || v.available != 860 || v.spent != 0 || v.keyHeld != 140 || v.settlements != 0 || v.attempts != 1 || v.usage != wantUsage || v.content != stream || f.posts.Load() != 1 {
					t.Fatalf("Gemini unknown usage state %+v posts=%d", v, f.posts.Load())
				}
			})
		}
	}
}
func TestGeminiAdmissionAndNativeCredentialBoundary(t *testing.T) {
	f := geminiFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) { t.Error("rejected Gemini dispatched") }}, nil)
	header := http.Header{"Authorization": []string{"Bearer " + f.token}}
	for _, path := range []string{
		"/v1beta/models/retail-model:generateContent?alt=sse", "/v1beta/models/retail-model:streamGenerateContent", "/v1beta/models/retail-model:streamGenerateContent?alt=sse&alt=sse",
		"/v1beta/models/retail%2dmodel:generateContent", "/v1beta/models/retail-model:countTokens", "/v1beta/models/../retail-model:generateContent",
	} {
		res := nativeRequest(t, f, path, geminiBody(), header)
		if res.StatusCode != 400 {
			t.Fatalf("Gemini accepted path %s HTTP %d", path, res.StatusCode)
		}
	}
	for _, field := range []string{"model", "stream", "tools", "cachedContent"} {
		body := geminiBody()
		body[field] = false
		res := nativeRequest(t, f, geminiPath(false), body, header)
		if res.StatusCode != 400 {
			t.Fatal("Gemini accepted unsupported body field", field, res.StatusCode)
		}
	}
	for _, headers := range []http.Header{
		{}, {"X-Goog-Api-Key": []string{f.token}}, {"Authorization": []string{"Bearer " + f.token}, "X-Goog-Api-Key": []string{f.token}}, {"Authorization": []string{"Bearer " + f.token}, "X-Api-Key": []string{f.token}},
	} {
		res := nativeRequest(t, f, geminiPath(false), geminiBody(), headers)
		if res.StatusCode != 401 {
			t.Fatal("Gemini accepted ambiguous credential", res.StatusCode)
		}
	}
	res := nativeRequest(t, f, geminiPath(false)+"?key=fixture-query-key", geminiBody(), header)
	if res.StatusCode != 401 {
		t.Fatal("Gemini accepted query credential", res.StatusCode)
	}
	res = nativeRequest(t, f, "/v1/chat/completions", nativeBody(provider.ProtocolMessages, false), header)
	if res.StatusCode != 400 {
		t.Fatal("Gemini accepted generation protocol mismatch", res.StatusCode)
	}
	probeAssertWalletUntouched(t, f)
	if f.posts.Load() != 0 {
		t.Fatal("rejected Gemini posted")
	}
}
func TestGeminiIdempotencyIncludesModelAndAction(t *testing.T) {
	f := geminiFixture(t, []http.HandlerFunc{geminiHandler(t, false, geminiResponseJSON)}, nil)
	ctx := context.Background()
	var actor string
	if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_operator' LIMIT 1").Scan(&actor); err != nil {
		t.Fatal(err)
	}
	second, err := f.server.Catalog.CreateModel(ctx, actor, catalog.ModelInput{PublicName: "second-model", Protocol: provider.ProtocolGemini, InputUpperBound: 100, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, second); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1", f.key, second); err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Catalog.PublishPrice(ctx, actor, second, "1000000", "2000000", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Catalog.Publish(ctx, actor, second, 0, []catalog.BindingInput{{ChannelID: probeChannels(t, f)[0], UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 4}}); err != nil {
		t.Fatal(err)
	}
	header := http.Header{"Authorization": []string{"Bearer " + f.token}, "Idempotency-Key": []string{"gemini-identical-body"}}
	first := nativeRequest(t, f, geminiPath(false), geminiBody(), header)
	_, _ = io.Copy(io.Discard, first.Body)
	if first.StatusCode != 200 {
		t.Fatal(first.StatusCode)
	}
	for i, path := range []string{geminiPath(false), geminiPath(true), "/v1beta/models/second-model:generateContent"} {
		res := nativeRequest(t, f, path, geminiBody(), header)
		body, _ := io.ReadAll(res.Body)
		code := "idempotency_conflict"
		if i == 0 {
			code = "request_exists"
		}
		if res.StatusCode != 409 || !bytes.Contains(body, []byte(code)) || res.Header.Get("X-Original-Request-ID") != first.Header.Get("X-Request-ID") {
			t.Fatalf("Gemini identity HTTP %d %s", res.StatusCode, body)
		}
	}
	geminiAssertUsage(t, f, first.Header.Get("X-Request-ID"), 1)
	if f.posts.Load() != 1 {
		t.Fatal("Gemini identity replayed", f.posts.Load())
	}
}
func TestGeminiSafeFailoverHasSingleCharge(t *testing.T) {
	f := geminiFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		geminiValidateUpstream(t, r, 20)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"private-message"}}`)
	}, geminiHandler(t, false, geminiResponseJSON)}, []int{429})
	res := nativeRequest(t, f, geminiPath(false), geminiBody(), http.Header{"Authorization": []string{"Bearer " + f.token}})
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || bytes.Contains(body, []byte("private-message")) {
		t.Fatalf("Gemini failover HTTP %d %s", res.StatusCode, body)
	}
	geminiAssertUsage(t, f, res.Header.Get("X-Request-ID"), 2)
	if f.posts.Load() != 2 {
		t.Fatal(f.posts.Load())
	}
}
func TestGeminiCancelledSSEClosesSocketAndKeepsUnknownUsage(t *testing.T) {
	closed := make(chan struct{})
	f := geminiFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		geminiValidateUpstream(t, r, 20)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"index":0}]}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(closed)
		case <-time.After(5 * time.Second):
			t.Error("Gemini upstream cancellation not observed")
		}
	}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, _ := json.Marshal(geminiBody())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+geminiPath(true), bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, "hello") {
		t.Fatal(line, err)
	}
	id := res.Header.Get("X-Request-ID")
	cancel()
	_ = res.Body.Close()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Gemini socket remained active")
	}
	retrievalResourcesReleased(t, f)
	v := f.state(t, id)
	if v.billing != "pending_reconciliation" || v.held != 140 || v.settlements != 0 || !v.content || v.usage != "missing" || f.posts.Load() != 1 {
		t.Fatalf("Gemini cancellation %+v posts=%d", v, f.posts.Load())
	}
}
func TestGeminiProbeAndCatalogConstraints(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(fmt.Sprint(confirmed), func(t *testing.T) {
			wire := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1,"totalTokenCount":11}}`
			if !confirmed {
				wire = strings.Replace(wire, `,"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1,"totalTokenCount":11}`, "", 1)
			}
			f := geminiFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
				geminiValidateUpstream(t, r, 1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, wire)
			}}, nil)
			raw, csrf := probeAdmin(t, f)
			channels := probeChannels(t, f)
			result := probeCall(context.Background(), f, channels[0], raw, csrf)
			if result.err != nil || result.status != 200 || !bytes.Contains(result.body, []byte(fmt.Sprintf(`"ok":%t`, confirmed))) {
				t.Fatalf("Gemini probe %d %s %v", result.status, result.body, result.err)
			}
			probeAssertWalletUntouched(t, f)
			if _, err := f.server.Catalog.Publish(context.Background(), "test", f.model, 1, []catalog.BindingInput{{ChannelID: channels[0], UpstreamModel: "../bad", Priority: 1, Weight: 1, MaxInflight: 4}}); !errors.Is(err, catalog.ErrInvalid) {
				t.Fatal("Gemini unsafe mapping accepted", err)
			}
			for i, change := range []func(*provider.Profile){func(p *provider.Profile) { p.SupportsTools = true }, func(p *provider.Profile) { p.UsageSemantics = "final" }, func(p *provider.Profile) { p.OutputLimitField = "max_tokens" }} {
				p := &provider.Profile{Protocol: provider.ProtocolGemini, InputTokenUpperBound: 100, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 20}
				change(p)
				if _, err := f.server.Catalog.CreateModel(context.Background(), "test", catalog.ModelInput{PublicName: fmt.Sprintf("bad-gemini-%d", i), Profile: p}); !errors.Is(err, catalog.ErrInvalid) {
					t.Fatal("Gemini invalid profile accepted", i, err)
				}
			}
		})
	}
}
func TestGeminiInferenceReadinessAndDeadline(t *testing.T) {
	f := geminiFixture(t, []http.HandlerFunc{geminiHandler(t, false, geminiResponseJSON)}, nil)
	var remaining time.Duration
	engine := gin.New()
	engine.Use(f.server.metadata())
	engine.POST("/v1beta/models/*action", func(c *gin.Context) {
		if d, ok := c.Request.Context().Deadline(); ok {
			remaining = time.Until(d)
		}
		c.Status(204)
	})
	// metadata must apply the 300s contract to every /v1beta request, including errors.
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, geminiPath(false), strings.NewReader(`{}`))
	engine.ServeHTTP(recorder, req)
	if remaining < 290*time.Second || remaining > 300*time.Second {
		t.Fatal("Gemini inference deadline", remaining)
	}
	f.server.Ready(false)
	res := nativeRequest(t, f, geminiPath(false), geminiBody(), http.Header{"Authorization": []string{"Bearer " + f.token}})
	if res.StatusCode != 503 {
		t.Fatal("Gemini bypassed readiness", res.StatusCode)
	}
	probeAssertWalletUntouched(t, f)
	if f.posts.Load() != 0 {
		t.Fatal("unready Gemini dispatched")
	}
}
