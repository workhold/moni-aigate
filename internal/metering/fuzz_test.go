package metering_test

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

func safeError(t testing.TB, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, allowed := range []error{metering.ErrTooLarge, metering.ErrInvalidJSON, metering.ErrInvalidSchema, metering.ErrInvalidVector, metering.ErrInvalidValue, metering.ErrSide, metering.ErrBindingMismatch, metering.ErrAmountOverflow} {
		if errors.Is(err, allowed) && err.Error() == allowed.Error() {
			return
		}
	}
	t.Fatal("error is not a fixed controlled code", err)
}

func FuzzStrictParsers(f *testing.F) {
	f.Add([]byte(tokenSchemaJSON))
	f.Add([]byte(quantityJSON("bounds", metering.Retail, "1", "1")))
	f.Add([]byte(quantityJSON("quantities", metering.Retail, "1", "1")))
	f.Add([]byte(`{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","side":"retail","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"1","denominator":"2"},{"dimension":"text_output_token","unit":"token","numerator":"0","denominator":"1"}]}`))
	f.Add([]byte(`{"a":"\ud800"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		s := mustSchema(t, tokenSchemaJSON)
		parsed, err := metering.ParseSchema(raw)
		safeError(t, err)
		if err == nil {
			canonical, _ := parsed.CanonicalJSON()
			again, err := metering.ParseSchema(canonical)
			if err != nil {
				t.Fatal("canonical schema not parseable", err)
			}
			d, _ := parsed.Digest()
			d2, _ := again.Digest()
			if d != d2 {
				t.Fatal("schema roundtrip digest changed")
			}
		}
		b, err := metering.ParseBounds(s, metering.Retail, raw)
		safeError(t, err)
		if err == nil {
			canonical, _ := b.CanonicalJSON()
			again, err := metering.ParseBounds(s, metering.Retail, canonical)
			if err != nil {
				t.Fatal("canonical bound rejected")
			}
			c2, _ := again.CanonicalJSON()
			if !bytes.Equal(canonical, c2) {
				t.Fatal("bound roundtrip differs")
			}
		}
		q, err := metering.ParseQuantities(s, metering.Retail, raw)
		safeError(t, err)
		if err == nil {
			canonical, _ := q.CanonicalJSON()
			again, err := metering.ParseQuantities(s, metering.Retail, canonical)
			if err != nil {
				t.Fatal("canonical quantity rejected")
			}
			d, _ := q.Digest()
			d2, _ := again.Digest()
			if d != d2 {
				t.Fatal("quantity roundtrip differs")
			}
		}
		r, err := metering.ParseRates(s, metering.Retail, raw)
		safeError(t, err)
		if err == nil {
			canonical, _ := r.CanonicalJSON()
			again, err := metering.ParseRates(s, metering.Retail, canonical)
			if err != nil {
				t.Fatal("canonical rate rejected")
			}
			q := mustQuantities(t, s, metering.Retail, 100, 20)
			a, err := metering.Amount(q, r)
			a2, err2 := metering.Amount(q, again)
			if err != nil || err2 != nil || a.Cmp(a2) != 0 {
				t.Fatal("canonical rate cost differs")
			}
		}
	})
}

func FuzzAmountMonotonicity(f *testing.F) {
	f.Add(uint64(1), uint64(1), uint64(500000), uint64(500000))
	f.Add(uint64(math.MaxInt64), uint64(math.MaxInt64), uint64(math.MaxInt64), uint64(math.MaxInt64))
	f.Fuzz(func(t *testing.T, a, b, p, o uint64) {
		i, output := int64(a&math.MaxInt64), int64(b&math.MaxInt64)
		inputRate, outputRate := int64(p&math.MaxInt64), int64(o&math.MaxInt64)
		s := mustSchema(t, `{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"9223372036854775807"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"9223372036854775807"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`)
		r, err := metering.ManagementTokenRates(s, metering.Retail, inputRate, outputRate)
		if err != nil {
			t.Fatal(err)
		}
		q := mustQuantities(t, s, metering.Retail, i, output)
		amount, err := metering.Amount(q, r)
		if err != nil || amount.Sign() < 0 {
			t.Fatal("invalid nonnegative cost", amount, err)
		}
		bound := mustBounds(t, s, metering.Retail, i, output)
		reserved, err := metering.ReservationAmount(bound, r)
		if amount.IsInt64() {
			if err != nil || reserved != amount.Int64() {
				t.Fatal("reservation diverged from amount", err)
			}
		} else if !errors.Is(err, metering.ErrAmountOverflow) || reserved != 0 {
			t.Fatal("overflow did not fail closed", err)
		}
		inside, err := metering.WithinBounds(q, bound)
		if err != nil || !inside {
			t.Fatal("equal quantity/bounds not within")
		}
		if i < math.MaxInt64 {
			higher := mustQuantities(t, s, metering.Retail, i+1, output)
			higherAmount, err := metering.Amount(higher, r)
			if err != nil || higherAmount.Cmp(amount) < 0 {
				t.Fatal("cost is not monotonic")
			}
		}
	})
}
