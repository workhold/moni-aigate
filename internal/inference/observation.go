package inference

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

type dimensionUsageWire struct {
	Dimension string     `json:"dimension"`
	Unit      string     `json:"unit"`
	Status    UsageState `json:"status"`
	Quantity  *string    `json:"quantity"`
}
type observationWire struct {
	Version                string               `json:"version"`
	TenantID               string               `json:"tenant_id"`
	RequestID              string               `json:"request_id"`
	AttemptID              string               `json:"attempt_id"`
	Source                 string               `json:"source"`
	SourceEventID          string               `json:"source_event_id"`
	ProfileVersion         string               `json:"profile_version"`
	SchemaID               string               `json:"schema_id"`
	SchemaDigest           string               `json:"schema_digest"`
	Variant                string               `json:"variant_key"`
	PlanDigest             string               `json:"plan_digest"`
	ProviderReference      *string              `json:"provider_reference"`
	ObservedAt             *string              `json:"observed_at"`
	ProtocolOutcome        string               `json:"protocol_outcome"`
	TransportEndReason     string               `json:"transport_end_reason"`
	DeliveryStage          string               `json:"delivery_stage"`
	ContentWritten         bool                 `json:"content_written"`
	FinishReason           string               `json:"finish_reason"`
	Quantities             []dimensionUsageWire `json:"quantities"`
	UnexpectedChargeReason *string              `json:"unexpected_charge_reason"`
}

func ParseObservation(raw []byte, expected ObservationBinding) (Observation, error) {
	m, err := envelope(raw, 32_768)
	if err != nil {
		return Observation{}, err
	}
	id, err := expected.Schema.ID()
	if err != nil {
		return Observation{}, fmt.Errorf("%w: %w", ErrInvalidValue, err)
	}
	if !validUUID(expected.TenantID) || !validUUID(expected.RequestID) || !validUUID(expected.AttemptID) || !validUUID(expected.ProfileVersion) || expected.VariantKey != "default" || expected.PlanDigest == [32]byte{} {
		return Observation{}, ErrInvalidValue
	}
	if !fields(m, "version", "tenant_id", "request_id", "attempt_id", "source", "source_event_id", "profile_version", "schema_id", "schema_digest", "variant_key", "plan_digest", "provider_reference", "observed_at", "protocol_outcome", "transport_end_reason", "delivery_stage", "content_written", "finish_reason", "quantities", "unexpected_charge_reason") || str(m["version"]) != "1" {
		return Observation{}, ErrInvalidObservation
	}
	if !validUUID(str(m["tenant_id"])) || !validUUID(str(m["request_id"])) || !validUUID(str(m["attempt_id"])) || !validUUID(str(m["profile_version"])) || !validUUID(str(m["schema_id"])) {
		return Observation{}, ErrInvalidObservation
	}
	sd, _ := expected.Schema.Digest()
	if !validHex(m["schema_digest"]) || !validHex(m["plan_digest"]) || !validVariant(m["variant_key"]) {
		return Observation{}, ErrInvalidObservation
	}
	meta := ObservationMetadata{Source: str(m["source"]), SourceEventID: str(m["source_event_id"]), ProtocolOutcome: str(m["protocol_outcome"]), TransportEndReason: str(m["transport_end_reason"]), DeliveryStage: str(m["delivery_stage"])}
	if !member(meta.Source, "adapter", "retrieval", "gateway", "approved_reconciliation") || !safeReference(meta.SourceEventID, 200) || !member(meta.ProtocolOutcome, "completed", "failed", "incomplete", "cancelled", "unknown") || !member(meta.TransportEndReason, "done", "eof", "provider_error", "platform_error", "platform_timeout", "client_cancelled", "connection_error", "unknown", "not_sent", "rejected_no_consumption") || !member(meta.DeliveryStage, "none", "committed", "content", "unknown") {
		return Observation{}, ErrInvalidObservation
	}
	if m["provider_reference"] != nil {
		meta.ProviderReference = str(m["provider_reference"])
		if !safeReference(meta.ProviderReference, 200) {
			return Observation{}, ErrInvalidObservation
		}
	}
	if m["unexpected_charge_reason"] != nil {
		meta.UnexpectedChargeReason = str(m["unexpected_charge_reason"])
		if !member(meta.UnexpectedChargeReason, "supplier_unmapped_charge", "unattributable_charge") {
			return Observation{}, ErrInvalidObservation
		}
	}
	written, ok := m["content_written"].(bool)
	if !ok || written != (meta.DeliveryStage == "content") {
		return Observation{}, ErrInvalidObservation
	}
	meta.ContentWritten = written
	meta.FinishReason, ok = m["finish_reason"].(string)
	if !ok || !safeText(meta.FinishReason, 80, true) {
		return Observation{}, ErrInvalidObservation
	}
	timestamp := str(m["observed_at"])
	meta.ObservedAt, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil || meta.ObservedAt.IsZero() || meta.ObservedAt.UTC().Format(time.RFC3339Nano) != timestamp {
		return Observation{}, ErrInvalidObservation
	}
	values, ok := m["quantities"].([]any)
	dimensions, _ := expected.Schema.Dimensions()
	if !ok || len(values) == 0 || len(values) > 8 {
		return Observation{}, ErrInvalidObservation
	}
	entries := make([]DimensionUsage, len(values))
	wireEntries := make([]dimensionUsageWire, len(values))
	for i, v := range values {
		entry, ok := v.(map[string]any)
		if !ok || !fields(entry, "dimension", "unit", "status", "quantity") || !safeIdentifier(entry["dimension"]) || !safeIdentifier(entry["unit"]) {
			return Observation{}, ErrInvalidObservation
		}
		status := UsageState(str(entry["status"]))
		if !member(string(status), "confirmed_final", "provisional", "missing", "conflicting") {
			return Observation{}, ErrInvalidObservation
		}
		entries[i] = DimensionUsage{Dimension: str(entry["dimension"]), Unit: str(entry["unit"]), Status: status}
		wireEntries[i] = dimensionUsageWire{Dimension: str(entry["dimension"]), Unit: str(entry["unit"]), Status: status}
		if status == UsageConfirmed || status == UsageProvisional {
			q, ok := decimal(entry["quantity"])
			if !ok {
				return Observation{}, ErrInvalidObservation
			}
			entries[i].Quantity = &q
			s := strconv.FormatInt(q, 10)
			wireEntries[i].Quantity = &s
		} else if entry["quantity"] != nil {
			return Observation{}, ErrInvalidObservation
		}
	}
	if str(m["tenant_id"]) != expected.TenantID || str(m["request_id"]) != expected.RequestID || str(m["attempt_id"]) != expected.AttemptID || str(m["profile_version"]) != expected.ProfileVersion || str(m["schema_id"]) != id || !matchesDigest(m["schema_digest"], sd) || str(m["variant_key"]) != expected.VariantKey || !matchesDigest(m["plan_digest"], expected.PlanDigest) {
		return Observation{}, ErrBindingMismatch
	}
	if len(entries) != len(dimensions) {
		return Observation{}, ErrInvalidObservation
	}
	for i, entry := range entries {
		if entry.Dimension != dimensions[i].Dimension || entry.Unit != dimensions[i].Unit {
			return Observation{}, ErrInvalidObservation
		}
	}
	var provider, reason *string
	if meta.ProviderReference != "" {
		s := meta.ProviderReference
		provider = &s
	}
	if meta.UnexpectedChargeReason != "" {
		s := meta.UnexpectedChargeReason
		reason = &s
	}
	wire := observationWire{"1", expected.TenantID, expected.RequestID, expected.AttemptID, meta.Source, meta.SourceEventID, expected.ProfileVersion, id, digestHex(sd), expected.VariantKey, digestHex(expected.PlanDigest), provider, &timestamp, meta.ProtocolOutcome, meta.TransportEndReason, meta.DeliveryStage, meta.ContentWritten, meta.FinishReason, wireEntries, reason}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return Observation{}, ErrInvalidObservation
	}
	wire.ObservedAt = nil
	identity, err := json.Marshal(wire)
	if err != nil {
		return Observation{}, ErrInvalidObservation
	}
	return Observation{valid: true, binding: expected, metadata: meta, entries: entries, canonical: canonical, digest: frameDigest("moni-aigate.inference.observation.v1", sd[:], identity)}, nil
}

func (o Observation) State(side metering.Side) (UsageState, error) {
	if !o.valid {
		return "", ErrInvalidValue
	}
	mask, err := o.binding.Schema.Mask(side)
	if err != nil {
		return "", err
	}
	missing, provisional := false, false
	for _, name := range mask {
		for _, entry := range o.entries {
			if entry.Dimension != name {
				continue
			}
			switch entry.Status {
			case UsageConflicting:
				return UsageConflicting, nil
			case UsageMissing:
				missing = true
			case UsageProvisional:
				provisional = true
			}
		}
	}
	if o.metadata.UnexpectedChargeReason == "unattributable_charge" || side == metering.Supplier && (o.metadata.UnexpectedChargeReason == "supplier_unmapped_charge" || o.metadata.Source == "approved_reconciliation") {
		missing = true
	}
	if missing {
		return UsageMissing, nil
	}
	if provisional {
		return UsageProvisional, nil
	}
	return UsageConfirmed, nil
}

func (o Observation) Confirmed(side metering.Side) (FinalQuantities, error) {
	state, err := o.State(side)
	if err != nil {
		return FinalQuantities{}, err
	}
	if state == UsageConflicting {
		return FinalQuantities{}, ErrConflicting
	}
	if state != UsageConfirmed {
		return FinalQuantities{}, ErrIncomplete
	}
	mask, _ := o.binding.Schema.Mask(side)
	type quantityWire struct {
		Dimension string `json:"dimension"`
		Unit      string `json:"unit"`
		Quantity  string `json:"quantity"`
	}
	quantities := make([]quantityWire, 0, len(mask))
	for _, name := range mask {
		for _, entry := range o.entries {
			if entry.Dimension == name {
				quantities = append(quantities, quantityWire{entry.Dimension, entry.Unit, strconv.FormatInt(*entry.Quantity, 10)})
			}
		}
	}
	id, _ := o.binding.Schema.ID()
	sd, _ := o.binding.Schema.Digest()
	wire := struct {
		SchemaID   string         `json:"schema_id"`
		Version    string         `json:"version"`
		Side       metering.Side  `json:"side"`
		Quantities []quantityWire `json:"quantities"`
	}{id, "1", side, quantities}
	raw, err := json.Marshal(wire)
	if err != nil {
		return FinalQuantities{}, ErrInvalidObservation
	}
	q, err := metering.ParseQuantities(o.binding.Schema, side, raw)
	if err != nil {
		return FinalQuantities{}, fmt.Errorf("%w: %w", ErrInvalidObservation, err)
	}
	canonical, _ := q.CanonicalJSON()
	return FinalQuantities{valid: true, quantities: q, canonical: canonical, digest: frameDigest("moni-aigate.inference.final.v1", sd[:], []byte(side), canonical)}, nil
}

func (o Observation) Binding() (ObservationBinding, error) {
	if !o.valid {
		return ObservationBinding{}, ErrInvalidValue
	}
	return o.binding, nil
}
func (o Observation) Metadata() (ObservationMetadata, error) {
	if !o.valid {
		return ObservationMetadata{}, ErrInvalidValue
	}
	return o.metadata, nil
}
func (o Observation) Entries() ([]DimensionUsage, error) {
	if !o.valid {
		return nil, ErrInvalidValue
	}
	entries := append([]DimensionUsage(nil), o.entries...)
	for i := range entries {
		if entries[i].Quantity != nil {
			q := *entries[i].Quantity
			entries[i].Quantity = &q
		}
	}
	return entries, nil
}
func (o Observation) CanonicalJSON() ([]byte, error) { return canonicalCopy(o.valid, o.canonical) }
func (o Observation) Digest() ([32]byte, error) {
	if !o.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return o.digest, nil
}
func (q FinalQuantities) Quantities() (metering.Quantities, error) {
	if !q.valid {
		return metering.Quantities{}, ErrInvalidValue
	}
	return q.quantities, nil
}
func (q FinalQuantities) CanonicalJSON() ([]byte, error) { return canonicalCopy(q.valid, q.canonical) }
func (q FinalQuantities) Digest() ([32]byte, error) {
	if !q.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return q.digest, nil
}
