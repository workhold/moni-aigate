package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"regexp"
	"unicode/utf8"
)

type Request struct {
	Model           string
	Stream          bool
	IncludeUsage    bool
	MaxOutputTokens int64
	InputUpperBound int64
	ProfileVersion  string
	profile         Profile
	fields          map[string]json.RawMessage
	stream          bool
	output          int64
	retrieval       retrievalExpectation
}

// ParseChat rejects unknown or ambiguous fields before any network or finance work.
func ParseChat(raw []byte, p Profile) (*Request, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.ProtocolName() != ProtocolChat {
		return nil, badRequest()
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
	base := map[string]bool{"model": true, "messages": true, "stream": true, "stream_options": true, "n": true, "max_tokens": true, "max_completion_tokens": true}
	optional := map[string]bool{"temperature": true, "top_p": true, "seed": true, "stop": true, "presence_penalty": true, "frequency_penalty": true}
	if p.AllowedParameters != nil {
		optional = map[string]bool{}
		for _, k := range p.AllowedParameters {
			optional[k] = true
		}
	}
	if p.SupportsTools {
		optional["tools"] = true
		optional["tool_choice"] = true
	}
	if p.SupportsParallelToolCalls {
		optional["parallel_tool_calls"] = true
	}
	for k := range f {
		if !base[k] && !optional[k] {
			return nil, failure("unsupported_parameter", 400, ClassCaller, ConsumptionNotSent)
		}
	}
	if _, ok := f["tools"]; ok && !p.SupportsTools {
		return nil, badRequest()
	}
	if _, ok := f["tool_choice"]; ok && !p.SupportsTools {
		return nil, badRequest()
	}
	var model string
	if json.Unmarshal(f["model"], &model) != nil || model != p.Model {
		return nil, badRequest()
	}
	if err = validateMessages(f["messages"], p.SupportsTools); err != nil {
		return nil, err
	}
	r := &Request{Model: model, ProfileVersion: p.Version, InputUpperBound: p.InputTokenUpperBound, profile: cloneProfile(p), fields: f}
	if v, ok := f["stream"]; ok {
		if !boolean(v, &r.Stream) {
			return nil, badRequest()
		}
	}
	r.stream = r.Stream
	if v, ok := f["stream_options"]; ok {
		s, e := object(v)
		if e != nil || !r.Stream || len(s) != 1 {
			return nil, badRequest()
		}
		if v, ok = s["include_usage"]; !ok || !boolean(v, &r.IncludeUsage) {
			return nil, badRequest()
		}
	}
	if v, ok := f["n"]; ok {
		n, valid := integer(v)
		if !valid || n != 1 {
			return nil, failure("unsupported_n", 400, ClassCaller, ConsumptionNotSent)
		}
	}
	a, aok := f["max_tokens"]
	b, bok := f["max_completion_tokens"]
	if aok && bok {
		return nil, badRequest()
	}
	r.output = p.DefaultOutputLimit
	if aok || bok {
		if bok {
			a = b
		}
		var ok bool
		r.output, ok = integer(a)
		if !ok || r.output <= 0 || r.output > p.OutputLimit {
			return nil, badRequest()
		}
	}
	r.MaxOutputTokens = r.output
	for _, x := range []struct {
		key    string
		lo, hi float64
	}{{"temperature", 0, 2}, {"top_p", 0, 1}, {"presence_penalty", -2, 2}, {"frequency_penalty", -2, 2}} {
		if v, ok := f[x.key]; ok {
			var n float64
			if json.Unmarshal(v, &n) != nil || bytes.Equal(v, []byte("null")) || math.IsNaN(n) || math.IsInf(n, 0) || n < x.lo || n > x.hi {
				return nil, badRequest()
			}
		}
	}
	if v, ok := f["seed"]; ok {
		if _, yes := integer(v); !yes {
			return nil, badRequest()
		}
	}
	if v, ok := f["parallel_tool_calls"]; ok {
		var b bool
		if !p.SupportsTools || !p.SupportsParallelToolCalls || !boolean(v, &b) {
			return nil, badRequest()
		}
	}
	if v, ok := f["stop"]; ok {
		var one string
		var many []string
		if json.Unmarshal(v, &one) == nil {
			if len(one) == 0 || len(one) > 256 {
				return nil, badRequest()
			}
		} else if json.Unmarshal(v, &many) != nil || len(many) == 0 || len(many) > 4 {
			return nil, badRequest()
		} else {
			for _, s := range many {
				if s == "" || len(s) > 256 {
					return nil, badRequest()
				}
			}
		}
	}
	if err := validateTools(f); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Request) UpstreamJSON(model string) ([]byte, error) {
	if r == nil || model == "" {
		return nil, badRequest()
	}
	if r.Protocol() == ProtocolGemini {
		return r.geminiUpstreamJSON(model)
	}
	f := make(map[string]json.RawMessage, len(r.fields)+1)
	for k, v := range r.fields {
		f[k] = v
	}
	f["model"], _ = json.Marshal(model)
	if r.profile.InputOnly() {
		return json.Marshal(f)
	}
	if r.Protocol() != ProtocolChat {
		field := "max_tokens"
		if r.Protocol() == ProtocolResponses {
			field = "max_output_tokens"
			f["store"] = json.RawMessage("false")
			if r.profile.SupportsTools && !r.profile.SupportsParallelToolCalls && f["tools"] != nil {
				f["parallel_tool_calls"] = json.RawMessage("false")
			}
		} else if r.profile.SupportsTools && !r.profile.SupportsParallelToolCalls && f["tools"] != nil {
			choice := map[string]json.RawMessage{"type": json.RawMessage(`"auto"`)}
			if raw, ok := f["tool_choice"]; ok {
				choice, _ = object(raw)
			}
			if !stringValue(choice["type"], "none") {
				choice["disable_parallel_tool_use"] = json.RawMessage("true")
			}
			f["tool_choice"], _ = json.Marshal(choice)
		}
		f[field], _ = json.Marshal(r.output)
		return json.Marshal(f)
	}
	delete(f, "max_tokens")
	delete(f, "max_completion_tokens")
	field := r.profile.OutputLimitField
	if field == "" {
		field = "max_tokens"
	}
	f[field], _ = json.Marshal(r.output)
	if r.stream {
		f["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}
	return json.Marshal(f)
}
func (r *Request) Protocol() string { return r.profile.ProtocolName() }
func (r *Request) EndpointSuffix() string {
	switch r.Protocol() {
	case ProtocolResponses:
		return "/responses"
	case ProtocolMessages:
		return "/messages"
	case ProtocolEmbeddings:
		return "/embeddings"
	case ProtocolRerank:
		return "/rerank"
	case ProtocolGemini:
		suffix, _ := r.EndpointSuffixForModel(r.profile.Model)
		return suffix
	default:
		return "/chat/completions"
	}
}
func (r *Request) Profile() Profile { return cloneProfile(r.profile) }
func cloneProfile(p Profile) Profile {
	p.AllowedParameters = append([]string(nil), p.AllowedParameters...)
	p.NoConsumptionStatuses = append([]int(nil), p.NoConsumptionStatuses...)
	return p
}
func badRequest() error { return failure("invalid_request", 400, ClassCaller, ConsumptionNotSent) }
func integer(v []byte) (int64, bool) {
	var n int64
	err := json.Unmarshal(v, &n)
	return n, err == nil && len(v) > 0 && !bytes.Equal(bytes.TrimSpace(v), []byte("null"))
}
func boolean(v []byte, n *bool) bool {
	return len(v) > 0 && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) && json.Unmarshal(v, n) == nil
}

// object also rejects duplicate keys recursively. encoding/json alone silently
// accepts the last duplicate, which would make validation and dispatch disagree.
func object(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := checkJSON(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, badRequest()
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, badRequest()
	}
	return m, nil
}
func checkJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return badRequest()
	}
	t, e := d.Token()
	if e != nil {
		return badRequest()
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return badRequest()
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return badRequest()
			}
			seen[s] = true
			if e := checkJSON(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e := checkJSON(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return badRequest()
	}
	_, e = d.Token()
	if e != nil {
		return badRequest()
	}
	return nil
}
func only(m map[string]json.RawMessage, keys ...string) bool {
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return false
		}
	}
	return true
}

var functionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validateMessages(raw []byte, tools bool) error {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 || len(messages) > 1024 {
		return badRequest()
	}
	for _, msg := range messages {
		m, e := object(msg)
		if e != nil || !only(m, "role", "content", "name", "tool_calls", "tool_call_id") {
			return badRequest()
		}
		var role string
		if json.Unmarshal(m["role"], &role) != nil {
			return badRequest()
		}
		switch role {
		case "system", "developer", "user", "assistant":
		case "tool":
			if !tools {
				return badRequest()
			}
		default:
			return badRequest()
		}
		if name, ok := m["name"]; ok {
			var s string
			if json.Unmarshal(name, &s) != nil || !functionName.MatchString(s) {
				return badRequest()
			}
		}
		v, has := m["content"]
		null := bytes.Equal(v, []byte("null"))
		if !has || null {
			if role != "assistant" || len(m["tool_calls"]) == 0 {
				return badRequest()
			}
		} else {
			var text string
			if json.Unmarshal(v, &text) != nil {
				var parts []map[string]json.RawMessage
				if json.Unmarshal(v, &parts) != nil || len(parts) == 0 {
					return badRequest()
				}
				for _, part := range parts {
					var typ, s string
					if !only(part, "type", "text") || json.Unmarshal(part["type"], &typ) != nil || typ != "text" || json.Unmarshal(part["text"], &s) != nil {
						return badRequest()
					}
				}
			}
		}
		if calls, ok := m["tool_calls"]; ok {
			if !tools || role != "assistant" || validateToolCalls(calls) != nil {
				return badRequest()
			}
		}
		if role == "tool" {
			var id string
			if json.Unmarshal(m["tool_call_id"], &id) != nil || id == "" {
				return badRequest()
			}
		} else if _, ok := m["tool_call_id"]; ok {
			return badRequest()
		}
	}
	return nil
}
func validateToolCalls(raw []byte) error {
	var calls []map[string]json.RawMessage
	if json.Unmarshal(raw, &calls) != nil || len(calls) == 0 || len(calls) > 64 {
		return badRequest()
	}
	seen := map[string]bool{}
	for _, call := range calls {
		var id, typ string
		if !only(call, "id", "type", "function") || json.Unmarshal(call["id"], &id) != nil || id == "" || seen[id] || json.Unmarshal(call["type"], &typ) != nil || typ != "function" {
			return badRequest()
		}
		seen[id] = true
		fn, e := object(call["function"])
		var name, args string
		if e != nil || !only(fn, "name", "arguments") || json.Unmarshal(fn["name"], &name) != nil || !functionName.MatchString(name) || json.Unmarshal(fn["arguments"], &args) != nil {
			return badRequest()
		}
	}
	return nil
}
func validateTools(f map[string]json.RawMessage) error {
	names := map[string]bool{}
	if raw, ok := f["tools"]; ok {
		var tools []map[string]json.RawMessage
		if json.Unmarshal(raw, &tools) != nil || len(tools) == 0 || len(tools) > 64 {
			return badRequest()
		}
		for _, tool := range tools {
			var typ string
			if !only(tool, "type", "function") || json.Unmarshal(tool["type"], &typ) != nil || typ != "function" {
				return badRequest()
			}
			fn, e := object(tool["function"])
			var name string
			if e != nil || !only(fn, "name", "description", "parameters", "strict") || json.Unmarshal(fn["name"], &name) != nil || !functionName.MatchString(name) || names[name] {
				return badRequest()
			}
			names[name] = true
			if v, ok := fn["parameters"]; ok {
				if _, e := object(v); e != nil {
					return badRequest()
				}
			}
			if v, ok := fn["description"]; ok {
				var s string
				if json.Unmarshal(v, &s) != nil {
					return badRequest()
				}
			}
			if v, ok := fn["strict"]; ok {
				var b bool
				if !boolean(v, &b) {
					return badRequest()
				}
			}
		}
	}
	if raw, ok := f["tool_choice"]; ok {
		if len(names) == 0 {
			return badRequest()
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if s != "auto" && s != "none" && s != "required" {
				return badRequest()
			}
		} else {
			m, e := object(raw)
			var typ string
			if e != nil || !only(m, "type", "function") || json.Unmarshal(m["type"], &typ) != nil || typ != "function" {
				return badRequest()
			}
			fn, e := object(m["function"])
			var name string
			if e != nil || !only(fn, "name") || json.Unmarshal(fn["name"], &name) != nil || !names[name] {
				return badRequest()
			}
		}
	}
	return nil
}
