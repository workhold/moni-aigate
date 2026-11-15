package billing

import (
	"encoding/json"
	"testing"
	"time"
)

func TestVoucherTimeRoundTripUsesSixMicroseconds(t *testing.T) {
	original := time.Date(2026, 11, 15, 1, 2, 3, 123000, time.UTC)
	var value VoucherTime
	if err := value.Scan(original); err != nil {
		t.Fatal(err)
	}
	if !value.Equal(original) {
		t.Fatalf("voucher timestamp changed: %v != %v", value.Time, original)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `"2026-11-15T01:02:03.000123Z"`; got != want {
		t.Fatalf("voucher timestamp format %q != %q", got, want)
	}
}
