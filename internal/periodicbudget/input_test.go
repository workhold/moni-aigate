package periodicbudget

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const validConfigure = `{"operation_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","expected_revision":"9007199254740993","period_kind":"utc_month","enabled":false,"limit_amount":"9223372036854775807"}`

func TestParseConfigurePreservesLosslessValuesAndRequiresExactObject(t *testing.T) {
	want := PutPeriodicBudget{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "9007199254740993", "utc_month", false, "9223372036854775807"}
	for _, raw := range []string{validConfigure, " \n\t" + validConfigure + " \r\n", strings.Replace(validConfigure, `"operation_id"`, `"\u006fperation_id"`, 1)} {
		got, err := ParseConfigure([]byte(raw))
		if err != nil || got != want {
			t.Fatalf("valid configuration changed: %#v %v", got, err)
		}
	}
	for _, tc := range []struct{ name, raw string }{
		{"empty", ""}, {"null", "null"}, {"array", "[" + validConfigure + "]"}, {"trailing_object", validConfigure + "{}"}, {"trailing_null", validConfigure + "null"},
		{"duplicate", strings.Replace(validConfigure, `"enabled":false`, `"enabled":false,"enabled":true`, 1)},
		{"escaped_duplicate", strings.Replace(validConfigure, `"enabled":false`, `"enabled":false,"\u0065nabled":false`, 1)},
		{"alias", strings.Replace(validConfigure, `"operation_id"`, `"Operation_ID"`, 1)},
		{"unknown", strings.Replace(validConfigure, `"enabled":false`, `"enabled":false,"actor":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"`, 1)},
		{"missing_false", strings.Replace(validConfigure, `"enabled":false,`, "", 1)},
		{"null_false", strings.Replace(validConfigure, `"enabled":false`, `"enabled":null`, 1)},
		{"string_false", strings.Replace(validConfigure, `"enabled":false`, `"enabled":"false"`, 1)},
		{"number_revision", strings.Replace(validConfigure, `"9007199254740993"`, `9007199254740993`, 1)},
		{"number_amount", strings.Replace(validConfigure, `"9223372036854775807"`, `9223372036854775807`, 1)},
		{"null_amount", strings.Replace(validConfigure, `"9223372036854775807"`, `null`, 1)},
		{"nested_amount", strings.Replace(validConfigure, `"9223372036854775807"`, `{}`, 1)},
		{"negative_amount", strings.Replace(validConfigure, `"9223372036854775807"`, `"-1"`, 1)},
		{"overflow", strings.Replace(validConfigure, `"9223372036854775807"`, `"9223372036854775808"`, 1)},
		{"leading_zero", strings.Replace(validConfigure, `"9007199254740993"`, `"01"`, 1)},
		{"space_value", strings.Replace(validConfigure, `"utc_month"`, `"utc_month "`, 1)},
		{"surrogate_high", strings.Replace(validConfigure, `"utc_month"`, `"\ud800"`, 1)},
		{"surrogate_low", strings.Replace(validConfigure, `"utc_month"`, `"\udc00"`, 1)},
		{"surrogate_reversed", strings.Replace(validConfigure, `"utc_month"`, `"\udc00\ud800"`, 1)},
		{"surrogate_nonpair", strings.Replace(validConfigure, `"utc_month"`, `"\ud800\u0061"`, 1)},
		{"utf8", strings.Replace(validConfigure, `utc_month`, string([]byte{0xff}), 1)},
		{"malformed_escape", strings.Replace(validConfigure, `"utc_month"`, `"\uZZZZ"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseConfigure([]byte(tc.raw))
			if ErrorCode(err) != "invalid_request" || got != (PutPeriodicBudget{}) {
				t.Fatalf("bad body accepted or leaked partial result: %#v %v", got, err)
			}
		})
	}
}

func TestConfigureRawLimitAndUnmarshalAtomicity(t *testing.T) {
	exact := append([]byte(validConfigure), bytes.Repeat([]byte{' '}, MaxConfigureBytes-len(validConfigure))...)
	if _, err := ParseConfigure(exact); err != nil {
		t.Fatal("8192-byte valid object rejected", err)
	}
	if got, err := ParseConfigure(append(exact, ' ')); ErrorCode(err) != "request_too_large" || got != (PutPeriodicBudget{}) {
		t.Fatal("8193 bytes not rejected safely")
	}
	var in PutPeriodicBudget
	if err := json.Unmarshal([]byte(validConfigure), &in); err != nil {
		t.Fatal(err)
	}
	prior := in
	if json.Unmarshal([]byte(strings.Replace(validConfigure, `"enabled":false`, `"enabled":null`, 1)), &in) == nil || in != prior {
		t.Fatal("failed decode changed prior command")
	}
}

func TestJSONUnicodeRejectsOnlyMalformedUnicode(t *testing.T) {
	for _, raw := range []string{`"plain"`, `"\u0061"`, `"\ud83d\ude00"`, `"\\ud800"`, `"\"escaped quote"`, `"你好"`} {
		if !validJSONUnicode([]byte(raw)) {
			t.Errorf("valid unicode rejected: %s", raw)
		}
	}
	for _, raw := range []string{`"\ud800"`, `"\udc00"`, `"\ud800\u0061"`, `"\ud800\\udc00"`, `"\ud800\ud800"`, `"\ud800\u123"`} {
		if validJSONUnicode([]byte(raw)) {
			t.Errorf("isolated surrogate accepted: %s", raw)
		}
	}
}
