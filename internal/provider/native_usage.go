package provider

import (
	"encoding/json"
	"math"
)

// Hosted tools are outside the published price contract. Their independent
// charge evidence must be checked before discarding optional response metadata.
func safeResponseToolUsage(raw []byte) bool {
	if isNull(raw) {
		return true
	}
	m, err := object(raw)
	if err != nil || !only(m, "web_search", "image_gen") {
		return false
	}
	for tool, value := range m {
		d, e := object(value)
		if e != nil {
			return false
		}
		if tool == "web_search" {
			if !only(d, "num_requests") {
				return false
			}
		} else if !only(d, "input_tokens", "output_tokens", "total_tokens", "input_tokens_details", "output_tokens_details") {
			return false
		}
		for key, value := range d {
			if key == "input_tokens_details" || key == "output_tokens_details" {
				details, e := object(value)
				if e != nil || !only(details, "text_tokens", "image_tokens") {
					return false
				}
				for _, v := range details {
					n, valid := integer(v)
					if !valid || n != 0 {
						return false
					}
				}
			} else {
				n, valid := integer(value)
				if !valid || n != 0 {
					return false
				}
			}
		}
	}
	return true
}

// Native usage is translated into the same evidence contract as Chat. Missing
// counters remain missing; protocol defaults are used only for optional subsets.
func responseUsage(raw []byte) []byte {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	m, err := object(raw)
	if err != nil || !only(m, "input_tokens", "output_tokens", "total_tokens", "input_tokens_details", "output_tokens_details") {
		return []byte(`{"invalid":1}`)
	}
	out := map[string]json.RawMessage{}
	for from, to := range map[string]string{"input_tokens": "prompt_tokens", "output_tokens": "completion_tokens", "total_tokens": "total_tokens", "input_tokens_details": "prompt_tokens_details", "output_tokens_details": "completion_tokens_details"} {
		if v, ok := m[from]; ok {
			out[to] = v
		}
	}
	b, _ := json.Marshal(out)
	return b
}

func messagesUsage(raw []byte) (Usage, bool) {
	if len(raw) == 0 || isNull(raw) {
		return Usage{Status: UsageMissing}, false
	}
	m, err := object(raw)
	bad := func() (Usage, bool) { return Usage{Status: UsageConflicting}, false }
	if err != nil || !only(m, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "cache_creation", "server_tool_use", "service_tier", "inference_geo", "speed", "output_tokens_details") {
		return bad()
	}
	for _, key := range []string{"inference_geo"} {
		if v, ok := m[key]; ok && !isNull(v) && !isString(v) {
			return bad()
		}
	}
	for _, key := range []string{"service_tier", "speed"} {
		if v, ok := m[key]; ok && !isNull(v) && !stringValue(v, "standard") {
			return bad()
		}
	}
	if v, ok := m["output_tokens_details"]; ok && !isNull(v) {
		d, e := object(v)
		if e != nil || !only(d, "thinking_tokens") {
			return bad()
		}
		n, valid := integer(d["thinking_tokens"])
		if !valid || n != 0 {
			return bad()
		}
	}
	if v, ok := m["server_tool_use"]; ok && !isNull(v) {
		d, e := object(v)
		if e != nil {
			return bad()
		}
		for _, v := range d {
			n, valid := integer(v)
			if !valid || n != 0 {
				return bad()
			}
		}
	}
	read := func(key string, optional bool) (int64, bool, bool) {
		v, ok := m[key]
		if !ok {
			return 0, optional, true
		}
		if isNull(v) {
			return 0, false, true
		}
		n, valid := integer(v)
		return n, true, valid && n >= 0
	}
	i, ip, iv := read("input_tokens", false)
	o, op, ov := read("output_tokens", false)
	c, cp, cv := read("cache_creation_input_tokens", true)
	cached, rp, rv := read("cache_read_input_tokens", true)
	if !iv || !ov || !cv || !rv || i > math.MaxInt64-c || i+c > math.MaxInt64-cached || i+c+cached > math.MaxInt64-o {
		return bad()
	}
	if v, ok := m["cache_creation"]; ok && !isNull(v) {
		d, e := object(v)
		if e != nil || !only(d, "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens") {
			return bad()
		}
		var sum int64
		for _, v := range d {
			n, valid := integer(v)
			if !valid || n < 0 || sum > math.MaxInt64-n {
				return bad()
			}
			sum += n
		}
		if !cp || sum != c {
			return bad()
		}
	}
	u := Usage{Status: UsageProvisional, InputTokens: i + c + cached, OutputTokens: o, TotalTokens: i + c + cached + o, CachedTokens: cached}
	return u, ip && op && cp && rp
}

func nativeUsageJSON(protocol string, u Usage) json.RawMessage {
	if u.Status != UsageConfirmed {
		return json.RawMessage("null")
	}
	var b []byte
	if protocol == ProtocolResponses {
		b, _ = json.Marshal(map[string]any{"input_tokens": u.InputTokens, "output_tokens": u.OutputTokens, "total_tokens": u.TotalTokens, "input_tokens_details": map[string]int64{"cached_tokens": u.CachedTokens}, "output_tokens_details": map[string]int64{"reasoning_tokens": u.ReasoningTokens}})
	} else {
		// For Messages retain the original cache split rather than reconstructing
		// cache creation from the normalized inclusive input total.
		return nil
	}
	return b
}

func cleanMessageUsage(raw []byte) json.RawMessage {
	u, _ := messagesUsage(raw)
	if u.Status == UsageMissing || u.Status == UsageConflicting {
		return json.RawMessage("null")
	}
	m, err := object(raw)
	if err != nil {
		return json.RawMessage("null")
	}
	return encoded(nativeClean(m, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "cache_creation"))
}

func (r *Response) finishResponseUsage(raw []byte) {
	translated := responseUsage(raw)
	_, complete := parseUsage(translated)
	r.Usage = r.tracker.observe(translated)
	r.Usage = r.tracker.finalize()
	if !complete && r.Usage.Status == UsageConfirmed {
		r.Usage.Status = UsageMissing
	}
}

func (r *Response) observeMessageUsage(raw []byte, final bool) {
	if len(raw) == 0 || isNull(raw) {
		return
	}
	m, err := object(raw)
	if err != nil {
		r.Usage = Usage{Status: UsageConflicting}
		return
	}
	if r.Usage.Status == UsageConflicting {
		return
	}
	if r.native.usage == nil {
		r.native.usage = map[string]json.RawMessage{}
	}
	for k, v := range m {
		old, seen := r.native.usage[k]
		// The delta schema uses nullable optional input/cache counters for
		// "no update"; unlike output_tokens these are not a new final value.
		if seen && k != "output_tokens" && (k == "input_tokens" || k == "cache_creation_input_tokens" || k == "cache_read_input_tokens") && isNull(v) {
			continue
		}
		if seen {
			if k == "input_tokens" || k == "output_tokens" || k == "cache_creation_input_tokens" || k == "cache_read_input_tokens" {
				if isNull(old) {
					r.native.usage[k] = v
					continue
				}
				a, av := integer(old)
				b, bv := integer(v)
				if !av || !bv || b < a {
					r.Usage = Usage{Status: UsageConflicting}
					return
				}
			} else if k == "cache_creation" {
				if isNull(old) || isNull(v) {
					r.native.usage[k] = v
					continue
				}
				before, e1 := object(old)
				after, e2 := object(v)
				if e1 != nil || e2 != nil {
					r.Usage = Usage{Status: UsageConflicting}
					return
				}
				for key, value := range before {
					a, av := integer(value)
					b, bv := integer(after[key])
					if !av || !bv || b < a {
						r.Usage = Usage{Status: UsageConflicting}
						return
					}
				}
			} else if k != "output_tokens_details" && k != "service_tier" && k != "speed" && k != "server_tool_use" && k != "inference_geo" && string(old) != string(v) {
				r.Usage = Usage{Status: UsageConflicting}
				return
			}
		}
		r.native.usage[k] = v
	}
	b, _ := json.Marshal(r.native.usage)
	u, complete := messagesUsage(b)
	r.Usage = u
	if final {
		_, outputPresent := integer(m["output_tokens"])
		r.native.finalUsage = complete && outputPresent
	}
}
