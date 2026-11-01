package provider

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// Parse validates a stateless, bounded request for the selected wire protocol.
// Protocol selection is a catalog decision, never inferred from caller content.
func Parse(protocol string, raw []byte, p Profile) (*Request, error) {
	if protocol == "" {
		protocol = ProtocolChat
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if protocol != p.ProtocolName() {
		return nil, badRequest()
	}
	if protocol == ProtocolChat {
		return ParseChat(raw, p)
	}
	if protocol == ProtocolGemini {
		return ParseGemini(raw, p, false)
	}
	if p.InputOnly() {
		return parseRetrieval(raw, p)
	}
	if len(raw) > MaxRequestBytes {
		return nil, failure("request_too_large", 413, ClassCaller, ConsumptionNotSent)
	}
	if !utf8.Valid(raw) {
		return nil, badRequest()
	}
	f, err := object(raw)
	if err != nil {
		return nil, badRequest()
	}
	base := map[string]bool{"model": true, "stream": true}
	optional := map[string]bool{"temperature": true, "top_p": true}
	if protocol == ProtocolResponses {
		for _, key := range []string{"input", "instructions", "max_output_tokens", "store"} {
			base[key] = true
		}
	} else {
		for _, key := range []string{"messages", "system", "max_tokens"} {
			base[key] = true
		}
		optional["top_k"], optional["stop_sequences"] = true, true
	}
	if p.AllowedParameters != nil {
		optional = map[string]bool{}
		for _, k := range p.AllowedParameters {
			optional[k] = true
		}
	}
	if p.SupportsTools {
		optional["tools"], optional["tool_choice"] = true, true
	}
	if p.SupportsParallelToolCalls && protocol == ProtocolResponses {
		optional["parallel_tool_calls"] = true
	}
	for k := range f {
		if !base[k] && !optional[k] {
			return nil, failure("unsupported_parameter", 400, ClassCaller, ConsumptionNotSent)
		}
	}
	if (!p.SupportsTools && (f["tools"] != nil || f["tool_choice"] != nil)) || (!p.SupportsParallelToolCalls && f["parallel_tool_calls"] != nil) {
		return nil, badRequest()
	}
	if !stringValue(f["model"], p.Model) {
		return nil, badRequest()
	}
	r := &Request{Model: p.Model, ProfileVersion: p.Version, InputUpperBound: p.InputTokenUpperBound, profile: cloneProfile(p), fields: f, output: p.DefaultOutputLimit}
	if v, ok := f["stream"]; ok && !boolean(v, &r.Stream) {
		return nil, badRequest()
	}
	r.stream = r.Stream
	field := "max_tokens"
	if protocol == ProtocolResponses {
		field = "max_output_tokens"
		if v, ok := f["store"]; ok {
			var store bool
			if !boolean(v, &store) || store {
				return nil, badRequest()
			}
		}
		if v, ok := f["instructions"]; ok && !isString(v) {
			return nil, badRequest()
		}
		if err := validateResponseInput(f["input"], p.SupportsTools); err != nil {
			return nil, err
		}
	} else {
		if v, ok := f["system"]; ok && !textParts(v, "text", true) {
			return nil, badRequest()
		}
		if err := validateAnthropicMessages(f["messages"], p.SupportsTools); err != nil {
			return nil, err
		}
	}
	if v, ok := f[field]; ok {
		var valid bool
		r.output, valid = integer(v)
		if !valid || r.output <= 0 || r.output > p.OutputLimit {
			return nil, badRequest()
		}
	}
	r.MaxOutputTokens = r.output
	for _, key := range []string{"temperature", "top_p"} {
		if v, ok := f[key]; ok {
			var value float64
			max := float64(1)
			if key == "temperature" && protocol == ProtocolResponses {
				max = 2
			}
			if isNull(v) || json.Unmarshal(v, &value) != nil || value < 0 || value > max {
				return nil, badRequest()
			}
		}
	}
	if v, ok := f["top_k"]; ok {
		n, valid := integer(v)
		if !valid || n <= 0 {
			return nil, badRequest()
		}
	}
	if v, ok := f["stop_sequences"]; ok {
		var stops []string
		if json.Unmarshal(v, &stops) != nil || len(stops) == 0 || len(stops) > 4 {
			return nil, badRequest()
		}
		for _, s := range stops {
			if s == "" || len(s) > 256 {
				return nil, badRequest()
			}
		}
	}
	if v, ok := f["parallel_tool_calls"]; ok {
		var b bool
		if !p.SupportsTools || !p.SupportsParallelToolCalls || !boolean(v, &b) {
			return nil, badRequest()
		}
	}
	if err := validateNativeTools(f, p); err != nil {
		return nil, err
	}
	return r, nil
}

func isNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func isString(raw []byte) bool {
	var s string
	return len(raw) > 0 && !isNull(raw) && json.Unmarshal(raw, &s) == nil
}
func stringValue(raw []byte, value string) bool {
	var s string
	return isString(raw) && json.Unmarshal(raw, &s) == nil && s == value
}
func nonemptyString(raw []byte) bool {
	var s string
	return isString(raw) && json.Unmarshal(raw, &s) == nil && s != ""
}
func namedFunction(raw []byte) bool {
	var s string
	return isString(raw) && json.Unmarshal(raw, &s) == nil && functionName.MatchString(s)
}

func textParts(raw []byte, typ string, allowString bool) bool {
	if allowString && isString(raw) {
		return true
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 || len(parts) > 1024 {
		return false
	}
	for _, part := range parts {
		if typ == "text" {
			if !messageTextBlock(part) {
				return false
			}
			continue
		}
		if !only(part, "type", "text") || !stringValue(part["type"], typ) || !isString(part["text"]) {
			return false
		}
	}
	return true
}

func emptyOrNullArray(raw []byte) bool {
	if isNull(raw) {
		return true
	}
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) == nil && values != nil && len(values) == 0
}

func messageTextBlock(block map[string]json.RawMessage) bool {
	if !only(block, "type", "text", "citations") || !stringValue(block["type"], "text") || !isString(block["text"]) {
		return false
	}
	if v, ok := block["citations"]; ok && !emptyOrNullArray(v) {
		return false
	}
	return true
}

func responsePhase(item map[string]json.RawMessage) bool {
	phase, ok := item["phase"]
	return !ok || isNull(phase) || stringValue(phase, "commentary") || stringValue(phase, "final_answer")
}

func validateResponseInput(raw []byte, tools bool) error {
	if isString(raw) {
		return nil
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 || len(items) > 1024 {
		return badRequest()
	}
	for _, item := range items {
		var typ string
		if v, ok := item["type"]; ok {
			if !isString(v) || json.Unmarshal(v, &typ) != nil {
				return badRequest()
			}
		}
		switch typ {
		case "", "message":
			if !only(item, "type", "role", "content", "id", "status", "phase") || !responsePhase(item) {
				return badRequest()
			}
			var role string
			if json.Unmarshal(item["role"], &role) != nil || (role != "user" && role != "assistant" && role != "system" && role != "developer") {
				return badRequest()
			}
			partType := "input_text"
			if role == "assistant" {
				partType = "output_text"
			}
			if v, ok := item["id"]; ok && (!nonemptyString(v) || role != "assistant") {
				return badRequest()
			}
			if v, ok := item["status"]; ok && (role != "assistant" || (!stringValue(v, "completed") && !stringValue(v, "incomplete"))) {
				return badRequest()
			}
			if _, ok := item["phase"]; ok && role != "assistant" {
				return badRequest()
			}
			validContent := textParts(item["content"], partType, true)
			if !validContent && role == "assistant" {
				var parts []map[string]json.RawMessage
				if json.Unmarshal(item["content"], &parts) == nil && len(parts) > 0 && len(parts) <= 1024 {
					validContent = true
					for _, part := range parts {
						if _, _, err := responsePart(part); err != nil {
							validContent = false
							break
						}
					}
				}
			}
			if !validContent {
				return badRequest()
			}
		case "function_call":
			if !tools || !only(item, "type", "id", "status", "call_id", "name", "arguments") || !nonemptyString(item["call_id"]) || !namedFunction(item["name"]) || !isString(item["arguments"]) {
				return badRequest()
			}
			if v, ok := item["id"]; ok && !nonemptyString(v) {
				return badRequest()
			}
			if v, ok := item["status"]; ok && !stringValue(v, "completed") && !stringValue(v, "incomplete") {
				return badRequest()
			}
		case "function_call_output":
			if !tools || !only(item, "type", "call_id", "output") || !nonemptyString(item["call_id"]) || !isString(item["output"]) {
				return badRequest()
			}
		default:
			return badRequest()
		}
	}
	return nil
}

func validateAnthropicMessages(raw []byte, tools bool) error {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 || len(messages) > 1024 {
		return badRequest()
	}
	for _, message := range messages {
		var role string
		if !only(message, "role", "content") || json.Unmarshal(message["role"], &role) != nil || (role != "user" && role != "assistant") {
			return badRequest()
		}
		if isString(message["content"]) {
			continue
		}
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(message["content"], &blocks) != nil || len(blocks) == 0 || len(blocks) > 1024 {
			return badRequest()
		}
		for _, block := range blocks {
			var typ string
			if json.Unmarshal(block["type"], &typ) != nil {
				return badRequest()
			}
			switch typ {
			case "text":
				if !messageTextBlock(block) {
					return badRequest()
				}
			case "tool_use":
				if !tools || role != "assistant" || !only(block, "type", "id", "name", "input") || !nonemptyString(block["id"]) || !namedFunction(block["name"]) {
					return badRequest()
				}
				if _, err := object(block["input"]); err != nil {
					return badRequest()
				}
			case "tool_result":
				if !tools || role != "user" || !only(block, "type", "tool_use_id", "content", "is_error") || !nonemptyString(block["tool_use_id"]) || !textParts(block["content"], "text", true) {
					return badRequest()
				}
				if v, ok := block["is_error"]; ok {
					var b bool
					if !boolean(v, &b) {
						return badRequest()
					}
				}
			default:
				return badRequest()
			}
		}
	}
	return nil
}

func validateNativeTools(f map[string]json.RawMessage, p Profile) error {
	names := map[string]bool{}
	if raw, ok := f["tools"]; ok {
		var tools []map[string]json.RawMessage
		if json.Unmarshal(raw, &tools) != nil || len(tools) == 0 || len(tools) > 64 {
			return badRequest()
		}
		for _, tool := range tools {
			schema := "input_schema"
			if p.ProtocolName() == ProtocolResponses {
				if !only(tool, "type", "name", "description", "parameters", "strict") || !stringValue(tool["type"], "function") {
					return badRequest()
				}
				schema = "parameters"
				if v, ok := tool["strict"]; ok {
					var b bool
					if !boolean(v, &b) {
						return badRequest()
					}
				}
			} else if !only(tool, "name", "description", "input_schema") {
				return badRequest()
			}
			var name string
			if !namedFunction(tool["name"]) || json.Unmarshal(tool["name"], &name) != nil || names[name] {
				return badRequest()
			}
			names[name] = true
			if v, ok := tool["description"]; ok && !isString(v) {
				return badRequest()
			}
			if _, err := object(tool[schema]); err != nil {
				return badRequest()
			}
		}
	}
	if raw, ok := f["tool_choice"]; ok {
		if len(names) == 0 {
			return badRequest()
		}
		if p.ProtocolName() == ProtocolResponses {
			if stringValue(raw, "auto") || stringValue(raw, "none") || stringValue(raw, "required") {
				return nil
			}
			choice, err := object(raw)
			var name string
			if err != nil || !only(choice, "type", "name") || !stringValue(choice["type"], "function") || json.Unmarshal(choice["name"], &name) != nil || !names[name] {
				return badRequest()
			}
		} else {
			choice, err := object(raw)
			var typ string
			if err != nil || !only(choice, "type", "name", "disable_parallel_tool_use") || json.Unmarshal(choice["type"], &typ) != nil {
				return badRequest()
			}
			if typ == "tool" {
				var name string
				if json.Unmarshal(choice["name"], &name) != nil || !names[name] {
					return badRequest()
				}
			} else if (typ != "auto" && typ != "any" && typ != "none") || choice["name"] != nil {
				return badRequest()
			}
			if v, ok := choice["disable_parallel_tool_use"]; ok {
				var b bool
				if !boolean(v, &b) || (!b && !p.SupportsParallelToolCalls) {
					return badRequest()
				}
			}
		}
	}
	return nil
}
