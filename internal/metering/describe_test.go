package metering

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestSchemaDescriptionIsImmutable(t *testing.T) {
	schema, err := ParseSchema([]byte(`{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"9223372036854775807"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"0"}],"retail_dimensions":["text_input_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := schema.ID()
	if err != nil || id != "11111111-1111-1111-1111-111111111111" {
		t.Fatal("schema identity changed", id, err)
	}
	dimensions, err := schema.Dimensions()
	want := []Dimension{{"text_input_token", "token", math.MaxInt64}, {"text_output_token", "token", 0}}
	if err != nil || !reflect.DeepEqual(dimensions, want) {
		t.Fatal("dimension order, unit or exact maximum changed", dimensions, err)
	}
	retail, err := schema.Mask(Retail)
	if err != nil || !reflect.DeepEqual(retail, []string{"text_input_token"}) {
		t.Fatal("retail mask changed", retail, err)
	}
	supplier, err := schema.Mask(Supplier)
	if err != nil || !reflect.DeepEqual(supplier, []string{"text_input_token", "text_output_token"}) {
		t.Fatal("supplier mask changed", supplier, err)
	}
	canonical, _ := schema.CanonicalJSON()
	digest, _ := schema.Digest()
	dimensions[0] = Dimension{"image_output_count", "image", 1}
	retail[0] = "image_output_count"
	supplier[0] = "request_count"
	fresh, _ := schema.Dimensions()
	r, _ := schema.Mask(Retail)
	s, _ := schema.Mask(Supplier)
	after, _ := schema.CanonicalJSON()
	afterDigest, _ := schema.Digest()
	if !reflect.DeepEqual(fresh, want) || r[0] != "text_input_token" || s[0] != "text_input_token" || !bytes.Equal(canonical, after) || afterDigest != digest {
		t.Fatal("mutating a description changed the immutable schema")
	}
	// A description cannot raise the global bound used by the real parser.
	if _, err := LegacyTokenBounds(schema, Supplier, 1, 1); !errors.Is(err, ErrInvalidSchema) {
		t.Fatal("description granted an incompatible token shape", err)
	}
	if _, err := ParseBounds(schema, Supplier, []byte(`{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","side":"supplier","bounds":[{"dimension":"text_input_token","unit":"token","quantity":"1"},{"dimension":"text_output_token","unit":"token","quantity":"1"}]}`)); !errors.Is(err, ErrInvalidVector) {
		t.Fatal("description mutation raised the global output bound", err)
	}
}

func TestSchemaDescriptionRejectsZeroAndInvalidSide(t *testing.T) {
	var zero Schema
	if id, err := zero.ID(); id != "" || !errors.Is(err, ErrInvalidValue) {
		t.Fatal("zero identity accepted", id, err)
	}
	if d, err := zero.Dimensions(); d != nil || !errors.Is(err, ErrInvalidValue) {
		t.Fatal("zero dimensions accepted", d, err)
	}
	for _, side := range []Side{Retail, Supplier, "invalid"} {
		if m, err := zero.Mask(side); m != nil || !errors.Is(err, ErrInvalidValue) {
			t.Fatal("zero mask accepted", side, m, err)
		}
	}
	schema, err := ParseSchema([]byte(`{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","dimensions":[{"dimension":"request_count","unit":"request","maximum_quantity":"1"}],"retail_dimensions":["request_count"],"supplier_dimensions":["request_count"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m, err := schema.Mask("invalid"); m != nil || !errors.Is(err, ErrSide) {
		t.Fatal("invalid side returned a partial mask", m, err)
	}
}

func TestRateDescriptionsPreserveBindingsAndAmounts(t *testing.T) {
	if entries, err := (Rates{}).Entries(); entries != nil || !errors.Is(err, ErrInvalidValue) {
		t.Fatal("zero rate descriptions accepted", entries, err)
	}
	schema, err := ParseSchema([]byte(`{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"10"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"0"}],"retail_dimensions":["text_input_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []Side{Retail, Supplier} {
		raw := `{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","side":"` + string(side) + `","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"1","denominator":"2"}`
		want := []Rate{{"text_input_token", "token", 1, 2}}
		if side == Supplier {
			raw += `,{"dimension":"text_output_token","unit":"token","numerator":"0","denominator":"1"}`
			want = append(want, Rate{"text_output_token", "token", 0, 1})
		}
		rates, err := ParseRates(schema, side, []byte(raw+`]}`))
		if err != nil {
			t.Fatal(err)
		}
		entries, err := rates.Entries()
		if err != nil || !reflect.DeepEqual(entries, want) {
			t.Fatal("rate mask or exact rational changed", side, entries, err)
		}
		before, _ := rates.CanonicalJSON()
		digest, _ := rates.Digest()
		entries[0] = Rate{"request_count", "request", math.MaxInt64, 1}
		entries = append(entries, Rate{"image_output_count", "image", 1, 1})
		fresh, _ := rates.Entries()
		after, _ := rates.CanonicalJSON()
		afterDigest, _ := rates.Digest()
		if !reflect.DeepEqual(fresh, want) || !bytes.Equal(before, after) || afterDigest != digest {
			t.Fatal("rate description mutation changed immutable rates")
		}
		qraw := `{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","side":"` + string(side) + `","quantities":[{"dimension":"text_input_token","unit":"token","quantity":"3"}`
		if side == Supplier {
			qraw += `,{"dimension":"text_output_token","unit":"token","quantity":"0"}`
		}
		q, err := ParseQuantities(schema, side, []byte(qraw+`]}`))
		if err != nil {
			t.Fatal(err)
		}
		amount, err := Amount(q, rates)
		if err != nil || amount.String() != "2" {
			t.Fatal("description changed the real calculation", amount, err)
		}
	}
}
