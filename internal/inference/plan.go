package inference

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

func NewTextPlan(in TextPlanInput) (BoundPlan, error) {
	if len(in.Candidates) == 0 || len(in.Candidates) > 256 || in.InputHMAC == [32]byte{} {
		return BoundPlan{}, ErrInvalidPlan
	}
	profiles := append([]Profile(nil), in.Candidates...)
	for _, p := range profiles {
		if !p.valid {
			return BoundPlan{}, ErrInvalidPlan
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return bytes.Compare(profiles[i].digest[:], profiles[j].digest[:]) < 0 })
	first := profiles[0].info
	sd, _ := first.Schema.Digest()
	seen := make(map[string]bool, len(profiles))
	var input int64
	for i, p := range profiles {
		info := p.info
		other, _ := info.Schema.Digest()
		if info.Model != first.Model || info.Protocol != first.Protocol || info.VariantKey != first.VariantKey || other != sd {
			return BoundPlan{}, ErrBindingMismatch
		}
		if i > 0 && profiles[i-1].digest == p.digest || seen[info.Version] {
			return BoundPlan{}, ErrInvalidPlan
		}
		seen[info.Version] = true
		if in.OutputLimit < 0 || in.OutputLimit > info.OutputLimit || inputOnly(info.Protocol) != (in.OutputLimit == 0) || inputOnly(info.Protocol) && in.Stream {
			return BoundPlan{}, ErrInvalidPlan
		}
		if info.InputTokenUpperBound > input {
			input = info.InputTokenUpperBound
		}
	}
	p := BoundPlan{scope: ScopeRequest, profiles: profiles, schema: first.Schema, model: first.Model, protocol: first.Protocol, variant: first.VariantKey, inputHMAC: in.InputHMAC, output: in.OutputLimit, stream: in.Stream}
	var err error
	p.retail, err = metering.ManagementTokenBounds(p.schema, metering.Retail, input, p.output)
	if err != nil {
		return BoundPlan{}, fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	p.supplier, err = metering.ManagementTokenBounds(p.schema, metering.Supplier, input, p.output)
	if err != nil {
		return BoundPlan{}, fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	return finishPlan(p)
}

func (p BoundPlan) ForAttempt(profile Profile) (BoundPlan, error) {
	if !p.valid || p.scope != ScopeRequest || !profile.valid {
		return BoundPlan{}, ErrInvalidPlan
	}
	found := false
	for _, candidate := range p.profiles {
		if candidate.digest == profile.digest {
			found = true
			break
		}
	}
	if !found {
		return BoundPlan{}, ErrBindingMismatch
	}
	a := p
	a.valid = false
	a.scope = ScopeAttempt
	a.selected = profile
	a.parent = p.digest
	var err error
	a.retail, err = metering.ManagementTokenBounds(p.schema, metering.Retail, profile.info.InputTokenUpperBound, p.output)
	if err != nil {
		return BoundPlan{}, fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	a.supplier, err = metering.ManagementTokenBounds(p.schema, metering.Supplier, profile.info.InputTokenUpperBound, p.output)
	if err != nil {
		return BoundPlan{}, fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	return finishPlan(a)
}

func finishPlan(p BoundPlan) (BoundPlan, error) {
	id, _ := p.schema.ID()
	sd, _ := p.schema.Digest()
	rawRetail, _ := p.retail.CanonicalJSON()
	rawSupplier, _ := p.supplier.CanonicalJSON()
	digests := make([]string, len(p.profiles))
	for i, profile := range p.profiles {
		digests[i] = digestHex(profile.digest)
	}
	var selected, parent *string
	if p.scope == ScopeAttempt {
		s, r := digestHex(p.selected.digest), digestHex(p.parent)
		selected = &s
		parent = &r
	}
	result := ResultJSON
	if p.stream {
		result = ResultEvents
	}
	wire := struct {
		Version       string          `json:"version"`
		Scope         PlanScope       `json:"scope"`
		Model         string          `json:"model"`
		Protocol      Protocol        `json:"protocol"`
		ExecutionKind string          `json:"execution_kind"`
		ResultKind    ResultKind      `json:"result_kind"`
		SchemaID      string          `json:"schema_id"`
		SchemaDigest  string          `json:"schema_digest"`
		Variant       string          `json:"variant_key"`
		InputHMAC     string          `json:"input_hmac"`
		Output        string          `json:"output_limit"`
		Candidates    []string        `json:"candidate_profile_digests"`
		Selected      *string         `json:"selected_profile_digest"`
		Parent        *string         `json:"parent_plan_digest"`
		Retail        json.RawMessage `json:"retail_bounds"`
		Supplier      json.RawMessage `json:"supplier_bounds"`
	}{"1", p.scope, p.model, p.protocol, "synchronous", result, id, digestHex(sd), p.variant, digestHex(p.inputHMAC), strconv.FormatInt(p.output, 10), digests, selected, parent, rawRetail, rawSupplier}
	raw, err := json.Marshal(wire)
	if err != nil {
		return BoundPlan{}, ErrInvalidPlan
	}
	if len(raw) > 65_536 {
		return BoundPlan{}, ErrTooLarge
	}
	p.valid = true
	p.canonical = raw
	p.digest = frameDigest("moni-aigate.inference.plan.v1", sd[:], raw)
	return p, nil
}

func (p BoundPlan) Schema() (metering.Schema, error) {
	if !p.valid {
		return metering.Schema{}, ErrInvalidValue
	}
	return p.schema, nil
}
func (p BoundPlan) Scope() (PlanScope, error) {
	if !p.valid {
		return "", ErrInvalidValue
	}
	return p.scope, nil
}
func (p BoundPlan) Bounds(side metering.Side) (metering.Bounds, error) {
	if !p.valid {
		return metering.Bounds{}, ErrInvalidValue
	}
	switch side {
	case metering.Retail:
		return p.retail, nil
	case metering.Supplier:
		return p.supplier, nil
	}
	return metering.Bounds{}, metering.ErrSide
}
func (p BoundPlan) Profile() (Profile, error) {
	if !p.valid {
		return Profile{}, ErrInvalidValue
	}
	if p.scope != ScopeAttempt {
		return Profile{}, ErrInvalidPlan
	}
	return p.selected, nil
}
func (p BoundPlan) VariantKey() (string, error) {
	if !p.valid {
		return "", ErrInvalidValue
	}
	return p.variant, nil
}
func (p BoundPlan) InputHMAC() ([32]byte, error) {
	if !p.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return p.inputHMAC, nil
}
func (p BoundPlan) CanonicalJSON() ([]byte, error) { return canonicalCopy(p.valid, p.canonical) }
func (p BoundPlan) Digest() ([32]byte, error) {
	if !p.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return p.digest, nil
}
