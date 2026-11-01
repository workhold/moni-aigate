package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

func protocolError(code string) error { return failure(code, 502, ClassProtocol, ConsumptionUnknown) }

func (r *Response) DecodeJSON() ([]byte, error) {
	if r.profile.ProtocolName() == ProtocolGemini {
		return r.decodeGeminiJSON()
	}
	if r.profile.InputOnly() {
		return r.decodeRetrievalJSON()
	}
	if r.profile.ProtocolName() != ProtocolChat {
		return r.decodeNativeJSON()
	}
	if r.stream {
		return nil, protocolError("expected_non_stream")
	}
	defer r.Close()
	body, err := io.ReadAll(io.LimitReader(&observedReader{response: r}, MaxResponseBytes+1))
	if err != nil {
		return nil, r.readFailure(err)
	}
	if len(body) > MaxResponseBytes {
		return nil, r.readFailure(protocolError("upstream_response_too_large"))
	}
	if !utf8.Valid(body) {
		return nil, r.readFailure(protocolError("upstream_invalid_json"))
	}
	m, err := object(body)
	if err != nil {
		return nil, r.readFailure(protocolError("upstream_invalid_json"))
	}
	if _, ok := m["error"]; ok {
		return nil, r.readFailure(protocolError("upstream_error"))
	}
	clean, event, err := r.normalize(m, false)
	if err != nil {
		return nil, r.readFailure(err)
	}
	if event.FinishReason == "" {
		return nil, r.readFailure(protocolError("upstream_missing_finish"))
	}
	r.FinishReason = event.FinishReason
	r.ProtocolOutcome = "completed"
	r.TransportEndReason = "complete_body"
	r.tracker.observe(m["usage"])
	r.Usage = r.tracker.finalize()
	if r.Usage.Status == UsageConfirmed {
		usage, _ := UsageJSON(r.Usage)
		clean["usage"] = usage
	}
	return json.Marshal(clean)
}

// ReadEvent returns one complete, bounded SSE event. It does not commit any
// downstream headers, emit heartbeats or perform retries.
func (r *Response) ReadEvent() (*Event, error) {
	if r.profile.ProtocolName() == ProtocolGemini {
		return r.readGeminiEvent()
	}
	if r.profile.InputOnly() {
		return nil, protocolError("expected_non_stream")
	}
	if r.profile.ProtocolName() != ProtocolChat {
		return r.readNativeEvent()
	}
	if !r.stream {
		return nil, protocolError("expected_stream")
	}
	if r.done {
		return nil, io.EOF
	}
	typ, data, err := r.frame()
	if err != nil {
		err = r.readFailure(err)
		_ = r.Close()
		return nil, err
	}
	if len(data) == 0 {
		return &Event{Kind: "heartbeat", Type: typ, Usage: r.Usage}, nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		if r.FinishReason == "" {
			err = r.readFailure(protocolError("upstream_missing_finish"))
			_ = r.Close()
			return nil, err
		}
		r.done = true
		r.ProtocolOutcome = "completed"
		r.TransportEndReason = "done"
		r.Usage = r.tracker.finalize()
		_ = r.Close()
		return &Event{Kind: "done", Usage: r.Usage, FinishReason: r.FinishReason}, nil
	}
	if !utf8.Valid(data) {
		err = r.readFailure(protocolError("upstream_invalid_json"))
		_ = r.Close()
		return nil, err
	}
	m, e := object(data)
	if e != nil {
		err = r.readFailure(protocolError("upstream_invalid_json"))
		_ = r.Close()
		return nil, err
	}
	if _, ok := m["error"]; ok || typ == "error" {
		err = r.readFailure(protocolError("upstream_stream_error"))
		_ = r.Close()
		return nil, err
	}
	clean, event, e := r.normalize(m, true)
	if e != nil {
		err = r.readFailure(e)
		_ = r.Close()
		return nil, err
	}
	if r.FinishReason != "" && event.Content {
		err = r.readFailure(protocolError("upstream_content_after_finish"))
		_ = r.Close()
		return nil, err
	}
	if event.FinishReason != "" {
		if r.FinishReason != "" && event.FinishReason != r.FinishReason {
			err = r.readFailure(protocolError("upstream_conflicting_finish"))
			_ = r.Close()
			return nil, err
		}
		r.FinishReason = event.FinishReason
	}
	r.Usage = r.tracker.observe(m["usage"])
	event.Usage = r.Usage
	event.Type = typ
	var choices []json.RawMessage
	_ = json.Unmarshal(m["choices"], &choices)
	if len(choices) == 0 {
		if _, ok := m["usage"]; !ok {
			err = r.readFailure(protocolError("upstream_empty_event"))
			_ = r.Close()
			return nil, err
		}
		event.Kind = "usage"
	} else {
		event.Kind = "chunk"
		event.Data, _ = json.Marshal(clean)
	}
	r.protocolEvent(event.Progress)
	return event, nil
}

func (r *Response) frame() (string, []byte, error) {
	var data []byte
	typ := ""
	size := 0
	seen := false
	for {
		line, n, err := r.line()
		size += n
		if size > MaxEventBytes {
			return "", nil, protocolError("upstream_event_too_large")
		}
		if err != nil {
			if _, ok := err.(*Error); ok {
				return "", nil, err
			}
			if seen || len(line) > 0 {
				return "", nil, io.ErrUnexpectedEOF
			}
			return "", nil, err
		}
		seen = true
		if len(line) == 0 {
			if len(data) > 0 {
				data = data[:len(data)-1]
			}
			return typ, data, nil
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte(":"))
		if !found {
			value = nil
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch string(field) {
		case "event":
			typ = string(value)
		case "data":
			data = append(data, value...)
			data = append(data, '\n')
		}
	}
}
func (r *Response) line() ([]byte, int, error) {
	var line []byte
	n := 0
	for {
		b, e := r.reader.ReadByte()
		if e != nil {
			return line, n, e
		}
		n++
		if n > MaxEventBytes {
			return nil, n, protocolError("upstream_event_too_large")
		}
		if r.skipLF {
			r.skipLF = false
			if b == '\n' {
				continue
			}
		}
		if b == '\r' {
			r.skipLF = true
			return line, n, nil
		}
		if b == '\n' {
			return line, n, nil
		}
		line = append(line, b)
	}
}

func (r *Response) normalize(m map[string]json.RawMessage, stream bool) (map[string]json.RawMessage, *Event, error) {
	var choices []json.RawMessage
	if json.Unmarshal(m["choices"], &choices) != nil || len(choices) > 1 || (!stream && len(choices) != 1) {
		return nil, nil, protocolError("upstream_invalid_choices")
	}
	event := &Event{}
	clean := map[string]json.RawMessage{}
	for _, k := range []string{"id", "object", "created"} {
		if v, ok := m[k]; ok {
			clean[k] = v
		}
	}
	clean["model"], _ = json.Marshal(r.model)
	cleanChoices := []map[string]json.RawMessage{}
	for _, raw := range choices {
		choice, err := object(raw)
		if err != nil {
			return nil, nil, protocolError("upstream_invalid_choices")
		}
		index, valid := integer(choice["index"])
		if !valid || index != 0 {
			return nil, nil, protocolError("upstream_invalid_choices")
		}
		var finish string
		if v, ok := choice["finish_reason"]; ok && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			if json.Unmarshal(v, &finish) != nil || !validFinish(finish) {
				return nil, nil, protocolError("upstream_invalid_finish")
			}
		}
		event.FinishReason = finish
		event.Progress = finish != ""
		field := "message"
		if stream {
			field = "delta"
		}
		message, err := object(choice[field])
		if err != nil {
			return nil, nil, protocolError("upstream_invalid_message")
		}
		out := map[string]json.RawMessage{}
		if v, ok := message["role"]; ok {
			var role string
			if json.Unmarshal(v, &role) != nil || role != "assistant" {
				return nil, nil, protocolError("upstream_invalid_role")
			}
			out["role"] = v
		} else if !stream {
			return nil, nil, protocolError("upstream_invalid_role")
		}
		for _, k := range []string{"content", "refusal"} {
			if v, ok := message[k]; ok {
				if !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
					var s string
					if json.Unmarshal(v, &s) != nil {
						return nil, nil, protocolError("upstream_non_text_output")
					}
					if s != "" {
						event.Content = true
						event.Progress = true
					}
				}
				out[k] = v
			}
		}
		if calls, ok := message["tool_calls"]; ok {
			if !r.profile.SupportsTools {
				return nil, nil, protocolError("upstream_unsupported_tool")
			}
			if stream {
				var deltas []map[string]json.RawMessage
				if json.Unmarshal(calls, &deltas) != nil || len(deltas) == 0 || len(deltas) > 64 {
					return nil, nil, protocolError("upstream_invalid_tool")
				}
				for _, delta := range deltas {
					if !only(delta, "index", "id", "type", "function") {
						return nil, nil, protocolError("upstream_invalid_tool")
					}
					index, valid := integer(delta["index"])
					if !valid || index < 0 || index >= 64 {
						return nil, nil, protocolError("upstream_invalid_tool")
					}
					for _, k := range []string{"id", "type"} {
						if v, ok := delta[k]; ok {
							var s string
							if json.Unmarshal(v, &s) != nil || (k == "type" && s != "function") {
								return nil, nil, protocolError("upstream_invalid_tool")
							}
						}
					}
					if v, ok := delta["function"]; ok {
						fn, err := object(v)
						if err != nil || !only(fn, "name", "arguments") {
							return nil, nil, protocolError("upstream_invalid_tool")
						}
						for _, value := range fn {
							var s string
							if json.Unmarshal(value, &s) != nil {
								return nil, nil, protocolError("upstream_invalid_tool")
							}
						}
					}
				}
			} else if validateToolCalls(calls) != nil {
				return nil, nil, protocolError("upstream_invalid_tool")
			}
			out["tool_calls"] = calls
			event.Content = true
			event.Progress = true
		}
		messageJSON, _ := json.Marshal(out)
		c := map[string]json.RawMessage{"index": json.RawMessage("0"), field: messageJSON}
		if finish != "" {
			c["finish_reason"], _ = json.Marshal(finish)
		} else {
			c["finish_reason"] = json.RawMessage("null")
		}
		cleanChoices = append(cleanChoices, c)
	}
	clean["choices"], _ = json.Marshal(cleanChoices)
	return clean, event, nil
}
func validFinish(s string) bool {
	return s == "stop" || s == "length" || s == "tool_calls" || s == "content_filter"
}
