package provider

import (
	"encoding/json"
	"io"
	"unicode/utf8"
)

type nativeBlock struct {
	typ       string
	active    bool
	arguments []byte
}
type nativeState struct {
	started    bool
	responseID string
	finalUsage bool
	usage      map[string]json.RawMessage
	blocks     map[int64]*nativeBlock
}

func (r *Response) decodeNativeJSON() ([]byte, error) {
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
	var clean map[string]json.RawMessage
	var event *Event
	if r.profile.ProtocolName() == ProtocolResponses {
		clean, event, err = r.normalizeResponse(m, true)
		if err == nil {
			r.finishResponseUsage(m["usage"])
			clean["usage"] = nativeUsageJSON(ProtocolResponses, r.Usage)
		}
	} else {
		clean, event, err = r.normalizeMessage(m, true)
		if err == nil {
			var complete bool
			r.Usage, complete = messagesUsage(m["usage"])
			if complete && r.Usage.Status != UsageConflicting {
				r.Usage.Status = UsageConfirmed
			}
			if r.Usage.Status == UsageConfirmed {
				clean["usage"] = cleanMessageUsage(m["usage"])
			} else {
				clean["usage"] = json.RawMessage("null")
			}
		}
	}
	if err != nil {
		return nil, r.readFailure(err)
	}
	r.FinishReason = event.FinishReason
	r.ProtocolOutcome = "completed"
	r.TransportEndReason = "complete_body"
	return json.Marshal(clean)
}

func (r *Response) readNativeEvent() (*Event, error) {
	if !r.stream {
		return nil, protocolError("expected_stream")
	}
	if r.done {
		return nil, io.EOF
	}
	fail := func(err error) (*Event, error) { err = r.readFailure(err); _ = r.Close(); return nil, err }
	typ, data, err := r.frame()
	if err != nil {
		return fail(err)
	}
	if len(data) == 0 {
		return &Event{Kind: "heartbeat", Type: typ, Usage: r.Usage}, nil
	}
	if !utf8.Valid(data) {
		return fail(protocolError("upstream_invalid_json"))
	}
	m, err := object(data)
	if err != nil {
		return fail(protocolError("upstream_invalid_json"))
	}
	var declared string
	if json.Unmarshal(m["type"], &declared) != nil || declared == "" || (typ != "" && typ != declared) {
		return fail(protocolError("upstream_invalid_event"))
	}
	typ = declared
	if typ == "error" || typ == "response.failed" || (m["error"] != nil && !isNull(m["error"])) {
		return fail(protocolError("upstream_stream_error"))
	}
	var event *Event
	if r.profile.ProtocolName() == ProtocolResponses {
		event, err = r.responseEvent(typ, m)
	} else {
		event, err = r.messageEvent(typ, m)
	}
	if err != nil {
		return fail(err)
	}
	event.Type = typ
	event.Usage = r.Usage
	r.protocolEvent(event.Progress)
	if event.Kind == "done" {
		r.done = true
		r.ProtocolOutcome = "completed"
		r.TransportEndReason = "done"
		_ = r.Close()
	}
	return event, nil
}

func nativeClean(m map[string]json.RawMessage, keys ...string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}
func encoded(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func invalidOutput() error          { return protocolError("upstream_invalid_native_output") }
func optionalString(m map[string]json.RawMessage, key string) bool {
	v, ok := m[key]
	return !ok || isNull(v) || isString(v)
}

func (r *Response) normalizeResponse(m map[string]json.RawMessage, final bool) (map[string]json.RawMessage, *Event, error) {
	if !nonemptyString(m["id"]) || !stringValue(m["object"], "response") || (m["error"] != nil && !isNull(m["error"])) {
		return nil, nil, invalidOutput()
	}
	if raw, ok := m["tool_usage"]; ok && !safeResponseToolUsage(raw) {
		return nil, nil, protocolError("upstream_unsupported_tool_usage")
	}
	var status string
	if json.Unmarshal(m["status"], &status) != nil {
		return nil, nil, invalidOutput()
	}
	event := &Event{}
	if final {
		switch status {
		case "completed":
			event.FinishReason = "stop"
		case "incomplete":
			d, err := object(m["incomplete_details"])
			if err != nil {
				return nil, nil, invalidOutput()
			}
			if stringValue(d["reason"], "max_output_tokens") {
				event.FinishReason = "length"
			} else if stringValue(d["reason"], "content_filter") {
				event.FinishReason = "content_filter"
			} else {
				return nil, nil, invalidOutput()
			}
		default:
			return nil, nil, invalidOutput()
		}
	} else if status != "in_progress" {
		return nil, nil, invalidOutput()
	}
	var output []map[string]json.RawMessage
	if json.Unmarshal(m["output"], &output) != nil || output == nil || len(output) > 64 {
		return nil, nil, invalidOutput()
	}
	cleanItems := make([]map[string]json.RawMessage, 0, len(output))
	toolCount := 0
	for _, item := range output {
		clean, content, err := r.responseItem(item, final)
		if err != nil {
			return nil, nil, err
		}
		cleanItems = append(cleanItems, clean)
		event.Content = event.Content || content
		if stringValue(item["type"], "function_call") && event.FinishReason == "stop" {
			event.FinishReason = "tool_calls"
		}
		if stringValue(item["type"], "function_call") {
			toolCount++
			if toolCount > 1 && !r.profile.SupportsParallelToolCalls {
				return nil, nil, invalidOutput()
			}
		}
	}
	event.Progress = event.Content || event.FinishReason != ""
	clean := nativeClean(m, "id", "object", "created_at", "status", "incomplete_details", "max_output_tokens")
	if v, ok := clean["created_at"]; ok {
		n, valid := integer(v)
		if !valid || n < 0 {
			return nil, nil, invalidOutput()
		}
	}
	if v, ok := clean["max_output_tokens"]; ok && !isNull(v) {
		n, valid := integer(v)
		if !valid || n <= 0 {
			return nil, nil, invalidOutput()
		}
	}
	if status == "incomplete" {
		d, _ := object(m["incomplete_details"])
		clean["incomplete_details"] = encoded(nativeClean(d, "reason"))
	} else {
		delete(clean, "incomplete_details")
	}
	clean["model"] = encoded(r.model)
	clean["output"] = encoded(cleanItems)
	return clean, event, nil
}

func (r *Response) responseItem(item map[string]json.RawMessage, final bool) (map[string]json.RawMessage, bool, error) {
	if !nonemptyString(item["id"]) {
		return nil, false, invalidOutput()
	}
	if v, ok := item["status"]; ok && !stringValue(v, "in_progress") && !stringValue(v, "completed") && !stringValue(v, "incomplete") {
		return nil, false, invalidOutput()
	}
	if stringValue(item["type"], "message") {
		if !only(item, "type", "id", "status", "role", "content", "phase") || !stringValue(item["role"], "assistant") || !responsePhase(item) {
			return nil, false, invalidOutput()
		}
		var parts []map[string]json.RawMessage
		if json.Unmarshal(item["content"], &parts) != nil || parts == nil || len(parts) > 1024 {
			return nil, false, invalidOutput()
		}
		out := make([]map[string]json.RawMessage, 0, len(parts))
		content := false
		for _, part := range parts {
			clean, nonempty, err := responsePart(part)
			if err != nil {
				return nil, false, err
			}
			out = append(out, clean)
			content = content || nonempty
		}
		clean := nativeClean(item, "type", "id", "status", "role", "phase")
		clean["content"] = encoded(out)
		return clean, content, nil
	}
	if stringValue(item["type"], "function_call") && r.profile.SupportsTools {
		if !only(item, "type", "id", "status", "call_id", "name", "arguments") || !nonemptyString(item["call_id"]) || !namedFunction(item["name"]) || !isString(item["arguments"]) {
			return nil, false, invalidOutput()
		}
		if final && !stringValue(item["status"], "incomplete") {
			var args string
			_ = json.Unmarshal(item["arguments"], &args)
			if _, err := object([]byte(args)); err != nil {
				return nil, false, invalidOutput()
			}
		}
		return nativeClean(item, "type", "id", "status", "call_id", "name", "arguments"), true, nil
	}
	return nil, false, invalidOutput()
}

func responsePart(part map[string]json.RawMessage) (map[string]json.RawMessage, bool, error) {
	if stringValue(part["type"], "output_text") {
		if !only(part, "type", "text", "annotations", "logprobs") || !isString(part["text"]) {
			return nil, false, invalidOutput()
		}
		for _, key := range []string{"annotations", "logprobs"} {
			if v, ok := part[key]; ok {
				if key == "logprobs" && isNull(v) {
					continue
				}
				var values []json.RawMessage
				if json.Unmarshal(v, &values) != nil || values == nil || len(values) != 0 {
					return nil, false, invalidOutput()
				}
			}
		}
		clean := nativeClean(part, "type", "text", "annotations")
		clean["logprobs"] = json.RawMessage("[]")
		return clean, !stringValue(part["text"], ""), nil
	}
	if stringValue(part["type"], "refusal") && only(part, "type", "refusal") && isString(part["refusal"]) {
		return nativeClean(part, "type", "refusal"), !stringValue(part["refusal"], ""), nil
	}
	return nil, false, invalidOutput()
}

func (r *Response) responseEvent(typ string, m map[string]json.RawMessage) (*Event, error) {
	event := &Event{Kind: "chunk"}
	clean := nativeClean(m, "type", "sequence_number")
	if v, ok := m["sequence_number"]; ok {
		n, valid := integer(v)
		if !valid || n < 0 {
			return nil, invalidOutput()
		}
	}
	if typ == "response.created" || typ == "response.in_progress" || typ == "response.completed" || typ == "response.incomplete" {
		if typ == "response.created" {
			if r.native.started {
				return nil, invalidOutput()
			}
			r.native.started = true
		} else if !r.native.started {
			return nil, invalidOutput()
		}
		response, err := object(m["response"])
		if err != nil {
			return nil, invalidOutput()
		}
		final := typ == "response.completed" || typ == "response.incomplete"
		if (typ == "response.completed" && !stringValue(response["status"], "completed")) || (typ == "response.incomplete" && !stringValue(response["status"], "incomplete")) {
			return nil, invalidOutput()
		}
		body, normalized, err := r.normalizeResponse(response, final)
		if err != nil {
			return nil, err
		}
		var responseID string
		_ = json.Unmarshal(response["id"], &responseID)
		if typ == "response.created" {
			r.native.responseID = responseID
		} else if responseID != r.native.responseID {
			return nil, protocolError("upstream_response_identity_changed")
		}
		r.Usage = r.tracker.observe(responseUsage(response["usage"]))
		if final {
			r.finishResponseUsage(response["usage"])
			body["usage"] = nativeUsageJSON(ProtocolResponses, r.Usage)
			event.Kind = "done"
			event.FinishReason = normalized.FinishReason
			r.FinishReason = normalized.FinishReason
		} else {
			body["usage"] = json.RawMessage("null")
		}
		clean["response"] = encoded(body)
		event.Progress = final
	} else {
		if !r.native.started || r.FinishReason != "" {
			return nil, invalidOutput()
		}
		index, valid := integer(m["output_index"])
		if !valid || index < 0 || index >= 64 {
			return nil, invalidOutput()
		}
		clean["output_index"] = m["output_index"]
		switch typ {
		case "response.output_item.added", "response.output_item.done":
			item, err := object(m["item"])
			if err != nil {
				return nil, invalidOutput()
			}
			out, content, err := r.responseItem(item, typ == "response.output_item.done")
			if err != nil {
				return nil, err
			}
			clean["item"] = encoded(out)
			event.Content = content
			event.Progress = content
		case "response.content_part.added", "response.content_part.done":
			if !nonemptyString(m["item_id"]) {
				return nil, invalidOutput()
			}
			ci, valid := integer(m["content_index"])
			if !valid || ci < 0 || ci >= 1024 {
				return nil, invalidOutput()
			}
			part, err := object(m["part"])
			if err != nil {
				return nil, invalidOutput()
			}
			out, content, err := responsePart(part)
			if err != nil {
				return nil, err
			}
			clean["item_id"] = m["item_id"]
			clean["content_index"] = m["content_index"]
			clean["part"] = encoded(out)
			event.Content = content
			event.Progress = content
		case "response.output_text.delta", "response.output_text.done", "response.refusal.delta", "response.refusal.done", "response.function_call_arguments.delta", "response.function_call_arguments.done":
			if !nonemptyString(m["item_id"]) {
				return nil, invalidOutput()
			}
			clean["item_id"] = m["item_id"]
			field := "delta"
			switch typ {
			case "response.output_text.done":
				field = "text"
			case "response.refusal.done":
				field = "refusal"
			case "response.function_call_arguments.done":
				field = "arguments"
			}
			if !isString(m[field]) {
				return nil, invalidOutput()
			}
			clean[field] = m[field]
			if typ == "response.output_text.delta" || typ == "response.output_text.done" {
				if v, ok := m["logprobs"]; ok && !emptyOrNullArray(v) {
					return nil, invalidOutput()
				}
				// The SDK's stream accumulator accesses this required field even
				// when no log probabilities were requested.
				clean["logprobs"] = json.RawMessage("[]")
			}
			event.Content = !stringValue(m[field], "")
			event.Progress = event.Content
			if typ == "response.function_call_arguments.delta" || typ == "response.function_call_arguments.done" {
				if !r.profile.SupportsTools {
					return nil, invalidOutput()
				}
			} else {
				ci, valid := integer(m["content_index"])
				if !valid || ci < 0 || ci >= 1024 {
					return nil, invalidOutput()
				}
				clean["content_index"] = m["content_index"]
			}
		default:
			return nil, protocolError("upstream_unsupported_event")
		}
	}
	event.Data = encoded(clean)
	return event, nil
}

func messageFinish(raw []byte) (string, bool) {
	if len(raw) == 0 || isNull(raw) {
		return "", true
	}
	var reason string
	if json.Unmarshal(raw, &reason) != nil {
		return "", false
	}
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop", true
	case "max_tokens", "model_context_window_exceeded":
		return "length", true
	case "tool_use":
		return "tool_calls", true
	case "refusal":
		return "content_filter", true
	}
	return "", false
}

func (r *Response) normalizeMessage(m map[string]json.RawMessage, final bool) (map[string]json.RawMessage, *Event, error) {
	if !nonemptyString(m["id"]) || !stringValue(m["type"], "message") || !stringValue(m["role"], "assistant") || (m["error"] != nil && !isNull(m["error"])) {
		return nil, nil, invalidOutput()
	}
	finish, valid := messageFinish(m["stop_reason"])
	if !valid || (final && finish == "") || (!final && finish != "") || !optionalString(m, "stop_sequence") {
		return nil, nil, invalidOutput()
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(m["content"], &blocks) != nil || blocks == nil || len(blocks) > 64 {
		return nil, nil, invalidOutput()
	}
	event := &Event{FinishReason: finish}
	out := make([]map[string]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		clean, content, err := r.messageBlock(block)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, clean)
		event.Content = event.Content || content
	}
	clean := nativeClean(m, "id", "type", "role", "stop_reason", "stop_sequence")
	clean["model"] = encoded(r.model)
	clean["content"] = encoded(out)
	event.Progress = event.Content || finish != ""
	return clean, event, nil
}

func (r *Response) messageBlock(block map[string]json.RawMessage) (map[string]json.RawMessage, bool, error) {
	if messageTextBlock(block) {
		return nativeClean(block, "type", "text", "citations"), !stringValue(block["text"], ""), nil
	}
	if stringValue(block["type"], "tool_use") && r.profile.SupportsTools && only(block, "type", "id", "name", "input") && nonemptyString(block["id"]) && namedFunction(block["name"]) {
		if _, err := object(block["input"]); err == nil {
			return nativeClean(block, "type", "id", "name", "input"), true, nil
		}
	}
	return nil, false, invalidOutput()
}

func (r *Response) messageEvent(typ string, m map[string]json.RawMessage) (*Event, error) {
	event := &Event{Kind: "chunk"}
	clean := nativeClean(m, "type")
	if typ == "ping" {
		if !only(m, "type") {
			return nil, invalidOutput()
		}
		event.Kind = "heartbeat"
		return event, nil
	}
	if typ == "message_start" {
		if r.native.started || !only(m, "type", "message") {
			return nil, invalidOutput()
		}
		message, err := object(m["message"])
		if err != nil {
			return nil, invalidOutput()
		}
		body, _, err := r.normalizeMessage(message, false)
		if err != nil {
			return nil, err
		}
		var content []json.RawMessage
		_ = json.Unmarshal(message["content"], &content)
		if len(content) != 0 {
			return nil, invalidOutput()
		}
		r.native.started = true
		r.native.blocks = map[int64]*nativeBlock{}
		r.observeMessageUsage(message["usage"], false)
		body["usage"] = cleanMessageUsage(message["usage"])
		clean["message"] = encoded(body)
	} else {
		if !r.native.started {
			return nil, invalidOutput()
		}
		if r.FinishReason != "" && typ != "message_stop" {
			return nil, invalidOutput()
		}
		switch typ {
		case "content_block_start", "content_block_delta", "content_block_stop":
			index, valid := integer(m["index"])
			if !valid || index < 0 || index >= 64 {
				return nil, invalidOutput()
			}
			clean["index"] = m["index"]
			block := r.native.blocks[index]
			if typ == "content_block_start" {
				if !only(m, "type", "index", "content_block") || block != nil || index != int64(len(r.native.blocks)) {
					return nil, invalidOutput()
				}
				for _, b := range r.native.blocks {
					if b.active {
						return nil, invalidOutput()
					}
				}
				v, err := object(m["content_block"])
				if err != nil {
					return nil, invalidOutput()
				}
				out, content, err := r.messageBlock(v)
				if err != nil {
					return nil, err
				}
				var blockType string
				_ = json.Unmarshal(v["type"], &blockType)
				if blockType == "tool_use" && !r.profile.SupportsParallelToolCalls {
					for _, b := range r.native.blocks {
						if b.typ == "tool_use" {
							return nil, invalidOutput()
						}
					}
				}
				r.native.blocks[index] = &nativeBlock{typ: blockType, active: true}
				clean["content_block"] = encoded(out)
				event.Content = content
				event.Progress = content
			} else {
				if block == nil || !block.active {
					return nil, invalidOutput()
				}
				if typ == "content_block_stop" {
					if !only(m, "type", "index") {
						return nil, invalidOutput()
					}
					block.active = false
					event.Progress = true
				} else {
					if !only(m, "type", "index", "delta") {
						return nil, invalidOutput()
					}
					delta, err := object(m["delta"])
					if err != nil {
						return nil, invalidOutput()
					}
					field := "text"
					deltaType := "text_delta"
					if block.typ == "tool_use" {
						field = "partial_json"
						deltaType = "input_json_delta"
					}
					if !only(delta, "type", field) || !stringValue(delta["type"], deltaType) || !isString(delta[field]) {
						return nil, invalidOutput()
					}
					if field == "partial_json" {
						var s string
						_ = json.Unmarshal(delta[field], &s)
						if len(block.arguments)+len(s) > MaxEventBytes {
							return nil, protocolError("upstream_event_too_large")
						}
						block.arguments = append(block.arguments, s...)
					}
					clean["delta"] = encoded(delta)
					event.Content = !stringValue(delta[field], "")
					event.Progress = event.Content
				}
			}
		case "message_delta":
			if !only(m, "type", "delta", "usage") {
				return nil, invalidOutput()
			}
			for _, b := range r.native.blocks {
				if b.active {
					return nil, invalidOutput()
				}
			}
			delta, err := object(m["delta"])
			if err != nil || !only(delta, "stop_reason", "stop_sequence", "container", "stop_details") || !optionalString(delta, "stop_sequence") {
				return nil, invalidOutput()
			}
			for _, key := range []string{"container", "stop_details"} {
				if v, ok := delta[key]; ok && !isNull(v) {
					return nil, invalidOutput()
				}
			}
			finish, valid := messageFinish(delta["stop_reason"])
			if !valid {
				return nil, invalidOutput()
			}
			r.observeMessageUsage(m["usage"], finish != "")
			r.FinishReason = finish
			event.FinishReason = finish
			event.Progress = finish != ""
			clean["delta"] = encoded(delta)
			if v, ok := m["usage"]; ok {
				clean["usage"] = cleanMessageUsage(v)
			}
		case "message_stop":
			if !only(m, "type") || r.FinishReason == "" {
				return nil, invalidOutput()
			}
			for _, b := range r.native.blocks {
				if b.active {
					return nil, invalidOutput()
				}
				if len(b.arguments) > 0 && r.FinishReason != "length" {
					if _, err := object(b.arguments); err != nil {
						return nil, invalidOutput()
					}
				}
			}
			if r.native.finalUsage && r.Usage.Status != UsageConflicting {
				r.Usage.Status = UsageConfirmed
			}
			event.Kind = "done"
			event.FinishReason = r.FinishReason
			event.Progress = true
		default:
			return nil, protocolError("upstream_unsupported_event")
		}
	}
	event.Data = encoded(clean)
	return event, nil
}
