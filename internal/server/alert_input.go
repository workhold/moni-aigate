package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/alerts"
)

// The shared canonical decoder checks object keys at every depth. Parameter
// names belong to nested objects and must not become valid top-level fields.
func alertBody(c *gin.Context, fields ...string) (map[string]json.RawMessage, bool) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 8193))
	if err != nil || len(raw) > 8192 || !pricingUnicode(raw) {
		return nil, false
	}
	allowed := append(append([]string{}, fields...), "threshold_amount", "recovery_amount", "minimum_age_seconds", "trigger_count", "recovery_count")
	var object map[string]json.RawMessage
	if !decodeCanonicalJSON(bytes.NewReader(raw), &object, 8192, allowed...) || len(object) != len(fields) {
		return nil, false
	}
	for _, key := range fields {
		if value, ok := object[key]; !ok || string(value) == "null" {
			return nil, false
		}
	}
	return object, true
}

func alertLabel(raw json.RawMessage) (string, bool) {
	v, ok := commerceString(raw)
	return v, ok && utf8.RuneCountInString(v) >= 1 && utf8.RuneCountInString(v) <= 80 && len(v) <= 160 && strings.TrimSpace(v) == v
}
func alertState(raw json.RawMessage) (alerts.RuleState, bool) {
	v, ok := commerceString(raw)
	return alerts.RuleState(v), ok && (v == "enabled" || v == "disabled")
}
func alertDecimal(raw json.RawMessage, positive bool) (string, bool) {
	_, ok := commerceInteger(raw, positive)
	v, _ := commerceString(raw)
	return v, ok
}
func alertKind(v string) bool {
	switch alerts.Kind(v) {
	case alerts.ChannelQuarantined, alerts.ProcurementBudgetLow, alerts.CustomerReconciliationAged, alerts.ProcurementUnresolvedAged:
		return true
	}
	return false
}
func alertParameters(raw json.RawMessage) (alerts.Parameters, bool) {
	var shape map[string]json.RawMessage
	if json.Unmarshal(raw, &shape) != nil || shape == nil {
		return nil, false
	}
	if len(shape) == 0 {
		obj, ok := commerceObject(bytes.NewReader(raw))
		return alerts.EmptyParameters{}, ok && len(obj) == 0
	}
	if len(shape) == 2 {
		obj, ok := commerceObject(bytes.NewReader(raw), "threshold_amount", "recovery_amount")
		if !ok {
			return nil, false
		}
		a, oka := commerceInteger(obj["threshold_amount"], false)
		b, okb := commerceInteger(obj["recovery_amount"], false)
		if !oka || !okb || b < a {
			return nil, false
		}
		x, _ := commerceString(obj["threshold_amount"])
		y, _ := commerceString(obj["recovery_amount"])
		return alerts.BudgetParameters{ThresholdAmount: x, RecoveryAmount: y}, true
	}
	obj, ok := commerceObject(bytes.NewReader(raw), "minimum_age_seconds", "trigger_count", "recovery_count")
	if !ok {
		return nil, false
	}
	a, oa := commerceInteger(obj["minimum_age_seconds"], true)
	b, ob := commerceInteger(obj["trigger_count"], true)
	d, od := commerceInteger(obj["recovery_count"], false)
	if !oa || !ob || !od || a < 600 || a > 604800 || b > 1000 || d > 999 || d >= b {
		return nil, false
	}
	x, _ := commerceString(obj["minimum_age_seconds"])
	y, _ := commerceString(obj["trigger_count"])
	z, _ := commerceString(obj["recovery_count"])
	return alerts.AgingParameters{MinimumAgeSeconds: x, TriggerCount: y, RecoveryCount: z}, true
}
func alertParametersMatch(k alerts.Kind, p alerts.Parameters) bool {
	switch k {
	case alerts.ChannelQuarantined:
		_, ok := p.(alerts.EmptyParameters)
		return ok
	case alerts.ProcurementBudgetLow:
		_, ok := p.(alerts.BudgetParameters)
		return ok
	case alerts.CustomerReconciliationAged, alerts.ProcurementUnresolvedAged:
		_, ok := p.(alerts.AgingParameters)
		return ok
	}
	return false
}
func alertScopeMatches(k alerts.Kind, v string) bool {
	switch k {
	case alerts.ChannelQuarantined:
		return v == "channel"
	case alerts.CustomerReconciliationAged:
		return v == "tenant"
	case alerts.ProcurementBudgetLow, alerts.ProcurementUnresolvedAged:
		return v == "procurement_account"
	}
	return false
}
func alertCursor(value, digest string) (*alerts.Cursor, bool) {
	if value == "" {
		return nil, true
	}
	cursor, err := alerts.DecodeCursor(value)
	if err != nil || cursor == nil || cursor.FilterDigest != digest {
		return nil, false
	}
	return cursor, true
}
func (s *Server) alertQuery(c *gin.Context, fields ...string) (url.Values, bool) {
	if c.Request.URL.ForceQuery {
		s.commerceInvalid(c)
		return nil, false
	}
	return s.commerceQuery(c, fields...)
}
