package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

const nativeResponsesJSON = `{"id":"resp_fixture","object":"response","status":"completed","model":"provider-model","output":[{"id":"msg_fixture","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":0}},"metadata":{"secret":"not-public-metadata"}}`
const nativeMessagesJSON = `{"id":"msg_fixture","type":"message","role":"assistant","model":"provider-model","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":6,"cache_read_input_tokens":3,"cache_creation_input_tokens":1,"output_tokens":5},"metadata":{"secret":"not-public-metadata"}}`

func nativeRequest(t *testing.T, f *gatewayFixture, path string, body map[string]any, headers http.Header) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest(http.MethodPost, f.http.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header = headers.Clone()
	r.Header.Set("Content-Type", "application/json")
	response, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}
func nativeBody(protocol string, stream bool) map[string]any {
	if protocol == provider.ProtocolResponses {
		return map[string]any{"model": "retail-model", "input": "hello", "max_output_tokens": 20, "stream": stream, "store": false}
	}
	return map[string]any{"model": "retail-model", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 20, "stream": stream}
}
func nativePath(protocol string) string {
	if protocol == provider.ProtocolResponses {
		return "/v1/responses"
	}
	return "/v1/messages"
}
func nativeJSON(protocol string) string {
	if protocol == provider.ProtocolResponses {
		return nativeResponsesJSON
	}
	return nativeMessagesJSON
}
func nativeStream(protocol string) string {
	frame := func(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }
	if protocol == provider.ProtocolResponses {
		return frame("response.created", `{"type":"response.created","sequence_number":0,"response":{"id":"resp_fixture","object":"response","status":"in_progress","output":[],"model":"provider-model"}}`) +
			frame("response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_fixture","output_index":0,"content_index":0,"delta":"hello"}`) +
			frame("response.completed", `{"type":"response.completed","sequence_number":2,"response":`+nativeResponsesJSON+`}`)
	}
	return frame("message_start", `{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"provider-model","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":6,"cache_read_input_tokens":3,"cache_creation_input_tokens":1,"output_tokens":0}}}`) +
		frame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		frame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`) +
		frame("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`) +
		frame("message_stop", `{"type":"message_stop"}`)
}

func TestNativeProtocolsCommercialRoundTrip(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolResponses, provider.ProtocolMessages} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", protocol, stream), func(t *testing.T) {
				f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
					raw, _ := io.ReadAll(r.Body)
					if !bytes.Contains(raw, []byte(`"model":"provider-model"`)) || bytes.Contains(raw, []byte("mg_")) {
						t.Error("upstream model or customer credential boundary failed")
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, nativeStream(protocol))
					} else {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, nativeJSON(protocol))
					}
				}}, nil, 8, protocol)
				header := http.Header{"Authorization": []string{"Bearer " + f.token}}
				if protocol == provider.ProtocolMessages {
					header = http.Header{"X-Api-Key": []string{f.token}}
				}
				res := nativeRequest(t, f, nativePath(protocol), nativeBody(protocol, stream), header)
				body, _ := io.ReadAll(res.Body)
				if res.StatusCode != 200 || !bytes.Contains(body, []byte("hello")) || bytes.Contains(body, []byte("not-public-metadata")) || bytes.Contains(body, []byte("provider-model")) || bytes.Contains(body, []byte("[DONE]")) {
					t.Fatalf("status=%d body=%s", res.StatusCode, body)
				}
				if stream && !strings.Contains(string(body), "event: ") {
					t.Fatal("native SSE name missing")
				}
				id := res.Header.Get("X-Request-ID")
				var status string
				var amount, input, output, cached int64
				if err := f.pool.QueryRow(context.Background(), `SELECT s.status,s.charged_amount,(meter_event_usage(e.id)->>'input_tokens')::bigint,(meter_event_usage(e.id)->>'output_tokens')::bigint,(meter_event_usage(e.id)->>'cached_tokens')::bigint FROM settlements s JOIN finalization_records f ON f.id=s.finalization_id JOIN metering_events e ON e.id=f.event_id WHERE s.request_id=$1`, id).Scan(&status, &amount, &input, &output, &cached); err != nil {
					t.Fatal(err)
				}
				if status != "settled" || amount != 20 || input != 10 || output != 5 || cached != 3 || f.posts.Load() != 1 {
					t.Fatalf("settlement=%s %d usage=%d/%d/%d posts=%d", status, amount, input, output, cached, f.posts.Load())
				}
				wallet, err := f.server.Billing.Wallet(context.Background(), billing.Query{TenantID: f.tenant})
				if err != nil || wallet.Available != 980 || wallet.Held != 0 {
					t.Fatal(wallet, err)
				}
			})
		}
	}
}

func TestNativeTerminalIsReleasedOnlyAfterSettlement(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolResponses, provider.ProtocolMessages} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, nativeStream(protocol))
			}}, nil, 8, protocol)
			res := nativeRequest(t, f, nativePath(protocol), nativeBody(protocol, true), http.Header{"Authorization": []string{"Bearer " + f.token}})
			scanner := bufio.NewScanner(res.Body)
			found := false
			for scanner.Scan() {
				line := scanner.Text()
				terminal := line == "event: response.completed" || line == "event: message_stop"
				if terminal {
					found = true
					var count int
					if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM settlements WHERE request_id=$1 AND status='settled'", res.Header.Get("X-Request-ID")).Scan(&count); err != nil || count != 1 {
						t.Fatalf("terminal escaped before money commit: %d %v", count, err)
					}
				}
			}
			if !found || scanner.Err() != nil {
				t.Fatalf("missing terminal: %v", scanner.Err())
			}
		})
	}
}

func TestNativeAdmissionRejectsBeforeMoneyOrDispatch(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolResponses, provider.ProtocolMessages} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request dispatched") }}, nil, 8, protocol)
			good := http.Header{"Authorization": []string{"Bearer " + f.token}}
			bad := nativeBody(protocol, false)
			bad["unsupported_billable_dimension"] = true
			res := nativeRequest(t, f, nativePath(protocol), bad, good)
			if res.StatusCode != 400 {
				t.Fatal(res.StatusCode)
			}
			res = nativeRequest(t, f, "/v1/chat/completions", nativeBody(provider.ProtocolMessages, false), good)
			if res.StatusCode != 400 {
				t.Fatal("protocol mismatch", res.StatusCode)
			}
			for _, headers := range []http.Header{
				{"Authorization": []string{"Bearer " + f.token}, "X-Api-Key": []string{f.token}},
				{"Authorization": []string{"Bearer " + f.token}, "X-Api-Key": []string{""}},
				{"Authorization": []string{"Bearer " + f.token, "Bearer " + f.token}},
				{"X-Api-Key": []string{f.token, f.token}},
			} {
				res = nativeRequest(t, f, nativePath(protocol), nativeBody(protocol, false), headers)
				if res.StatusCode != 401 {
					t.Fatal("ambiguous credentials", res.StatusCode)
				}
			}
			if protocol == provider.ProtocolResponses {
				res = nativeRequest(t, f, nativePath(protocol), nativeBody(protocol, false), http.Header{"X-Api-Key": []string{f.token}})
				if res.StatusCode != 401 {
					t.Fatal("Responses accepted Messages auth", res.StatusCode)
				}
			}
			var count int
			if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM request_records").Scan(&count); err != nil || count != 0 || f.posts.Load() != 0 {
				t.Fatal("admission created financial state", count, f.posts.Load(), err)
			}
		})
	}
}

func TestNativeMissingUsageNeverEmitsSuccess(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolResponses, provider.ProtocolMessages} {
		t.Run(protocol, func(t *testing.T) {
			stream := nativeStream(protocol)
			if protocol == provider.ProtocolResponses {
				stream = strings.ReplaceAll(stream, `"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":0}}`, `"usage":null`)
			} else {
				stream = strings.ReplaceAll(stream, `"usage":{"output_tokens":5}`, `"usage":null`)
			}
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, stream)
			}}, nil, 8, protocol)
			res := nativeRequest(t, f, nativePath(protocol), nativeBody(protocol, true), http.Header{"Authorization": []string{"Bearer " + f.token}})
			body, _ := io.ReadAll(res.Body)
			if bytes.Contains(body, []byte("event: message_stop")) || bytes.Contains(body, []byte("event: response.completed")) || bytes.Contains(body, []byte("[DONE]")) || !bytes.Contains(body, []byte("event: error")) {
				t.Fatalf("false success: %s", body)
			}
			wallet, err := f.server.Billing.Wallet(context.Background(), billing.Query{TenantID: f.tenant})
			if err != nil || wallet.Available+wallet.Held != 1000 {
				t.Fatal("unconfirmed usage charged", wallet, err)
			}
			if f.posts.Load() != 1 {
				t.Fatal("unknown usage replayed", f.posts.Load())
			}
		})
	}
}

func TestNativePlatformProbeDoesNotTouchCustomerMoney(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolResponses, provider.ProtocolMessages} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, nativeJSON(protocol))
			}}, nil, 8, protocol)
			raw, csrf := probeAdmin(t, f)
			result := probeCall(context.Background(), f, probeChannels(t, f)[0], raw, csrf)
			if result.err != nil || result.status != 200 {
				t.Fatalf("probe: %d %s %v", result.status, result.body, result.err)
			}
			probeAssertWalletUntouched(t, f)
		})
	}
}

func TestModelProtocolCreationDefaultsAndValidation(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	for _, protocol := range []string{"", provider.ProtocolResponses, provider.ProtocolMessages, "invalid"} {
		id, err := f.server.Catalog.CreateModel(context.Background(), "fixture", catalog.ModelInput{PublicName: "model-" + protocol, Protocol: protocol, ContextLimit: 120, InputUpperBound: 100, OutputLimit: 20})
		if protocol == "invalid" {
			if err == nil {
				t.Fatal("invalid protocol created")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var raw []byte
		if err = f.pool.QueryRow(context.Background(), "SELECT profile FROM models WHERE id=$1", id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var p provider.Profile
		if json.Unmarshal(raw, &p) != nil {
			t.Fatal("bad profile")
		}
		want := "max_tokens"
		if protocol == provider.ProtocolResponses {
			want = "max_output_tokens"
		}
		if p.OutputLimitField != want {
			t.Fatal(p.OutputLimitField, want)
		}
	}
}

func TestNativeCancellationClosesUpstreamWithoutReplay(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolResponses, provider.ProtocolMessages} {
		t.Run(protocol, func(t *testing.T) {
			exited := make(chan struct{})
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
				defer close(exited)
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				sequence := nativeStream(protocol)
				cut := strings.Index(sequence, "event: message_delta")
				if protocol == provider.ProtocolResponses {
					cut = strings.Index(sequence, "event: response.completed")
				}
				io.WriteString(w, sequence[:cut])
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-time.After(3 * time.Second):
					t.Error("cancel did not close upstream")
				}
			}}, nil, 8, protocol)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			raw, _ := json.Marshal(nativeBody(protocol, true))
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+nativePath(protocol), bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+f.token)
			req.Header.Set("Content-Type", "application/json")
			res, err := f.http.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			scanner := bufio.NewScanner(res.Body)
			seen := false
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), "data:") && strings.Contains(scanner.Text(), "hello") {
					seen = true
					break
				}
			}
			if !seen {
				t.Fatal("content not delivered before cancellation")
			}
			cancel()
			res.Body.Close()
			select {
			case <-exited:
			case <-time.After(4 * time.Second):
				t.Fatal("upstream reader leaked")
			}
			deadline := time.Now().Add(3 * time.Second)
			for len(f.server.global) > 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if len(f.server.global) != 0 || f.posts.Load() != 1 {
				t.Fatal("cancel leaked permit or replayed", len(f.server.global), f.posts.Load())
			}
			wallet, err := f.server.Billing.Wallet(context.Background(), billing.Query{TenantID: f.tenant})
			if err != nil || wallet.Available+wallet.Held != 1000 {
				t.Fatal("unknown usage charged", wallet, err)
			}
		})
	}
}

func TestModelDefaultInputBoundUsesWholeContext(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	for _, protocol := range []string{provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages} {
		id, err := f.server.Catalog.CreateModel(context.Background(), "fixture", catalog.ModelInput{PublicName: "default-bound-" + protocol, Protocol: protocol, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var bound int64
		if err = f.pool.QueryRow(context.Background(), "SELECT (profile->>'input_token_upper_bound')::bigint FROM models WHERE id=$1", id).Scan(&bound); err != nil || bound != 120 {
			t.Fatal(bound, err)
		}
	}
}
