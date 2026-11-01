package provider

import (
	"encoding/json"
	"strings"
)

// Only machine-readable provider codes are classified. Human messages are
// neither matched nor copied to errors, logs or customer responses.
func classifyUpstream(status int, raw []byte) FailureClass {
	class := ClassUpstream
	switch status {
	case 400:
		class = ClassCaller
	case 401:
		class = ClassRejected
	case 402:
		class = ClassQuota
	case 403:
		class = ClassRejected
	case 404:
		class = ClassRejected
	case 429:
		class = ClassRateLimit
	}
	if status >= 500 {
		return class
	}
	m, err := object(raw)
	if err != nil {
		return class
	}
	detail, err := object(m["error"])
	if err != nil {
		return class
	}
	for _, key := range []string{"code", "type"} {
		var s string
		if json.Unmarshal(detail[key], &s) != nil {
			continue
		}
		switch strings.ToLower(s) {
		case "invalid_api_key", "invalid_authentication", "authentication_error", "insufficient_permissions":
			return ClassCredential
		case "insufficient_quota", "billing_hard_limit_reached", "credit_balance_too_low":
			return ClassQuota
		case "model_not_found", "model_not_available", "unsupported_model", "context_length_exceeded":
			return ClassModel
		case "content_filter", "content_policy_violation", "policy_violation":
			return ClassPolicy
		}
	}
	return class
}
