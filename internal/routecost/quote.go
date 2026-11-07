// Package routecost holds immutable procurement selection values for 028-U.
// It grants no dispatch or financial authority.
package routecost

import (
	"bytes"
	"errors"
	"math/big"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

var (
	ErrInvalidQuote  = errors.New("route_cost_invalid_quote")
	ErrInvalidLimits = errors.New("route_cost_invalid_limits")
	ErrUnknownQuote  = errors.New("route_cost_unknown_quote")
)

// Quote is a frozen selection value, not a second inference quote identity.
// It must not be converted to a per-million mirror for a scalar fallback.
type Quote struct {
	valid                             bool
	channel, upstream, version, basis string
	schema                            metering.Schema
	rates                             metering.Rates
	schemaDigest, ratesDigest         [32]byte
}
type Input struct {
	ChannelID, UpstreamModel, VersionID, Basis, VariantKey string
	Schema                                                 metering.Schema
	Rates                                                  metering.Rates
}

func NewQuote(in Input) (Quote, error) {
	sd, err := in.Schema.Digest()
	if err != nil || in.ChannelID == "" || in.UpstreamModel == "" || in.VariantKey != "default" {
		return Quote{}, ErrInvalidQuote
	}
	q := Quote{valid: true, channel: in.ChannelID, upstream: in.UpstreamModel, version: in.VersionID, basis: in.Basis, schema: in.Schema, schemaDigest: sd}
	raw, rateErr := in.Rates.CanonicalJSON()
	if in.Basis == "unknown" {
		if rateErr == nil {
			return Quote{}, ErrInvalidQuote
		}
		return q, nil
	}
	if in.VersionID == "" || (in.Basis != "contract" && in.Basis != "invoice") || rateErr != nil {
		return Quote{}, ErrInvalidQuote
	}
	rates, err := metering.ParseRates(in.Schema, metering.Supplier, raw)
	if err != nil {
		return Quote{}, ErrInvalidQuote
	}
	rd, _ := rates.Digest()
	inputDigest, _ := in.Rates.Digest()
	if rd != inputDigest {
		return Quote{}, ErrInvalidQuote
	}
	q.rates, q.ratesDigest = rates, rd
	return q, nil
}

// StoredInput is the exact SQL storage projection, separate from parsed inputs.
type StoredInput struct {
	ChannelID, UpstreamModel, VersionID, Basis, VariantKey     string
	SchemaCanonical, SchemaDigest, RatesCanonical, RatesDigest []byte
}

// ParseStoredQuote verifies canonical bytes as well as their schema-bound
// digests. A malformed known version must never become an unknown quote.
func ParseStoredQuote(in StoredInput) (Quote, error) {
	schema, err := metering.ParseSchema(in.SchemaCanonical)
	if err != nil {
		return Quote{}, ErrInvalidQuote
	}
	canonical, _ := schema.CanonicalJSON()
	digest, _ := schema.Digest()
	if !bytes.Equal(canonical, in.SchemaCanonical) || !bytes.Equal(digest[:], in.SchemaDigest) {
		return Quote{}, ErrInvalidQuote
	}
	parsed := Input{ChannelID: in.ChannelID, UpstreamModel: in.UpstreamModel, VersionID: in.VersionID, Basis: in.Basis, VariantKey: in.VariantKey, Schema: schema}
	if in.Basis == "unknown" {
		if in.RatesCanonical != nil || in.RatesDigest != nil {
			return Quote{}, ErrInvalidQuote
		}
	} else {
		parsed.Rates, err = metering.ParseRates(schema, metering.Supplier, in.RatesCanonical)
		if err != nil {
			return Quote{}, ErrInvalidQuote
		}
		canonical, _ = parsed.Rates.CanonicalJSON()
		digest, _ = parsed.Rates.Digest()
		if !bytes.Equal(canonical, in.RatesCanonical) || !bytes.Equal(digest[:], in.RatesDigest) {
			return Quote{}, ErrInvalidQuote
		}
	}
	return NewQuote(parsed)
}
func (q Quote) Valid() bool             { return q.valid }
func (q Quote) Known() bool             { return q.valid && q.basis != "unknown" }
func (q Quote) VersionID() string       { return q.version }
func (q Quote) Basis() string           { return q.basis }
func (q Quote) Schema() metering.Schema { return q.schema }
func (q Quote) Rates() (metering.Rates, error) {
	if !q.Valid() {
		return metering.Rates{}, ErrInvalidQuote
	}
	if !q.Known() {
		return metering.Rates{}, ErrUnknownQuote
	}
	return q.rates, nil
}
func (q Quote) Equal(other Quote) bool {
	return q.valid && other.valid && q.channel == other.channel && q.upstream == other.upstream && q.version == other.version && q.basis == other.basis && q.schemaDigest == other.schemaDigest && q.ratesDigest == other.ratesDigest
}
func (q Quote) Matches(channel, upstream string) bool {
	return q.valid && q.channel == channel && q.upstream == upstream
}

// A zero value means no optional unit-price caps. Nonempty sets are immutable
// and bound to the complete supplier schema, not a second money calculator.
type Limits struct {
	schemaDigest [32]byte
	entries      []metering.Rate
}

func NewLimits(schema metering.Schema, entries []metering.Rate) (Limits, error) {
	digest, err := schema.Digest()
	if err != nil {
		return Limits{}, ErrInvalidLimits
	}
	mask, _ := schema.Mask(metering.Supplier)
	dimensions, _ := schema.Dimensions()
	for i, e := range entries {
		if e.Numerator < 0 || e.Denominator <= 0 || e.Denominator > 1_000_000 || i > 0 && entries[i-1].Dimension >= e.Dimension {
			return Limits{}, ErrInvalidLimits
		}
		found := false
		for _, d := range dimensions {
			for _, name := range mask {
				if d.Dimension == name && e.Dimension == name && e.Unit == d.Unit {
					found = true
				}
			}
		}
		if !found || new(big.Int).GCD(nil, nil, big.NewInt(e.Numerator), big.NewInt(e.Denominator)).Cmp(big.NewInt(1)) != 0 {
			return Limits{}, ErrInvalidLimits
		}
	}
	if len(entries) == 0 {
		return Limits{}, nil
	}
	return Limits{digest, append([]metering.Rate(nil), entries...)}, nil
}
func ParseLimits(schema metering.Schema, raw []byte) (Limits, error) {
	value, err := strictjson.Parse(raw, 8192)
	if err != nil {
		return Limits{}, ErrInvalidLimits
	}
	array, ok := value.([]any)
	if !ok || len(array) > 8 {
		return Limits{}, ErrInvalidLimits
	}
	entries := make([]metering.Rate, len(array))
	for i, item := range array {
		row, ok := item.(map[string]any)
		if !ok || len(row) != 4 {
			return Limits{}, ErrInvalidLimits
		}
		d, dok := row["dimension"].(string)
		u, uok := row["unit"].(string)
		n, nok := row["numerator"].(string)
		den, denok := row["denominator"].(string)
		ni, ne := strconv.ParseInt(n, 10, 64)
		di, de := strconv.ParseInt(den, 10, 64)
		if !dok || !uok || !nok || !denok || ne != nil || de != nil || strconv.FormatInt(ni, 10) != n || strconv.FormatInt(di, 10) != den {
			return Limits{}, ErrInvalidLimits
		}
		entries[i] = metering.Rate{Dimension: d, Unit: u, Numerator: ni, Denominator: di}
	}
	return NewLimits(schema, entries)
}
func (l Limits) Empty() bool              { return len(l.entries) == 0 }
func (l Limits) Entries() []metering.Rate { return append([]metering.Rate(nil), l.entries...) }
func (l Limits) Equal(other Limits) bool {
	if l.schemaDigest != other.schemaDigest || len(l.entries) != len(other.entries) {
		return false
	}
	for i, e := range l.entries {
		if e != other.entries[i] {
			return false
		}
	}
	return true
}

// ValidFor checks binding only, not unknown-cost or financial admission policy.
func (l Limits) ValidFor(q Quote) bool {
	return q.Valid() && (l.Empty() || l.schemaDigest == q.schemaDigest)
}

// Exceeded compares exact unit-price rationals without calculating or rounding
// money. Business unknown/deny policy remains with the caller.
func (l Limits) Exceeded(q Quote) (bool, error) {
	if !q.Valid() {
		return false, ErrInvalidQuote
	}
	if !l.Empty() && l.schemaDigest != q.schemaDigest {
		return false, ErrInvalidLimits
	}
	if !q.Known() {
		return false, ErrUnknownQuote
	}
	rates, _ := q.rates.Entries()
	for _, cap := range l.entries {
		for _, rate := range rates {
			if cap.Dimension != rate.Dimension {
				continue
			}
			left := new(big.Int).Mul(big.NewInt(rate.Numerator), big.NewInt(cap.Denominator))
			right := new(big.Int).Mul(big.NewInt(cap.Numerator), big.NewInt(rate.Denominator))
			if left.Cmp(right) > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}
