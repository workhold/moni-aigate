package routecost_test

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/routecost"
)

const schemaID = "10000000-0000-4000-8000-000000000001"
const otherID = "10000000-0000-4000-8000-000000000002"

func schema(t testing.TB, id string, maximum int64) metering.Schema {
	t.Helper()
	raw := fmt.Sprintf(`{"schema_id":%q,"version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"%d"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"%d"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`, id, maximum, maximum)
	s, err := metering.ParseSchema([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func rates(t testing.TB, s metering.Schema, side metering.Side, n, d, out, od int64) metering.Rates {
	t.Helper()
	id, _ := s.ID()
	raw := fmt.Sprintf(`{"schema_id":%q,"version":"1","side":%q,"currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"%d","denominator":"%d"},{"dimension":"text_output_token","unit":"token","numerator":"%d","denominator":"%d"}]}`, id, side, n, d, out, od)
	r, err := metering.ParseRates(s, side, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func input(t testing.TB) routecost.Input {
	s := schema(t, schemaID, math.MaxInt64)
	return routecost.Input{ChannelID: "channel", UpstreamModel: "upstream", VersionID: "original-cost", Basis: "contract", VariantKey: "default", Schema: s, Rates: rates(t, s, metering.Supplier, 1, 3, 1, 5)}
}
func quote(t testing.TB, in routecost.Input) routecost.Quote {
	t.Helper()
	q, err := routecost.NewQuote(in)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
func stored(t testing.TB, in routecost.Input) routecost.StoredInput {
	t.Helper()
	raw, _ := in.Schema.CanonicalJSON()
	sd, _ := in.Schema.Digest()
	out := routecost.StoredInput{ChannelID: in.ChannelID, UpstreamModel: in.UpstreamModel, VersionID: in.VersionID, Basis: in.Basis, VariantKey: in.VariantKey, SchemaCanonical: raw, SchemaDigest: append([]byte(nil), sd[:]...)}
	if in.Basis != "unknown" {
		out.RatesCanonical, _ = in.Rates.CanonicalJSON()
		rd, _ := in.Rates.Digest()
		out.RatesDigest = append([]byte(nil), rd[:]...)
	}
	return out
}
func limit(t testing.TB, s metering.Schema, entries ...metering.Rate) routecost.Limits {
	t.Helper()
	l, err := routecost.NewLimits(s, entries)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func rate(n, d int64) metering.Rate {
	return metering.Rate{Dimension: "text_input_token", Unit: "token", Numerator: n, Denominator: d}
}

func TestQuoteKnownZeroUnknownAndInvalidAreDistinct(t *testing.T) {
	base := input(t)
	for _, kind := range []string{"contract", "invoice", "version_missing", "basis_unknown"} {
		t.Run(kind, func(t *testing.T) {
			in := base
			known := kind == "contract" || kind == "invoice"
			if known {
				in.Basis = kind
				in.Rates = rates(t, in.Schema, metering.Supplier, 0, 1, 0, 1)
			} else {
				in.Basis = "unknown"
				in.Rates = metering.Rates{}
				if kind == "version_missing" {
					in.VersionID = ""
				}
			}
			q := quote(t, in)
			if !q.Valid() || q.Known() != known || q.VersionID() != in.VersionID || q.Basis() != in.Basis || !q.Matches("channel", "upstream") || !q.Equal(q) {
				t.Fatal("quote union/domain lost")
			}
			parsed, err := routecost.ParseStoredQuote(stored(t, in))
			if err != nil || !parsed.Equal(q) {
				t.Fatal("storage roundtrip", err)
			}
			r, err := q.Rates()
			if !known {
				if !errors.Is(err, routecost.ErrUnknownQuote) {
					t.Fatal("unknown became numeric", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := metering.LegacyTokenBounds(in.Schema, metering.Supplier, 100, 20)
			n, err := metering.ReservationAmount(b, r)
			if err != nil || n != 0 {
				t.Fatal("known zero lost", n, err)
			}
		})
	}
	var zero routecost.Quote
	if zero.Valid() || zero.Known() || zero.Equal(zero) || zero.Matches("", "") {
		t.Fatal("zero quote accepted")
	}
	if _, err := zero.Rates(); !errors.Is(err, routecost.ErrInvalidQuote) {
		t.Fatal(err)
	}
	if _, err := zero.Schema().Digest(); err == nil {
		t.Fatal("zero schema escaped")
	}
	if _, err := (routecost.Limits{}).Exceeded(zero); !errors.Is(err, routecost.ErrInvalidQuote) {
		t.Fatal(err)
	}
}
func TestQuoteRejectsInvalidUnionAndFullSchemaSideMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*routecost.Input)
	}{
		{"schema", func(in *routecost.Input) { in.Schema = metering.Schema{} }},
		{"channel", func(in *routecost.Input) { in.ChannelID = "" }},
		{"upstream", func(in *routecost.Input) { in.UpstreamModel = "" }},
		{"variant_missing", func(in *routecost.Input) { in.VariantKey = "" }},
		{"variant_foreign", func(in *routecost.Input) { in.VariantKey = "other" }},
		{"known_missing_version", func(in *routecost.Input) { in.VersionID = "" }},
		{"basis", func(in *routecost.Input) { in.Basis = "estimated" }},
		{"known_missing_rates", func(in *routecost.Input) { in.Rates = metering.Rates{} }},
		{"unknown_has_rates", func(in *routecost.Input) { in.Basis = "unknown" }},
		{"retail_side", func(in *routecost.Input) { in.Rates = rates(t, in.Schema, metering.Retail, 1, 3, 1, 5) }},
		{"foreign_schema", func(in *routecost.Input) {
			in.Rates = rates(t, schema(t, otherID, math.MaxInt64), metering.Supplier, 1, 3, 1, 5)
		}},
		{"same_uuid_other_definition", func(in *routecost.Input) { in.Schema = schema(t, schemaID, 1000) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			tc.mutate(&in)
			q, err := routecost.NewQuote(in)
			if !errors.Is(err, routecost.ErrInvalidQuote) || q.Valid() {
				t.Fatal("invalid quote accepted", err)
			}
		})
	}
}
func TestQuoteEqualityBindsCompleteSelectionNotRoundedAmount(t *testing.T) {
	base := input(t)
	q := quote(t, base)
	for _, tc := range []struct {
		name   string
		mutate func(*routecost.Input)
	}{
		{"channel", func(in *routecost.Input) { in.ChannelID = "other" }},
		{"case_sensitive_upstream", func(in *routecost.Input) { in.UpstreamModel = "UPSTREAM" }},
		{"version", func(in *routecost.Input) { in.VersionID = "other" }},
		{"basis", func(in *routecost.Input) { in.Basis = "invoice" }},
		{"one_rate", func(in *routecost.Input) { in.Rates = rates(t, in.Schema, metering.Supplier, 1, 3, 1, 4) }},
		{"same_uuid_other_definition", func(in *routecost.Input) {
			in.Schema = schema(t, schemaID, 1000)
			in.Rates = rates(t, in.Schema, metering.Supplier, 1, 3, 1, 5)
		}},
		{"other_schema_uuid", func(in *routecost.Input) {
			in.Schema = schema(t, otherID, math.MaxInt64)
			in.Rates = rates(t, in.Schema, metering.Supplier, 1, 3, 1, 5)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			other := quote(t, in)
			if q.Equal(other) || other.Equal(q) {
				t.Fatal("incomplete quote equality")
			}
		})
	}
	missing := base
	missing.Basis = "unknown"
	missing.VersionID = ""
	missing.Rates = metering.Rates{}
	explicit := missing
	explicit.VersionID = "unknown-version"
	if quote(t, missing).Equal(quote(t, explicit)) {
		t.Fatal("missing and explicit unknown collapsed")
	}
}
func TestStoredQuoteRejectsCorruptionRatherThanDowngrading(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*routecost.StoredInput)
	}{
		{"schema_nil", func(in *routecost.StoredInput) { in.SchemaCanonical = nil }},
		{"schema_noncanonical", func(in *routecost.StoredInput) { in.SchemaCanonical = append([]byte(" "), in.SchemaCanonical...) }},
		{"schema_trailing", func(in *routecost.StoredInput) { in.SchemaCanonical = append(in.SchemaCanonical, []byte("{}")...) }},
		{"schema_digest_nil", func(in *routecost.StoredInput) { in.SchemaDigest = nil }},
		{"schema_digest_length", func(in *routecost.StoredInput) { in.SchemaDigest = in.SchemaDigest[:31] }},
		{"schema_digest_wrong", func(in *routecost.StoredInput) { in.SchemaDigest[0] ^= 1 }},
		{"rates_nil", func(in *routecost.StoredInput) { in.RatesCanonical = nil }},
		{"rates_empty", func(in *routecost.StoredInput) { in.RatesCanonical = []byte{} }},
		{"rates_noncanonical", func(in *routecost.StoredInput) { in.RatesCanonical = append(in.RatesCanonical, ' ') }},
		{"rates_duplicate", func(in *routecost.StoredInput) {
			in.RatesCanonical = bytes.Replace(in.RatesCanonical, []byte(`"currency":"CNY"`), []byte(`"currency":"CNY","currency":"CNY"`), 1)
		}},
		{"rates_retaiI", func(in *routecost.StoredInput) {
			in.RatesCanonical = bytes.Replace(in.RatesCanonical, []byte(`"side":"supplier"`), []byte(`"side":"retail"`), 1)
		}},
		{"rates_wrong_schema", func(in *routecost.StoredInput) {
			in.RatesCanonical = bytes.ReplaceAll(in.RatesCanonical, []byte(schemaID), []byte(otherID))
		}},
		{"rates_wrong_unit", func(in *routecost.StoredInput) {
			in.RatesCanonical = bytes.Replace(in.RatesCanonical, []byte(`"unit":"token"`), []byte(`"unit":"byte"`), 1)
		}},
		{"rates_unreduced", func(in *routecost.StoredInput) {
			in.RatesCanonical = bytes.Replace(in.RatesCanonical, []byte(`"numerator":"1","denominator":"3"`), []byte(`"numerator":"2","denominator":"6"`), 1)
		}},
		{"rates_partial", func(in *routecost.StoredInput) {
			in.RatesCanonical = bytes.Replace(in.RatesCanonical, []byte(`,{"dimension":"text_output_token","unit":"token","numerator":"1","denominator":"5"}`), nil, 1)
		}},
		{"rates_digest_nil", func(in *routecost.StoredInput) { in.RatesDigest = nil }},
		{"rates_digest_length", func(in *routecost.StoredInput) { in.RatesDigest = append(in.RatesDigest, 0) }},
		{"rates_digest_wrong", func(in *routecost.StoredInput) { in.RatesDigest[31] ^= 1 }},
		{"known_no_version", func(in *routecost.StoredInput) { in.VersionID = "" }},
		{"unknown_nonnull_rates", func(in *routecost.StoredInput) { in.Basis = "unknown" }},
		{"unknown_empty_rates", func(in *routecost.StoredInput) {
			in.Basis = "unknown"
			in.RatesCanonical = []byte{}
			in.RatesDigest = nil
		}},
		{"unknown_empty_digest", func(in *routecost.StoredInput) {
			in.Basis = "unknown"
			in.RatesCanonical = nil
			in.RatesDigest = []byte{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := stored(t, input(t))
			tc.mutate(&in)
			q, err := routecost.ParseStoredQuote(in)
			if !errors.Is(err, routecost.ErrInvalidQuote) || q.Valid() {
				t.Fatal("corrupt storage accepted", err)
			}
		})
	}
}
func TestLimitsExactFractionsAndUnknown(t *testing.T) {
	base := input(t)
	for _, tc := range []struct {
		name          string
		n, d, cap, cd int64
		exceeded      bool
	}{
		{"equal", 1, 3, 1, 3, false},
		{"below", 1, 3, 2, 5, false},
		{"above", 2, 5, 1, 3, true},
		{"near_one", 1000001, 1000000, 1, 1, true},
		{"known_zero_at_zero", 0, 1, 0, 1, false},
		{"positive_at_zero", 1, 1000000, 0, 1, true},
		{"overflow_safe_below", math.MaxInt64, 999983, math.MaxInt64, 999979, false},
		{"overflow_safe_above", math.MaxInt64, 999979, math.MaxInt64, 999983, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.Rates = rates(t, in.Schema, metering.Supplier, tc.n, tc.d, 0, 1)
			l := limit(t, in.Schema, rate(tc.cap, tc.cd))
			got, err := l.Exceeded(quote(t, in))
			if err != nil || got != tc.exceeded {
				t.Fatal(got, err)
			}
		})
	}
	zero := limit(t, base.Schema, rate(0, 1))
	none := limit(t, base.Schema)
	if zero.Empty() || !none.Empty() || zero.Equal(none) || !none.Equal(routecost.Limits{}) {
		t.Fatal("missing cap became zero")
	}
	if zero.Equal(limit(t, base.Schema, rate(1, 1))) || zero.Equal(limit(t, schema(t, otherID, math.MaxInt64), rate(0, 1))) {
		t.Fatal("cap equality lost rate or schema binding")
	}
	if exceeded, err := none.Exceeded(quote(t, base)); err != nil || exceeded {
		t.Fatal("no cap incorrectly limited", err)
	}
	unknown := base
	unknown.Basis = "unknown"
	unknown.Rates = metering.Rates{}
	for _, l := range []routecost.Limits{zero, none} {
		if _, err := l.Exceeded(quote(t, unknown)); !errors.Is(err, routecost.ErrUnknownQuote) {
			t.Fatal("unknown accepted", err)
		}
	}
	for _, s := range []metering.Schema{schema(t, otherID, math.MaxInt64), schema(t, schemaID, 1000)} {
		l := limit(t, s, rate(0, 1))
		if _, err := l.Exceeded(quote(t, base)); !errors.Is(err, routecost.ErrInvalidLimits) {
			t.Fatal("cap applied to other schema", err)
		}
	}
}
func TestLimitsValidateCanonicalUnitDimensionAndFraction(t *testing.T) {
	s := input(t).Schema
	for _, tc := range []struct {
		name    string
		entries []metering.Rate
	}{
		{"negative", []metering.Rate{rate(-1, 1)}},
		{"zero_denominator", []metering.Rate{rate(1, 0)}},
		{"negative_denominator", []metering.Rate{rate(1, -1)}},
		{"too_large_denominator", []metering.Rate{rate(1, 1000001)}},
		{"unreduced", []metering.Rate{rate(2, 6)}},
		{"noncanonical_zero", []metering.Rate{rate(0, 2)}},
		{"duplicate", []metering.Rate{rate(1, 3), rate(1, 3)}},
		{"order", []metering.Rate{{Dimension: "text_output_token", Unit: "token", Numerator: 1, Denominator: 1}, rate(1, 3)}},
		{"unknown", []metering.Rate{{Dimension: "request_count", Unit: "request", Numerator: 1, Denominator: 1}}},
		{"wrong_unit", []metering.Rate{{Dimension: "text_input_token", Unit: "byte", Numerator: 1, Denominator: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := routecost.NewLimits(s, tc.entries); !errors.Is(err, routecost.ErrInvalidLimits) {
				t.Fatal("invalid caps", err)
			}
		})
	}
	if _, err := routecost.NewLimits(metering.Schema{}, nil); !errors.Is(err, routecost.ErrInvalidLimits) {
		t.Fatal(err)
	}
	// A schema dimension outside supplier mask is not a supplier unit-price cap.
	raw, _ := s.CanonicalJSON()
	raw = bytes.Replace(raw, []byte(`"supplier_dimensions":["text_input_token","text_output_token"]`), []byte(`"supplier_dimensions":["text_output_token"]`), 1)
	reduced, err := metering.ParseSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = routecost.NewLimits(reduced, []metering.Rate{rate(1, 3)}); !errors.Is(err, routecost.ErrInvalidLimits) {
		t.Fatal("retail-only cap", err)
	}
	output := metering.Rate{Dimension: "text_output_token", Unit: "token", Numerator: 1, Denominator: 5}
	l := limit(t, s, output)
	if got, err := l.Exceeded(quote(t, input(t))); err != nil || got {
		t.Fatal("partial output cap", got, err)
	}
}
func TestLimitsValidForRejectsZeroAndSameUUIDDifferentSchema(t *testing.T) {
	in := input(t)
	q := quote(t, in)
	l := limit(t, in.Schema, rate(1, 1))
	unknown := in
	unknown.Basis, unknown.Rates = "unknown", metering.Rates{}
	other := in
	other.Schema = schema(t, "10000000-0000-4000-8000-000000000001", 99)
	other.Rates = rates(t, other.Schema, metering.Supplier, 1, 1, 1, 1)
	if !(routecost.Limits{}).ValidFor(q) || !l.ValidFor(q) || !l.ValidFor(quote(t, unknown)) || l.ValidFor(routecost.Quote{}) || (routecost.Limits{}).ValidFor(routecost.Quote{}) || l.ValidFor(quote(t, other)) {
		t.Fatal("limits binding accepted invalid/cross-schema quote or rejected legitimate unknown")
	}
}

func TestLimitsStrictJSON(t *testing.T) {
	s := input(t).Schema
	good := `[{"dimension":"text_input_token","unit":"token","numerator":"1","denominator":"3"}]`
	for _, raw := range []string{"[]", " \n" + good + "\t"} {
		if _, err := routecost.ParseLimits(s, []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		"", "null", "{}", good + "[]", "[" + good + "]", strings.Repeat(" ", 8193),
		strings.Replace(good, `"numerator":"1"`, `"numerator":1`, 1),
		strings.Replace(good, `"numerator":"1"`, `"numerator":null`, 1),
		strings.Replace(good, `"numerator":"1"`, `"numerator":"01"`, 1),
		strings.Replace(good, `"numerator":"1"`, `"numerator":"+1"`, 1),
		strings.Replace(good, `"numerator":"1"`, `"numerator":"-0"`, 1),
		strings.Replace(good, `"numerator":"1"`, `"numerator":"1.0"`, 1),
		strings.Replace(good, `"numerator":"1"`, `"numerator":"1e0"`, 1),
		strings.Replace(good, `"numerator":"1"`, `"numerator":"9223372036854775808"`, 1),
		strings.Replace(good, `"numerator":"1"`, `"numerator":"-1"`, 1),
		strings.Replace(good, `"denominator":"3"`, `"denominator":"0"`, 1),
		strings.Replace(good, `"denominator":"3"`, `"denominator":"1000001"`, 1),
		strings.Replace(good, `"denominator":"3"`, `"denominator":"03"`, 1),
		strings.Replace(good, `"denominator":"3"`, `"denominator":"3","extra":true`, 1),
		strings.Replace(good, `"denominator":"3"`, `"denominator":"3","unit":"token"`, 1),
		strings.Replace(good, `"unit":"token",`, "", 1),
		strings.Replace(good, "text_input_token", "TEXT_INPUT_TOKEN", 1),
		strings.Replace(good, "token\",\"numerator", "byte\",\"numerator", 1),
		string([]byte{0xff}),
	} {
		if _, err := routecost.ParseLimits(s, []byte(raw)); !errors.Is(err, routecost.ErrInvalidLimits) {
			t.Fatalf("accepted %q: %v", raw, err)
		}
	}
}
func TestImmutableCopiesAndConcurrentReads(t *testing.T) {
	in := input(t)
	original := quote(t, in)
	raw := stored(t, in)
	q, err := routecost.ParseStoredQuote(raw)
	if err != nil {
		t.Fatal(err)
	}
	entries := []metering.Rate{rate(1, 3)}
	l := limit(t, in.Schema, entries...)
	entries[0].Numerator = 999
	raw.SchemaCanonical[0] ^= 1
	raw.SchemaDigest[0] ^= 1
	raw.RatesCanonical[0] ^= 1
	raw.RatesDigest[0] ^= 1
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r, _ := q.Rates()
				data, _ := r.CanonicalJSON()
				data[0] ^= 1
				got, _ := r.Entries()
				got[0].Numerator = 999
				s := q.Schema()
				canonical, _ := s.CanonicalJSON()
				canonical[0] ^= 1
				dims, _ := s.Dimensions()
				dims[0].MaximumQuantity = 0
				caps := l.Entries()
				caps[0].Numerator = 0
				if !q.Equal(original) || !reflect.DeepEqual(l.Entries(), []metering.Rate{rate(1, 3)}) {
					t.Error("mutable data escaped")
				}
				if exceeded, err := l.Exceeded(q); err != nil || exceeded {
					t.Error("concurrent change", err)
				}
			}
		}()
	}
	wg.Wait()
}
func TestExactCostAndSingleCeilRemainInMetering(t *testing.T) {
	in := input(t)
	in.Rates = rates(t, in.Schema, metering.Supplier, 1, 1000000, 0, 1)
	low := quote(t, in)
	in.Rates = rates(t, in.Schema, metering.Supplier, 1, 500000, 0, 1)
	high := quote(t, in)
	bounds, _ := metering.LegacyTokenBounds(in.Schema, metering.Supplier, 1, 0)
	lr, _ := low.Rates()
	hr, _ := high.Rates()
	ln, le := metering.ReservationAmount(bounds, lr)
	hn, he := metering.ReservationAmount(bounds, hr)
	if le != nil || he != nil || ln != 1 || hn != 1 {
		t.Fatal("same-ceil setup", ln, hn, le, he)
	}
	comparison, err := metering.CompareExactCost(bounds, lr, bounds, hr)
	if err != nil || comparison != -1 || low.Equal(high) {
		t.Fatal("rounded fee used as quote equality or order", comparison, err)
	}
	// Both dimensions are accumulated before the only ceil.
	in.Rates = rates(t, in.Schema, metering.Supplier, 1, 3, 1, 5)
	q := quote(t, in)
	r, _ := q.Rates()
	b, _ := metering.LegacyTokenBounds(in.Schema, metering.Supplier, 1, 1)
	if n, err := metering.ReservationAmount(b, r); err != nil || n != 1 {
		t.Fatal("per-dimension ceil introduced", n, err)
	}
	huge, _ := metering.LegacyTokenBounds(in.Schema, metering.Supplier, math.MaxInt64, math.MaxInt64)
	in.Rates = rates(t, in.Schema, metering.Supplier, math.MaxInt64, 1, math.MaxInt64, 1)
	large := quote(t, in)
	r, _ = large.Rates()
	if _, err := metering.ReservationAmount(huge, r); !errors.Is(err, metering.ErrAmountOverflow) {
		t.Fatal("reservation overflow lost", err)
	}
	if cmp, err := metering.CompareExactCost(huge, r, huge, r); err != nil || cmp != 0 {
		t.Fatal("exact large comparison overflow", cmp, err)
	}
	foreign, _ := metering.LegacyTokenBounds(schema(t, otherID, math.MaxInt64), metering.Supplier, 1, 1)
	if _, err := metering.CompareExactCost(foreign, lr, bounds, hr); !errors.Is(err, metering.ErrBindingMismatch) {
		t.Fatal("cross-schema comparison", err)
	}
}
func FuzzStoredQuoteAndLimits(f *testing.F) {
	in := input(f)
	original := stored(f, in)
	f.Add(original.RatesCanonical, []byte("[]"))
	f.Add([]byte("{}"), []byte(`[{"dimension":"text_input_token","unit":"token","numerator":"0","denominator":"1"}]`))
	f.Fuzz(func(t *testing.T, ratesRaw, capsRaw []byte) {
		raw := original
		raw.RatesCanonical = ratesRaw
		q, err := routecost.ParseStoredQuote(raw)
		if err == nil {
			if !q.Known() || !q.Equal(quote(t, in)) {
				t.Fatal("accepted drifted original canonical")
			}
			r, _ := q.Rates()
			got, _ := r.CanonicalJSON()
			if !bytes.Equal(got, ratesRaw) {
				t.Fatal("noncanonical rates accepted")
			}
		}
		l, err := routecost.ParseLimits(in.Schema, capsRaw)
		if err == nil {
			reconstructed := limit(t, in.Schema, l.Entries()...)
			if !l.Equal(reconstructed) {
				t.Fatal("limits unstable")
			}
		}
	})
}
