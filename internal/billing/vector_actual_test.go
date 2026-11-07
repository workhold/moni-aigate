package billing

import (
	"fmt"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

func TestCanonicalActualAmount(t *testing.T) {
	const id = "10000000-0000-4000-8000-000000000002"
	schemaRaw := []byte(`{"schema_id":"` + id + `","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"100"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"20"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`)
	schema, err := metering.ParseSchema(schemaRaw)
	if err != nil {
		t.Fatal(err)
	}
	sd, _ := schema.Digest()
	for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
		for _, tc := range []struct {
			name, input, output, inN, inD, outN, outD, want string
		}{
			{"single_ceil", "1", "1", "1", "2", "1", "2", "1"},
			{"fractional_sum", "1", "1", "3", "5", "3", "5", "2"},
			{"explicit_zero", "10", "5", "0", "1", "0", "1", "0"},
			{"small_rate", "1", "1", "1", "1000000", "1", "1000000", "1"},
			{"actual_exceeds_bounds", "110", "30", "1", "2", "1", "2", "70"},
			{"large_actual", "9223372036854775807", "0", "9223372036854775807", "1", "0", "1", "85070591730234615847396907784232501249"},
		} {
			t.Run(string(side)+"/"+tc.name, func(t *testing.T) {
				ratesRaw := []byte(fmt.Sprintf(`{"schema_id":"%s","version":"1","side":"%s","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"%s","denominator":"%s"},{"dimension":"text_output_token","unit":"token","numerator":"%s","denominator":"%s"}]}`, id, side, tc.inN, tc.inD, tc.outN, tc.outD))
				rates, err := metering.ParseRates(schema, side, ratesRaw)
				if err != nil {
					t.Fatal(err)
				}
				rd, _ := rates.Digest()
				quantitiesRaw := []byte(fmt.Sprintf(`{"schema_id":"%s","version":"1","side":"%s","quantities":[{"dimension":"text_input_token","unit":"token","quantity":"%s"},{"dimension":"text_output_token","unit":"token","quantity":"%s"}]}`, id, side, tc.input, tc.output))
				got, err := canonicalActualAmount(schemaRaw, sd[:], ratesRaw, rd[:], quantitiesRaw, side)
				if err != nil || got.String() != tc.want {
					t.Fatal(got, err, "want", tc.want)
				}
				if tc.name != "single_ceil" {
					return
				}
				for _, invalid := range []struct {
					name, schema, rates, quantities string
					sd, rd                          []byte
				}{
					{"schema_missing", "", string(ratesRaw), string(quantitiesRaw), sd[:], rd[:]},
					{"schema_not_canonical", " " + string(schemaRaw), string(ratesRaw), string(quantitiesRaw), sd[:], rd[:]},
					{"schema_digest", string(schemaRaw), string(ratesRaw), string(quantitiesRaw), make([]byte, 32), rd[:]},
					{"rates_missing", string(schemaRaw), "", string(quantitiesRaw), sd[:], rd[:]},
					{"rates_digest", string(schemaRaw), string(ratesRaw), string(quantitiesRaw), sd[:], make([]byte, 32)},
					{"rates_not_canonical", string(schemaRaw), " " + string(ratesRaw), string(quantitiesRaw), sd[:], rd[:]},
					{"rates_wrong_side", string(schemaRaw), strings.Replace(string(ratesRaw), `"side":"`+string(side)+`"`, `"side":"invalid"`, 1), string(quantitiesRaw), sd[:], rd[:]},
					{"rates_wrong_schema", string(schemaRaw), strings.Replace(string(ratesRaw), id, "10000000-0000-4000-8000-000000000003", 1), string(quantitiesRaw), sd[:], rd[:]},
					{"rates_incomplete", string(schemaRaw), strings.Replace(string(ratesRaw), `,{"dimension":"text_output_token","unit":"token","numerator":"1","denominator":"2"}`, "", 1), string(quantitiesRaw), sd[:], rd[:]},
					{"rates_unreduced", string(schemaRaw), strings.Replace(string(ratesRaw), `"numerator":"1","denominator":"2"`, `"numerator":"2","denominator":"4"`, 1), string(quantitiesRaw), sd[:], rd[:]},
					{"rates_currency", string(schemaRaw), strings.Replace(string(ratesRaw), "CNY", "USD", 1), string(quantitiesRaw), sd[:], rd[:]},
					{"quantities_missing", string(schemaRaw), string(ratesRaw), "", sd[:], rd[:]},
					{"quantities_not_canonical", string(schemaRaw), string(ratesRaw), " " + string(quantitiesRaw), sd[:], rd[:]},
					{"quantities_wrong_side", string(schemaRaw), string(ratesRaw), strings.Replace(string(quantitiesRaw), `"side":"`+string(side)+`"`, `"side":"invalid"`, 1), sd[:], rd[:]},
					{"quantities_wrong_schema", string(schemaRaw), string(ratesRaw), strings.Replace(string(quantitiesRaw), id, "10000000-0000-4000-8000-000000000003", 1), sd[:], rd[:]},
					{"quantities_incomplete", string(schemaRaw), string(ratesRaw), strings.Replace(string(quantitiesRaw), `,{"dimension":"text_output_token","unit":"token","quantity":"1"}`, "", 1), sd[:], rd[:]},
				} {
					t.Run(invalid.name, func(t *testing.T) {
						_, err := canonicalActualAmount([]byte(invalid.schema), invalid.sd, []byte(invalid.rates), invalid.rd, []byte(invalid.quantities), side)
						assertCode(t, err, "invalid_meter_snapshot")
					})
				}
			})
		}
	}
}
