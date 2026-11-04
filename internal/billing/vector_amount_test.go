package billing

import (
	"math"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

func TestVectorAmountMatchesLegacyCeil(t *testing.T) {
	schema, err := metering.ParseSchema([]byte(`{"schema_id":"11111111-1111-4111-8111-111111111111","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"9223372036854775807"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"9223372036854775807"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		i, o, ir, or int64
	}{{1000, 500, 2000000, 8000000}, {1, 1, 1, 1}, {1, 1, 500000, 500000}, {1, 1, 500001, 500001}, {0, 0, math.MaxInt64, math.MaxInt64}} {
		quantities, err := metering.LegacyTokenQuantities(schema, metering.Retail, tc.i, tc.o)
		if err != nil {
			t.Fatal(err)
		}
		rates, err := metering.LegacyTokenRates(schema, metering.Retail, tc.ir, tc.or)
		if err != nil {
			t.Fatal(err)
		}
		got, err := metering.Amount(quantities, rates)
		want, wantErr := Calculate(tc.i, tc.o, Money(tc.ir), Money(tc.or))
		if err != nil || wantErr != nil || !got.IsInt64() || got.Int64() != int64(want) {
			t.Fatalf("vector %v err=%v legacy %v err=%v for %d/%d", got, err, want, wantErr, tc.i, tc.o)
		}
	}
}
