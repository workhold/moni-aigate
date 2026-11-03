package procurementprefix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// These declarations independently reproduce the persisted Go JSON ABI. They
// deliberately do not import billing, a writer, or the SQL identity codec.
type requestUsage struct {
	Status          string `json:"status"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
}

func requestOneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func requestUsageValid(u requestUsage) bool {
	if !requestOneOf(u.Status, "confirmed_final", "provisional", "estimated", "missing", "conflicting") || u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 || u.CachedTokens < 0 || u.ReasoningTokens < 0 || u.CachedTokens > u.InputTokens || u.ReasoningTokens > u.OutputTokens {
		return false
	}
	return u.Status != "confirmed_final" || u.InputTokens <= math.MaxInt64-u.OutputTokens && u.TotalTokens == u.InputTokens+u.OutputTokens
}

func requestIncomingUsage(in IncomingSourceRow) (requestUsage, bool) {
	u := requestUsage{Status: in.UsageStatus}
	targets := []*int64{&u.InputTokens, &u.OutputTokens, &u.TotalTokens, &u.CachedTokens, &u.ReasoningTokens}
	for i, value := range []string{in.InputTokens, in.OutputTokens, in.TotalTokens, in.CachedTokens, in.ReasoningTokens} {
		if !signedInteger(value, 64) {
			return requestUsage{}, false
		}
		n, _ := strconv.ParseInt(value, 10, 64)
		*targets[i] = n
	}
	return u, requestUsageValid(u)
}

func requestParseUsage(raw string) (requestUsage, bool) {
	var u requestUsage
	d := json.NewDecoder(strings.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return u, false
	}
	fields := make(map[string]json.RawMessage, 6)
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || !requestOneOf(name, "status", "input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens") {
			return u, false
		}
		if _, exists := fields[name]; exists {
			return u, false
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return u, false
		}
		fields[name] = value
	}
	if last, err := d.Token(); err != nil || last != json.Delim('}') || len(fields) != 6 {
		return u, false
	}
	if err := d.Decode(new(any)); err != io.EOF || json.Unmarshal(fields["status"], &u.Status) != nil {
		return u, false
	}
	targets := []*int64{&u.InputTokens, &u.OutputTokens, &u.TotalTokens, &u.CachedTokens, &u.ReasoningTokens}
	for i, name := range []string{"input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens"} {
		value := strings.TrimSpace(string(fields[name]))
		if !signedInteger(value, 64) {
			return requestUsage{}, false
		}
		n, _ := strconv.ParseInt(value, 10, 64)
		*targets[i] = n
	}
	return u, requestUsageValid(u)
}

func requestHash(v any) string {
	raw, _ := json.Marshal(v) // All callers use closed string/int64/bool/time-zero structs.
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func requestInputDigest(in IncomingSourceRow, u requestUsage) string {
	return requestHash(struct {
		ID, TenantID, RequestID, AttemptID, Source, SourceEventID        string
		Usage                                                            requestUsage
		ProtocolOutcome, TransportEndReason, DeliveryStage, FinishReason string
		ContentWritten                                                   bool
		ObservedAt                                                       time.Time
	}{in.InputFinalizationID, in.TenantID, in.RequestID, requestOptional(in.AttemptID), in.Source, in.SourceEventID, u, in.ProtocolOutcome, in.TransportEndReason, in.RawDeliveryStage, in.FinishReason, in.RawContentWritten, time.Time{}})
}

func requestEvidenceDigest(u requestUsage, reason string) string {
	return requestHash(struct {
		Usage  requestUsage
		Reason string
	}{u, reason})
}

func requestOptional(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func requestSameOptional(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func requestPointer(value *string, expected string) bool { return value != nil && *value == expected }

func requestIncomingValid(in IncomingSourceRow) (requestUsage, bool) {
	u, ok := requestIncomingUsage(in)
	_, idErr := uuid.Parse(in.InputFinalizationID)
	if !ok || idErr != nil || !validText(in.Source) || len(in.Source) < 1 || len(in.Source) > 80 || !validText(in.SourceEventID) || len(in.SourceEventID) < 1 || len(in.SourceEventID) > 200 || !validText(in.FinishReason) || len(in.FinishReason) > 80 || !requestOneOf(in.ProtocolOutcome, "completed", "failed", "incomplete", "cancelled", "unknown") || !requestOneOf(in.TransportEndReason, "done", "eof", "provider_error", "platform_error", "platform_timeout", "client_cancelled", "connection_error", "unknown", "not_sent", "rejected_no_consumption") || !requestOneOf(in.RawDeliveryStage, "none", "committed", "content", "unknown") || !finite(in.EffectiveObservedAt) {
		return u, false
	}
	if !nullTime(in.ObservedAtInput) && (!finite(in.ObservedAtInput) || in.ObservedAtInput.Time.IsZero() || !in.ObservedAtInput.Time.Equal(in.EffectiveObservedAt.Time)) {
		return u, false
	}
	return u, in.AttemptID == nil || *in.AttemptID != ""
}

// Duplicate natural keys become unavailable instead of selecting a row by input
// order. SourceClosure separately reports the complete duplicate-row domain.
func requestIndex[T any](w *replayWork, rows []T, key func(T) string) (map[string]T, error) {
	out := make(map[string]T, len(rows))
	duplicates := make(keySet)
	for _, row := range rows {
		if err := w.tick(); err != nil {
			return nil, err
		}
		k := key(row)
		if _, duplicate := duplicates[k]; duplicate {
			continue
		}
		if _, exists := out[k]; exists {
			delete(out, k)
			duplicates[k] = struct{}{}
		} else {
			out[k] = row
		}
	}
	return out, nil
}

func (r *requestReplay) parseUsage(raw string) (requestUsage, bool, error) {
	if err := r.w.b.check(); err != nil {
		return requestUsage{}, false, err
	}
	u, ok := requestParseUsage(raw)
	if err := r.w.b.check(); err != nil {
		return requestUsage{}, false, err
	}
	return u, ok, nil
}
