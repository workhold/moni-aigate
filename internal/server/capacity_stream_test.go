package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

type capacityByteCounter struct{ n int }

func (c *capacityByteCounter) Write(p []byte) (int, error) { c.n += len(p); return len(p), nil }

// capacityReadStream is a client-side timing probe for the controlled capacity
// fixture. It is deliberately independent of the gateway's provider parser.
// A complete SSE event containing nonempty content/refusal or function name/
// arguments is the observable content boundary; this is not a tokenizer clock.
func capacityReadStream(r io.Reader, content func()) (done bool, err error) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	seenContent := false
	for scan.Scan() {
		line := strings.TrimSuffix(scan.Text(), "\r")
		if line != "" {
			if v, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(v, " "))
			}
			continue
		}
		if len(data) == 0 {
			continue
		}
		payload := strings.Join(data, "\n")
		data = nil
		if done {
			return false, fmt.Errorf("event after DONE")
		}
		if payload == "[DONE]" {
			done = true
			continue
		}
		var event struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					Refusal string `json:"refusal"`
					Tools   []struct {
						Function struct{ Name, Arguments string } `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return false, fmt.Errorf("invalid capacity SSE JSON")
		}
		if len(event.Error) != 0 && string(event.Error) != "null" {
			return false, fmt.Errorf("capacity SSE error event")
		}
		useful := false
		for _, choice := range event.Choices {
			useful = useful || choice.Delta.Content != "" || choice.Delta.Refusal != ""
			for _, tool := range choice.Delta.Tools {
				useful = useful || tool.Function.Name != "" || tool.Function.Arguments != ""
			}
		}
		if useful && !seenContent {
			seenContent = true
			content()
		}
	}
	if err := scan.Err(); err != nil {
		return false, err
	}
	if !done || len(data) != 0 {
		return false, fmt.Errorf("capacity SSE missing complete DONE")
	}
	return done, nil
}

func TestCapacityContentBoundary(t *testing.T) {
	prefix := ": heartbeat\n\ndata: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"completion_tokens\":1}}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\"}]}}]}\n\n"
	t.Run("noncontent events", func(t *testing.T) {
		n := 0
		done, err := capacityReadStream(strings.NewReader(prefix+"data: [DONE]\n\n"), func() { n++ })
		if err != nil || !done || n != 0 {
			t.Fatalf("noncontent event triggered callback: done=%v callbacks=%d err=%v", done, n, err)
		}
	})
	for _, delta := range []string{`{"content":"hello"}`, `{"refusal":"cannot"}`, `{"tool_calls":[{"function":{"name":"lookup"}}]}`, `{"tool_calls":[{"function":{"arguments":"{}"}}]}`} {
		t.Run(delta, func(t *testing.T) {
			n := 0
			stream := prefix + fmt.Sprintf("data: {\"choices\":[{\"delta\":%s}]}\n\ndata: {\"choices\":[{\"delta\":%s}]}\n\ndata: [DONE]\n\n", delta, delta)
			done, err := capacityReadStream(strings.NewReader(stream), func() { n++ })
			if err != nil || !done || n != 1 {
				t.Fatalf("content boundary done=%v callbacks=%d err=%v", done, n, err)
			}
		})
	}
}

func TestCapacityContentBoundaryRejectsInvalidCompletion(t *testing.T) {
	for _, stream := range []string{
		"data: {\n\ndata: [DONE]\n\n",
		"data: {\"error\":{\"code\":\"failed\"}}\n\ndata: [DONE]\n\n",
		"data: {\"choices\":[]}\n\n",
		"data: [DONE]\n\ndata: {\"choices\":[]}\n\n",
	} {
		if done, err := capacityReadStream(strings.NewReader(stream), func() {}); done || err == nil {
			t.Fatal("invalid stream treated as completed")
		}
	}
}
