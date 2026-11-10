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
		if !knownChatUsageKey(k) {
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
	hit, hitPresent, hitValid := read("prompt_cache_hit_tokens")
	miss, missPresent, missValid := read("prompt_cache_miss_tokens")
	if !hitValid || !missValid {
		u.Status = UsageConflicting
		return u, false
	}
	_, hitKeyPresent := m["prompt_cache_hit_tokens"]
	_, missKeyPresent := m["prompt_cache_miss_tokens"]
	var cachedDetailPresent bool
	var cachedDetail int64
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
				if key == "prompt_tokens_details" {
					cachedDetailPresent = true
					cachedDetail = n
				}
			} else { // A nonzero unfamiliar dimension may carry a separate charge.
				n, ok := integer(raw)
				if !ok || n != 0 {
					u.Status = UsageConflicting
					return u, false
				}
			}
		}
	}
	if hitPresent && cachedDetailPresent && hit != cachedDetail {
		u.Status = UsageConflicting
		return u, false
	}
	if hitPresent {
		u.CachedTokens = hit
	}
	if ip && (u.CachedTokens > i || (missPresent && miss > i)) {
		u.Status = UsageConflicting
		return u, false
	}
	if op && u.ReasoningTokens > o {
		u.Status = UsageConflicting
		return u, false
	}
	// DeepSeek exposes a charge-relevant cache split alongside the aggregate
	// prompt count. Keep it only when the split is complete and self-consistent.
	cacheComplete := true
	if hitKeyPresent || missKeyPresent {
		cacheComplete = (hitPresent || cachedDetailPresent) && missPresent
		if cacheComplete {
			if u.CachedTokens > math.MaxInt64-miss || (ip && u.CachedTokens+miss != i) {
				u.Status = UsageConflicting
				return u, false
			}
		}
	}
	return u, ip && op && tp && cacheComplete
}

// knownChatUsageKey is an explicit allowlist, not a prefix or "ignore unknown"
// rule: a key that may carry a separate charge must keep producing a conflict.
// The two DeepSeek keys partition prompt_tokens and add no charge of their own;
// the metadata keys describe money or routing, never a token dimension, and the
// gateway prices only token counts it has validated.
func knownChatUsageKey(k string) bool {
	switch k {
	case "prompt_tokens", "completion_tokens", "total_tokens", "prompt_tokens_details", "completion_tokens_details",
		"prompt_cache_hit_tokens", "prompt_cache_miss_tokens",
		"cost", "cost_details", "is_byok":
		return true
	}
	return false
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
