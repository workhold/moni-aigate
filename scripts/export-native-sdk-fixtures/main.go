// Export synthetic native responses after the real provider validation path.
// This development tool never accepts an upstream URL or a real credential.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

const responsesJSON = `{"id":"resp_sdk_fixture","object":"response","created_at":0,"status":"completed","model":"fixture-upstream-model","error":null,"incomplete_details":null,"output":[{"id":"msg_sdk_fixture","type":"message","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Hello SDK!","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6,"input_tokens_details":{"cached_tokens":1,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}},"tool_usage":{"web_search":{"num_requests":0}}}`
const responsesCreated = `{"type":"response.created","sequence_number":0,"response":{"id":"resp_sdk_fixture","object":"response","created_at":0,"status":"in_progress","model":"fixture-upstream-model","output":[],"usage":null}}`
const messagesJSON = `{"id":"msg_sdk_fixture","type":"message","role":"assistant","model":"fixture-upstream-model","content":[{"type":"text","text":"Hello SDK!","citations":null}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":1,"cache_creation":null,"output_tokens_details":{"thinking_tokens":0},"service_tier":"standard","speed":"standard"}}`
const messagesCreated = `{"type":"message_start","message":{"id":"msg_sdk_fixture","type":"message","role":"assistant","model":"fixture-upstream-model","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":0,"cache_creation_input_tokens":3,"cache_read_input_tokens":1,"cache_creation":null,"output_tokens_details":null,"service_tier":null,"speed":null}}}`

func frame(typ, data string) string { return "event: " + typ + "\ndata: " + data + "\n\n" }

func responsesSSE() string {
	return frame("response.created", responsesCreated) +
		frame("response.in_progress", strings.Replace(responsesCreated, "response.created", "response.in_progress", 1)) +
		frame("response.output_item.added", `{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"msg_sdk_fixture","type":"message","status":"in_progress","role":"assistant","phase":"final_answer","content":[]}}`) +
		frame("response.content_part.added", `{"type":"response.content_part.added","sequence_number":3,"item_id":"msg_sdk_fixture","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}}`) +
		frame("response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_sdk_fixture","output_index":0,"content_index":0,"delta":"Hello SDK!","logprobs":[]}`) +
		frame("response.output_text.done", `{"type":"response.output_text.done","sequence_number":5,"item_id":"msg_sdk_fixture","output_index":0,"content_index":0,"text":"Hello SDK!","logprobs":[]}`) +
		frame("response.content_part.done", `{"type":"response.content_part.done","sequence_number":6,"item_id":"msg_sdk_fixture","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello SDK!","annotations":[],"logprobs":[]}}`) +
		frame("response.output_item.done", `{"type":"response.output_item.done","sequence_number":7,"output_index":0,"item":{"id":"msg_sdk_fixture","type":"message","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Hello SDK!","annotations":[],"logprobs":[]}]}}`) +
		frame("response.completed", `{"type":"response.completed","sequence_number":8,"response":`+responsesJSON+`}`)
}

func messagesSSE() string {
	return frame("message_start", messagesCreated) +
		frame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"","citations":null}}`) +
		frame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello SDK!"}}`) +
		frame("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"container":null,"stop_details":null},"usage":{"input_tokens":null,"cache_creation_input_tokens":null,"cache_read_input_tokens":null,"output_tokens":2,"output_tokens_details":{"thinking_tokens":0},"service_tier":"standard","speed":"standard"}}`) +
		frame("message_stop", `{"type":"message_stop"}`)
}

func request(protocol string, stream bool) (*provider.Request, error) {
	p := provider.Profile{Protocol: protocol, Version: "sdk-fixture-v1", Model: "public-model", InputTokenUpperBound: 128, ContextLimit: 256, OutputLimit: 128, DefaultOutputLimit: 32}
	payload := map[string]any{"model": "public-model", "stream": stream}
	if protocol == provider.ProtocolResponses {
		payload["input"] = "SDK fixture"
		payload["store"] = false
		payload["max_output_tokens"] = 32
	} else {
		payload["messages"] = []map[string]string{{"role": "user", "content": "SDK fixture"}}
		payload["max_tokens"] = 32
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return provider.Parse(protocol, raw, p)
}

func export(protocol string, stream bool) ([]byte, error) {
	req, err := request(protocol, stream)
	if err != nil {
		return nil, err
	}
	body := responsesJSON
	if protocol == provider.ProtocolMessages {
		body = messagesJSON
	}
	if stream {
		body = responsesSSE()
		if protocol == provider.ProtocolMessages {
			body = messagesSSE()
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/v1"+req.EndpointSuffix() {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		media := "application/json"
		if stream {
			media = "text/event-stream"
		}
		w.Header().Set("Content-Type", media)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil || u.Hostname() != "127.0.0.1" {
		return nil, errors.New("fixture_address")
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint := provider.Endpoint{URL: server.URL + "/v1" + req.EndpointSuffix(), APIKey: "local-fixture-only", Model: "fixture-upstream-model", Network: provider.NetworkPolicy{AllowHTTP: true, AllowedCIDRs: []string{"127.0.0.1/32"}, AllowedPorts: []uint16{uint16(port)}}}
	response, err := provider.NewClient(provider.ClientOptions{}).Do(ctx, endpoint, req)
	if err != nil {
		return nil, err
	}
	defer response.Close()
	var result []byte
	if stream {
		var out strings.Builder
		for {
			event, err := response.ReadEvent()
			if err != nil {
				return nil, err
			}
			if event.Kind == "heartbeat" || event.Kind == "usage" {
				continue
			}
			if strings.ContainsAny(event.Type, "\r\n") || strings.ContainsAny(string(event.Data), "\r\n") || !json.Valid(event.Data) {
				return nil, errors.New("fixture_event")
			}
			out.WriteString(frame(event.Type, string(event.Data)))
			if event.Kind == "done" {
				break
			}
		}
		result = []byte(out.String())
	} else {
		result, err = response.DecodeJSON()
		if err != nil {
			return nil, err
		}
	}
	if response.ProtocolOutcome != "completed" || response.Usage.Status != provider.UsageConfirmed {
		return nil, errors.New("fixture_final_usage")
	}
	return result, nil
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("export-native-sdk-fixtures", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	out := flags.String("out", "", "output directory")
	if err := flags.Parse(arguments); err != nil || *out == "" || flags.NArg() != 0 || strings.Contains(*out, "://") {
		return errors.New("arguments")
	}
	if err := os.MkdirAll(*out, 0700); err != nil {
		return err
	}
	for _, protocol := range []string{provider.ProtocolResponses, provider.ProtocolMessages} {
		for _, stream := range []bool{false, true} {
			body, err := export(protocol, stream)
			if err != nil {
				return err
			}
			extension := ".json"
			if stream {
				extension = ".sse"
			}
			path := filepath.Join(*out, protocol+extension)
			if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
				return errors.New("fixture_destination")
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.WriteFile(path, body, 0600); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL native_sdk_fixture_export")
		os.Exit(1)
	}
	fmt.Println("PASS native_sdk_fixtures")
}
