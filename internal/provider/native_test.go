package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func nativeProfile(protocol string) Profile { p := fixtureProfile(); p.Protocol = protocol; return p }
func nativeRequest(t *testing.T, protocol string, stream bool) *Request {
	t.Helper()
	raw := `{"model":"public-model","input":"hello"`
	if protocol == ProtocolMessages {
		raw = `{"model":"public-model","messages":[{"role":"user","content":"hello"}]`
	}
	if stream {
		raw += `,"stream":true`
	}
	raw += `}`
	r, err := Parse(protocol, []byte(raw), nativeProfile(protocol))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNativeRequestsAndModelMapping(t *testing.T) {
	for protocol, raw := range map[string]string{
		ProtocolResponses: `{"model":"public-model","store":false,"input":[{"role":"user","content":[{"type":"input_text","text":"weather"}]},{"type":"function_call","call_id":"c1","name":"weather","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"sunny"}],"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}],"tool_choice":{"type":"function","name":"weather"},"max_output_tokens":32}`,
		ProtocolMessages:  `{"model":"public-model","system":[{"type":"text","text":"help"}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"weather","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"sunny"}]}],"tools":[{"name":"weather","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"weather"},"max_tokens":32}`,
	} {
		t.Run(protocol, func(t *testing.T) {
			r, err := Parse(protocol, []byte(raw), nativeProfile(protocol))
			if err != nil {
				t.Fatal(err)
			}
			if r.MaxOutputTokens != 32 || r.InputUpperBound != 8192 || r.Protocol() != protocol {
				t.Fatalf("bad request %+v", r)
			}
			// Public mutable fields must never change the validated upstream payload.
			r.Stream = true
			r.MaxOutputTokens = 9999
			b, err := r.UpstreamJSON("private-model")
			if err != nil {
				t.Fatal(err)
			}
			m, _ := object(b)
			limit := "max_tokens"
			if protocol == ProtocolResponses {
				limit = "max_output_tokens"
				if string(m["store"]) != "false" {
					t.Fatal("storage enabled")
				}
			}
			if !stringValue(m["model"], "private-model") || string(m[limit]) != "32" || m["stream_options"] != nil {
				t.Fatalf("payload %s", b)
			}
			p := nativeProfile(protocol)
			p.SupportsTools = false
			if _, err := Parse(protocol, []byte(raw), p); err == nil {
				t.Fatal("tools bypassed profile")
			}
		})
	}
	if nativeRequest(t, ProtocolResponses, false).EndpointSuffix() != "/responses" || nativeRequest(t, ProtocolMessages, false).EndpointSuffix() != "/messages" {
		t.Fatal("wrong endpoint suffix")
	}
}

func TestNativeStatelessReplayAndParallelToolRestriction(t *testing.T) {
	raw := `{"model":"public-model","input":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]},{"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"weather","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"sunny"}],"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}]}`
	p := nativeProfile(ProtocolResponses)
	p.SupportsParallelToolCalls = false
	r, err := Parse(ProtocolResponses, []byte(raw), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.UpstreamJSON("provider-model")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := object(b)
	if string(m["parallel_tool_calls"]) != "false" {
		t.Fatalf("parallel tool restriction missing %s", b)
	}
	p = nativeProfile(ProtocolMessages)
	p.SupportsParallelToolCalls = false
	r, err = Parse(ProtocolMessages, []byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"weather","input_schema":{"type":"object"}}]}`), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err = r.UpstreamJSON("provider-model")
	if err != nil {
		t.Fatal(err)
	}
	m, _ = object(b)
	choice, _ := object(m["tool_choice"])
	if string(choice["disable_parallel_tool_use"]) != "true" {
		t.Fatalf("parallel tool restriction missing %s", b)
	}
}

func TestNativePhaseCompatibility(t *testing.T) {
	for _, phase := range []string{`null`, `"commentary"`, `"final_answer"`} {
		t.Run(phase, func(t *testing.T) {
			final := strings.Replace(responseFinal, `"role":"assistant"`, `"role":"assistant","phase":`+phase, 1)
			for _, stream := range []bool{false, true} {
				wire := final
				if stream {
					wire = responsesWire(final)
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
					} else {
						w.Header().Set("Content-Type", "application/json")
					}
					_, _ = io.WriteString(w, wire)
				}))
				ctx, cancel := testContext(t)
				r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolResponses, stream))
				if err != nil {
					cancel()
					srv.Close()
					t.Fatal(err)
				}
				var data []byte
				if stream {
					for {
						event, e := r.ReadEvent()
						if e != nil {
							err = e
							break
						}
						if event.Kind == "done" {
							data = event.Data
							break
						}
					}
				} else {
					data, err = r.DecodeJSON()
				}
				_ = r.Close()
				cancel()
				srv.Close()
				if err != nil || !strings.Contains(string(data), `"phase":`+phase) || r.Usage.Status != UsageConfirmed {
					t.Fatalf("phase lost stream=%v data=%s err=%v", stream, data, err)
				}
			}
			input := `{"model":"public-model","input":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","phase":` + phase + `,"content":[{"type":"output_text","text":"hello","annotations":[]}]}]}`
			if _, err := Parse(ProtocolResponses, []byte(input), nativeProfile(ProtocolResponses)); err != nil {
				t.Fatalf("phase replay rejected %v", err)
			}
		})
	}
	for _, phase := range []string{`"invalid"`, `{}`, `1`} {
		r := &Response{model: "public-model", profile: nativeProfile(ProtocolResponses)}
		m, _ := object([]byte(strings.Replace(responseFinal, `"role":"assistant"`, `"role":"assistant","phase":`+phase, 1)))
		if _, _, err := r.normalizeResponse(m, true); err == nil {
			t.Fatal("invalid output phase accepted", phase)
		}
		if _, err := Parse(ProtocolResponses, []byte(`{"model":"public-model","input":[{"role":"assistant","phase":`+phase+`,"content":"hello"}]}`), nativeProfile(ProtocolResponses)); err == nil {
			t.Fatal("invalid input phase accepted", phase)
		}
	}
}

func TestNativeEmptyCitationsCompatibility(t *testing.T) {
	for _, citations := range []string{`null`, `[]`} {
		t.Run(citations, func(t *testing.T) {
			final := strings.Replace(messageFinal, `"type":"text","text":"你好"`, `"type":"text","text":"你好","citations":`+citations, 1)
			for _, stream := range []bool{false, true} {
				wire := final
				if stream {
					wire = strings.Replace(messagesWire(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`), `"type":"text","text":""`, `"type":"text","text":"","citations":`+citations, 1)
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
					} else {
						w.Header().Set("Content-Type", "application/json")
					}
					_, _ = io.WriteString(w, wire)
				}))
				ctx, cancel := testContext(t)
				r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolMessages, stream))
				if err != nil {
					cancel()
					srv.Close()
					t.Fatal(err)
				}
				preserved := false
				if stream {
					for {
						event, e := r.ReadEvent()
						if e != nil {
							err = e
							break
						}
						if strings.Contains(string(event.Data), `"citations":`+citations) {
							preserved = true
						}
						if event.Kind == "done" {
							break
						}
					}
				} else {
					var data []byte
					data, err = r.DecodeJSON()
					preserved = strings.Contains(string(data), `"citations":`+citations)
				}
				_ = r.Close()
				cancel()
				srv.Close()
				if err != nil || !preserved || r.Usage.Status != UsageConfirmed {
					t.Fatalf("empty citations rejected stream=%v err=%v", stream, err)
				}
			}
			request := `{"model":"public-model","system":[{"type":"text","text":"system","citations":` + citations + `}],"messages":[{"role":"assistant","content":[{"type":"text","text":"hello","citations":` + citations + `}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"ok","citations":` + citations + `}]}]}]}`
			if _, err := Parse(ProtocolMessages, []byte(request), nativeProfile(ProtocolMessages)); err != nil {
				t.Fatalf("empty citations replay rejected %v", err)
			}
		})
	}
	for _, citations := range []string{`[{}]`, `{}`, `""`, `0`} {
		block, _ := object([]byte(`{"type":"text","text":"hello","citations":` + citations + `}`))
		r := &Response{profile: nativeProfile(ProtocolMessages)}
		if _, _, err := r.messageBlock(block); err == nil {
			t.Fatal("nonempty or invalid output citations accepted", citations)
		}
		if _, err := Parse(ProtocolMessages, []byte(`{"model":"public-model","messages":[{"role":"assistant","content":[{"type":"text","text":"hello","citations":`+citations+`}]}]}`), nativeProfile(ProtocolMessages)); err == nil {
			t.Fatal("nonempty or invalid input citations accepted", citations)
		}
	}
}

func TestNativeOfficialResponsesStreamingFields(t *testing.T) {
	// Full basic sequence from the official schema, including empty arrays the
	// SDK's high-level stream accumulator reads as required event attributes.
	for _, logprobs := range []string{`[]`, `null`} {
		t.Run(logprobs, func(t *testing.T) {
			wire := nativeSSE("response.created", responseCreated) +
				nativeSSE("response.in_progress", strings.Replace(responseCreated, "response.created", "response.in_progress", 1)) +
				nativeSSE("response.output_item.added", `{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","phase":"final_answer","content":[]}}`) +
				nativeSSE("response.content_part.added", `{"type":"response.content_part.added","sequence_number":3,"item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":`+logprobs+`}}`) +
				nativeSSE("response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"你好","logprobs":`+logprobs+`}`) +
				nativeSSE("response.output_text.done", `{"type":"response.output_text.done","sequence_number":5,"item_id":"msg_1","output_index":0,"content_index":0,"text":"你好","logprobs":`+logprobs+`}`) +
				nativeSSE("response.content_part.done", `{"type":"response.content_part.done","sequence_number":6,"item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"你好","annotations":[],"logprobs":`+logprobs+`}}`) +
				nativeSSE("response.output_item.done", `{"type":"response.output_item.done","sequence_number":7,"output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"你好","annotations":[],"logprobs":`+logprobs+`}]}}`) +
				nativeSSE("response.completed", `{"type":"response.completed","sequence_number":8,"response":`+responseFinal+`}`)
			srv := serveStream(t, wire, false)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolResponses, true))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			count := 0
			for {
				event, e := r.ReadEvent()
				if errors.Is(e, io.EOF) {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				count++
				if event.Type == "response.output_text.delta" || event.Type == "response.output_text.done" {
					m, _ := object(event.Data)
					if string(m["logprobs"]) != "[]" {
						t.Fatalf("required SDK logprobs missing: %s", event.Data)
					}
				}
			}
			if count != 9 || r.Usage.Status != UsageConfirmed {
				t.Fatalf("official stream incomplete count=%d usage=%+v", count, r.Usage)
			}
		})
	}
	for _, raw := range []string{`[{"token":"x","logprob":0}]`, `{}`, `0`} {
		r := &Response{profile: nativeProfile(ProtocolResponses), native: nativeState{started: true}}
		m, _ := object([]byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"x","logprobs":` + raw + `}`))
		if _, err := r.responseEvent("response.output_text.delta", m); err == nil {
			t.Fatal("unsupported logprobs accepted", raw)
		}
	}
}

func TestNativeMessagesOfficialOptionalMetadata(t *testing.T) {
	start := strings.Replace(messageStart, `"cache_creation_input_tokens":3`, `"cache_creation_input_tokens":0,"cache_creation":null,"output_tokens_details":null,"service_tier":null,"speed":null,"server_tool_use":null`, 1)
	finalDelta := `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"container":null,"stop_details":null},"usage":{"input_tokens":null,"cache_creation_input_tokens":null,"cache_read_input_tokens":null,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":2,"output_tokens_details":{"thinking_tokens":0},"speed":"standard","service_tier":"standard","server_tool_use":{"web_search_requests":0,"web_fetch_requests":0}}}`
	wire := strings.Replace(messagesWire(finalDelta), messageStart, start, 1)
	srv := serveStream(t, wire, false)
	defer srv.Close()
	ctx, cancel := testContext(t)
	defer cancel()
	r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolMessages, true))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for {
		_, err = r.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if r.Usage.Status != UsageConfirmed || r.Usage.InputTokens != 5 || r.Usage.OutputTokens != 2 || r.Usage.CachedTokens != 1 {
		t.Fatalf("valid nullable cumulative metadata %+v", r.Usage)
	}
	for _, extra := range []string{`"output_tokens_details":null,"speed":null`, `"output_tokens_details":{"thinking_tokens":0},"speed":"standard"`} {
		u, complete := messagesUsage([]byte(`{"input_tokens":4,"output_tokens":2,` + extra + `}`))
		if !complete || u.Status == UsageConflicting {
			t.Fatalf("valid nonstream metadata rejected %+v", u)
		}
	}
	for _, extra := range []string{`"output_tokens_details":{"thinking_tokens":1}`, `"output_tokens_details":{"unknown":0}`, `"speed":"fast"`, `"speed":false`, `"service_tier":"priority"`, `"server_tool_use":{"web_fetch_requests":1}`} {
		u, complete := messagesUsage([]byte(`{"input_tokens":4,"output_tokens":2,` + extra + `}`))
		if complete || u.Status != UsageConflicting {
			t.Fatalf("unsupported metadata accepted %s %+v", extra, u)
		}
	}
	for _, field := range []string{"container", "stop_details"} {
		state := &Response{profile: nativeProfile(ProtocolMessages), native: nativeState{started: true, blocks: map[int64]*nativeBlock{}}}
		m, _ := object([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","` + field + `":{"secret":"canary"}},"usage":{"output_tokens":2}}`))
		if _, err := state.messageEvent("message_delta", m); err == nil {
			t.Fatal("nonempty metadata accepted", field)
		}
	}
}

func TestNativeResponseLifecycleIdentity(t *testing.T) {
	for _, typ := range []string{"response.in_progress", "response.completed", "response.incomplete"} {
		t.Run(typ, func(t *testing.T) {
			body := responseFinal
			if typ == "response.in_progress" {
				m, _ := object([]byte(responseCreated))
				body = string(m["response"])
			}
			if typ == "response.incomplete" {
				body = strings.Replace(body, `"status":"completed"`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}`, 1)
			}
			body = strings.Replace(body, `"id":"resp_1"`, `"id":"different-response"`, 1)
			wire := nativeSSE("response.created", responseCreated) + nativeSSE(typ, `{"type":"`+typ+`","response":`+body+`}`)
			srv := serveStream(t, wire, false)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolResponses, true))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err = r.ReadEvent(); err != nil {
				t.Fatal(err)
			}
			event, err := r.ReadEvent()
			var pe *Error
			if event != nil || !errors.As(err, &pe) || pe.Code != "upstream_response_identity_changed" || r.Usage.Status == UsageConfirmed || r.ProtocolOutcome != "failed" {
				t.Fatalf("changed response identity accepted event=%+v usage=%+v err=%v", event, r.Usage, err)
			}
		})
	}
	// Nonstream responses have no preceding lifecycle snapshot to compare.
	r := &Response{model: "public-model", profile: nativeProfile(ProtocolResponses)}
	m, _ := object([]byte(responseFinal))
	if _, _, err := r.normalizeResponse(m, true); err != nil {
		t.Fatal(err)
	}
	for _, wire := range []string{nativeSSE("message_start", messageStart) + nativeSSE("message_start", messageStart), nativeSSE("message_start", messageStart) + nativeSSE("message_delta", `{"type":"message_delta","id":"different-message","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`)} {
		srv := serveStream(t, wire, false)
		ctx, cancel := testContext(t)
		response, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolMessages, true))
		if err != nil {
			cancel()
			srv.Close()
			t.Fatal(err)
		}
		_, err = response.ReadEvent()
		if err == nil {
			_, err = response.ReadEvent()
		}
		_ = response.Close()
		cancel()
		srv.Close()
		if err == nil || response.Usage.Status == UsageConfirmed {
			t.Fatal("message lifecycle override accepted")
		}
	}
}

func TestNativeMessagesContextWindowStop(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			body := strings.Replace(messageFinal, `"stop_reason":"end_turn"`, `"stop_reason":"model_context_window_exceeded"`, 1)
			if stream {
				body = messagesWire(`{"type":"message_delta","delta":{"stop_reason":"model_context_window_exceeded","stop_sequence":null},"usage":{"output_tokens":2}}`)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolMessages, stream))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			preserved := false
			if stream {
				for {
					event, e := r.ReadEvent()
					if e != nil {
						t.Fatal(e)
					}
					if strings.Contains(string(event.Data), "model_context_window_exceeded") {
						preserved = true
					}
					if event.Kind == "done" {
						if event.Type != "message_stop" || event.FinishReason != "length" {
							t.Fatalf("terminal %+v", event)
						}
						break
					}
				}
			} else {
				data, e := r.DecodeJSON()
				if e != nil {
					t.Fatal(e)
				}
				preserved = strings.Contains(string(data), "model_context_window_exceeded")
			}
			if !preserved || r.FinishReason != "length" || r.Usage.Status != UsageConfirmed || r.ProtocolOutcome != "completed" {
				t.Fatalf("context stop lost finish=%s usage=%+v", r.FinishReason, r.Usage)
			}
		})
	}
}

func TestNativeResponsesIndependentToolUsageEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		valid       bool
	}{
		{"null", `null`, true}, {"empty", `{}`, true}, {"zero", `{"web_search":{"num_requests":0},"image_gen":{"input_tokens":0,"output_tokens":0,"total_tokens":0,"input_tokens_details":{"text_tokens":0,"image_tokens":0},"output_tokens_details":{"text_tokens":0,"image_tokens":0}}}`, true},
		{"web_charge", `{"web_search":{"num_requests":1}}`, false}, {"image_charge", `{"image_gen":{"input_tokens":1}}`, false}, {"invalid_root", `[]`, false}, {"invalid_tool", `{"web_search":null}`, false}, {"invalid_counter", `{"web_search":{"num_requests":"secret-canary"}}`, false}, {"unknown_tool", `{"unknown":{"count":0}}`, false}, {"unknown_dimension", `{"web_search":{"unknown":0}}`, false}, {"invalid_details", `{"image_gen":{"input_tokens_details":null}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				body := strings.Replace(responseFinal, `"usage":`, `"tool_usage":`+tc.usage+`,"usage":`, 1)
				wire := body
				if stream {
					wire = responsesWire(body)
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
					} else {
						w.Header().Set("Content-Type", "application/json")
					}
					_, _ = io.WriteString(w, wire)
				}))
				ctx, cancel := testContext(t)
				r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolResponses, stream))
				if err != nil {
					cancel()
					srv.Close()
					t.Fatal(err)
				}
				var data []byte
				if stream {
					for {
						event, e := r.ReadEvent()
						if e != nil {
							err = e
							break
						}
						if event.Kind == "done" {
							data = event.Data
							break
						}
					}
				} else {
					data, err = r.DecodeJSON()
				}
				_ = r.Close()
				cancel()
				srv.Close()
				if tc.valid {
					if err != nil || r.Usage.Status != UsageConfirmed || strings.Contains(string(data), "tool_usage") {
						t.Fatalf("safe evidence stream=%v usage=%+v data=%s err=%v", stream, r.Usage, data, err)
					}
				} else if err == nil || r.Usage.Status == UsageConfirmed || strings.Contains(err.Error(), "secret-canary") || len(data) != 0 {
					t.Fatalf("unsupported charges accepted stream=%v usage=%+v data=%s err=%v", stream, r.Usage, data, err)
				}
			}
		})
	}
}

func TestNativeRejectsUnboundedOrUnsupportedInputs(t *testing.T) {
	for _, tc := range []struct{ name, protocol, extra string }{
		{"store", ProtocolResponses, `"store":true`},
		{"background", ProtocolResponses, `"background":false`},
		{"prior_response", ProtocolResponses, `"previous_response_id":"resp_1"`},
		{"hosted", ProtocolResponses, `"tools":[{"type":"web_search"}]`},
		{"unknown", ProtocolMessages, `"metadata":{"user_id":"a"}`},
		{"thinking", ProtocolMessages, `"thinking":{"type":"enabled","budget_tokens":1024}`},
		{"cache", ProtocolMessages, `"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}]`},
		{"null", ProtocolMessages, `"stream":null`},
		{"too_many_tokens", ProtocolResponses, `"max_output_tokens":9999`},
		{"duplicate_nested", ProtocolResponses, `"input":[{"role":"user","content":"x","content":"y"}]`},
		{"media", ProtocolResponses, `"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com"}]}]`},
		{"media", ProtocolMessages, `"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com"}}]}]`},
	} {
		t.Run(tc.protocol+"/"+tc.name, func(t *testing.T) {
			base := `"model":"public-model"`
			if !strings.Contains(tc.extra, `"input":`) && tc.protocol == ProtocolResponses {
				base += `,"input":"hello"`
			}
			if !strings.Contains(tc.extra, `"messages":`) && tc.protocol == ProtocolMessages {
				base += `,"messages":[{"role":"user","content":"hello"}]`
			}
			if _, err := Parse(tc.protocol, []byte(`{`+base+`,`+tc.extra+`}`), nativeProfile(tc.protocol)); err == nil {
				t.Fatal("unsupported request accepted")
			}
		})
	}
	p := nativeProfile(ProtocolResponses)
	p.OutputLimitField = "max_tokens"
	if p.Validate() == nil {
		t.Fatal("wrong protocol output field")
	}
	p = nativeProfile(ProtocolMessages)
	p.AllowedParameters = []string{"seed"}
	if p.Validate() == nil {
		t.Fatal("wrong protocol parameter")
	}
	if _, err := ParseChat([]byte(`{}`), nativeProfile(ProtocolResponses)); err == nil {
		t.Fatal("chat protocol mismatch")
	}
	if _, err := Parse(ProtocolMessages, []byte(`{}`), nativeProfile(ProtocolResponses)); err == nil {
		t.Fatal("protocol mismatch")
	}
	if _, err := Parse(ProtocolResponses, []byte(strings.Repeat(" ", MaxRequestBytes+1)), nativeProfile(ProtocolResponses)); err == nil {
		t.Fatal("unbounded body")
	}
}

const responseFinal = `{"id":"resp_1","object":"response","status":"completed","model":"private-model","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"你好","annotations":[]}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6,"input_tokens_details":{"cached_tokens":1},"output_tokens_details":{"reasoning_tokens":0}},"instructions":"secret-canary"}`
const responseCreated = `{"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"private-model","output":[],"usage":null}}`
const messageFinal = `{"id":"msg_1","type":"message","role":"assistant","model":"private-model","content":[{"type":"text","text":"你好"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":1},"private_debug":"secret-canary"}`
const messageStart = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"private-model","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":0,"cache_creation_input_tokens":3,"cache_read_input_tokens":1}}}`

func TestNativeHTTPHeadersJSONAndUsage(t *testing.T) {
	for protocol, body := range map[string]string{ProtocolResponses: responseFinal, ProtocolMessages: messageFinal} {
		t.Run(protocol, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if protocol == ProtocolMessages {
					if req.Header.Get("X-Api-Key") != "secret-canary" || req.Header.Get("Anthropic-Version") != "2023-06-01" || req.Header.Get("Authorization") != "" {
						t.Error("wrong Anthropic headers")
					}
				} else if req.Header.Get("Authorization") != "Bearer secret-canary" || req.Header.Get("X-Api-Key") != "" {
					t.Error("wrong Responses headers")
				}
				payload, _ := io.ReadAll(req.Body)
				m, _ := object(payload)
				if !stringValue(m["model"], "provider-model") {
					t.Error("model mapping missing")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, protocol, false))
			if err != nil {
				t.Fatal(err)
			}
			clean, err := r.DecodeJSON()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(clean), "private-model") || strings.Contains(string(clean), "secret-canary") || !strings.Contains(string(clean), "public-model") {
				t.Fatalf("unsanitized %s", clean)
			}
			wantInput := int64(4)
			if protocol == ProtocolMessages {
				wantInput = 8
			}
			if r.Usage.Status != UsageConfirmed || r.Usage.InputTokens != wantInput || r.Usage.OutputTokens != 2 || r.Usage.CachedTokens != 1 || r.FinishReason != "stop" || r.ProtocolOutcome != "completed" {
				t.Fatalf("wrong outcome %+v", r)
			}
		})
	}
}

func nativeSSE(typ, data string) string { return "event: " + typ + "\r\ndata: " + data + "\r\n\r\n" }
func responsesWire(final string) string {
	return nativeSSE("response.created", responseCreated) + nativeSSE("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"你好"}`) + nativeSSE("response.completed", `{"type":"response.completed","response":`+final+`}`)
}
func messagesWire(delta string) string {
	return nativeSSE("message_start", messageStart) + nativeSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) + nativeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}`) + nativeSSE("content_block_stop", `{"type":"content_block_stop","index":0}`) + nativeSSE("message_delta", delta) + nativeSSE("message_stop", `{"type":"message_stop"}`)
}

func TestNativeSSEFragmentationFinalEventsAndCacheAccounting(t *testing.T) {
	for protocol, wire := range map[string]string{ProtocolResponses: responsesWire(responseFinal), ProtocolMessages: messagesWire(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`)} {
		t.Run(protocol, func(t *testing.T) {
			srv := serveStream(t, wire, true)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, protocol, true))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			content, done := false, false
			for {
				event, err := r.ReadEvent()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if strings.ContainsAny(string(event.Data), "\r\n") {
					t.Fatal("native event was not marshaled as one line")
				}
				if event.Content {
					content = true
				}
				if event.Kind == "done" {
					done = true
					if event.Type != "response.completed" && event.Type != "message_stop" {
						t.Fatalf("bad terminal %+v", event)
					}
					if event.Usage.Status != UsageConfirmed {
						t.Fatalf("usage %+v", event.Usage)
					}
				} else if event.Type == "response.completed" || event.Type == "message_stop" {
					t.Fatal("success terminal escaped before settlement")
				}
			}
			wantInput := int64(4)
			if protocol == ProtocolMessages {
				wantInput = 8
			}
			if !content || !done || r.Usage.InputTokens != wantInput || r.Usage.CachedTokens != 1 || r.ProtocolOutcome != "completed" {
				t.Fatalf("outcome %+v", r)
			}
		})
	}
}

func TestNativeUsageFailuresRemainUnbillable(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, wire string
		status               UsageStatus
	}{
		{"responses_missing", ProtocolResponses, responsesWire(strings.Replace(responseFinal, `"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6,"input_tokens_details":{"cached_tokens":1},"output_tokens_details":{"reasoning_tokens":0}}`, `"usage":null`, 1)), UsageMissing},
		{"responses_conflict", ProtocolResponses, responsesWire(strings.Replace(responseFinal, `"total_tokens":6`, `"total_tokens":7`, 1)), UsageConflicting},
		{"messages_missing_final", ProtocolMessages, messagesWire(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`), UsageProvisional},
		{"messages_null_final", ProtocolMessages, messagesWire(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":null}}`), UsageConflicting},
		{"messages_decrease", ProtocolMessages, messagesWire(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":-1}}`), UsageConflicting},
		{"messages_input_conflict", ProtocolMessages, messagesWire(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":3,"output_tokens":2}}`), UsageConflicting},
		{"messages_hosted_charge", ProtocolMessages, messagesWire(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"server_tool_use":{"web_search_requests":1}}}`), UsageConflicting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveStream(t, tc.wire, false)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, tc.protocol, true))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			for {
				_, err = r.ReadEvent()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if r.Usage.Status != tc.status {
				t.Fatalf("usage %+v want %s", r.Usage, tc.status)
			}
		})
	}
}

func TestNativeFinalUsageRequiresTerminalEvidenceAndAllowsCumulativeMessages(t *testing.T) {
	// A valid snapshot on response.created is still provisional and cannot
	// substitute for absent terminal evidence.
	start := strings.Replace(responseCreated, `"usage":null`, `"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}`, 1)
	final := strings.Replace(responseFinal, `"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6,"input_tokens_details":{"cached_tokens":1},"output_tokens_details":{"reasoning_tokens":0}}`, `"usage":null`, 1)
	wire := nativeSSE("response.created", start) + nativeSSE("response.completed", `{"type":"response.completed","response":`+final+`}`)
	for _, tc := range []struct {
		protocol, wire string
		want           UsageStatus
		input          int64
	}{
		{ProtocolResponses, wire, UsageMissing, 4},
		{ProtocolMessages, messagesWire(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":5,"output_tokens":2,"cache_read_input_tokens":2}}`), UsageConfirmed, 10},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			srv := serveStream(t, tc.wire, false)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, tc.protocol, true))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			for {
				_, err = r.ReadEvent()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if r.Usage.Status != tc.want || r.Usage.InputTokens != tc.input {
				t.Fatalf("usage %+v", r.Usage)
			}
		})
	}
}

func TestResponsesIncompleteHasNativeTerminalAndLengthFinish(t *testing.T) {
	final := strings.Replace(responseFinal, `"status":"completed"`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens","debug":"secret-canary"}`, 1)
	wire := nativeSSE("response.created", responseCreated) + nativeSSE("response.incomplete", `{"type":"response.incomplete","response":`+final+`}`)
	srv := serveStream(t, wire, false)
	defer srv.Close()
	ctx, cancel := testContext(t)
	defer cancel()
	r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, ProtocolResponses, true))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = r.ReadEvent(); err != nil {
		t.Fatal(err)
	}
	event, err := r.ReadEvent()
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != "done" || event.Type != "response.incomplete" || event.FinishReason != "length" || event.Usage.Status != UsageConfirmed || strings.Contains(string(event.Data), "secret-canary") {
		t.Fatalf("terminal %+v", event)
	}
}

func TestNativeToolsAndJSONEvidence(t *testing.T) {
	responseTool := strings.Replace(responseFinal, `[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"你好","annotations":[]}]}]`, `[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call1","name":"weather","arguments":"{}"}]`, 1)
	messageTool := nativeSSE("message_start", messageStart) + nativeSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call1","name":"weather","input":{}}}`) + nativeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`) + nativeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"北京\"}"}}`) + nativeSSE("content_block_stop", `{"type":"content_block_stop","index":0}`) + nativeSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":2}}`) + nativeSSE("message_stop", `{"type":"message_stop"}`)
	for protocol, wire := range map[string]string{ProtocolResponses: responsesWire(responseTool), ProtocolMessages: messageTool} {
		t.Run(protocol, func(t *testing.T) {
			srv := serveStream(t, wire, true)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, protocol, true))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			for {
				_, err = r.ReadEvent()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if r.FinishReason != "tool_calls" || r.Usage.Status != UsageConfirmed {
				t.Fatalf("outcome %+v", r)
			}
		})
	}
	for name, raw := range map[string]string{"cache_overflow": `{"input_tokens":9223372036854775807,"output_tokens":0,"cache_read_input_tokens":1}`, "cache_mismatch": `{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":2}}`, "missing_input": `{"output_tokens":0}`, "explicit_zero": `{"input_tokens":0,"output_tokens":0}`} {
		t.Run(name, func(t *testing.T) {
			u, complete := messagesUsage([]byte(raw))
			if name == "explicit_zero" {
				if !complete || u.TotalTokens != 0 {
					t.Fatal(u)
				}
			} else if complete || u.Status == UsageConfirmed {
				t.Fatalf("unconfirmed evidence accepted %+v", u)
			}
		})
	}
}

func TestNativeProtocolErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct{ name, protocol, wire string }{
		{"responses_eof", ProtocolResponses, nativeSSE("response.created", responseCreated)},
		{"responses_error", ProtocolResponses, nativeSSE("response.created", responseCreated) + nativeSSE("error", `{"type":"error","error":{"message":"secret-canary"}}`)},
		{"responses_unknown", ProtocolResponses, nativeSSE("response.created", responseCreated) + nativeSSE("response.web_search_call.completed", `{"type":"response.web_search_call.completed","output_index":0}`)},
		{"messages_eof", ProtocolMessages, nativeSSE("message_start", messageStart)},
		{"messages_premature_stop", ProtocolMessages, nativeSSE("message_start", messageStart) + nativeSSE("message_stop", `{"type":"message_stop"}`)},
		{"messages_error", ProtocolMessages, nativeSSE("message_start", messageStart) + nativeSSE("error", `{"type":"error","error":{"message":"secret-canary"}}`)},
		{"mismatched_type", ProtocolMessages, nativeSSE("message_stop", messageStart)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveStream(t, tc.wire, false)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, tc.protocol, true))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			for {
				_, err = r.ReadEvent()
				if err != nil {
					break
				}
			}
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "secret-canary") || r.ProtocolOutcome != "failed" {
				t.Fatalf("failure %v %+v", err, r)
			}
		})
	}
	for _, protocol := range []string{ProtocolResponses, ProtocolMessages} {
		t.Run(protocol+"/cancel", func(t *testing.T) {
			closed := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				defer close(closed)
				_, _ = io.Copy(io.Discard, req.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				start := responseCreated
				typ := "response.created"
				if protocol == ProtocolMessages {
					start = messageStart
					typ = "message_start"
				}
				_, _ = io.WriteString(w, nativeSSE(typ, start))
				w.(http.Flusher).Flush()
				<-req.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), nativeRequest(t, protocol, true))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err = r.ReadEvent(); err != nil {
				t.Fatal(err)
			}
			cancel()
			_, err = r.ReadEvent()
			var pErr *Error
			if !errors.As(err, &pErr) || pErr.Class != ClassCancelled {
				t.Fatalf("wrong cancellation %v", err)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream was not closed")
			}
		})
	}
}
