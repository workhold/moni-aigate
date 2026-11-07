package metering_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

const (
	schemaID        = "11111111-1111-1111-1111-111111111111"
	tokenSchemaJSON = `{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"100"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"20"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`
)

func mustSchema(t testing.TB, raw string) metering.Schema {
	t.Helper()
	s, err := metering.ParseSchema([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func mustRates(t testing.TB, s metering.Schema, side metering.Side, numerators, denominators [2]string) metering.Rates {
	t.Helper()
	raw := fmt.Sprintf(`{"schema_id":%q,"version":"1","side":%q,"currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":%q,"denominator":%q},{"dimension":"text_output_token","unit":"token","numerator":%q,"denominator":%q}]}`, schemaID, side, numerators[0], denominators[0], numerators[1], denominators[1])
	r, err := metering.ParseRates(s, side, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func mustBounds(t testing.TB, s metering.Schema, side metering.Side, input, output int64) metering.Bounds {
	t.Helper()
	b, err := metering.LegacyTokenBounds(s, side, input, output)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func mustQuantities(t testing.TB, s metering.Schema, side metering.Side, input, output int64) metering.Quantities {
	t.Helper()
	q, err := metering.LegacyTokenQuantities(s, side, input, output)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
func quantityJSON(field string, side metering.Side, input, output string) string {
	return fmt.Sprintf(`{"schema_id":%q,"version":"1","side":%q,%q:[{"dimension":"text_input_token","unit":"token","quantity":%q},{"dimension":"text_output_token","unit":"token","quantity":%q}]}`, schemaID, side, field, input, output)
}
func wantError(t testing.TB, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || err.Error() != want.Error() {
		t.Fatalf("error = %v; want controlled %v", err, want)
	}
}

func TestStrictSchemaAndJSON(t *testing.T) {
	cases := []struct {
		name, raw string
		want      error
	}{
		{"empty", "", metering.ErrInvalidJSON},
		{"BOM", "\xef\xbb\xbf" + tokenSchemaJSON, metering.ErrInvalidJSON},
		{"invalid UTF8", "\xff" + tokenSchemaJSON, metering.ErrInvalidJSON},
		{"trailing object", tokenSchemaJSON + `{}`, metering.ErrInvalidJSON},
		{"deep", `{"a":` + strings.Repeat(`[`, 9) + `0` + strings.Repeat(`]`, 9) + `}`, metering.ErrInvalidJSON},
		{"duplicate escaped", strings.Replace(tokenSchemaJSON, `"version":"1"`, `"version":"1","\u0076ersion":"1"`, 1), metering.ErrInvalidJSON},
		{"duplicate nested", strings.Replace(tokenSchemaJSON, `"unit":"token"`, `"unit":"token","unit":"token"`, 1), metering.ErrInvalidJSON},
		{"bad high surrogate", strings.Replace(tokenSchemaJSON, `"token"`, `"\ud800"`, 1), metering.ErrInvalidJSON},
		{"bad low surrogate", strings.Replace(tokenSchemaJSON, `"token"`, `"\udc00"`, 1), metering.ErrInvalidJSON},
		{"bad surrogate pair", strings.Replace(tokenSchemaJSON, `"token"`, `"\ud800\u0041"`, 1), metering.ErrInvalidJSON},
		{"valid nonregistry pair", strings.Replace(tokenSchemaJSON, `"token"`, `"\ud800\udc00"`, 1), metering.ErrInvalidSchema},
		{"top null", `null`, metering.ErrInvalidSchema},
		{"top array", `[]`, metering.ErrInvalidSchema},
		{"number version", strings.Replace(tokenSchemaJSON, `"version":"1"`, `"version":1`, 1), metering.ErrInvalidSchema},
		{"nil UUID", strings.ReplaceAll(tokenSchemaJSON, schemaID, "00000000-0000-0000-0000-000000000000"), metering.ErrInvalidSchema},
		{"uppercase UUID", strings.ReplaceAll(tokenSchemaJSON, schemaID, "aBCdef11-1111-1111-1111-111111111111"), metering.ErrInvalidSchema},
		{"extra schema field", strings.Replace(tokenSchemaJSON, `"version":"1"`, `"version":"1","diagnostics":[]`, 1), metering.ErrInvalidSchema},
		{"unknown registry", strings.ReplaceAll(tokenSchemaJSON, "text_input_token", "video_output_frame"), metering.ErrInvalidSchema},
		{"unit mismatch", strings.Replace(tokenSchemaJSON, `"unit":"token"`, `"unit":"byte"`, 1), metering.ErrInvalidSchema},
		{"leading zero", strings.Replace(tokenSchemaJSON, `"100"`, `"0100"`, 1), metering.ErrInvalidSchema},
		{"maximum number", strings.Replace(tokenSchemaJSON, `"100"`, `100`, 1), metering.ErrInvalidSchema},
		{"maximum overflow", strings.Replace(tokenSchemaJSON, `"100"`, `"9223372036854775808"`, 1), metering.ErrInvalidSchema},
		{"missing mask", strings.Replace(tokenSchemaJSON, `"supplier_dimensions":["text_input_token","text_output_token"]`, `"supplier_dimensions":null`, 1), metering.ErrInvalidSchema},
		{"mask duplicate", strings.Replace(tokenSchemaJSON, `"retail_dimensions":["text_input_token","text_output_token"]`, `"retail_dimensions":["text_input_token","text_input_token"]`, 1), metering.ErrInvalidSchema},
		{"mask unordered", strings.Replace(tokenSchemaJSON, `"retail_dimensions":["text_input_token","text_output_token"]`, `"retail_dimensions":["text_output_token","text_input_token"]`, 1), metering.ErrInvalidSchema},
		{"uncovered dimension", strings.Replace(strings.Replace(tokenSchemaJSON, `"retail_dimensions":["text_input_token","text_output_token"]`, `"retail_dimensions":["text_input_token"]`, 1), `"supplier_dimensions":["text_input_token","text_output_token"]`, `"supplier_dimensions":["text_input_token"]`, 1), metering.ErrInvalidSchema},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := metering.ParseSchema([]byte(tc.raw))
			wantError(t, err, tc.want)
			if raw, err := s.CanonicalJSON(); raw != nil || !errors.Is(err, metering.ErrInvalidValue) {
				t.Fatal("failed parser exposed a partial schema")
			}
		})
	}
	for _, n := range []int{16_384, 16_385} {
		raw := tokenSchemaJSON + strings.Repeat(" ", n-len(tokenSchemaJSON))
		_, err := metering.ParseSchema([]byte(raw))
		if n == 16_384 && err != nil {
			t.Fatal("exact schema byte limit rejected", err)
		}
		if n == 16_385 {
			wantError(t, err, metering.ErrTooLarge)
		}
	}
	// Identity does not invent RFC version/variant restrictions.
	if _, err := metering.ParseSchema([]byte(strings.ReplaceAll(tokenSchemaJSON, schemaID, "00000000-0000-0000-0000-000000000001"))); err != nil {
		t.Fatal("valid nonnil UUID rejected", err)
	}
}

func TestAllRegistryAndDifferentSideMasks(t *testing.T) {
	names := []string{"audio_input_millisecond", "audio_output_millisecond", "image_output_count", "request_count", "text_input_token", "text_input_unicode_scalar", "text_input_utf8_byte", "text_output_token"}
	units := []string{"millisecond", "millisecond", "image", "request", "token", "unicode_scalar", "byte", "token"}
	dimensions, quantities, rates := []any{}, []any{}, []any{}
	for i, name := range names {
		dimensions = append(dimensions, map[string]any{"dimension": name, "unit": units[i], "maximum_quantity": "9223372036854775807"})
		quantities = append(quantities, map[string]any{"dimension": name, "unit": units[i], "quantity": "1"})
		rates = append(rates, map[string]any{"dimension": name, "unit": units[i], "numerator": "1", "denominator": "1"})
	}
	schema := map[string]any{"schema_id": schemaID, "version": "1", "dimensions": dimensions, "retail_dimensions": names[:4], "supplier_dimensions": names[4:]}
	sraw, _ := json.Marshal(schema)
	s, err := metering.ParseSchema(sraw)
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
		start, end := 0, 4
		if side == metering.Supplier {
			start, end = 4, 8
		}
		qraw, _ := json.Marshal(map[string]any{"schema_id": schemaID, "version": "1", "side": side, "quantities": quantities[start:end]})
		rraw, _ := json.Marshal(map[string]any{"schema_id": schemaID, "version": "1", "side": side, "currency": "CNY", "rates": rates[start:end]})
		q, err := metering.ParseQuantities(s, side, qraw)
		if err != nil {
			t.Fatal(err)
		}
		r, err := metering.ParseRates(s, side, rraw)
		if err != nil {
			t.Fatal(err)
		}
		amount, err := metering.Amount(q, r)
		if err != nil || amount.String() != "4" {
			t.Fatal("different masks failed", amount, err)
		}
		_, err = metering.LegacyTokenRates(s, side, 1, 1)
		wantError(t, err, metering.ErrInvalidSchema)
	}
	// The registry contains exactly 8 names; a ninth repeated dimension is invalid.
	dimensions = append(dimensions, dimensions[len(dimensions)-1])
	schema["dimensions"] = dimensions
	raw, _ := json.Marshal(schema)
	_, err = metering.ParseSchema(raw)
	wantError(t, err, metering.ErrInvalidSchema)
}

func TestVectorStrictValuesAndErrorPrecedence(t *testing.T) {
	s := mustSchema(t, tokenSchemaJSON)
	valid := quantityJSON("bounds", metering.Retail, "100", "20")
	cases := []struct {
		name, raw string
		want      error
	}{
		{"quantity leading zero", strings.Replace(valid, `"100"`, `"0100"`, 1), metering.ErrInvalidVector},
		{"quantity negative", strings.Replace(valid, `"100"`, `"-1"`, 1), metering.ErrInvalidVector},
		{"quantity plus", strings.Replace(valid, `"100"`, `"+1"`, 1), metering.ErrInvalidVector},
		{"quantity exponential", strings.Replace(valid, `"100"`, `"1e2"`, 1), metering.ErrInvalidVector},
		{"quantity null", strings.Replace(valid, `"100"`, `null`, 1), metering.ErrInvalidVector},
		{"quantity number", strings.Replace(valid, `"100"`, `100`, 1), metering.ErrInvalidVector},
		{"quantity overflow", strings.Replace(valid, `"100"`, `"9223372036854775808"`, 1), metering.ErrInvalidVector},
		{"global maximum", strings.Replace(valid, `"100"`, `"101"`, 1), metering.ErrInvalidVector},
		{"missing dimension", strings.Replace(valid, `,{"dimension":"text_output_token","unit":"token","quantity":"20"}`, ``, 1), metering.ErrInvalidVector},
		{"wrong field type", strings.Replace(valid, `"bounds":[`, `"bounds":{`, 1), metering.ErrInvalidJSON},
		{"unit mismatch", strings.Replace(valid, `"unit":"token"`, `"unit":"byte"`, 1), metering.ErrInvalidVector},
		{"version null", strings.Replace(valid, `"version":"1"`, `"version":null`, 1), metering.ErrInvalidVector},
		{"unknown side", strings.Replace(valid, `"retail"`, `"unknown"`, 1), metering.ErrInvalidVector},
		{"other side", strings.Replace(valid, `"retail"`, `"supplier"`, 1), metering.ErrBindingMismatch},
		{"other schema", strings.ReplaceAll(valid, schemaID, "22222222-2222-2222-2222-222222222222"), metering.ErrBindingMismatch},
		{"extra diagnostic", strings.Replace(valid, `"version":"1"`, `"version":"1","diagnostics":[]`, 1), metering.ErrInvalidVector},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := metering.ParseBounds(s, metering.Retail, []byte(tc.raw))
			wantError(t, err, tc.want)
			if d, err := b.Digest(); d != [32]byte{} || !errors.Is(err, metering.ErrInvalidValue) {
				t.Fatal("partial bound escaped")
			}
		})
	}
	for _, n := range []int{8192, 8193} {
		_, err := metering.ParseBounds(s, metering.Retail, []byte(valid+strings.Repeat(" ", n-len(valid))))
		if n == 8192 && err != nil {
			t.Fatal("exact vector size rejected", err)
		}
		if n == 8193 {
			wantError(t, err, metering.ErrTooLarge)
		}
	}
	_, err := metering.ParseBounds(metering.Schema{}, "wrong", bytes.Repeat([]byte("x"), 8193))
	wantError(t, err, metering.ErrTooLarge)
	_, err = metering.ParseBounds(metering.Schema{}, "wrong", []byte(`{`))
	wantError(t, err, metering.ErrInvalidJSON)
	_, err = metering.ParseBounds(metering.Schema{}, "wrong", []byte(valid))
	wantError(t, err, metering.ErrInvalidValue)
	_, err = metering.ParseBounds(s, "wrong", []byte(valid))
	wantError(t, err, metering.ErrSide)
	// Invalid decimal is a shape/value failure even when wire binding differs.
	invalid := strings.ReplaceAll(strings.Replace(valid, `"100"`, `"01"`, 1), schemaID, "22222222-2222-2222-2222-222222222222")
	_, err = metering.ParseBounds(s, metering.Retail, []byte(invalid))
	wantError(t, err, metering.ErrInvalidVector)
	// Set/upper validation occurs after an otherwise valid identity mismatch.
	invalid = strings.ReplaceAll(strings.Replace(valid, `"100"`, `"101"`, 1), schemaID, "22222222-2222-2222-2222-222222222222")
	_, err = metering.ParseBounds(s, metering.Retail, []byte(invalid))
	wantError(t, err, metering.ErrBindingMismatch)
	// Actuals can preserve quantities beyond a schema bound; bounds cannot be
	// parsed with the quantity field and vice versa.
	q, err := metering.ParseQuantities(s, metering.Retail, []byte(quantityJSON("quantities", metering.Retail, "101", "21")))
	if err != nil {
		t.Fatal(err)
	}
	inside, err := metering.WithinBounds(q, mustBounds(t, s, metering.Retail, 100, 20))
	if inside || err != nil {
		t.Fatal("actual excess not preserved", inside, err)
	}
	_, err = metering.ParseQuantities(s, metering.Retail, []byte(valid))
	wantError(t, err, metering.ErrInvalidVector)
}

func TestCanonicalRationalRates(t *testing.T) {
	s := mustSchema(t, tokenSchemaJSON)
	for _, pair := range [][2]string{{"0", "2"}, {"2", "4"}, {"1", "0"}, {"1", "1000001"}, {"01", "1"}, {"-1", "1"}, {"9223372036854775808", "1"}} {
		raw := fmt.Sprintf(`{"schema_id":%q,"version":"1","side":"retail","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":%q,"denominator":%q},{"dimension":"text_output_token","unit":"token","numerator":"0","denominator":"1"}]}`, schemaID, pair[0], pair[1])
		_, err := metering.ParseRates(s, metering.Retail, []byte(raw))
		wantError(t, err, metering.ErrInvalidVector)
	}
	for _, pair := range [][2]string{{"0", "1"}, {"1", "1"}, {"1", "1000000"}, {"9223372036854775807", "1"}} {
		_ = mustRates(t, s, metering.Retail, [2]string{pair[0], "0"}, [2]string{pair[1], "1"})
	}
}

func TestExactAmountReservationAndComparison(t *testing.T) {
	s := mustSchema(t, tokenSchemaJSON)
	q := mustQuantities(t, s, metering.Supplier, 1, 1)
	b := mustBounds(t, s, metering.Supplier, 1, 1)
	for _, tc := range []struct {
		n, d   [2]string
		amount string
	}{
		{[2]string{"1", "1"}, [2]string{"2", "2"}, "1"},
		{[2]string{"1", "1"}, [2]string{"3", "6"}, "1"},
		{[2]string{"0", "0"}, [2]string{"1", "1"}, "0"},
		{[2]string{"9007199254740993", "0"}, [2]string{"1", "1"}, "9007199254740993"},
	} {
		r := mustRates(t, s, metering.Supplier, tc.n, tc.d)
		amount, err := metering.Amount(q, r)
		if err != nil || amount.String() != tc.amount {
			t.Fatalf("amount %v %v; want %s", amount, err, tc.amount)
		}
		reserved, err := metering.ReservationAmount(b, r)
		if err != nil || fmt.Sprint(reserved) != tc.amount {
			t.Fatal("reservation differs", reserved, err)
		}
	}
	third := mustRates(t, s, metering.Supplier, [2]string{"1", "0"}, [2]string{"3", "1"})
	half := mustRates(t, s, metering.Supplier, [2]string{"1", "0"}, [2]string{"2", "1"})
	for _, tc := range []struct {
		l, r metering.Rates
		want int
	}{{third, half, -1}, {half, third, 1}, {half, half, 0}} {
		got, err := metering.CompareExactCost(b, tc.l, b, tc.r)
		if err != nil || got != tc.want {
			t.Fatal("unrounded comparison", got, err)
		}
	}
	maxSchema := mustSchema(t, strings.ReplaceAll(strings.Replace(tokenSchemaJSON, `"100"`, `"9223372036854775807"`, 1), `"20"`, `"9223372036854775807"`))
	maxQ := mustQuantities(t, maxSchema, metering.Retail, math.MaxInt64, math.MaxInt64)
	maxB := mustBounds(t, maxSchema, metering.Retail, math.MaxInt64, math.MaxInt64)
	maxR := mustRates(t, maxSchema, metering.Retail, [2]string{"9223372036854775807", "9223372036854775807"}, [2]string{"1", "1"})
	got, err := metering.Amount(maxQ, maxR)
	want := new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(math.MaxInt64))
	want.Mul(want, big.NewInt(2))
	if err != nil || got.Cmp(want) != 0 {
		t.Fatal("large actual cost lost", got, err)
	}
	value, err := metering.ReservationAmount(maxB, maxR)
	if value != 0 {
		t.Fatal("reservation saturated", value)
	}
	wantError(t, err, metering.ErrAmountOverflow)
	// A caller can mutate its result without corrupting the immutable inputs.
	got.SetInt64(0)
	again, err := metering.Amount(maxQ, maxR)
	if err != nil || again.Cmp(want) != 0 {
		t.Fatal("amount aliases internal integer")
	}
}

func TestQuoteDigestAndBoundsFailClosedAcrossBindings(t *testing.T) {
	s := mustSchema(t, tokenSchemaJSON)
	bounds := mustBounds(t, s, metering.Supplier, 100, 20)
	actual, err := metering.ParseQuantities(s, metering.Supplier, []byte(quantityJSON("quantities", metering.Supplier, "101", "20")))
	if err != nil {
		t.Fatal(err)
	}
	if within, err := metering.WithinBounds(actual, bounds); err != nil || within {
		t.Fatalf("actual overage was accepted: within=%v err=%v", within, err)
	}
	if _, err := metering.ParseBounds(s, metering.Supplier, []byte(quantityJSON("bounds", metering.Supplier, "101", "20"))); !errors.Is(err, metering.ErrInvalidVector) {
		t.Fatalf("bound over schema maximum was accepted: %v", err)
	}

	low := mustRates(t, s, metering.Supplier, [2]string{"1", "0"}, [2]string{"3", "1"})
	high := mustRates(t, s, metering.Supplier, [2]string{"1", "0"}, [2]string{"2", "1"})
	lowAmount, err := metering.ReservationAmount(bounds, low)
	if err != nil {
		t.Fatal(err)
	}
	highAmount, err := metering.ReservationAmount(bounds, high)
	if err != nil {
		t.Fatal(err)
	}
	if lowAmount != 34 || highAmount != 50 {
		t.Fatalf("unexpected single-ceil amounts: low=%d high=%d", lowAmount, highAmount)
	}
	if comparison, err := metering.CompareExactCost(bounds, low, bounds, high); err != nil || comparison >= 0 {
		t.Fatalf("exact rational ordering was lost: comparison=%d err=%v", comparison, err)
	}
	lowDigest, err := metering.QuoteDigest(bounds, low)
	if err != nil {
		t.Fatal(err)
	}
	highDigest, err := metering.QuoteDigest(bounds, high)
	if err != nil {
		t.Fatal(err)
	}
	if lowDigest == highDigest {
		t.Fatal("quote digest collapsed distinct exact rates")
	}

	foreignSchema := mustSchema(t, strings.Replace(tokenSchemaJSON, `"100"`, `"99"`, 1))
	foreignBounds := mustBounds(t, foreignSchema, metering.Supplier, 99, 20)
	foreignRates := mustRates(t, foreignSchema, metering.Supplier, [2]string{"1", "0"}, [2]string{"3", "1"})
	if _, err := metering.QuoteDigest(foreignBounds, low); !errors.Is(err, metering.ErrBindingMismatch) {
		t.Fatalf("cross-schema quote digest was accepted: %v", err)
	}
	if _, err := metering.Amount(actual, foreignRates); !errors.Is(err, metering.ErrBindingMismatch) {
		t.Fatalf("cross-schema amount was accepted: %v", err)
	}
}

func TestBindingZeroValuesAndImmutableCanonical(t *testing.T) {
	raw := []byte(tokenSchemaJSON)
	s, err := metering.ParseSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Digest()
	for i := range raw {
		raw[i] = 'x'
	}
	encoded, _ := s.CanonicalJSON()
	if string(encoded) != tokenSchemaJSON {
		t.Fatal("schema retained caller buffer")
	}
	encoded[0] = 'x'
	after, _ := s.Digest()
	if before != after {
		t.Fatal("canonical output mutated schema")
	}
	same := mustSchema(t, tokenSchemaJSON)
	different := mustSchema(t, strings.Replace(tokenSchemaJSON, `"100"`, `"101"`, 1))
	b := mustBounds(t, s, metering.Supplier, 1, 1)
	q := mustQuantities(t, same, metering.Supplier, 1, 1)
	r := mustRates(t, same, metering.Supplier, [2]string{"1", "1"}, [2]string{"1", "1"})
	if value, err := metering.Amount(q, r); err != nil || value.String() != "2" {
		t.Fatal("independent identical schema does not bind")
	}
	other := mustRates(t, different, metering.Supplier, [2]string{"1", "1"}, [2]string{"1", "1"})
	if value, err := metering.Amount(q, other); value != nil || !errors.Is(err, metering.ErrBindingMismatch) {
		t.Fatal("same UUID different schema accepted")
	}
	retail := mustRates(t, s, metering.Retail, [2]string{"1", "1"}, [2]string{"1", "1"})
	if value, err := metering.Amount(q, retail); value != nil || !errors.Is(err, metering.ErrBindingMismatch) {
		t.Fatal("cross-side amount accepted")
	}
	if value, err := metering.CompareExactCost(b, retail, b, r); value != 0 || !errors.Is(err, metering.ErrSide) {
		t.Fatal("retail procurement comparison accepted")
	}
	if digest, err := metering.QuoteDigest(b, other); digest != [32]byte{} || !errors.Is(err, metering.ErrBindingMismatch) {
		t.Fatal("quote binding ignored")
	}
	for _, value := range []interface {
		CanonicalJSON() ([]byte, error)
		Digest() ([32]byte, error)
	}{metering.Schema{}, metering.Bounds{}, metering.Quantities{}, metering.Rates{}} {
		if raw, err := value.CanonicalJSON(); raw != nil || !errors.Is(err, metering.ErrInvalidValue) {
			t.Fatal("zero canonical accepted")
		}
		if digest, err := value.Digest(); digest != [32]byte{} || !errors.Is(err, metering.ErrInvalidValue) {
			t.Fatal("zero digest accepted")
		}
	}
	if value, err := metering.Amount(metering.Quantities{}, r); value != nil || !errors.Is(err, metering.ErrInvalidValue) {
		t.Fatal("zero amount accepted")
	}
	if value, err := metering.WithinBounds(q, metering.Bounds{}); value || !errors.Is(err, metering.ErrInvalidValue) {
		t.Fatal("zero bounds accepted")
	}
	if value, err := metering.ReservationAmount(metering.Bounds{}, r); value != 0 || !errors.Is(err, metering.ErrInvalidValue) {
		t.Fatal("zero reservation accepted")
	}
	if value, err := metering.CompareExactCost(b, r, metering.Bounds{}, r); value != 0 || !errors.Is(err, metering.ErrInvalidValue) {
		t.Fatal("zero comparison accepted")
	}
	for _, tc := range []struct{ i, o int64 }{{-1, 0}, {0, -1}} {
		_, err := metering.LegacyTokenRates(s, metering.Retail, tc.i, tc.o)
		wantError(t, err, metering.ErrInvalidValue)
		_, err = metering.LegacyTokenQuantities(s, metering.Retail, tc.i, tc.o)
		wantError(t, err, metering.ErrInvalidValue)
		_, err = metering.LegacyTokenBounds(s, metering.Retail, tc.i, tc.o)
		wantError(t, err, metering.ErrInvalidValue)
	}
	for _, value := range []interface {
		CanonicalJSON() ([]byte, error)
		Digest() ([32]byte, error)
	}{b, q, r} {
		digest, _ := value.Digest()
		encoded, _ := value.CanonicalJSON()
		encoded[0] = 'x'
		again, _ := value.CanonicalJSON()
		got, _ := value.Digest()
		if again[0] != '{' || got != digest {
			t.Fatal("vector canonical output aliases internal state")
		}
	}
}

func TestLegacyTokenAmountIndependentOracle(t *testing.T) {
	s := mustSchema(t, strings.ReplaceAll(strings.Replace(tokenSchemaJSON, `"100"`, `"9223372036854775807"`, 1), `"20"`, `"9223372036854775807"`))
	random := rand.New(rand.NewPCG(18271, 72419))
	cases := [][4]int64{{0, 0, 0, 0}, {1, 0, 999999, 0}, {1, 0, 1000000, 0}, {1, 0, 1000001, 0}, {1, 1, 500000, 500000}, {math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}}
	for range 512 {
		cases = append(cases, [4]int64{random.Int64(), random.Int64(), random.Int64(), random.Int64()})
	}
	for _, c := range cases {
		for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
			q := mustQuantities(t, s, side, c[0], c[1])
			r, err := metering.LegacyTokenRates(s, side, c[2], c[3])
			if err != nil {
				t.Fatal(err)
			}
			got, err := metering.Amount(q, r)
			// This is the old two-token formula, independent of rational helpers.
			want := new(big.Int).Mul(big.NewInt(c[0]), big.NewInt(c[2]))
			want.Add(want, new(big.Int).Mul(big.NewInt(c[1]), big.NewInt(c[3])))
			want.Add(want, big.NewInt(999999))
			want.Quo(want, big.NewInt(1000000))
			if err != nil || got.Cmp(want) != 0 {
				t.Fatalf("legacy mismatch: %v %v want %v", got, err, want)
			}
		}
	}
}

func TestImmutableObjectsConcurrentUse(t *testing.T) {
	s := mustSchema(t, tokenSchemaJSON)
	q := mustQuantities(t, s, metering.Retail, 100, 20)
	b := mustBounds(t, s, metering.Retail, 100, 20)
	r := mustRates(t, s, metering.Retail, [2]string{"1", "1"}, [2]string{"3", "6"})
	digest, _ := metering.QuoteDigest(b, r)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 16 {
				amount, err := metering.Amount(q, r)
				if err != nil || amount.String() != "37" {
					t.Error("concurrent amount changed", amount, err)
					return
				}
				amount.SetInt64(0)
				canonical, _ := r.CanonicalJSON()
				canonical[0] = 'x'
				got, err := metering.QuoteDigest(b, r)
				if err != nil || got != digest {
					t.Error("concurrent digest changed", err)
					return
				}
			}
		})
	}
	workers.Wait()
}

// Intentionally separate from production framing and includes the domain as a
// length-prefixed frame, not a plain prefix. Python hashlib independently fixed
// the schema golden below; the oracle also checks every vector's full preimage.
func digestOracle(frames ...[]byte) [32]byte {
	var framed bytes.Buffer
	for _, frame := range frames {
		_ = binary.Write(&framed, binary.BigEndian, uint32(len(frame)))
		_, _ = framed.Write(frame)
	}
	return sha256.Sum256(framed.Bytes())
}
func TestCanonicalDigestGoldenAndDomains(t *testing.T) {
	s := mustSchema(t, tokenSchemaJSON)
	sd, _ := s.Digest()
	if hex.EncodeToString(sd[:]) != "cb5ec303ae366738e83d617e68ca415a9eba025a806a1eda307d0bdd3d67fa82" {
		t.Fatal("schema golden changed", hex.EncodeToString(sd[:]))
	}
	if sd != digestOracle([]byte("moni-aigate.metering.schema.v1"), []byte(tokenSchemaJSON)) {
		t.Fatal("schema framing differs")
	}
	// Standard escape and object-field order have identical canonical meaning.
	escaped := strings.ReplaceAll(strings.Replace(tokenSchemaJSON, `"schema_id":"`+schemaID+`","version":"1"`, `"version":"1","schema_id":"`+schemaID+`"`, 1), "text_input_token", `\u0074ext_input_token`)
	other := mustSchema(t, escaped)
	od, _ := other.Digest()
	if sd != od {
		t.Fatal("semantic escape/order changed digest")
	}
	b := mustBounds(t, s, metering.Supplier, 1, 1)
	q := mustQuantities(t, s, metering.Supplier, 1, 1)
	r := mustRates(t, s, metering.Supplier, [2]string{"1", "1"}, [2]string{"2", "2"})
	for _, tc := range []struct {
		domain string
		value  interface {
			CanonicalJSON() ([]byte, error)
			Digest() ([32]byte, error)
		}
	}{{"bounds", b}, {"quantities", q}, {"rates", r}} {
		raw, _ := tc.value.CanonicalJSON()
		got, _ := tc.value.Digest()
		want := digestOracle([]byte("moni-aigate.metering."+tc.domain+".v1"), sd[:], []byte("supplier"), raw)
		if got != want {
			t.Fatal("vector framing changed", tc.domain)
		}
	}
	bd, _ := b.Digest()
	qd, _ := q.Digest()
	if bd == qd {
		t.Fatal("bounds and quantity domains collide")
	}
	br, _ := b.CanonicalJSON()
	rr, _ := r.CanonicalJSON()
	digest, _ := metering.QuoteDigest(b, r)
	want := digestOracle([]byte("moni-aigate.metering.quote.v1"), sd[:], []byte("supplier"), br, rr)
	if digest != want {
		t.Fatal("quote framing changed")
	}
	changedBound := mustBounds(t, s, metering.Supplier, 2, 1)
	changedRate := mustRates(t, s, metering.Supplier, [2]string{"1", "1"}, [2]string{"3", "2"})
	for _, tc := range []struct {
		b metering.Bounds
		r metering.Rates
	}{{changedBound, r}, {b, changedRate}, {mustBounds(t, s, metering.Retail, 1, 1), mustRates(t, s, metering.Retail, [2]string{"1", "1"}, [2]string{"2", "2"})}} {
		got, err := metering.QuoteDigest(tc.b, tc.r)
		if err != nil || got == digest {
			t.Fatal("quote did not bind changed value", err)
		}
	}
}
