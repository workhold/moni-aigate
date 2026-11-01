package procurement

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProcurementCanonicalMetadataValidation(t *testing.T) {
	for _, value := range []string{"0", "9007199254740993", "9223372036854775807"} {
		if !canonicalNumber(value, false) {
			t.Fatal(value)
		}
	}
	for _, value := range []string{"", "01", " 1", "+1", "-1", "1.0", "1e2", "9223372036854775808"} {
		if canonicalNumber(value, false) {
			t.Fatal(value)
		}
	}
	if canonicalNumber("0", true) {
		t.Fatal("zero CAS accepted")
	}
	for _, value := range []string{"", " bad", "bad ", "bad\nlabel", strings.Repeat("中", 34)} {
		if text(value, 100) {
			t.Fatal(value)
		}
	}
	if !text("人工余额记录", 100) {
		t.Fatal("safe UTF8 rejected")
	}
}

func TestObservationStrictTimestampAndStringAmounts(t *testing.T) {
	base := `{"operation_id":"f169d344-9acd-4a40-b82b-7179c511b31f","status":"known","amount":"9007199254740993","currency":"CNY","source":"manual","semantic":"cash_balance","observed_at":"TIME","provider_as_of":null,"evidence_ref":"statement:1"}`
	for _, value := range []string{"2026-11-01T00:00:00Z", "2026-11-01T00:00:00.123456789+08:00"} {
		var in ObservationInput
		if err := json.Unmarshal([]byte(strings.Replace(base, "TIME", value, 1)), &in); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"2026-11-01T0:00:00Z", "2026-11-01T00:00:00,1Z", "2026-11-01T00:00:00+24:00", "2026-11-01T00:00:00+00:60", "2026-11-01T00:00:00.1234567890Z", "2026-02-30T00:00:00Z"} {
		var in ObservationInput
		if err := json.Unmarshal([]byte(strings.Replace(base, "TIME", value, 1)), &in); err == nil {
			t.Fatal(value)
		}
	}
	valid := strings.Replace(base, "TIME", "2026-11-01T00:00:00Z", 1)
	for _, raw := range []string{strings.Replace(valid, `"amount":"9007199254740993"`, `"amount":9007199254740993`, 1), strings.TrimSuffix(valid, "}") + `,"secret":"unexpected"}`} {
		var in ObservationInput
		if json.Unmarshal([]byte(raw), &in) == nil {
			t.Fatal(raw)
		}
	}
}
