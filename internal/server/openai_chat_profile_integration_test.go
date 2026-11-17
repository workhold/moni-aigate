package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/catalog"
)

const openAIChatToolJSON = `{"id":"chat-tool-fixture","object":"chat.completion","model":"provider-model","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_weather","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}},{"id":"call_time","type":"function","function":{"name":"time","arguments":"{\"zone\":\"UTC\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

var openAIChatToolChunks = []string{
	`{"id":"chat-tool-fixture","object":"chat.completion.chunk","model":"provider-model","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"weather","arguments":""}}]},"finish_reason":null}]}`,
	`{"id":"chat-tool-fixture","object":"chat.completion.chunk","model":"provider-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]},"finish_reason":null}]}`,
	`{"id":"chat-tool-fixture","object":"chat.completion.chunk","model":"provider-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"finish_reason":null}]}`,
	`{"id":"chat-tool-fixture","object":"chat.completion.chunk","model":"provider-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_time","type":"function","function":{"name":"time","arguments":""}}]},"finish_reason":null}]}`,
	`{"id":"chat-tool-fixture","object":"chat.completion.chunk","model":"provider-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"zone\":\"UTC\"}"}}]},"finish_reason":null}]}`,
	`{"id":"chat-tool-fixture","object":"chat.completion.chunk","model":"provider-model","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	`{"id":"chat-tool-fixture","object":"chat.completion.chunk","model":"provider-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
}

type chatToolCall struct {
	index                    int
	id, typ, name, arguments string
}

func openAIChatToolRequest(t *testing.T, f *relayFixture, stream bool) (string, int, []byte) {
	t.Helper()
	body := map[string]any{
		"model": "retail-model", "stream": stream, "max_tokens": 20,
		"messages": []any{map[string]any{"role": "user", "content": "weather and time"}},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "weather", "parameters": map[string]any{"type": "object"}}},
			map[string]any{"type": "function", "function": map[string]any{"name": "time", "parameters": map[string]any{"type": "object"}}},
		},
		"tool_choice": "auto", "parallel_tool_calls": true,
	}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.http.URL+"/v1/chat/completions", bytes.NewReader(raw))
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
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.Header.Get("X-Request-ID"), response.StatusCode, responseBody
}

func assertOpenAIChatToolResponse(t *testing.T, raw []byte, stream bool) {
	t.Helper()
	calls := map[int]*chatToolCall{}
	finishes, usageFrames, done := []string{}, 0, 0
	observe := func(value []byte) {
		var event struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				Message struct {
					ToolCalls []struct {
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
				Finish string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				Input  int64 `json:"prompt_tokens"`
				Output int64 `json:"completion_tokens"`
				Total  int64 `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(value, &event); err != nil {
			t.Fatalf("invalid normalized Chat response: %s (%v)", value, err)
		}
		if event.Model != "retail-model" {
			t.Fatalf("upstream model escaped into public response: %s", value)
		}
		for _, choice := range event.Choices {
			if choice.Finish != "" {
				finishes = append(finishes, choice.Finish)
			}
			if stream {
				for _, delta := range choice.Delta.ToolCalls {
					call := calls[delta.Index]
					if call == nil {
						call = &chatToolCall{index: delta.Index}
						calls[delta.Index] = call
					}
					if delta.ID != "" {
						call.id = delta.ID
					}
					if delta.Type != "" {
						call.typ = delta.Type
					}
					call.name += delta.Function.Name
					call.arguments += delta.Function.Arguments
				}
			} else {
				for _, item := range choice.Message.ToolCalls {
					calls[len(calls)] = &chatToolCall{index: len(calls), id: item.ID, typ: item.Type, name: item.Function.Name, arguments: item.Function.Arguments}
				}
			}
		}
		if event.Usage != nil {
			usageFrames++
			if event.Usage.Input != 10 || event.Usage.Output != 5 || event.Usage.Total != 15 {
				t.Fatalf("unexpected final usage: %+v", event.Usage)
			}
		}
	}
	if stream {
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				done++
				continue
			}
			observe([]byte(data))
		}
	} else {
		observe(raw)
	}
	if len(calls) != 2 || calls[0].id != "call_weather" || calls[0].typ != "function" || calls[0].name != "weather" || calls[0].arguments != `{"city":"Paris"}` || calls[1].id != "call_time" || calls[1].typ != "function" || calls[1].name != "time" || calls[1].arguments != `{"zone":"UTC"}` {
		t.Fatalf("tool calls were dropped or reordered: %+v body=%s", calls, raw)
	}
	if len(finishes) != 1 || finishes[0] != "tool_calls" || usageFrames != 1 || stream && done != 1 {
		t.Fatalf("invalid terminal evidence: finishes=%v usage=%d done=%d body=%s", finishes, usageFrames, done, raw)
	}
}

func TestOpenAIChatProfileToolJSONAndSSEAcrossImplementations(t *testing.T) {
	for _, implementation := range []string{"external", "embedded"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_stream_%t", implementation, stream), func(t *testing.T) {
				f := newRelayFixture(t)
				upstream, channel, upstreamModel, other := f.newAPI, f.newAPIChannel, "relay-newapi-model", f.direct
				if implementation == "embedded" {
					upstream, channel, upstreamModel, other = f.direct, f.directChannel, "provider-model", f.newAPI
				}
				upstream.setBehavior("chat_tool_calls")
				revision := f.publish(t, catalog.BindingInput{ChannelID: channel, UpstreamModel: upstreamModel, Priority: 1, Weight: 1, MaxInflight: 4})
				requestID, status, raw := openAIChatToolRequest(t, f, stream)
				if status != http.StatusOK {
					t.Fatalf("Chat tool request returned HTTP %d: %s", status, raw)
				}
				assertOpenAIChatToolResponse(t, raw, stream)
				model, _ := upstream.observed()
				observed := upstream.observedRequest()
				if model != upstreamModel || observed.stream != stream || !observed.parallel || observed.tools != 2 || stream && !observed.includeUsage {
					t.Fatalf("normalized upstream request: model=%q observed=%+v", model, observed)
				}
				attempts, settled, gotRevision := f.requestFacts(t, requestID)
				if attempts != 1 || !settled || gotRevision != revision || upstream.count() != 1 || other.count() != 0 {
					t.Fatalf("profile facts: attempts=%d settled=%v revision=%d upstream=%d other=%d", attempts, settled, gotRevision, upstream.count(), other.count())
				}
				var charge, usage int64
				if err := f.pool.QueryRow(context.Background(), `SELECT s.charged_amount,(SELECT count(*) FROM metering_events WHERE request_id=$1) FROM settlements s WHERE s.request_id=$1`, requestID).Scan(&charge, &usage); err != nil || charge != 20 || usage != 1 {
					t.Fatalf("unique exact settlement: charge=%d usage_events=%d err=%v", charge, usage, err)
				}
			})
		}
	}
}

func TestOpenAIChatProfileLengthAndContentFilterSettleWithoutReplay(t *testing.T) {
	for _, reason := range []string{"length", "content_filter"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_stream_%t", reason, stream), func(t *testing.T) {
				f := newRelayFixture(t)
				f.direct.setBehavior("finish_" + reason)
				f.publish(t, catalog.BindingInput{ChannelID: f.directChannel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 4})
				requestID, status, raw := openAIChatToolRequest(t, f, stream)
				if status != http.StatusOK || !bytes.Contains(raw, []byte(`"finish_reason":"`+reason+`"`)) || !bytes.Contains(raw, []byte(`"model":"retail-model"`)) {
					t.Fatalf("terminal response %s stream=%t: HTTP %d %s", reason, stream, status, raw)
				}
				attempts, settled, _ := f.requestFacts(t, requestID)
				if attempts != 1 || !settled || f.direct.count() != 1 || f.newAPI.count() != 0 {
					t.Fatalf("terminal %s replayed or failed settlement: attempts=%d settled=%v direct=%d external=%d", reason, attempts, settled, f.direct.count(), f.newAPI.count())
				}
			})
		}
	}
}

func TestOpenAIChatProfilePublishesFreshRevisionAfterRollback(t *testing.T) {
	f := newRelayFixture(t)
	external := catalog.BindingInput{ChannelID: f.newAPIChannel, UpstreamModel: "relay-newapi-model", Priority: 1, Weight: 1, MaxInflight: 4}
	embedded := catalog.BindingInput{ChannelID: f.directChannel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 4}
	relayRevision := f.publish(t, external)
	embeddedRevision := f.publish(t, embedded)
	f.activate(t, embeddedRevision, relayRevision)
	freshRevision := f.publish(t, embedded)
	if relayRevision != 1 || embeddedRevision != 2 || freshRevision != 3 {
		t.Fatalf("revision allocation reused history: relay=%d embedded=%d fresh=%d", relayRevision, embeddedRevision, freshRevision)
	}
	var count, distinct, active int64
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*),count(DISTINCT revision),(SELECT active_revision FROM models WHERE id=$1) FROM route_policy_versions WHERE model_id=$1`, f.model).Scan(&count, &distinct, &active); err != nil || count != 3 || distinct != 3 || active != freshRevision {
		t.Fatalf("fresh publication changed history: count=%d distinct=%d active=%d err=%v", count, distinct, active, err)
	}
	requestID, status, raw := f.request(t)
	if status != http.StatusOK {
		t.Fatalf("fresh revision did not serve: %d %s", status, raw)
	}
	attempts, settled, requestRevision := f.requestFacts(t, requestID)
	if attempts != 1 || !settled || requestRevision != freshRevision || f.direct.count() != 1 || f.newAPI.count() != 0 {
		t.Fatalf("fresh revision facts: attempts=%d settled=%v revision=%d direct=%d external=%d", attempts, settled, requestRevision, f.direct.count(), f.newAPI.count())
	}
}
