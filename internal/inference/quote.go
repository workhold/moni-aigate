package inference

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

func ParseQuoteSnapshot(raw []byte, plan BoundPlan, side metering.Side) (QuoteSnapshot, error) {
	m, err := envelope(raw, 16_384)
	if err != nil {
		return QuoteSnapshot{}, err
	}
	if !plan.valid {
		return QuoteSnapshot{}, ErrInvalidValue
	}
	if side != metering.Retail && side != metering.Supplier {
		return QuoteSnapshot{}, metering.ErrSide
	}
	if !fields(m, "version", "status", "side", "plan_digest", "schema_id", "schema_digest", "variant_key", "version_id", "unknown_reason", "bounds", "rates") || str(m["version"]) != "1" || !member(str(m["status"]), "known", "unknown") || !member(str(m["side"]), "retail", "supplier") {
		return QuoteSnapshot{}, ErrInvalidQuote
	}
	// Validate envelope lexical values before comparing any expected identity.
	if !validUUID(str(m["schema_id"])) || !validHex(m["schema_digest"]) || !validHex(m["plan_digest"]) || !validVariant(m["variant_key"]) {
		return QuoteSnapshot{}, ErrInvalidQuote
	}
	if m["version_id"] != nil && !validUUID(str(m["version_id"])) {
		return QuoteSnapshot{}, ErrInvalidQuote
	}
	if m["unknown_reason"] != nil && !member(str(m["unknown_reason"]), "version_missing", "basis_unknown") {
		return QuoteSnapshot{}, ErrInvalidQuote
	}
	boundsJSON, _ := json.Marshal(m["bounds"])
	bounds, boundsErr := metering.ParseBounds(plan.schema, side, boundsJSON)
	if boundsErr != nil && !errors.Is(boundsErr, metering.ErrBindingMismatch) {
		return QuoteSnapshot{}, fmt.Errorf("%w: %w", ErrInvalidQuote, boundsErr)
	}
	var rates metering.Rates
	var ratesErr error
	if m["rates"] != nil {
		ratesJSON, _ := json.Marshal(m["rates"])
		rates, ratesErr = metering.ParseRates(plan.schema, side, ratesJSON)
		if ratesErr != nil && !errors.Is(ratesErr, metering.ErrBindingMismatch) {
			return QuoteSnapshot{}, fmt.Errorf("%w: %w", ErrInvalidQuote, ratesErr)
		}
	}
	id, _ := plan.schema.ID()
	sd, _ := plan.schema.Digest()
	if str(m["side"]) != string(side) || str(m["schema_id"]) != id || !matchesDigest(m["schema_digest"], sd) || !matchesDigest(m["plan_digest"], plan.digest) || str(m["variant_key"]) != plan.variant {
		return QuoteSnapshot{}, ErrBindingMismatch
	}
	if side == metering.Retail && plan.scope != ScopeRequest || side == metering.Supplier && plan.scope != ScopeAttempt {
		return QuoteSnapshot{}, ErrBindingMismatch
	}
	if boundsErr != nil || ratesErr != nil {
		return QuoteSnapshot{}, fmt.Errorf("%w: %w", ErrBindingMismatch, metering.ErrBindingMismatch)
	}
	canonicalBounds, _ := bounds.CanonicalJSON()
	expected, _ := plan.Bounds(side)
	expectedJSON, _ := expected.CanonicalJSON()
	if !bytes.Equal(canonicalBounds, expectedJSON) {
		return QuoteSnapshot{}, ErrBindingMismatch
	}
	q := QuoteSnapshot{status: QuoteStatus(str(m["status"])), side: side, bounds: bounds}
	var canonicalRates json.RawMessage
	if q.status == QuoteKnown {
		q.versionID = str(m["version_id"])
		if !validUUID(q.versionID) || m["unknown_reason"] != nil || m["rates"] == nil {
			return QuoteSnapshot{}, ErrInvalidQuote
		}
		q.rates = rates
		if inputOnly(plan.protocol) {
			entries, _ := q.rates.Entries()
			for _, entry := range entries {
				if entry.Dimension == "text_output_token" && entry.Numerator != 0 {
					return QuoteSnapshot{}, ErrInvalidQuote
				}
			}
		}
		if _, err = metering.QuoteDigest(bounds, q.rates); err != nil {
			return QuoteSnapshot{}, fmt.Errorf("%w: %w", ErrInvalidQuote, err)
		}
		q.amount, err = metering.ReservationAmount(bounds, q.rates)
		if err != nil {
			return QuoteSnapshot{}, fmt.Errorf("%w: %w", ErrInvalidQuote, err)
		}
		canonicalRates, _ = q.rates.CanonicalJSON()
	} else {
		if side != metering.Supplier || m["rates"] != nil {
			return QuoteSnapshot{}, ErrInvalidQuote
		}
		q.reason = str(m["unknown_reason"])
		switch q.reason {
		case "version_missing":
			if m["version_id"] != nil {
				return QuoteSnapshot{}, ErrInvalidQuote
			}
		case "basis_unknown":
			q.versionID = str(m["version_id"])
			if !validUUID(q.versionID) {
				return QuoteSnapshot{}, ErrInvalidQuote
			}
		default:
			return QuoteSnapshot{}, ErrInvalidQuote
		}
	}
	var version, reason *string
	if q.versionID != "" {
		value := q.versionID
		version = &value
	}
	if q.reason != "" {
		value := q.reason
		reason = &value
	}
	wire := struct {
		Version       string          `json:"version"`
		Status        QuoteStatus     `json:"status"`
		Side          metering.Side   `json:"side"`
		PlanDigest    string          `json:"plan_digest"`
		SchemaID      string          `json:"schema_id"`
		SchemaDigest  string          `json:"schema_digest"`
		Variant       string          `json:"variant_key"`
		VersionID     *string         `json:"version_id"`
		UnknownReason *string         `json:"unknown_reason"`
		Bounds        json.RawMessage `json:"bounds"`
		Rates         json.RawMessage `json:"rates"`
	}{"1", q.status, side, digestHex(plan.digest), id, digestHex(sd), plan.variant, version, reason, canonicalBounds, canonicalRates}
	q.canonical, err = json.Marshal(wire)
	if err != nil {
		return QuoteSnapshot{}, ErrInvalidQuote
	}
	q.valid = true
	q.digest = frameDigest("moni-aigate.inference.quote.v1", sd[:], q.canonical)
	return q, nil
}

func (q QuoteSnapshot) Status() (QuoteStatus, error) {
	if !q.valid {
		return "", ErrInvalidValue
	}
	return q.status, nil
}
func (q QuoteSnapshot) Bounds() (metering.Bounds, error) {
	if !q.valid {
		return metering.Bounds{}, ErrInvalidValue
	}
	return q.bounds, nil
}
func (q QuoteSnapshot) Rates() (metering.Rates, error) {
	if !q.valid {
		return metering.Rates{}, ErrInvalidValue
	}
	if q.status != QuoteKnown {
		return metering.Rates{}, ErrUnknownQuote
	}
	return q.rates, nil
}
func (q QuoteSnapshot) VersionID() (string, bool, error) {
	if !q.valid {
		return "", false, ErrInvalidValue
	}
	return q.versionID, q.versionID != "", nil
}
func (q QuoteSnapshot) UnknownReason() (string, error) {
	if !q.valid {
		return "", ErrInvalidValue
	}
	return q.reason, nil
}
func (q QuoteSnapshot) Amount() (int64, error) {
	if !q.valid {
		return 0, ErrInvalidValue
	}
	if q.status != QuoteKnown {
		return 0, ErrUnknownQuote
	}
	return q.amount, nil
}
func (q QuoteSnapshot) CanonicalJSON() ([]byte, error) { return canonicalCopy(q.valid, q.canonical) }
func (q QuoteSnapshot) Digest() ([32]byte, error) {
	if !q.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return q.digest, nil
}
