package provider

import (
	"bytes"
	"encoding/json"
	"math"
)

type usageTracker struct {
	value     Usage
	seen      bool
	complete  bool
	semantics string
}

func parseUsage(raw []byte) (Usage, bool) {
	u := Usage{Status: UsageMissing}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return u, false
	}
	m, err := object(raw)
	if err != nil {
		u.Status = UsageConflicting
		return u, false
	}
	for k := range m {
		if k != "prompt_tokens" && k != "completion_tokens" && k != "total_tokens" && k != "prompt_tokens_details" && k != "completion_tokens_details" {
			u.Status = UsageConflicting
			return u, false
		}
	}
	read := func(key string) (int64, bool, bool) {
		v, exists := m[key]
		if !exists || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return 0, false, true
		}
		n, valid := integer(v)
		return n, true, valid && n >= 0
	}
	i, ip, iv := read("prompt_tokens")
	o, op, ov := read("completion_tokens")
	t, tp, tv := read("total_tokens")
	if !iv || !ov || !tv || i > math.MaxInt64-o {
		u.Status = UsageConflicting
		return u, false
	}
	u.InputTokens = i
	u.OutputTokens = o
	u.TotalTokens = t
	u.Status = UsageProvisional
	if ip && op && tp && t != i+o {
		u.Status = UsageConflicting
		return u, false
	}
	for key, dest := range map[string]*int64{"prompt_tokens_details": &u.CachedTokens, "completion_tokens_details": &u.ReasoningTokens} {
		v, exists := m[key]
		if !exists || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			continue
		}
		details, e := object(v)
		if e != nil {
			u.Status = UsageConflicting
			return u, false
		}
		field := "cached_tokens"
		if key == "completion_tokens_details" {
			field = "reasoning_tokens"
		}
		for k, raw := range details {
			if k == field {
				n, ok := integer(raw)
				if !ok || n < 0 {
					u.Status = UsageConflicting
					return u, false
				}
				*dest = n
			} else { // A nonzero unfamiliar dimension may carry a separate charge.
				n, ok := integer(raw)
				if !ok || n != 0 {
					u.Status = UsageConflicting
					return u, false
				}
			}
		}
	}
	if (ip && u.CachedTokens > i) || (op && u.ReasoningTokens > o) {
		u.Status = UsageConflicting
		return u, false
	}
	return u, ip && op && tp
}

func (t *usageTracker) observe(raw []byte) Usage {
	u, complete := parseUsage(raw)
	if u.Status == UsageMissing {
		if t.seen {
			return t.value
		}
		return u
	}
	if t.value.Status == UsageConflicting {
		return t.value
	}
	if u.Status == UsageConflicting {
		t.value = u
		t.seen = true
		t.complete = false
		return u
	}
	if t.seen {
		old := t.value
		if t.semantics == "cumulative" {
			if u.InputTokens < old.InputTokens || u.OutputTokens < old.OutputTokens || u.TotalTokens < old.TotalTokens || u.CachedTokens < old.CachedTokens || u.ReasoningTokens < old.ReasoningTokens {
				u.Status = UsageConflicting
				complete = false
			}
		} else if u.InputTokens != old.InputTokens || u.OutputTokens != old.OutputTokens || u.TotalTokens != old.TotalTokens || u.CachedTokens != old.CachedTokens || u.ReasoningTokens != old.ReasoningTokens || complete != t.complete {
			u.Status = UsageConflicting
			complete = false
		}
	}
	t.seen = true
	t.complete = complete
	t.value = u
	return u
}
func (t *usageTracker) finalize() Usage {
	if !t.seen {
		return Usage{Status: UsageMissing}
	}
	if t.complete && t.value.Status != UsageConflicting {
		t.value.Status = UsageConfirmed
	}
	return t.value
}

// UsageJSON returns only normalized OpenAI usage dimensions and never estimates.
func UsageJSON(u Usage) ([]byte, error) {
	if u.Status != UsageConfirmed {
		return nil, failure("usage_unconfirmed", 502, ClassProtocol, ConsumptionUnknown)
	}
	return json.Marshal(map[string]any{"prompt_tokens": u.InputTokens, "completion_tokens": u.OutputTokens, "total_tokens": u.TotalTokens, "prompt_tokens_details": map[string]int64{"cached_tokens": u.CachedTokens}, "completion_tokens_details": map[string]int64{"reasoning_tokens": u.ReasoningTokens}})
}
