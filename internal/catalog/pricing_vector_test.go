package catalog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

func TestGroupPriceVectorFingerprintGolden(t *testing.T) {
	const sid = "10000000-0000-4000-8000-000000000001"
	rates := json.RawMessage(`{"schema_id":"` + sid + `","version":"1","side":"retail","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"9007199254740993","denominator":"1000000"},{"dimension":"text_output_token","unit":"token","numerator":"0","denominator":"1"}]}`)
	c := GroupPriceCommand{ModelID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", SchemaID: sid, SchemaDigest: strings.Repeat("ab", 32), VariantKey: "default", EffectiveAt: time.Date(2026, 10, 1, 0, 0, 0, 123456000, time.UTC)}
	got := pricingFingerprint("group_price.publish", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "platform", "/api/platform/price-groups/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/price-versions", groupPriceSafe(c, rates))
	if got != "135996ee4b4062565ed45073ac70a79f4b8151abe0ecb88ea5cad58fe469e261" {
		t.Fatal(got)
	}
}

func TestGroupPriceVectorResultRejectsScalarAndInvalidRates(t *testing.T) {
	const sid = "10000000-0000-4000-8000-000000000001"
	schema, err := metering.ParseSchema([]byte(`{"schema_id":"` + sid + `","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"100"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"20"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := metering.ManagementTokenRates(schema, metering.Retail, 250000, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := r.CanonicalJSON()
	v := GroupPriceView{ID: sid, ModelID: sid, PriceGroupID: sid, SchemaID: sid, SchemaDigest: strings.Repeat("ab", 32), VariantKey: "default", Currency: "COIN", Rates: raw, EffectiveAt: "2026-11-01T00:00:00.000000Z", CreatedAt: "2026-11-01T00:00:00.000001Z"}
	if err = validateGroupPriceSchema(&v, schema); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"numerator":"1"`, `"numerator":"2"`, 1), strings.Replace(string(raw), `"side":"retail"`, `"side":"supplier"`, 1), strings.Replace(string(raw), `"numerator":"1"`, `"numerator":"01"`, 1), strings.Replace(string(raw), `"unit":"token"`, `"unit":"image"`, 1)} {
		v.Rates = json.RawMessage(bad)
		if err = validateGroupPriceSchema(&v, schema); err == nil {
			t.Fatalf("accepted invalid rates %s", bad)
		}
	}
	old := []byte(`{"id":"` + sid + `","model_id":"` + sid + `","price_group_id":"` + sid + `","currency":"CNY","input_rate":"0","output_rate":"0","effective_at":"2026-11-01T00:00:00.000000Z","created_at":"2026-11-01T00:00:00.000001Z"}`)
	if _, err = pricingDecodeResult("group_price.publish", old); err == nil {
		t.Fatal("legacy scalar response accepted")
	}
}
