package provider

import (
	"encoding/json"
	"math"
)

// geminiUsage translates inclusive prompt and generated/hidden-thought counts
// into the common evidence schema. It never invents a required counter.
func geminiUsage(raw []byte) []byte {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	invalid := []byte(`{"invalid":1}`)
	m, err := object(raw)
	if err != nil || !only(m, "promptTokenCount", "candidatesTokenCount", "totalTokenCount", "thoughtsTokenCount", "cachedContentTokenCount", "toolUsePromptTokenCount", "promptTokensDetails", "cacheTokensDetails", "candidatesTokensDetails", "toolUsePromptTokensDetails") {
		return invalid
	}
	values := map[string]int64{}
	present := map[string]bool{}
	for _, key := range []string{"promptTokenCount", "candidatesTokenCount", "totalTokenCount", "thoughtsTokenCount", "cachedContentTokenCount", "toolUsePromptTokenCount"} {
		if raw, exists := m[key]; exists {
			value, valid := integer(raw)
			if !valid || value < 0 || (key == "toolUsePromptTokenCount" && value != 0) {
				return invalid
			}
			values[key], present[key] = value, true
		}
	}
	if values["candidatesTokenCount"] > math.MaxInt64-values["thoughtsTokenCount"] || (present["promptTokenCount"] && values["cachedContentTokenCount"] > values["promptTokenCount"]) {
		return invalid
	}
	for detail, counter := range map[string]string{"promptTokensDetails": "promptTokenCount", "cacheTokensDetails": "cachedContentTokenCount", "candidatesTokensDetails": "candidatesTokenCount", "toolUsePromptTokensDetails": "toolUsePromptTokenCount"} {
		if raw, exists := m[detail]; exists && !geminiTextTokens(raw, values[counter]) {
			return invalid
		}
	}
	out := map[string]any{"prompt_tokens_details": map[string]int64{"cached_tokens": values["cachedContentTokenCount"]}, "completion_tokens_details": map[string]int64{"reasoning_tokens": values["thoughtsTokenCount"]}}
	if present["promptTokenCount"] {
		out["prompt_tokens"] = values["promptTokenCount"]
	}
	if present["candidatesTokenCount"] {
		out["completion_tokens"] = values["candidatesTokenCount"] + values["thoughtsTokenCount"]
	}
	if present["totalTokenCount"] {
		out["total_tokens"] = values["totalTokenCount"]
	}
	return encoded(out)
}

func geminiTextTokens(raw []byte, total int64) bool {
	var details []json.RawMessage
	if json.Unmarshal(raw, &details) != nil || details == nil || len(details) > 1 {
		return false
	}
	if len(details) == 0 {
		return total == 0
	}
	m, err := object(details[0])
	if err != nil || !only(m, "modality", "tokenCount") || !stringValue(m["modality"], "TEXT") {
		return false
	}
	n, valid := integer(m["tokenCount"])
	return valid && n >= 0 && n == total
}

func geminiUsageJSON(u Usage) json.RawMessage {
	return encoded(map[string]int64{
		"promptTokenCount": u.InputTokens, "candidatesTokenCount": u.OutputTokens - u.ReasoningTokens,
		"totalTokenCount": u.TotalTokens, "thoughtsTokenCount": u.ReasoningTokens, "cachedContentTokenCount": u.CachedTokens,
	})
}
