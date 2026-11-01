package periodicbudget

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCanonicalProtocolValues(t *testing.T) {
	for _, tc := range []struct {
		name           string
		valid          func(string) bool
		accept, reject []string
	}{
		{"uuid", ValidUUID, []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "00000000-0000-0000-0000-000000000001"}, []string{"", "00000000-0000-0000-0000-000000000000", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "aaaaaaaaaaaa4aaa8aaaaaaaaaaaaaaa", "{aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa}", " aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\n"}},
		{"int64_amount", ValidInt64AmountString, []string{"0", "1", "9007199254740993", "9223372036854775807"}, []string{"", "01", "00", "+1", "-0", "1e2", "1.0", "9223372036854775808", "18446744073709551615", " 1", "1\n", "１"}},
		{"revision", ValidRevision, []string{"0", "1", "9223372036854775807"}, []string{"-1", "01", "9223372036854775808"}},
		{"positive_revision", ValidPositiveRevision, []string{"1", "9223372036854775807"}, []string{"0", "-1", "01", "9223372036854775808"}},
		{"numeric100", ValidNumeric100, []string{"0", "9007199254740993", strings.Repeat("9", 100)}, []string{"", "00", "01", "-1", "+1", "1.1", "1e99", strings.Repeat("9", 101), " 1", "1\t"}},
		{"utc6", ValidUTC6, []string{"0001-01-01T00:00:00.000000Z", "9999-12-31T23:59:59.999999Z", "2024-02-29T23:59:59.000001Z"}, []string{"0000-01-01T00:00:00.000000Z", "10000-01-01T00:00:00.000000Z", "2023-02-29T00:00:00.000000Z", "2026-11-01T24:00:00.000000Z", "2026-11-01T00:00:60.000000Z", "2026-11-01T00:00:00Z", "2026-11-01T00:00:00.00000Z", "2026-11-01T00:00:00.0000000Z", "2026-11-01T00:00:00.000000+00:00", "2026-11-01t00:00:00.000000z", "infinity", "2026-11-01T00:00:00.000000Z\n"}},
		{"hex64", ValidLowerHex64, []string{strings.Repeat("a", 64), strings.Repeat("0", 64)}, []string{strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("g", 64)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range tc.accept {
				if !tc.valid(v) {
					t.Errorf("rejected canonical value %q", v)
				}
			}
			for _, v := range tc.reject {
				if tc.valid(v) {
					t.Errorf("accepted noncanonical value %q", v)
				}
			}
		})
	}
}

func TestErrorCodeRecognizesOnlyOwnControlledErrors(t *testing.T) {
	for _, err := range []error{ErrInvalidRequest, ErrInvalidCursor, ErrRequestTooLarge, ErrForbidden, ErrNotFound, ErrConflict, ErrCalendarImmutable, ErrMaintenance, ErrOperationConflict, ErrRevisionExhausted, ErrClockRegression, ErrInvariant, ErrOutcomeUnknown, ErrUnavailable, ErrRateLimited} {
		if got := ErrorCode(fmt.Errorf("private wrapper: %w", err)); got != err.Error() {
			t.Fatalf("own error not recognized: %q", got)
		}
	}
	for _, err := range []error{nil, errors.New("invalid_cursor"), errors.New("private database error"), domainError{"uncontrolled_secret"}} {
		if ErrorCode(err) != "" {
			t.Fatal("classified unrelated or uncontrolled error")
		}
	}
}
