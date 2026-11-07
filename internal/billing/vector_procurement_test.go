package billing

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

func TestCanonicalSupplierReservationAmount(t *testing.T) {
	const id = "10000000-0000-4000-8000-000000000002"
	schemaRaw := []byte(`{"schema_id":"` + id + `","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"9223372036854775807"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"9223372036854775807"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`)
	schema, err := metering.ParseSchema(schemaRaw)
	if err != nil {
		t.Fatal(err)
	}
	sd, _ := schema.Digest()
	for _, tc := range []struct {
		name, input, output, inN, inD, outN, outD, want string
	}{
		{"single_ceil", "1", "1", "1", "2", "1", "2", "1"},
		{"full_bounds", "100", "20", "2", "1", "3", "1", "260"},
		{"fractional_sum", "100", "20", "3", "5", "2", "5", "68"},
		{"zero", "100", "20", "0", "1", "0", "1", "0"},
		{"input_only", "100", "0", "2", "1", "0", "1", "200"},
		{"micro_rate", "100", "20", "1", "1000000", "1", "1000000", "1"},
		{"maximum", "9223372036854775807", "0", "1", "1", "0", "1", "9223372036854775807"},
		{"overflow", "9223372036854775807", "1", "1", "1", "1", "1", "overflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ratesRaw := []byte(fmt.Sprintf(`{"schema_id":"%s","version":"1","side":"supplier","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"%s","denominator":"%s"},{"dimension":"text_output_token","unit":"token","numerator":"%s","denominator":"%s"}]}`, id, tc.inN, tc.inD, tc.outN, tc.outD))
			rates, err := metering.ParseRates(schema, metering.Supplier, ratesRaw)
			if err != nil {
				t.Fatal(err)
			}
			rd, _ := rates.Digest()
			boundsRaw := []byte(fmt.Sprintf(`{"schema_id":"%s","version":"1","side":"supplier","bounds":[{"dimension":"text_input_token","unit":"token","quantity":"%s"},{"dimension":"text_output_token","unit":"token","quantity":"%s"}]}`, id, tc.input, tc.output))
			got, err := canonicalSupplierReservationAmount(schemaRaw, sd[:], ratesRaw, rd[:], boundsRaw)
			if tc.want == "overflow" {
				assertCode(t, err, "procurement_bound_invalid")
				if got != 0 {
					t.Fatal("overflow returned a truncated amount", got)
				}
				return
			}
			if err != nil || fmt.Sprint(got) != tc.want {
				t.Fatal("wrong original supplier reservation amount", got, err, tc.want)
			}
			if tc.name != "single_ceil" {
				return
			}
			for _, invalid := range []struct {
				name, schema, rates, bounds string
				sd, rd                      []byte
			}{
				{"schema_missing", "", string(ratesRaw), string(boundsRaw), sd[:], rd[:]},
				{"schema_digest", string(schemaRaw), string(ratesRaw), string(boundsRaw), bytes.Repeat([]byte{1}, 32), rd[:]},
				{"schema_not_canonical", " " + string(schemaRaw), string(ratesRaw), string(boundsRaw), sd[:], rd[:]},
				{"rates_missing", string(schemaRaw), "", string(boundsRaw), sd[:], rd[:]},
				{"rates_digest", string(schemaRaw), string(ratesRaw), string(boundsRaw), sd[:], bytes.Repeat([]byte{1}, 32)},
				{"rates_not_canonical", string(schemaRaw), " " + string(ratesRaw), string(boundsRaw), sd[:], rd[:]},
				{"rates_side", string(schemaRaw), strings.Replace(string(ratesRaw), "supplier", "retail", 1), string(boundsRaw), sd[:], rd[:]},
				{"rates_binding", string(schemaRaw), strings.Replace(string(ratesRaw), id, "10000000-0000-4000-8000-000000000003", 1), string(boundsRaw), sd[:], rd[:]},
				{"bounds_missing", string(schemaRaw), string(ratesRaw), "", sd[:], rd[:]},
				{"bounds_not_canonical", string(schemaRaw), string(ratesRaw), " " + string(boundsRaw), sd[:], rd[:]},
				{"bounds_side", string(schemaRaw), string(ratesRaw), strings.Replace(string(boundsRaw), "supplier", "retail", 1), sd[:], rd[:]},
				{"bounds_binding", string(schemaRaw), string(ratesRaw), strings.Replace(string(boundsRaw), id, "10000000-0000-4000-8000-000000000003", 1), sd[:], rd[:]},
				{"bounds_unit", string(schemaRaw), string(ratesRaw), strings.Replace(string(boundsRaw), `"unit":"token"`, `"unit":"byte"`, 1), sd[:], rd[:]},
				{"bounds_incomplete", string(schemaRaw), string(ratesRaw), strings.Replace(string(boundsRaw), `,{"dimension":"text_output_token","unit":"token","quantity":"1"}`, "", 1), sd[:], rd[:]},
				{"bounds_negative", string(schemaRaw), string(ratesRaw), strings.Replace(string(boundsRaw), `"quantity":"1"`, `"quantity":"-1"`, 1), sd[:], rd[:]},
				{"bounds_over_maximum", string(schemaRaw), string(ratesRaw), strings.Replace(string(boundsRaw), `"quantity":"1"`, `"quantity":"9223372036854775808"`, 1), sd[:], rd[:]},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					got, err := canonicalSupplierReservationAmount([]byte(invalid.schema), invalid.sd, []byte(invalid.rates), invalid.rd, []byte(invalid.bounds))
					assertCode(t, err, "invalid_meter_snapshot")
					if got != 0 {
						t.Fatal("bad original returned an amount", got)
					}
				})
			}
		})
	}
}
