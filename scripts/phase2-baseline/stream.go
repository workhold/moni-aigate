package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type byteTimingReader struct {
	reader io.Reader
	first  *time.Time
}

func (r *byteTimingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.first == nil {
		now := time.Now()
		r.first = &now
	}
	return n, err
}

// readChatStream implements the measurement_version=2 client-side TTFC
// boundary. Role-only, heartbeat, empty delta, usage-only and DONE are not
// content; tool id/index alone are not content.
func readChatStream(reader io.Reader, content func()) (bool, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	seenContent := false
	done := false
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line != "" {
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(value, " "))
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
		if json.Unmarshal([]byte(payload), &event) != nil {
			return false, fmt.Errorf("invalid SSE JSON")
		}
		if len(event.Error) != 0 && string(event.Error) != "null" {
			return false, fmt.Errorf("SSE error event")
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
	if err := scanner.Err(); err != nil {
		return false, err
	}
	if !done || len(data) != 0 {
		return false, fmt.Errorf("missing complete DONE")
	}
	if !seenContent {
		return false, fmt.Errorf("stream completed without useful content")
	}
	return true, nil
}

func rejectionCode(body []byte) string {
	var value struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &value) != nil {
		return "unclassified"
	}
	if value.Error.Code != "" {
		return value.Error.Code
	}
	if value.Error.Type != "" {
		return value.Error.Type
	}
	return "unclassified"
}
