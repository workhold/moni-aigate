package productvalue_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/productvalue"
)

func TestMonthRetainsOriginalAnchorAndLeapRules(t *testing.T) {
	tests := []struct{ anchor, offset, want string }{
		{"2025-01-31T23:59:58.123456Z", "0", "2025-01-31T23:59:58.123456Z"},
		{"2025-01-31T23:59:58.123456Z", "1", "2025-02-28T23:59:58.123456Z"},
		{"2025-01-31T23:59:58.123456Z", "2", "2025-03-31T23:59:58.123456Z"},
		{"2025-01-31T23:59:58.123456Z", "3", "2025-04-30T23:59:58.123456Z"},
		{"2024-01-31T01:02:03.000001Z", "1", "2024-02-29T01:02:03.000001Z"},
		{"2024-01-31T01:02:03.000001Z", "2", "2024-03-31T01:02:03.000001Z"},
		{"2024-02-29T01:02:03.999999Z", "12", "2025-02-28T01:02:03.999999Z"},
		{"2024-02-29T01:02:03.999999Z", "48", "2028-02-29T01:02:03.999999Z"},
		{"1899-12-31T00:00:00.000000Z", "2", "1900-02-28T00:00:00.000000Z"},
		{"1999-12-31T00:00:00.000000Z", "2", "2000-02-29T00:00:00.000000Z"},
		{"2000-02-29T00:00:00.000000Z", "4800", "2400-02-29T00:00:00.000000Z"},
		{"0001-01-01T00:00:00.000000Z", "0", "0001-01-01T00:00:00.000000Z"},
		{"0001-01-31T23:59:59.999999Z", "119987", "9999-12-31T23:59:59.999999Z"},
		{"9999-12-31T23:59:59.999999Z", "0", "9999-12-31T23:59:59.999999Z"},
	}
	for _, tc := range tests {
		got, err := productvalue.MonthAt(tc.anchor, tc.offset)
		if err != nil || got != tc.want {
			t.Fatalf("MonthAt(%s,%s)=%s,%v want %s", tc.anchor, tc.offset, got, err, tc.want)
		}
	}
	// Using the clamped February result as a new anchor would incorrectly yield
	// March 28. The original anchor must produce a March 31 second term end.
	got, err := productvalue.TermAt("2025-01-31T23:59:58.123456Z", "1")
	want := productvalue.Term{StartsAt: "2025-02-28T23:59:58.123456Z", EndsAt: "2025-03-31T23:59:58.123456Z"}
	if err != nil || got != want {
		t.Fatalf("term drifted from original anchor: %#v %v", got, err)
	}
}

func TestTermHalfOpenStatesAndUpperYear(t *testing.T) {
	const anchor = "2024-01-31T12:34:56.123456Z"
	for _, tc := range []struct {
		at   string
		want productvalue.TermState
	}{
		{"2024-02-29T12:34:56.123455Z", productvalue.Scheduled},
		{"2024-02-29T12:34:56.123456Z", productvalue.Active},
		{"2024-03-31T12:34:56.123455Z", productvalue.Active},
		{"2024-03-31T12:34:56.123456Z", productvalue.Expired},
		{"9999-12-31T23:59:59.999999Z", productvalue.Expired},
	} {
		state, err := productvalue.TermStateAt(anchor, "1", tc.at)
		if err != nil || state != tc.want {
			t.Fatalf("half-open state at %s: %q %v, want %q", tc.at, state, err, tc.want)
		}
	}
	last, err := productvalue.TermAt("0001-01-31T00:00:00.000000Z", "119986")
	if err != nil || last != (productvalue.Term{StartsAt: "9999-11-30T00:00:00.000000Z", EndsAt: "9999-12-31T00:00:00.000000Z"}) {
		t.Fatalf("last fully representable term rejected: %#v %v", last, err)
	}
	for _, tc := range [][2]string{
		{"0001-01-01T00:00:00.000000Z", "119988"},
		{"9999-12-31T23:59:59.999999Z", "1"},
		{anchor, "9223372036854775807"},
	} {
		got, err := productvalue.MonthAt(tc[0], tc[1])
		if got != "" || !errors.Is(err, productvalue.ErrMonthOverflow) || err.Error() != productvalue.ErrMonthOverflow.Error() {
			t.Fatal("month overflow returned partial date or an uncontrolled error")
		}
	}
	for _, tc := range [][2]string{
		{"9999-12-01T00:00:00.000000Z", "0"},
		{"0001-01-01T00:00:00.000000Z", "119987"},
		{anchor, "9223372036854775807"},
	} {
		got, err := productvalue.TermAt(tc[0], tc[1])
		if got != (productvalue.Term{}) || !errors.Is(err, productvalue.ErrMonthOverflow) {
			t.Fatal("term overflow returned partial boundaries")
		}
		state, err := productvalue.TermStateAt(tc[0], tc[1], "bad-as-of")
		if state != "" || !errors.Is(err, productvalue.ErrMonthOverflow) {
			t.Fatal("as-of validation replaced earlier term failure")
		}
	}
}

func TestStrictUTC6AndOffsets(t *testing.T) {
	badTimes := []string{
		"", "2026-11-01T00:00:00Z", "2026-11-01T00:00:00.00000Z", "2026-11-01T00:00:00.0000000Z",
		"2026-11-01T00:00:00,000000Z", "2026-11-01t00:00:00.000000z", "2026-11-01T00:00:00.000000+00:00",
		"2026-11-01T00:00:00.000000+08:00", "2026-11-01 00:00:00.000000Z", "2026-11-01T00:00:60.000000Z",
		"2026-11-01T24:00:00.000000Z", "2026-02-29T00:00:00.000000Z", "1900-02-29T00:00:00.000000Z",
		"2024-04-31T00:00:00.000000Z", "2024-00-01T00:00:00.000000Z", "2024-13-01T00:00:00.000000Z",
		"0000-01-01T00:00:00.000000Z", "10000-01-01T00:00:00.000000Z", " 2026-11-01T00:00:00.000000Z",
		"2026-11-01T00:00:00.000000Z\n", "２０２６-11-01T00:00:00.000000Z", "2026-11-01T00:00:00.00000\x00Z",
	}
	const valid = "2026-11-01T00:00:00.000000Z"
	for _, s := range badTimes {
		got, err := productvalue.MonthAt(s, "0")
		if got != "" || !errors.Is(err, productvalue.ErrInvalidTime) || err.Error() != productvalue.ErrInvalidTime.Error() {
			t.Fatal("noncanonical time accepted or leaked into error")
		}
		term, err := productvalue.TermAt(s, "0")
		if !reflect.DeepEqual(term, productvalue.Term{}) || !errors.Is(err, productvalue.ErrInvalidTime) {
			t.Fatal("bad anchor returned term data")
		}
		state, err := productvalue.TermStateAt(valid, "0", s)
		if state != "" || !errors.Is(err, productvalue.ErrInvalidTime) {
			t.Fatal("bad as-of returned a term state")
		}
	}
	for _, s := range []string{"", "-1", "+1", "00", "01", "1 ", "1.0", "1e1", "١", "9223372036854775808", "10000000000000000000"} {
		got, err := productvalue.MonthAt(valid, s)
		if got != "" || !errors.Is(err, productvalue.ErrInvalidValue) {
			t.Fatal("bad month offset accepted")
		}
		term, err := productvalue.TermAt(valid, s)
		if term != (productvalue.Term{}) || !errors.Is(err, productvalue.ErrInvalidValue) {
			t.Fatal("bad term offset returned partial data")
		}
	}
}
